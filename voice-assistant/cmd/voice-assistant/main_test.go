package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/informaticadiaz/proyectos-go/voice-assistant/internal/turn"
)

type stage struct{}

func (stage) Decode(context.Context, []byte) ([]byte, error)     { return []byte("wav"), nil }
func (stage) Transcribe(context.Context, []byte) (string, error) { return "hola", nil }
func (stage) Reply(context.Context, string) (string, error)      { return "buenas", nil }
func (stage) Synthesize(context.Context, string) ([]byte, error) { return []byte("RIFF-reply"), nil }

func TestHandlerRoutes(t *testing.T) {
	srv := httptest.NewServer(newHandler(
		turn.Stages{Decoder: stage{}, Transcriber: stage{}, Chatter: stage{}, Synthesizer: stage{}},
		turn.Options{MaxUploadBytes: 1 << 20, TurnTimeout: 5 * time.Second, Logger: slog.New(slog.DiscardHandler)},
	))
	t.Cleanup(srv.Close)

	for _, tc := range []struct {
		method, path, body string
		status             int
		contentType        string
		bodyHas            string
	}{
		{"GET", "/", "", 200, "text/html; charset=utf-8", "Asistente de voz"},
		{"GET", "/app.js", "", 200, "text/javascript; charset=utf-8", "/v1/turn"},
		{"GET", "/healthz", "", 200, "", "ok\n"},
		{"POST", "/v1/turn", "audio", 200, "audio/wav", "RIFF-reply"},
		{"GET", "/v1/turn", "", 405, "", ""},
		{"POST", "/v1/turn", "", 400, "application/json", `"stage":"read"`},
		{"GET", "/nope", "", 404, "", ""},
	} {
		req, err := http.NewRequest(tc.method, srv.URL+tc.path, strings.NewReader(tc.body))
		if err != nil {
			t.Fatal(err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != tc.status {
			t.Errorf("%s %s status = %d, want %d", tc.method, tc.path, resp.StatusCode, tc.status)
		}
		if tc.contentType != "" && resp.Header.Get("Content-Type") != tc.contentType {
			t.Errorf("%s %s Content-Type = %q", tc.method, tc.path, resp.Header.Get("Content-Type"))
		}
		if !strings.Contains(string(body), tc.bodyHas) {
			t.Errorf("%s %s body = %q, want it to contain %q", tc.method, tc.path, body, tc.bodyHas)
		}
	}
}

func TestPageHeadersDoNotLeakIntoAPI(t *testing.T) {
	// The turn API keeps its stage-1 response headers.
	rec := httptest.NewRecorder()
	newHandler(turn.Stages{Decoder: stage{}, Transcriber: stage{}, Chatter: stage{}, Synthesizer: stage{}},
		turn.Options{MaxUploadBytes: 1 << 20, TurnTimeout: time.Second, Logger: slog.New(slog.DiscardHandler)},
	).ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))
	if csp := rec.Header().Get("Content-Security-Policy"); csp != "" {
		t.Errorf("/healthz has a page CSP: %q", csp)
	}
}
