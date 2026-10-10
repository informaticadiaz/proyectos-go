// Package chat asks an OpenAI-compatible chat completions endpoint (the
// llm-gateway) for a spoken-style reply.
package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/informaticadiaz/proyectos-go/voice-assistant/internal/upstream"
)

const service = "llm-gateway"

// SystemPrompt shapes replies for speech: short, plain sentences in
// Spanish, without formatting a synthesizer would read aloud.
const SystemPrompt = "Sos un asistente de voz. Respondé en español, de forma breve y " +
	"natural, en una a tres oraciones cortas, como si hablaras. No uses " +
	"markdown, listas, emojis, tablas ni código. Escribí los números y " +
	"símbolos como se pronuncian cuando sea necesario."

// Client calls POST /v1/chat/completions without streaming.
type Client struct {
	endpoint  string
	key       string
	model     string
	maxTokens int
	http      *http.Client
}

// New returns a client for the gateway at base, authenticating with key.
func New(base *url.URL, key, model string, maxTokens int, client *http.Client) *Client {
	return &Client{
		endpoint:  base.JoinPath("v1", "chat", "completions").String(),
		key:       key,
		model:     model,
		maxTokens: maxTokens,
		http:      client,
	}
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Reply returns the model's answer to text, cleaned for speech.
func (c *Client) Reply(ctx context.Context, text string) (string, error) {
	payload, err := json.Marshal(struct {
		Model     string    `json:"model"`
		Messages  []message `json:"messages"`
		Stream    bool      `json:"stream"`
		MaxTokens int       `json:"max_tokens"`
	}{
		Model:     c.model,
		Messages:  []message{{"system", SystemPrompt}, {"user", text}},
		MaxTokens: c.maxTokens,
	})
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.key)
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("gateway request: %w", err)
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
		Choices []struct {
			Message message `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("decode gateway response: %w", err)
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("%s returned no choices", service)
	}
	return speakable(out.Choices[0].Message.Content), nil
}

// speakable removes markdown markers the prompt asks the model to avoid but
// small models still emit, so the synthesizer does not trip over them.
func speakable(s string) string {
	s = strings.NewReplacer("**", "", "__", "", "`", "", "#", "").Replace(s)
	return strings.Join(strings.Fields(s), " ")
}
