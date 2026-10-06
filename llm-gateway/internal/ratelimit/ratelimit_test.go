package ratelimit_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/informaticadiaz/proyectos-go/llm-gateway/internal/auth"
	"github.com/informaticadiaz/proyectos-go/llm-gateway/internal/ratelimit"
)

// clock is a manually advanced time source.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func newClock() *clock { return &clock{now: time.Unix(1_700_000_000, 0)} }

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func TestBurstThenReject(t *testing.T) {
	clk := newClock()
	l := ratelimit.New(60, 3, clk.Now) // one token per second, burst of 3

	for i := range 3 {
		if ok, _ := l.Allow("alice"); !ok {
			t.Fatalf("request %d rejected within burst", i+1)
		}
	}
	ok, retry := l.Allow("alice")
	if ok {
		t.Fatal("request beyond burst allowed")
	}
	if retry != time.Second {
		t.Errorf("retryAfter = %v, want 1s", retry)
	}
}

func TestRefillsOverTime(t *testing.T) {
	clk := newClock()
	l := ratelimit.New(60, 2, clk.Now)
	l.Allow("alice")
	l.Allow("alice")

	clk.Advance(500 * time.Millisecond)
	if ok, retry := l.Allow("alice"); ok || retry != 500*time.Millisecond {
		t.Fatalf("half refilled: ok=%v retry=%v, want false 500ms", ok, retry)
	}

	clk.Advance(500 * time.Millisecond)
	if ok, _ := l.Allow("alice"); !ok {
		t.Fatal("token not refilled after 1s")
	}
}

func TestRefillIsCappedAtBurst(t *testing.T) {
	clk := newClock()
	l := ratelimit.New(60, 2, clk.Now)
	l.Allow("alice")

	clk.Advance(time.Hour)
	allowed := 0
	for range 5 {
		if ok, _ := l.Allow("alice"); ok {
			allowed++
		}
	}
	if allowed != 2 {
		t.Errorf("allowed %d after long idle, want burst of 2", allowed)
	}
}

func TestClientsHaveIndependentBuckets(t *testing.T) {
	clk := newClock()
	l := ratelimit.New(60, 1, clk.Now)

	if ok, _ := l.Allow("alice"); !ok {
		t.Fatal("alice rejected")
	}
	if ok, _ := l.Allow("bob"); !ok {
		t.Fatal("bob rejected because alice spent her token")
	}
}

func TestConcurrentRequestsNeverExceedBurst(t *testing.T) {
	clk := newClock() // frozen: no refill during the test
	l := ratelimit.New(60, 10, clk.Now)

	var allowed atomic.Int32
	var wg sync.WaitGroup
	for range 100 {
		wg.Go(func() {
			if ok, _ := l.Allow("alice"); ok {
				allowed.Add(1)
			}
		})
	}
	wg.Wait()

	if n := allowed.Load(); n != 10 {
		t.Errorf("allowed %d concurrent requests, want exactly 10", n)
	}
}

func TestMiddleware(t *testing.T) {
	clk := newClock()
	l := ratelimit.New(60, 1, clk.Now)
	var calls atomic.Int32
	handler := ratelimit.Middleware(l, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
	}))

	send := func(ctx context.Context) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(ctx)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	alice := auth.WithClient(context.Background(), "alice")

	if rec := send(alice); rec.Code != http.StatusOK {
		t.Fatalf("first request: status = %d", rec.Code)
	}

	clk.Advance(200 * time.Millisecond)
	rec := send(alice)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second request: status = %d, want 429", rec.Code)
	}
	// 800ms left, rounded up to whole seconds as Retry-After requires.
	if got := rec.Header().Get("Retry-After"); got != "1" {
		t.Errorf("Retry-After = %q, want 1", got)
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Error.Code != "rate_limit_exceeded" {
		t.Errorf("body = %q", rec.Body.String())
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("next handler called %d times, want 1", n)
	}
}

func TestMiddlewareFailsClosedWithoutClient(t *testing.T) {
	l := ratelimit.New(60, 1, newClock().Now)
	called := false
	handler := ratelimit.Middleware(l, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil))

	if rec.Code != http.StatusInternalServerError || called {
		t.Errorf("status = %d, called = %v; want 500 and not called", rec.Code, called)
	}
}
