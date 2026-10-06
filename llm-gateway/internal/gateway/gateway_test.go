package gateway_test

import (
	"bufio"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/informaticadiaz/proyectos-go/llm-gateway/internal/auth"
	"github.com/informaticadiaz/proyectos-go/llm-gateway/internal/gateway"
	"github.com/informaticadiaz/proyectos-go/llm-gateway/internal/ratelimit"
)

const testKey = "gw_test"

// newGateway starts a gateway with a limit high enough to stay out of the way.
func newGateway(t *testing.T, upstream string) *httptest.Server {
	t.Helper()
	return newLimitedGateway(t, upstream, ratelimit.New(6000, 100, time.Now))
}

func newLimitedGateway(t *testing.T, upstream string, limiter *ratelimit.Limiter) *httptest.Server {
	t.Helper()
	u, err := url.Parse(upstream)
	if err != nil {
		t.Fatalf("parse upstream: %v", err)
	}
	keys, err := auth.ParseKeys(strings.NewReader("tester:" + auth.HashKey(testKey)))
	if err != nil {
		t.Fatalf("ParseKeys: %v", err)
	}
	srv := httptest.NewServer(gateway.New(u, keys, limiter))
	t.Cleanup(srv.Close)
	return srv
}

func do(t *testing.T, method, target, key, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, target, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, target, err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestHealthzIsPublic(t *testing.T) {
	gw := newGateway(t, "http://127.0.0.1:1")

	resp := do(t, http.MethodGet, gw.URL+"/healthz", "", "")

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}

func TestProxiesOpenAIRoutes(t *testing.T) {
	var gotMethod, gotPath, gotBody, gotAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotMethod, gotPath, gotBody = r.Method, r.URL.Path, string(body)
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"chatcmpl-1"}`)
	}))
	t.Cleanup(upstream.Close)
	gw := newGateway(t, upstream.URL)

	resp := do(t, http.MethodPost, gw.URL+"/v1/chat/completions", testKey, `{"model":"m"}`)
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if gotMethod != http.MethodPost || gotPath != "/v1/chat/completions" || gotBody != `{"model":"m"}` {
		t.Errorf("upstream got %s %s %q", gotMethod, gotPath, gotBody)
	}
	if gotAuth != "" {
		t.Errorf("client API key leaked upstream: %q", gotAuth)
	}
	if string(body) != `{"id":"chatcmpl-1"}` {
		t.Errorf("body = %q", body)
	}
}

func TestRejectsRequestsWithoutValidKey(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
	}))
	t.Cleanup(upstream.Close)
	gw := newGateway(t, upstream.URL)

	for _, key := range []string{"", "gw_wrong"} {
		resp := do(t, http.MethodPost, gw.URL+"/v1/chat/completions", key, `{}`)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("key %q: status = %d, want %d", key, resp.StatusCode, http.StatusUnauthorized)
		}
	}
	if n := calls.Load(); n != 0 {
		t.Errorf("upstream called %d times", n)
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

	resp := do(t, http.MethodPost, gw.URL+"/v1/chat/completions", testKey, `{"stream":true}`)

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
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
	}))
	t.Cleanup(upstream.Close)
	gw := newGateway(t, upstream.URL)

	// Ollama's native API can pull and delete models; it must never be
	// exposed, not even to authenticated clients.
	resp := do(t, http.MethodDelete, gw.URL+"/api/delete", testKey, "")

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}
	if n := calls.Load(); n != 0 {
		t.Errorf("upstream called %d times", n)
	}
}

func TestUpstreamDownReturnsBadGateway(t *testing.T) {
	upstream := httptest.NewServer(http.NotFoundHandler())
	addr := upstream.URL
	upstream.Close()
	gw := newGateway(t, addr)

	resp := do(t, http.MethodPost, gw.URL+"/v1/chat/completions", testKey, `{}`)

	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusBadGateway)
	}
}

func TestRateLimitsPerClient(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
	}))
	t.Cleanup(upstream.Close)
	gw := newLimitedGateway(t, upstream.URL, ratelimit.New(1, 1, time.Now))

	first := do(t, http.MethodPost, gw.URL+"/v1/chat/completions", testKey, `{}`)
	second := do(t, http.MethodPost, gw.URL+"/v1/chat/completions", testKey, `{}`)

	if first.StatusCode != http.StatusOK || second.StatusCode != http.StatusTooManyRequests {
		t.Errorf("statuses = %d, %d; want 200, 429", first.StatusCode, second.StatusCode)
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("upstream called %d times, want 1", n)
	}
}

func TestUnauthenticatedRequestsDoNotSpendTokens(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	t.Cleanup(upstream.Close)
	gw := newLimitedGateway(t, upstream.URL, ratelimit.New(1, 1, time.Now))

	for range 3 {
		do(t, http.MethodPost, gw.URL+"/v1/chat/completions", "gw_wrong", `{}`)
	}
	resp := do(t, http.MethodPost, gw.URL+"/v1/chat/completions", testKey, `{}`)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200: rejected requests consumed the client's tokens", resp.StatusCode)
	}
}
