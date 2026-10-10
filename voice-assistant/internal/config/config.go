// Package config loads the voice assistant settings from environment
// variables.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"time"
)

// Config holds the runtime settings of the orchestrator.
type Config struct {
	// Addr is the listen address of the orchestrator.
	Addr string
	// WhisperURL is the base URL of the whisper.cpp server.
	WhisperURL *url.URL
	// GatewayURL is the base URL of the OpenAI-compatible llm-gateway.
	GatewayURL *url.URL
	// GatewayKeyFile is the path of a file holding the gateway API key.
	// The key itself is never read from the environment, so it does not
	// leak through process listings or unit files.
	GatewayKeyFile string
	// PiperURL is the base URL of the Piper HTTP server.
	PiperURL *url.URL
	// Model is the chat model requested from the gateway.
	Model string
	// Language is the spoken language passed to whisper.
	Language string
	// MaxUploadBytes caps the size of the request audio.
	MaxUploadBytes int64
	// MaxTokens caps the length of the spoken reply.
	MaxTokens int
	// TurnTimeout bounds a whole turn, from decoding to synthesis.
	TurnTimeout time.Duration
	// FFmpegPath is the ffmpeg binary used to decode the input audio.
	FFmpegPath string
}

// Load reads the configuration through getenv (os.Getenv in production),
// applying defaults for unset variables.
func Load(getenv func(string) string) (Config, error) {
	cfg := Config{
		Addr:           withDefault(getenv, "VOICE_ADDR", "127.0.0.1:8095"),
		GatewayKeyFile: getenv("VOICE_GATEWAY_KEY_FILE"),
		Model:          withDefault(getenv, "VOICE_MODEL", "llama3.2:3b"),
		Language:       withDefault(getenv, "VOICE_LANGUAGE", "es"),
		FFmpegPath:     withDefault(getenv, "VOICE_FFMPEG", "ffmpeg"),
	}
	if cfg.GatewayKeyFile == "" {
		return Config{}, errors.New("VOICE_GATEWAY_KEY_FILE is required")
	}

	var err error
	if cfg.WhisperURL, err = httpURL(getenv, "VOICE_WHISPER_URL", "http://127.0.0.1:8092"); err != nil {
		return Config{}, err
	}
	if cfg.GatewayURL, err = httpURL(getenv, "VOICE_GATEWAY_URL", "http://127.0.0.1:8090"); err != nil {
		return Config{}, err
	}
	if cfg.PiperURL, err = httpURL(getenv, "VOICE_PIPER_URL", "http://127.0.0.1:8093"); err != nil {
		return Config{}, err
	}

	maxUpload, err := positiveInt(getenv, "VOICE_MAX_UPLOAD_BYTES", 10<<20)
	if err != nil {
		return Config{}, err
	}
	cfg.MaxUploadBytes = int64(maxUpload)
	if cfg.MaxTokens, err = positiveInt(getenv, "VOICE_MAX_TOKENS", 200); err != nil {
		return Config{}, err
	}

	cfg.TurnTimeout = 2 * time.Minute
	if raw := getenv("VOICE_TURN_TIMEOUT"); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil || d <= 0 {
			return Config{}, fmt.Errorf("VOICE_TURN_TIMEOUT: %q must be a positive duration such as 90s", raw)
		}
		cfg.TurnTimeout = d
	}
	return cfg, nil
}

func withDefault(getenv func(string) string, name, def string) string {
	if v := getenv(name); v != "" {
		return v
	}
	return def
}

// httpURL reads name as an absolute http(s) URL, or def when it is unset.
func httpURL(getenv func(string) string, name, def string) (*url.URL, error) {
	raw := withDefault(getenv, name, def)
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("%s: %q must be an absolute http(s) URL", name, raw)
	}
	return u, nil
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
