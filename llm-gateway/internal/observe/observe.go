// Package observe records one structured log entry and a set of metrics per
// gateway request: client, status, model, latency, time to first byte and
// token usage.
//
// Token usage comes from the upstream response, which is inspected while it
// streams to the client, so observation never buffers or delays tokens.
package observe

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/informaticadiaz/proyectos-go/llm-gateway/internal/auth"
)

// Observer logs requests and accumulates their metrics. It is safe for
// concurrent use.
type Observer struct {
	logger  *slog.Logger
	metrics *metrics
}

// New returns an Observer that writes request logs to logger.
func New(logger *slog.Logger) *Observer {
	return &Observer{logger: logger, metrics: newMetrics()}
}

// record collects what is learned about one request while it is served.
// It is only touched by the goroutine serving that request.
type record struct {
	model            string
	stream           bool
	usageKnown       bool
	promptTokens     int
	completionTokens int
}

type recordKey struct{}

// Middleware observes every request that reaches next. It must run after
// auth.Middleware so the client is known.
func (o *Observer) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &record{}
		rw := &responseWriter{ResponseWriter: w}

		next.ServeHTTP(rw, r.WithContext(context.WithValue(r.Context(), recordKey{}, rec)))

		duration := time.Since(start)
		client, _ := auth.ClientFrom(r.Context())
		status := rw.status
		if status == 0 {
			status = http.StatusOK
		}

		attrs := []slog.Attr{
			slog.String("client", client),
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", status),
			slog.Bool("stream", rec.stream),
			slog.Float64("duration_ms", milliseconds(duration)),
		}
		if !rw.firstByte.IsZero() {
			attrs = append(attrs, slog.Float64("ttfb_ms", milliseconds(rw.firstByte.Sub(start))))
		}
		if rec.model != "" {
			attrs = append(attrs, slog.String("model", rec.model))
		}
		if rec.usageKnown {
			attrs = append(attrs,
				slog.Int("prompt_tokens", rec.promptTokens),
				slog.Int("completion_tokens", rec.completionTokens),
			)
		}
		o.logger.LogAttrs(r.Context(), slog.LevelInfo, "request", attrs...)
		o.metrics.observe(client, status, duration, rec)
	})
}

// ModifyResponse is meant for httputil.ReverseProxy.ModifyResponse. It wraps
// the upstream body so model and usage are read as the proxy copies it.
func (o *Observer) ModifyResponse(resp *http.Response) error {
	rec, ok := resp.Request.Context().Value(recordKey{}).(*record)
	if !ok {
		return nil
	}
	resp.Body = newInspector(resp.Body, rec, isEventStream(resp.Header.Get("Content-Type")))
	return nil
}

// ServeMetrics writes the accumulated metrics in Prometheus text format.
func (o *Observer) ServeMetrics(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	o.metrics.writeTo(w)
}

// responseWriter captures the status code and the time of the first body
// byte. Unwrap lets http.ResponseController reach the underlying writer, so
// the proxy can still flush streamed responses.
type responseWriter struct {
	http.ResponseWriter
	status    int
	firstByte time.Time
}

func (w *responseWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *responseWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if w.firstByte.IsZero() {
		w.firstByte = time.Now()
	}
	return w.ResponseWriter.Write(b)
}

func (w *responseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func milliseconds(d time.Duration) float64 {
	return float64(d) / float64(time.Millisecond)
}
