package observe

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
)

// maxInspected bounds the memory used to inspect one JSON response or one
// SSE line. Larger payloads still reach the client; only their usage is lost.
const maxInspected = 1 << 20

// inspector passes the upstream body through unchanged while extracting the
// model and token usage. JSON responses are parsed once fully read; SSE
// streams are parsed line by line as chunks arrive.
type inspector struct {
	body   io.ReadCloser
	rec    *record
	stream bool

	buf      []byte
	overflow bool // current payload or line exceeded maxInspected
	done     bool
}

func newInspector(body io.ReadCloser, rec *record, stream bool) *inspector {
	rec.stream = stream
	return &inspector{body: body, rec: rec, stream: stream}
}

func (in *inspector) Read(p []byte) (int, error) {
	n, err := in.body.Read(p)
	if n > 0 {
		in.feed(p[:n])
	}
	if err == io.EOF {
		in.finish()
	}
	return n, err
}

func (in *inspector) Close() error {
	in.finish()
	return in.body.Close()
}

func (in *inspector) feed(p []byte) {
	if !in.stream {
		if in.overflow || len(in.buf)+len(p) > maxInspected {
			in.overflow, in.buf = true, nil
			return
		}
		in.buf = append(in.buf, p...)
		return
	}

	for len(p) > 0 {
		i := bytes.IndexByte(p, '\n')
		if i < 0 {
			in.appendLine(p)
			return
		}
		in.appendLine(p[:i])
		if !in.overflow {
			in.parseEvent(in.buf)
		}
		in.buf, in.overflow = in.buf[:0], false
		p = p[i+1:]
	}
}

// appendLine accumulates a partial SSE line, dropping lines that grow
// beyond maxInspected.
func (in *inspector) appendLine(p []byte) {
	if in.overflow {
		return
	}
	if len(in.buf)+len(p) > maxInspected {
		in.overflow, in.buf = true, in.buf[:0]
		return
	}
	in.buf = append(in.buf, p...)
}

func (in *inspector) finish() {
	if in.done {
		return
	}
	in.done = true
	switch {
	case in.overflow:
	case in.stream:
		in.parseEvent(in.buf) // a final line without trailing newline
	default:
		in.parse(in.buf)
	}
	in.buf = nil
}

// parseEvent handles one SSE line; only "data:" lines carry chunks.
func (in *inspector) parseEvent(line []byte) {
	payload, ok := bytes.CutPrefix(bytes.TrimRight(line, "\r"), []byte("data:"))
	if !ok {
		return
	}
	payload = bytes.TrimSpace(payload)
	if string(payload) == "[DONE]" {
		return
	}
	in.parse(payload)
}

// parse reads the fields shared by OpenAI responses and stream chunks.
func (in *inspector) parse(data []byte) {
	var msg struct {
		Model string `json:"model"`
		Usage *struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal(data, &msg) != nil {
		return
	}
	if msg.Model != "" {
		in.rec.model = msg.Model
	}
	if msg.Usage != nil {
		in.rec.usageKnown = true
		in.rec.promptTokens = msg.Usage.PromptTokens
		in.rec.completionTokens = msg.Usage.CompletionTokens
	}
}

func isEventStream(contentType string) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	return err == nil && mediaType == "text/event-stream"
}
