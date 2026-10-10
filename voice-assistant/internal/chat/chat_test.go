package chat_test

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

	"github.com/informaticadiaz/proyectos-go/voice-assistant/internal/chat"
)

func newClient(t *testing.T, h http.HandlerFunc) *chat.Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	return chat.New(u, "gw_secret", "llama3.2:3b", 200, http.DefaultClient)
}

func TestReplySendsChatCompletion(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer gw_secret" {
			t.Errorf("Authorization = %q", got)
		}
		var req struct {
			Model     string `json:"model"`
			Stream    bool   `json:"stream"`
			MaxTokens int    `json:"max_tokens"`
			Messages  []struct {
				Role, Content string
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.Model != "llama3.2:3b" || req.Stream || req.MaxTokens != 200 || len(req.Messages) != 2 ||
			req.Messages[0].Role != "system" || req.Messages[0].Content != chat.SystemPrompt ||
			req.Messages[1].Role != "user" || req.Messages[1].Content != "¿Qué es Go?" {
			t.Errorf("request = %+v", req)
		}
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"**Go** es un lenguaje.\n"}}]}`)
	})
	reply, err := c.Reply(context.Background(), "¿Qué es Go?")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if reply != "Go es un lenguaje." {
		t.Errorf("reply = %q, want markdown stripped", reply)
	}
}

func TestReplyReportsUpstreamErrors(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"error":{"message":"invalid API key"}}`)
	})
	_, err := c.Reply(context.Background(), "hola")
	if err == nil || !strings.Contains(err.Error(), "401") || !strings.Contains(err.Error(), "invalid API key") {
		t.Errorf("err = %v", err)
	}
	if strings.Contains(err.Error(), "gw_secret") {
		t.Errorf("error leaks the key: %v", err)
	}
}

func TestReplyRejectsEmptyChoices(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, `{"choices":[]}`) })
	if _, err := c.Reply(context.Background(), "hola"); err == nil {
		t.Error("expected error without choices")
	}
}

func TestReplyHonoursContext(t *testing.T) {
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
	if _, err := c.Reply(ctx, "hola"); err == nil {
		t.Error("expected error after the deadline")
	}
}
