package web_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/informaticadiaz/proyectos-go/voice-assistant/internal/web"
)

func get(t *testing.T, method, target string) (*http.Response, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	web.Handler().ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	resp := rec.Result()
	body, _ := io.ReadAll(resp.Body)
	return resp, string(body)
}

func TestServesEmbeddedFiles(t *testing.T) {
	for _, tc := range []struct {
		path, file, contentType string
	}{
		{"/", "index.html", "text/html; charset=utf-8"},
		{"/app.js", "app.js", "text/javascript; charset=utf-8"},
		{"/style.css", "style.css", "text/css; charset=utf-8"},
		{"/icon.svg", "icon.svg", "image/svg+xml"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			resp, body := get(t, http.MethodGet, tc.path)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d", resp.StatusCode)
			}
			if ct := resp.Header.Get("Content-Type"); ct != tc.contentType {
				t.Errorf("Content-Type = %q, want %q", ct, tc.contentType)
			}
			want, err := os.ReadFile(filepath.Join("static", tc.file))
			if err != nil {
				t.Fatal(err)
			}
			if body != string(want) {
				t.Errorf("body differs from static/%s", tc.file)
			}
			if cc := resp.Header.Get("Cache-Control"); cc != "no-cache" {
				t.Errorf("Cache-Control = %q, want no-cache", cc)
			}
		})
	}
}

func TestPageLinksItsAssets(t *testing.T) {
	_, body := get(t, http.MethodGet, "/")
	for _, ref := range []string{`src="/app.js"`, `href="/style.css"`, `href="/icon.svg"`, `lang="es"`, `name="viewport"`} {
		if !strings.Contains(body, ref) {
			t.Errorf("index.html lacks %s", ref)
		}
	}
	// The strict CSP forbids inline code; keep the page free of it.
	for _, inline := range []string{"<script>", "<style", " style=", " onclick="} {
		if strings.Contains(body, inline) {
			t.Errorf("index.html contains inline %q, which the CSP blocks", inline)
		}
	}
}

func TestHeadIsAllowed(t *testing.T) {
	resp, _ := get(t, http.MethodHead, "/")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("HEAD / status = %d", resp.StatusCode)
	}
}

func TestUnknownPathsAre404(t *testing.T) {
	for _, path := range []string{"/nope", "/index.html", "/static/app.js", "/app.js/", "/web.go", "/favicon.ico"} {
		if resp, _ := get(t, http.MethodGet, path); resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s status = %d, want 404", path, resp.StatusCode)
		}
	}
}

func TestOtherMethodsAre405(t *testing.T) {
	if resp, _ := get(t, http.MethodPost, "/"); resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST / status = %d, want 405", resp.StatusCode)
	}
}

func TestSecurityHeaders(t *testing.T) {
	want := map[string]string{
		"Content-Security-Policy": "default-src 'none'; script-src 'self'; style-src 'self'; " +
			"img-src 'self'; media-src 'self' blob:; connect-src 'self'; " +
			"base-uri 'none'; form-action 'none'; frame-ancestors 'none'",
		"Permissions-Policy":     "microphone=(self), camera=(), geolocation=()",
		"X-Content-Type-Options": "nosniff",
		"Referrer-Policy":        "no-referrer",
		"X-Frame-Options":        "DENY",
	}
	for _, path := range []string{"/", "/app.js", "/nope"} {
		resp, _ := get(t, http.MethodGet, path)
		for name, value := range want {
			if got := resp.Header.Get(name); got != value {
				t.Errorf("GET %s %s = %q, want %q", path, name, got, value)
			}
		}
	}
}
