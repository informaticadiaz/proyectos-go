// Command gateway runs the LLM gateway in front of an Ollama server.
//
// Usage:
//
//	gateway                 start the server
//	gateway keygen <name>   create an API key for a client
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/informaticadiaz/proyectos-go/llm-gateway/internal/auth"
	"github.com/informaticadiaz/proyectos-go/llm-gateway/internal/config"
	"github.com/informaticadiaz/proyectos-go/llm-gateway/internal/gateway"
	"github.com/informaticadiaz/proyectos-go/llm-gateway/internal/observe"
	"github.com/informaticadiaz/proyectos-go/llm-gateway/internal/ratelimit"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	var err error
	if len(os.Args) > 1 && os.Args[1] == "keygen" {
		err = keygen(os.Args[2:])
	} else {
		err = run()
	}
	if err != nil {
		slog.Error("gateway stopped", "err", err)
		os.Exit(1)
	}
}

// keygen prints a new key once; only its hash goes into the keys file.
func keygen(args []string) error {
	if len(args) != 1 || args[0] == "" {
		return errors.New("usage: gateway keygen <client-name>")
	}
	key, err := auth.GenerateKey()
	if err != nil {
		return err
	}
	fmt.Printf("API key (shown once, give it to the client): %s\n", key)
	fmt.Printf("Keys file line: %s:%s\n", args[0], auth.HashKey(key))
	return nil
}

func run() error {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}

	keys, err := loadKeys(cfg.KeysFile)
	if err != nil {
		return err
	}

	obs := observe.New(slog.Default())
	servers := []*http.Server{{
		Addr:              cfg.Addr,
		Handler:           gateway.New(cfg.Upstream, keys, ratelimit.New(cfg.RatePerMinute, cfg.RateBurst, time.Now), obs),
		ReadHeaderTimeout: 10 * time.Second,
	}}
	if cfg.MetricsAddr != "" {
		mux := http.NewServeMux()
		mux.HandleFunc("GET /metrics", obs.ServeMetrics)
		servers = append(servers, &http.Server{
			Addr:              cfg.MetricsAddr,
			Handler:           mux,
			ReadHeaderTimeout: 10 * time.Second,
		})
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	slog.Info("gateway starting", "addr", cfg.Addr, "metrics_addr", cfg.MetricsAddr,
		"upstream", cfg.Upstream.String(), "clients", keys.Len(),
		"rate_per_minute", cfg.RatePerMinute, "rate_burst", cfg.RateBurst)

	errCh := make(chan error, len(servers))
	for _, srv := range servers {
		go func() { errCh <- srv.ListenAndServe() }()
	}

	// Run until a signal arrives or any server fails, then stop them all.
	var runErr error
	select {
	case err := <-errCh:
		runErr = err
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, srv := range servers {
		if err := srv.Shutdown(shutdownCtx); err != nil && runErr == nil {
			runErr = err
		}
	}
	if runErr != nil && !errors.Is(runErr, http.ErrServerClosed) {
		return runErr
	}
	return nil
}

func loadKeys(path string) (*auth.Store, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open keys file: %w", err)
	}
	defer f.Close()
	return auth.ParseKeys(f)
}
