// Package upstream holds helpers shared by the HTTP clients of the resident
// servers (whisper.cpp, llm-gateway, Piper).
package upstream

import (
	"fmt"
	"io"
	"net/http"
	"strings"
)

// maxErrorBody bounds how much of an error response ends up in a message.
const maxErrorBody = 512

// Check returns an error naming the service, status and the start of the
// body when resp is not a 2xx response. It does not close the body.
func Check(service string, resp *http.Response) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	return fmt.Errorf("%s returned %d: %s", service, resp.StatusCode, strings.TrimSpace(string(body)))
}

// ReadAll reads a response body of at most limit bytes.
func ReadAll(service string, r io.Reader, limit int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read %s response: %w", service, err)
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("%s response exceeds %d bytes", service, limit)
	}
	return b, nil
}
