package whisper_test

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

	"github.com/informaticadiaz/proyectos-go/voice-assistant/internal/whisper"
)

func newClient(t *testing.T, h http.HandlerFunc) *whisper.Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	return whisper.New(u, "es", http.DefaultClient)
}

func TestTranscribeSendsMultipartAndReadsText(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/inference" {
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatalf("ParseMultipartForm: %v", err)
		}
		if got := r.FormValue("response_format"); got != "json" {
			t.Errorf("response_format = %q", got)
		}
		if got := r.FormValue("language"); got != "es" {
			t.Errorf("language = %q", got)
		}
		f, hdr, err := r.FormFile("file")
		if err != nil {
			t.Fatalf("file: %v", err)
		}
		body, _ := io.ReadAll(f)
		if string(body) != "RIFF-wav" || !strings.HasSuffix(hdr.Filename, ".wav") {
			t.Errorf("file %q = %q", hdr.Filename, body)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"text":" Hola, ¿cómo estás?\n"}`)
	})
	text, err := c.Transcribe(context.Background(), []byte("RIFF-wav"))
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if text != "Hola, ¿cómo estás?" {
		t.Errorf("text = %q", text)
	}
}

func TestTranscribeReportsUpstreamErrors(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"error":"failed to read audio data"}`)
	})
	_, err := c.Transcribe(context.Background(), []byte("x"))
	if err == nil || !strings.Contains(err.Error(), "400") || !strings.Contains(err.Error(), "failed to read audio data") {
		t.Errorf("err = %v", err)
	}
}

func TestTranscribeRejectsMalformedJSON(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "<html>") })
	if _, err := c.Transcribe(context.Background(), []byte("x")); err == nil {
		t.Error("expected error for a non-JSON body")
	}
}

func TestTranscribeHonoursContext(t *testing.T) {
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
	if _, err := c.Transcribe(ctx, []byte("x")); err == nil {
		t.Error("expected error after the deadline")
	}
}

func TestTranscribeDropsNonSpeechAnnotations(t *testing.T) {
	cases := map[string]string{
		" [MÚSICA]\n":                 "",
		"(risas) [Música]":            "",
		"[aplausos] Hola. (tos) Chau": "Hola. Chau",
		"Hola, mundo.":                "Hola, mundo.",
	}
	for in, want := range cases {
		c := newClient(t, func(w http.ResponseWriter, _ *http.Request) {
			b, _ := json.Marshal(map[string]string{"text": in})
			w.Write(b)
		})
		got, err := c.Transcribe(context.Background(), []byte("x"))
		if err != nil || got != want {
			t.Errorf("%q -> %q (%v), want %q", in, got, err, want)
		}
	}
}
