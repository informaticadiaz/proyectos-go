package config_test

import (
	"testing"
	"time"

	"github.com/informaticadiaz/proyectos-go/voice-assistant/internal/config"
)

// envWith returns a getenv that only knows the given variables.
func envWith(vars map[string]string) func(string) string {
	return func(k string) string { return vars[k] }
}

// required holds the variables without a default.
func required() map[string]string {
	return map[string]string{"VOICE_GATEWAY_KEY_FILE": "gateway.key"}
}

func TestDefaults(t *testing.T) {
	cfg, err := config.Load(envWith(required()))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	checks := []struct {
		name, got, want string
	}{
		{"Addr", cfg.Addr, "127.0.0.1:8095"},
		{"WhisperURL", cfg.WhisperURL.String(), "http://127.0.0.1:8092"},
		{"GatewayURL", cfg.GatewayURL.String(), "http://127.0.0.1:8090"},
		{"PiperURL", cfg.PiperURL.String(), "http://127.0.0.1:8093"},
		{"GatewayKeyFile", cfg.GatewayKeyFile, "gateway.key"},
		{"Model", cfg.Model, "llama3.2:3b"},
		{"Language", cfg.Language, "es"},
		{"FFmpegPath", cfg.FFmpegPath, "ffmpeg"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.name, c.got, c.want)
		}
	}
	if cfg.MaxUploadBytes != 10<<20 {
		t.Errorf("MaxUploadBytes = %d, want 10 MiB", cfg.MaxUploadBytes)
	}
	if cfg.MaxTokens != 200 {
		t.Errorf("MaxTokens = %d, want 200", cfg.MaxTokens)
	}
	if cfg.TurnTimeout != 2*time.Minute {
		t.Errorf("TurnTimeout = %v, want 2m", cfg.TurnTimeout)
	}
}

func TestOverridesFromEnv(t *testing.T) {
	env := required()
	for k, v := range map[string]string{
		"VOICE_ADDR":             ":9000",
		"VOICE_WHISPER_URL":      "http://whisper.internal:1",
		"VOICE_GATEWAY_URL":      "https://gw.internal",
		"VOICE_PIPER_URL":        "http://piper.internal:2",
		"VOICE_MODEL":            "qwen2.5:7b",
		"VOICE_LANGUAGE":         "en",
		"VOICE_MAX_UPLOAD_BYTES": "1024",
		"VOICE_MAX_TOKENS":       "50",
		"VOICE_TURN_TIMEOUT":     "30s",
		"VOICE_FFMPEG":           "/usr/local/bin/ffmpeg",
	} {
		env[k] = v
	}
	cfg, err := config.Load(envWith(env))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Addr != ":9000" || cfg.WhisperURL.Host != "whisper.internal:1" ||
		cfg.GatewayURL.Scheme != "https" || cfg.PiperURL.Host != "piper.internal:2" ||
		cfg.Model != "qwen2.5:7b" || cfg.Language != "en" || cfg.MaxUploadBytes != 1024 ||
		cfg.MaxTokens != 50 || cfg.TurnTimeout != 30*time.Second ||
		cfg.FFmpegPath != "/usr/local/bin/ffmpeg" {
		t.Errorf("cfg = %+v", cfg)
	}
}

func TestRequiresGatewayKeyFile(t *testing.T) {
	if _, err := config.Load(envWith(nil)); err == nil {
		t.Error("expected error when VOICE_GATEWAY_KEY_FILE is unset")
	}
}

func TestRejectsInvalidURLs(t *testing.T) {
	for _, name := range []string{"VOICE_WHISPER_URL", "VOICE_GATEWAY_URL", "VOICE_PIPER_URL"} {
		for _, raw := range []string{"not a url", "ftp://host", "http://"} {
			env := required()
			env[name] = raw
			if _, err := config.Load(envWith(env)); err == nil {
				t.Errorf("%s=%q: expected error", name, raw)
			}
		}
	}
}

func TestRejectsInvalidNumbers(t *testing.T) {
	for _, name := range []string{"VOICE_MAX_UPLOAD_BYTES", "VOICE_MAX_TOKENS"} {
		for _, v := range []string{"0", "-1", "ten", "1.5"} {
			env := required()
			env[name] = v
			if _, err := config.Load(envWith(env)); err == nil {
				t.Errorf("%s=%q: expected error", name, v)
			}
		}
	}
	for _, v := range []string{"0s", "-1s", "soon", "30"} {
		env := required()
		env["VOICE_TURN_TIMEOUT"] = v
		if _, err := config.Load(envWith(env)); err == nil {
			t.Errorf("VOICE_TURN_TIMEOUT=%q: expected error", v)
		}
	}
}
