// Package gateway exposes an OpenAI-compatible HTTP surface in front of an
// Ollama upstream.
package gateway

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"

	"github.com/informaticadiaz/proyectos-go/llm-gateway/internal/auth"
	"github.com/informaticadiaz/proyectos-go/llm-gateway/internal/observe"
	"github.com/informaticadiaz/proyectos-go/llm-gateway/internal/ratelimit"
)

// New returns the gateway handler. Only the OpenAI-compatible routes under
// /v1/ are forwarded, and only for clients holding a key in keys and within
// their rate limit; Ollama's native API (/api/*), which can pull and delete
// models, is never exposed. Every authenticated request is reported to obs.
func New(upstream *url.URL, keys *auth.Store, limiter *ratelimit.Limiter, obs *observe.Observer) http.Handler {
	proxy := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(upstream)
			r.SetXForwarded()
		},
		ModifyResponse: obs.ModifyResponse,
		// ReverseProxy flushes text/event-stream responses immediately, so
		// streamed tokens reach the client as soon as Ollama emits them.
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			slog.Error("upstream request failed", "path", r.URL.Path, "err", err)
			http.Error(w, "upstream unavailable", http.StatusBadGateway)
		},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, "ok\n")
	})
	// Authentication runs first so unauthenticated requests never spend a
	// client's tokens; observation wraps the rate limiter so 429s are
	// recorded too.
	mux.Handle("/v1/", auth.Middleware(keys, obs.Middleware(ratelimit.Middleware(limiter, proxy))))
	return mux
}
