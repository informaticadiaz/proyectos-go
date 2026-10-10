// Package web serves the push-to-talk page: one HTML document, its script,
// stylesheet and icon, embedded in the binary. The page records speech with
// MediaRecorder, posts it to /v1/turn and plays the spoken reply.
package web

import (
	"embed"
	"net/http"
)

//go:embed static
var static embed.FS

// assets maps each served path to its embedded file and content type. The
// types are fixed here rather than taken from the system MIME tables, which
// differ between machines.
var assets = map[string]struct{ file, contentType string }{
	"/{$}":       {"static/index.html", "text/html; charset=utf-8"},
	"/app.js":    {"static/app.js", "text/javascript; charset=utf-8"},
	"/style.css": {"static/style.css", "text/css; charset=utf-8"},
	"/icon.svg":  {"static/icon.svg", "image/svg+xml"},
}

// Security headers for a same-origin page: only its own files may run or
// load, media may also come from blob: URLs (the recorded and received
// audio), the microphone is allowed for this origin only, and the page may
// not be framed.
var securityHeaders = map[string]string{
	"Content-Security-Policy": "default-src 'none'; script-src 'self'; style-src 'self'; " +
		"img-src 'self'; media-src 'self' blob:; connect-src 'self'; " +
		"base-uri 'none'; form-action 'none'; frame-ancestors 'none'",
	"Permissions-Policy":     "microphone=(self), camera=(), geolocation=()",
	"X-Content-Type-Options": "nosniff",
	"Referrer-Policy":        "no-referrer",
	"X-Frame-Options":        "DENY",
}

// Handler serves the page at GET / and its assets; any other path is 404.
func Handler() http.Handler {
	mux := http.NewServeMux()
	for pattern, a := range assets {
		body, err := static.ReadFile(a.file)
		if err != nil {
			panic("web: missing embedded file " + a.file) // caught by the tests
		}
		contentType := a.contentType
		mux.HandleFunc("GET "+pattern, func(w http.ResponseWriter, _ *http.Request) {
			h := w.Header()
			h.Set("Content-Type", contentType)
			// Revalidate on every load so a rebuilt binary reaches the phone.
			h.Set("Cache-Control", "no-cache")
			w.Write(body)
		})
	}
	return withSecurityHeaders(mux)
}

func withSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for name, value := range securityHeaders {
			w.Header().Set(name, value)
		}
		next.ServeHTTP(w, r)
	})
}
