package observe_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/informaticadiaz/proyectos-go/llm-gateway/internal/auth"
	"github.com/informaticadiaz/proyectos-go/llm-gateway/internal/observe"
)

// harness wires an Observer the way the gateway does: an authenticated
// client in the context, the observe middleware, and a reverse proxy whose
// responses are inspected through ModifyResponse.
type harness struct {
	obs  *observe.Observer
	logs *bytes.Buffer
	srv  *httptest.Server
	// served receives once the handler has returned. A client can finish
	// reading a Content-Length response before that, while the middleware
	// is still writing the log entry.
	served chan struct{}
	waited bool
}

func newHarness(t *testing.T, client string, upstream http.Handler) *harness {
	t.Helper()
	up := httptest.NewServer(upstream)
	t.Cleanup(up.Close)
	target, _ := url.Parse(up.URL)

	logs := &bytes.Buffer{}
	obs := observe.New(slog.New(slog.NewJSONHandler(logs, nil)))
	proxy := &httputil.ReverseProxy{
		Rewrite:        func(r *httputil.ProxyRequest) { r.SetURL(target) },
		ModifyResponse: obs.ModifyResponse,
	}
	served := make(chan struct{}, 1)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		obs.Middleware(proxy).ServeHTTP(w, r.WithContext(auth.WithClient(r.Context(), client)))
		served <- struct{}{}
	})
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return &harness{obs: obs, logs: logs, srv: srv, served: served}
}

// wait blocks until the single request under test has been fully handled.
func (h *harness) wait(t *testing.T) {
	t.Helper()
	if h.served == nil || h.waited {
		return
	}
	select {
	case <-h.served:
		h.waited = true
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not finish")
	}
}

func (h *harness) post(t *testing.T, body string) string {
	t.Helper()
	resp, err := http.Post(h.srv.URL+"/v1/chat/completions", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return string(out)
}

// record returns the single request log entry.
func (h *harness) record(t *testing.T) map[string]any {
	t.Helper()
	h.wait(t)
	var entries []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(h.logs.String()), "\n") {
		var e map[string]any
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("decode log line %q: %v", line, err)
		}
		if e["msg"] == "request" {
			entries = append(entries, e)
		}
	}
	if len(entries) != 1 {
		t.Fatalf("got %d request log entries, want 1: %s", len(entries), h.logs)
	}
	return entries[0]
}

func (h *harness) metrics(t *testing.T) string {
	t.Helper()
	h.wait(t)
	rec := httptest.NewRecorder()
	h.obs.ServeMetrics(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain; version=0.0.4") {
		t.Errorf("metrics Content-Type = %q", ct)
	}
	return rec.Body.String()
}

func assertField(t *testing.T, rec map[string]any, key string, want any) {
	t.Helper()
	if got := rec[key]; got != want {
		t.Errorf("log %s = %v (%T), want %v (%T)", key, got, got, want, want)
	}
}

func assertMetric(t *testing.T, metrics, line string) {
	t.Helper()
	if !strings.Contains(metrics, line+"\n") {
		t.Errorf("metrics missing %q:\n%s", line, metrics)
	}
}

func TestRecordsUsageFromJSONResponse(t *testing.T) {
	const body = `{"id":"c1","model":"qwen","choices":[],"usage":{"prompt_tokens":12,"completion_tokens":34,"total_tokens":46}}`
	h := newHarness(t, "alice", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, body)
	}))

	if got := h.post(t, `{}`); got != body {
		t.Fatalf("client got %q, want body unchanged", got)
	}

	rec := h.record(t)
	assertField(t, rec, "client", "alice")
	assertField(t, rec, "status", float64(200))
	assertField(t, rec, "model", "qwen")
	assertField(t, rec, "stream", false)
	assertField(t, rec, "prompt_tokens", float64(12))
	assertField(t, rec, "completion_tokens", float64(34))
	for _, k := range []string{"duration_ms", "ttfb_ms"} {
		if v, ok := rec[k].(float64); !ok || v < 0 {
			t.Errorf("log %s = %v, want a non-negative number", k, rec[k])
		}
	}

	m := h.metrics(t)
	assertMetric(t, m, `gateway_requests_total{client="alice",code="200"} 1`)
	assertMetric(t, m, `gateway_tokens_total{client="alice",model="qwen",type="prompt"} 12`)
	assertMetric(t, m, `gateway_tokens_total{client="alice",model="qwen",type="completion"} 34`)
	assertMetric(t, m, `gateway_request_duration_seconds_count{client="alice"} 1`)
}

func TestRecordsUsageFromStreamSplitAcrossWrites(t *testing.T) {
	stream := "data: {\"model\":\"qwen\",\"choices\":[{\"delta\":{\"content\":\"Hi\"}}]}\n\n" +
		"data: {\"model\":\"qwen\",\"choices\":[],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":7}}\n\n" +
		"data: [DONE]\n\n"
	h := newHarness(t, "alice", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		// Flush in small pieces so SSE lines arrive split across reads.
		for i := 0; i < len(stream); i += 7 {
			io.WriteString(w, stream[i:min(i+7, len(stream))])
			w.(http.Flusher).Flush()
		}
	}))

	if got := h.post(t, `{"stream":true}`); got != stream {
		t.Fatalf("client got %q, want stream unchanged", got)
	}

	rec := h.record(t)
	assertField(t, rec, "stream", true)
	assertField(t, rec, "model", "qwen")
	assertField(t, rec, "prompt_tokens", float64(5))
	assertField(t, rec, "completion_tokens", float64(7))
}

func TestStreamWithoutUsageLeavesTokensUnknown(t *testing.T) {
	h := newHarness(t, "alice", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"model\":\"qwen\",\"choices\":[]}\n\ndata: [DONE]\n\n")
	}))

	h.post(t, `{"stream":true}`)

	rec := h.record(t)
	assertField(t, rec, "model", "qwen")
	if _, ok := rec["prompt_tokens"]; ok {
		t.Error("prompt_tokens logged although the stream carried no usage")
	}
	if strings.Contains(h.metrics(t), "gateway_tokens_total{") {
		t.Error("token metrics recorded although usage was unknown")
	}
}

func TestRecordsResponsesThatNeverReachUpstream(t *testing.T) {
	logs := &bytes.Buffer{}
	obs := observe.New(slog.New(slog.NewJSONHandler(logs, nil)))
	handler := obs.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "slow down", http.StatusTooManyRequests)
	}))

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	handler.ServeHTTP(httptest.NewRecorder(), req.WithContext(auth.WithClient(req.Context(), "bob")))

	h := &harness{obs: obs, logs: logs}
	rec := h.record(t)
	assertField(t, rec, "client", "bob")
	assertField(t, rec, "status", float64(429))
	assertMetric(t, h.metrics(t), `gateway_requests_total{client="bob",code="429"} 1`)
}

func TestEscapesMetricLabels(t *testing.T) {
	h := newHarness(t, `we"ird\name`, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{}`)
	}))

	h.post(t, `{}`)

	assertMetric(t, h.metrics(t), `gateway_requests_total{client="we\"ird\\name",code="200"} 1`)
}
