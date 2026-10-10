package piper_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/informaticadiaz/proyectos-go/voice-assistant/internal/piper"
)

func newClient(t *testing.T, h http.HandlerFunc) *piper.Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	return piper.New(u, http.DefaultClient)
}

func TestSynthesizePostsTextAndReturnsWAV(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/synthesize" {
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q", ct)
		}
		var req struct{ Text string }
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Text != "Hola, mundo." {
			t.Errorf("request = %+v (%v)", req, err)
		}
		// Piper's Flask server answers WAV bytes with a text/html type.
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		io.WriteString(w, "RIFF\x24\x00\x00\x00WAVEfmt ")
	})
	wav, err := c.Synthesize(context.Background(), "Hola, mundo.")
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if !strings.HasPrefix(string(wav), "RIFF") {
		t.Errorf("wav = %q", wav)
	}
}

func TestSynthesizeReportsUpstreamErrors(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	})
	_, err := c.Synthesize(context.Background(), "hola")
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Errorf("err = %v", err)
	}
}

func TestSynthesizeRejectsNonWAV(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "<html>oops</html>") })
	if _, err := c.Synthesize(context.Background(), "hola"); err == nil {
		t.Error("expected error for a non-WAV body")
	}
}

func TestSynthesizeHonoursContext(t *testing.T) {
	c := newClient(t, func(_ http.ResponseWriter, r *http.Request) {
		// The server notices the client leaving only after the body is read.
		io.Copy(io.Discard, r.Body)
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := c.Synthesize(ctx, "hola"); err == nil {
		t.Error("expected error after the deadline")
	}
}
