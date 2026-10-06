package observe

import (
	"fmt"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// metrics holds counters keyed by label values. Label cardinality is
// bounded: clients come from the keys file, models from the upstream.
type metrics struct {
	mu       sync.Mutex
	requests map[[2]string]uint64 // client, code
	tokens   map[[3]string]uint64 // client, model, type
	durSum   map[string]float64   // client
	durCount map[string]uint64    // client
}

func newMetrics() *metrics {
	return &metrics{
		requests: make(map[[2]string]uint64),
		tokens:   make(map[[3]string]uint64),
		durSum:   make(map[string]float64),
		durCount: make(map[string]uint64),
	}
}

func (m *metrics) observe(client string, status int, d time.Duration, rec *record) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.requests[[2]string{client, strconv.Itoa(status)}]++
	m.durSum[client] += d.Seconds()
	m.durCount[client]++
	if rec.usageKnown {
		m.tokens[[3]string{client, rec.model, "prompt"}] += uint64(rec.promptTokens)
		m.tokens[[3]string{client, rec.model, "completion"}] += uint64(rec.completionTokens)
	}
}

// writeTo renders the metrics in Prometheus text exposition format, with
// series sorted so the output is stable.
func (m *metrics) writeTo(w io.Writer) {
	m.mu.Lock()
	defer m.mu.Unlock()

	fmt.Fprintln(w, "# HELP gateway_requests_total Requests served, by client and status code.")
	fmt.Fprintln(w, "# TYPE gateway_requests_total counter")
	for _, k := range slices.SortedFunc(maps.Keys(m.requests), compareKeys) {
		fmt.Fprintf(w, "gateway_requests_total{client=%s,code=%s} %d\n", label(k[0]), label(k[1]), m.requests[k])
	}

	fmt.Fprintln(w, "# HELP gateway_tokens_total Tokens reported by the upstream, by client, model and type.")
	fmt.Fprintln(w, "# TYPE gateway_tokens_total counter")
	for _, k := range slices.SortedFunc(maps.Keys(m.tokens), compareKeys) {
		fmt.Fprintf(w, "gateway_tokens_total{client=%s,model=%s,type=%s} %d\n", label(k[0]), label(k[1]), label(k[2]), m.tokens[k])
	}

	fmt.Fprintln(w, "# HELP gateway_request_duration_seconds Request latency, by client.")
	fmt.Fprintln(w, "# TYPE gateway_request_duration_seconds summary")
	for _, c := range slices.Sorted(maps.Keys(m.durCount)) {
		fmt.Fprintf(w, "gateway_request_duration_seconds_sum{client=%s} %s\n", label(c), strconv.FormatFloat(m.durSum[c], 'g', -1, 64))
		fmt.Fprintf(w, "gateway_request_duration_seconds_count{client=%s} %d\n", label(c), m.durCount[c])
	}
}

func compareKeys[K [2]string | [3]string](a, b K) int {
	for i := range len(a) {
		if c := strings.Compare(a[i], b[i]); c != 0 {
			return c
		}
	}
	return 0
}

var labelEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)

// label quotes a label value, escaping it as the exposition format requires.
func label(v string) string {
	return `"` + labelEscaper.Replace(v) + `"`
}
