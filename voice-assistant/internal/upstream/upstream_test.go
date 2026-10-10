package upstream_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/informaticadiaz/proyectos-go/voice-assistant/internal/upstream"
)

func TestCheckAcceptsSuccess(t *testing.T) {
	resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("x"))}
	if err := upstream.Check("svc", resp); err != nil {
		t.Errorf("err = %v", err)
	}
}

func TestCheckTruncatesErrorBody(t *testing.T) {
	resp := &http.Response{StatusCode: http.StatusBadGateway, Body: io.NopCloser(strings.NewReader(strings.Repeat("e", 4096)))}
	err := upstream.Check("svc", resp)
	if err == nil || !strings.HasPrefix(err.Error(), "svc returned 502: ") || len(err.Error()) > 600 {
		t.Errorf("err = %v", err)
	}
}

func TestReadAllEnforcesLimit(t *testing.T) {
	if b, err := upstream.ReadAll("svc", strings.NewReader("1234"), 4); err != nil || string(b) != "1234" {
		t.Errorf("at limit: %q, %v", b, err)
	}
	if _, err := upstream.ReadAll("svc", strings.NewReader("12345"), 4); err == nil {
		t.Error("expected error over the limit")
	}
}
