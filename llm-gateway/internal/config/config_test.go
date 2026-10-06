package config_test

import (
	"testing"

	"github.com/informaticadiaz/proyectos-go/llm-gateway/internal/config"
)

// envWith returns a getenv that only knows the given variables.
func envWith(vars map[string]string) func(string) string {
	return func(k string) string { return vars[k] }
}

func TestDefaults(t *testing.T) {
	cfg, err := config.Load(envWith(map[string]string{"GATEWAY_KEYS_FILE": "keys.txt"}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Addr != "127.0.0.1:8090" {
		t.Errorf("Addr = %q", cfg.Addr)
	}
	if cfg.Upstream.String() != "http://127.0.0.1:11434" {
		t.Errorf("Upstream = %q", cfg.Upstream)
	}
	if cfg.KeysFile != "keys.txt" {
		t.Errorf("KeysFile = %q", cfg.KeysFile)
	}
	if cfg.RatePerMinute != 60 || cfg.RateBurst != 10 {
		t.Errorf("rate = %d/min burst %d, want 60/min burst 10", cfg.RatePerMinute, cfg.RateBurst)
	}
}

func TestOverridesFromEnv(t *testing.T) {
	cfg, err := config.Load(envWith(map[string]string{
		"GATEWAY_ADDR":            ":9000",
		"OLLAMA_URL":              "http://ollama.internal:11434",
		"GATEWAY_KEYS_FILE":       "keys.txt",
		"GATEWAY_RATE_PER_MINUTE": "120",
		"GATEWAY_RATE_BURST":      "5",
	}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Addr != ":9000" || cfg.Upstream.Host != "ollama.internal:11434" ||
		cfg.RatePerMinute != 120 || cfg.RateBurst != 5 {
		t.Errorf("cfg = %+v", cfg)
	}
}

func TestRequiresKeysFile(t *testing.T) {
	if _, err := config.Load(envWith(nil)); err == nil {
		t.Error("expected error when GATEWAY_KEYS_FILE is unset")
	}
}

func TestRejectsInvalidUpstream(t *testing.T) {
	for _, raw := range []string{"not a url", "ftp://host", "http://"} {
		_, err := config.Load(envWith(map[string]string{
			"OLLAMA_URL":        raw,
			"GATEWAY_KEYS_FILE": "keys.txt",
		}))
		if err == nil {
			t.Errorf("OLLAMA_URL=%q: expected error", raw)
		}
	}
}

func TestRejectsInvalidRateLimits(t *testing.T) {
	for _, v := range []string{"0", "-1", "ten", "1.5"} {
		for _, name := range []string{"GATEWAY_RATE_PER_MINUTE", "GATEWAY_RATE_BURST"} {
			_, err := config.Load(envWith(map[string]string{
				"GATEWAY_KEYS_FILE": "keys.txt",
				name:                v,
			}))
			if err == nil {
				t.Errorf("%s=%q: expected error", name, v)
			}
		}
	}
}
