// Package config loads the gateway settings from environment variables.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
)

// Config holds the runtime settings of the gateway.
type Config struct {
	// Addr is the listen address of the gateway.
	Addr string
	// Upstream is the base URL of the Ollama server.
	Upstream *url.URL
	// KeysFile is the path of the client API keys file. It is required so
	// the gateway never starts without authentication.
	KeysFile string
	// RatePerMinute is the average number of requests allowed per client.
	RatePerMinute int
	// RateBurst is the number of requests a client may send at once.
	RateBurst int
	// MetricsAddr is the listen address of the Prometheus metrics endpoint.
	// Empty disables it. It is kept off the public listener because metrics
	// reveal client names.
	MetricsAddr string
}

// Load reads the configuration through getenv (os.Getenv in production),
// applying defaults for unset variables.
func Load(getenv func(string) string) (Config, error) {
	addr := getenv("GATEWAY_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8090"
	}

	raw := getenv("OLLAMA_URL")
	if raw == "" {
		raw = "http://127.0.0.1:11434"
	}
	upstream, err := url.Parse(raw)
	if err != nil {
		return Config{}, fmt.Errorf("OLLAMA_URL: %w", err)
	}
	if (upstream.Scheme != "http" && upstream.Scheme != "https") || upstream.Host == "" {
		return Config{}, fmt.Errorf("OLLAMA_URL: %q must be an absolute http(s) URL", raw)
	}

	keysFile := getenv("GATEWAY_KEYS_FILE")
	if keysFile == "" {
		return Config{}, errors.New("GATEWAY_KEYS_FILE is required")
	}

	perMinute, err := positiveInt(getenv, "GATEWAY_RATE_PER_MINUTE", 60)
	if err != nil {
		return Config{}, err
	}
	burst, err := positiveInt(getenv, "GATEWAY_RATE_BURST", 10)
	if err != nil {
		return Config{}, err
	}

	return Config{
		Addr:          addr,
		Upstream:      upstream,
		KeysFile:      keysFile,
		RatePerMinute: perMinute,
		RateBurst:     burst,
		MetricsAddr:   getenv("GATEWAY_METRICS_ADDR"),
	}, nil
}

// positiveInt reads name as an integer greater than zero, or returns def
// when it is unset.
func positiveInt(getenv func(string) string, name string, def int) (int, error) {
	raw := getenv(name)
	if raw == "" {
		return def, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("%s: %q must be a positive integer", name, raw)
	}
	return n, nil
}
