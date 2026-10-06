package auth_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/informaticadiaz/proyectos-go/llm-gateway/internal/auth"
)

func mustStore(t *testing.T, lines ...string) *auth.Store {
	t.Helper()
	store, err := auth.ParseKeys(strings.NewReader(strings.Join(lines, "\n")))
	if err != nil {
		t.Fatalf("ParseKeys: %v", err)
	}
	return store
}

func TestGeneratedKeyRoundTrip(t *testing.T) {
	key, err := auth.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	if !strings.HasPrefix(key, "gw_") {
		t.Errorf("key %q lacks gw_ prefix", key)
	}
	other, _ := auth.GenerateKey()
	if key == other {
		t.Error("two generated keys are equal")
	}

	store := mustStore(t, "alice:"+auth.HashKey(key))
	client, ok := store.Lookup(key)
	if !ok || client != "alice" {
		t.Errorf("Lookup = %q, %v; want alice, true", client, ok)
	}
	if _, ok := store.Lookup(other); ok {
		t.Error("unknown key accepted")
	}
}

func TestParseKeysSkipsCommentsAndBlankLines(t *testing.T) {
	store := mustStore(t,
		"# clients",
		"",
		"alice:"+auth.HashKey("gw_a"),
		"  bob:"+auth.HashKey("gw_b")+"  ",
	)
	if store.Len() != 2 {
		t.Errorf("Len = %d, want 2", store.Len())
	}
}

func TestParseKeysRejectsInvalidLines(t *testing.T) {
	hash := auth.HashKey("gw_a")
	cases := map[string]string{
		"missing separator": "alice" + hash,
		"empty name":        ":" + hash,
		"short hash":        "alice:abc123",
		"non-hex hash":      "alice:" + strings.Repeat("z", 64),
		"duplicate name":    "alice:" + hash + "\nalice:" + auth.HashKey("gw_b"),
		"duplicate hash":    "alice:" + hash + "\nbob:" + hash,
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := auth.ParseKeys(strings.NewReader(input)); err == nil {
				t.Error("expected error")
			}
		})
	}
}

func TestParseKeysRejectsEmptyStore(t *testing.T) {
	if _, err := auth.ParseKeys(strings.NewReader("# nothing here\n")); err == nil {
		t.Error("expected error for a file without keys")
	}
}

func TestMiddleware(t *testing.T) {
	store := mustStore(t, "alice:"+auth.HashKey("gw_valid"))

	var gotClient string
	var gotAuthHeader string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotClient, _ = auth.ClientFrom(r.Context())
		gotAuthHeader = r.Header.Get("Authorization")
	})
	handler := auth.Middleware(store, next)

	cases := []struct {
		name   string
		header string
		status int
	}{
		{"valid key", "Bearer gw_valid", http.StatusOK},
		{"lowercase scheme", "bearer gw_valid", http.StatusOK},
		{"missing header", "", http.StatusUnauthorized},
		{"wrong scheme", "Basic gw_valid", http.StatusUnauthorized},
		{"unknown key", "Bearer gw_other", http.StatusUnauthorized},
		{"empty key", "Bearer ", http.StatusUnauthorized},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotClient = ""
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d", rec.Code, tc.status)
			}
			if tc.status == http.StatusOK {
				if gotClient != "alice" {
					t.Errorf("client in context = %q, want alice", gotClient)
				}
				if gotAuthHeader != "" {
					t.Error("Authorization header must not reach the next handler")
				}
				return
			}
			if gotClient != "" {
				t.Error("next handler ran for a rejected request")
			}
			assertOpenAIError(t, rec)
		})
	}
}

// assertOpenAIError checks that rejections use the OpenAI error envelope,
// so existing SDKs surface a meaningful message.
func assertOpenAIError(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
	if rec.Header().Get("WWW-Authenticate") != "Bearer" {
		t.Error("missing WWW-Authenticate: Bearer")
	}
	var body struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body %q: %v", rec.Body.String(), err)
	}
	if body.Error.Code != "invalid_api_key" || body.Error.Message == "" {
		t.Errorf("error = %+v", body.Error)
	}
}
