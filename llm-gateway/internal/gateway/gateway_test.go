package gateway_test

import (
	"bufio"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/informaticadiaz/proyectos-go/llm-gateway/internal/gateway"
)

func newGateway(t *testing.T, upstream string) *httptest.Server {
	t.Helper()
	u, err := url.Parse(upstream)
	if err != nil {
		t.Fatalf("parse upstream: %v", err)
	}
	srv := httptest.NewServer(gateway.New(u))
	t.Cleanup(srv.Close)
	return srv
}

func TestHealthz(t *testing.T) {
	gw := newGateway(t, "http://127.0.0.1:1")

	resp, err := http.Get(gw.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}

func TestProxiesOpenAIRoutes(t *testing.T) {
	var gotMethod, gotPath, gotBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotMethod, gotPath, gotBody = r.Method, r.URL.Path, string(body)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"chatcmpl-1"}`)
	}))
	t.Cleanup(upstream.Close)
	gw := newGateway(t, upstream.URL)

	resp, err := http.Post(gw.URL+"/v1/chat/completions", "application/json", strings.NewReader(`{"model":"m"}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if gotMethod != http.MethodPost || gotPath != "/v1/chat/completions" || gotBody != `{"model":"m"}` {
		t.Errorf("upstream got %s %s %q", gotMethod, gotPath, gotBody)
	}
	if string(body) != `{"id":"chatcmpl-1"}` {
		t.Errorf("body = %q", body)
	}
}

func TestStreamsChunksWithoutBuffering(t *testing.T) {
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: first\n\n")
		w.(http.Flusher).Flush()
		<-release
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(upstream.Close)
	t.Cleanup(func() { close(release) })
	gw := newGateway(t, upstream.URL)

	resp, err := http.Post(gw.URL+"/v1/chat/completions", "application/json", strings.NewReader(`{"stream":true}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	line := make(chan string, 1)
	go func() {
		s, _ := bufio.NewReader(resp.Body).ReadString('\n')
		line <- s
	}()

	select {
	case got := <-line:
		if got != "data: first\n" {
			t.Errorf("first line = %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("first chunk not delivered while upstream was still streaming")
	}
}

func TestBlocksNonOpenAIRoutes(t *testing.T) {
	called := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	t.Cleanup(upstream.Close)
	gw := newGateway(t, upstream.URL)

	// Ollama's native API can pull and delete models; it must never be exposed.
	req, _ := http.NewRequest(http.MethodDelete, gw.URL+"/api/delete", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}
	if called {
		t.Error("request reached upstream")
	}
}

func TestUpstreamDownReturnsBadGateway(t *testing.T) {
	upstream := httptest.NewServer(http.NotFoundHandler())
	addr := upstream.URL
	upstream.Close()
	gw := newGateway(t, addr)

	resp, err := http.Post(gw.URL+"/v1/chat/completions", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusBadGateway)
	}
}
