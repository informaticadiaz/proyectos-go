package config_test

import (
	"testing"

	"github.com/informaticadiaz/proyectos-go/llm-gateway/internal/config"
)

func TestDefaults(t *testing.T) {
	cfg, err := config.Load(func(string) string { return "" })
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Addr != "127.0.0.1:8090" {
		t.Errorf("Addr = %q", cfg.Addr)
	}
	if cfg.Upstream.String() != "http://127.0.0.1:11434" {
		t.Errorf("Upstream = %q", cfg.Upstream)
	}
}

func TestOverridesFromEnv(t *testing.T) {
	env := map[string]string{
		"GATEWAY_ADDR": ":9000",
		"OLLAMA_URL":   "http://ollama.internal:11434",
	}
	cfg, err := config.Load(func(k string) string { return env[k] })
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Addr != ":9000" || cfg.Upstream.Host != "ollama.internal:11434" {
		t.Errorf("cfg = %+v", cfg)
	}
}

func TestRejectsInvalidUpstream(t *testing.T) {
	for _, raw := range []string{"not a url", "ftp://host", "http://"} {
		_, err := config.Load(func(k string) string {
			if k == "OLLAMA_URL" {
				return raw
			}
			return ""
		})
		if err == nil {
			t.Errorf("OLLAMA_URL=%q: expected error", raw)
		}
	}
}
