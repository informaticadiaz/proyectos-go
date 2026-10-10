// Command voice-assistant runs the push-to-talk voice assistant
// orchestrator: POST /v1/turn takes recorded speech and answers with spoken
// audio, chaining ffmpeg, whisper.cpp, the llm-gateway and Piper. GET /
// serves the push-to-talk web page.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/informaticadiaz/proyectos-go/voice-assistant/internal/audio"
	"github.com/informaticadiaz/proyectos-go/voice-assistant/internal/chat"
	"github.com/informaticadiaz/proyectos-go/voice-assistant/internal/config"
	"github.com/informaticadiaz/proyectos-go/voice-assistant/internal/piper"
	"github.com/informaticadiaz/proyectos-go/voice-assistant/internal/turn"
	"github.com/informaticadiaz/proyectos-go/voice-assistant/internal/web"
	"github.com/informaticadiaz/proyectos-go/voice-assistant/internal/whisper"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	if err := run(); err != nil {
		slog.Error("voice-assistant stopped", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}
	key, err := readKey(cfg.GatewayKeyFile)
	if err != nil {
		return err
	}

	// Upstream calls are bounded by the turn context, not a client timeout.
	client := &http.Client{}
	stages := turn.Stages{
		Decoder:     audio.NewFFmpeg(cfg.FFmpegPath),
		Transcriber: whisper.New(cfg.WhisperURL, cfg.Language, client),
		Chatter:     chat.New(cfg.GatewayURL, key, cfg.Model, cfg.MaxTokens, client),
		Synthesizer: piper.New(cfg.PiperURL, client),
	}
	srv := &http.Server{
		Addr: cfg.Addr,
		Handler: newHandler(stages, turn.Options{
			MaxUploadBytes: cfg.MaxUploadBytes,
			TurnTimeout:    cfg.TurnTimeout,
			Logger:         slog.Default(),
		}),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	slog.Info("voice-assistant starting", "addr", cfg.Addr,
		"whisper", cfg.WhisperURL.String(), "gateway", cfg.GatewayURL.String(),
		"piper", cfg.PiperURL.String(), "model", cfg.Model, "language", cfg.Language,
		"max_upload_bytes", cfg.MaxUploadBytes, "turn_timeout", cfg.TurnTimeout.String())

	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()

	// Run until a signal arrives or the server fails, then let in-flight
	// turns finish.
	var runErr error
	select {
	case runErr = <-errCh:
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.TurnTimeout+5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil && runErr == nil {
		runErr = err
	}
	if runErr != nil && !errors.Is(runErr, http.ErrServerClosed) {
		return runErr
	}
	return nil
}

// newHandler routes the turn API and health check to the turn handler and
// everything else to the web page, which answers 404 for unknown paths.
func newHandler(stages turn.Stages, opts turn.Options) http.Handler {
	api := turn.New(stages, opts)
	mux := http.NewServeMux()
	mux.Handle("/v1/turn", api)
	mux.Handle("/healthz", api)
	mux.Handle("/", web.Handler())
	return mux
}

// readKey loads the gateway API key from a file so it never sits in the
// environment or the repository.
func readKey(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read gateway key file: %w", err)
	}
	key := strings.TrimSpace(string(raw))
	if key == "" || strings.ContainsAny(key, " \t\r\n") {
		return "", fmt.Errorf("gateway key file %s must hold exactly one key", path)
	}
	return key, nil
}
