// Package config loads the gateway settings from environment variables.
package config

import (
	"fmt"
	"net/url"
)

// Config holds the runtime settings of the gateway.
type Config struct {
	// Addr is the listen address of the gateway.
	Addr string
	// Upstream is the base URL of the Ollama server.
	Upstream *url.URL
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

	return Config{Addr: addr, Upstream: upstream}, nil
}
