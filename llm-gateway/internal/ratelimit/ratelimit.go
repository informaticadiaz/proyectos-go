// Package ratelimit limits request rates per authenticated client using a
// token bucket: each client holds up to burst tokens, refilled at a steady
// rate, and every request spends one.
package ratelimit

import (
	"io"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/informaticadiaz/proyectos-go/llm-gateway/internal/auth"
)

// Limiter holds one token bucket per client. It is safe for concurrent use.
type Limiter struct {
	ratePerSec float64
	burst      float64
	now        func() time.Time

	mu      sync.Mutex
	buckets map[string]*bucket
}

type bucket struct {
	tokens float64
	last   time.Time
}

// New returns a limiter allowing perMinute requests per minute on average,
// with bursts of up to burst requests. now is the time source (time.Now in
// production). Buckets are created per client name; since clients come from
// the keys file, the map is bounded by the number of configured keys.
func New(perMinute, burst int, now func() time.Time) *Limiter {
	return &Limiter{
		ratePerSec: float64(perMinute) / 60,
		burst:      float64(burst),
		now:        now,
		buckets:    make(map[string]*bucket),
	}
}

// Allow spends one token from client's bucket. When the bucket is empty it
// returns false and how long until the next token is available.
func (l *Limiter) Allow(client string) (bool, time.Duration) {
	now := l.now()

	l.mu.Lock()
	defer l.mu.Unlock()

	b, ok := l.buckets[client]
	if !ok {
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[client] = b
	}

	elapsed := now.Sub(b.last).Seconds()
	b.tokens = math.Min(l.burst, b.tokens+elapsed*l.ratePerSec)
	b.last = now

	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	wait := (1 - b.tokens) / l.ratePerSec
	return false, time.Duration(math.Ceil(wait * float64(time.Second)))
}

// Middleware rejects requests over the client's limit with 429 and a
// Retry-After header. It must run after auth.Middleware: a request without
// an authenticated client means a wiring bug, so it fails closed with 500.
func Middleware(l *Limiter, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		client, ok := auth.ClientFrom(r.Context())
		if !ok {
			slog.Error("rate limiter reached without an authenticated client", "path", r.URL.Path)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		allowed, retryAfter := l.Allow(client)
		if !allowed {
			seconds := int(math.Ceil(retryAfter.Seconds()))
			w.Header().Set("Retry-After", strconv.Itoa(seconds))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			io.WriteString(w, `{"error":{"message":"Rate limit exceeded. Retry after the time in the Retry-After header.","type":"requests","code":"rate_limit_exceeded"}}`)
			return
		}
		next.ServeHTTP(w, r)
	})
}
