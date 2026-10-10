// Package piper is a client for Piper's HTTP server
// (python -m piper.http_server).
package piper

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/informaticadiaz/proyectos-go/voice-assistant/internal/upstream"
)

const service = "piper"

// maxAudio bounds the synthesized reply (about 15 minutes at 22 kHz).
const maxAudio = 64 << 20

// Client synthesizes speech with a resident Piper server, so the voice is
// loaded once instead of on every turn.
type Client struct {
	endpoint string
	http     *http.Client
}

// New returns a client for the server at base.
func New(base *url.URL, client *http.Client) *Client {
	return &Client{endpoint: base.JoinPath("synthesize").String(), http: client}
}

// Synthesize posts {"text": text} to POST /synthesize and returns the WAV
// audio. The server's default voice and synthesis settings are used.
func (c *Client) Synthesize(ctx context.Context, text string) ([]byte, error) {
	payload, err := json.Marshal(struct {
		Text string `json:"text"`
	}{text})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("piper request: %w", err)
	}
	defer resp.Body.Close()
	if err := upstream.Check(service, resp); err != nil {
		return nil, err
	}

	wav, err := upstream.ReadAll(service, resp.Body, maxAudio)
	if err != nil {
		return nil, err
	}
	// Piper's Flask server labels the WAV as text/html, so check the bytes.
	if !bytes.HasPrefix(wav, []byte("RIFF")) {
		return nil, fmt.Errorf("%s returned %d bytes that are not WAV", service, len(wav))
	}
	return wav, nil
}
