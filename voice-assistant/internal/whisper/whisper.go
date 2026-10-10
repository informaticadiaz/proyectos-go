// Package whisper is a client for the whisper.cpp HTTP server
// (examples/server, binary whisper-server).
package whisper

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/informaticadiaz/proyectos-go/voice-assistant/internal/upstream"
)

const service = "whisper"

// Client transcribes WAV audio with a resident whisper-server, so the model
// is loaded once instead of on every turn.
type Client struct {
	endpoint string
	language string
	http     *http.Client
}

// New returns a client for the server at base, transcribing in language
// (an ISO 639-1 code such as "es", or "auto").
func New(base *url.URL, language string, client *http.Client) *Client {
	return &Client{
		endpoint: base.JoinPath("inference").String(),
		language: language,
		http:     client,
	}
}

// Transcribe sends wav to POST /inference as multipart form data and
// returns the recognised text without surrounding whitespace.
func (c *Client) Transcribe(ctx context.Context, wav []byte) (string, error) {
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("file", "turn.wav")
	if err != nil {
		return "", err
	}
	part.Write(wav)
	form.WriteField("response_format", "json")
	form.WriteField("language", c.language)
	if err := form.Close(); err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", form.FormDataContentType())
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("whisper request: %w", err)
	}
	defer resp.Body.Close()
	if err := upstream.Check(service, resp); err != nil {
		return "", err
	}

	raw, err := upstream.ReadAll(service, resp.Body, 1<<20)
	if err != nil {
		return "", err
	}
	var out struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("decode whisper response: %w", err)
	}
	return speechOnly(out.Text), nil
}

// annotation matches the bracketed tags whisper emits for non-speech sounds,
// such as "[MÚSICA]" or "(risas)". On silence it often returns only a tag,
// which would otherwise be answered as if the user had said it.
var annotation = regexp.MustCompile(`\[[^\]]*\]|\([^)]*\)`)

// speechOnly drops annotations and normalises whitespace.
func speechOnly(text string) string {
	return strings.Join(strings.Fields(annotation.ReplaceAllString(text, " ")), " ")
}
