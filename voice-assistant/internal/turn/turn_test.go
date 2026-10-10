package turn_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/informaticadiaz/proyectos-go/voice-assistant/internal/turn"
)

// Fakes implement each stage with a function.
type decodeFunc func(context.Context, []byte) ([]byte, error)

func (f decodeFunc) Decode(ctx context.Context, b []byte) ([]byte, error) { return f(ctx, b) }

type transcribeFunc func(context.Context, []byte) (string, error)

func (f transcribeFunc) Transcribe(ctx context.Context, b []byte) (string, error) { return f(ctx, b) }

type replyFunc func(context.Context, string) (string, error)

func (f replyFunc) Reply(ctx context.Context, s string) (string, error) { return f(ctx, s) }

type synthFunc func(context.Context, string) ([]byte, error)

func (f synthFunc) Synthesize(ctx context.Context, s string) ([]byte, error) { return f(ctx, s) }

const (
	transcript = "¿Qué hora es en Buenos Aires?"
	reply      = "Son las tres, 100% seguro."
	replyWAV   = "RIFF....WAVEfmt reply"
)

// happyStages chains the stages and checks each one receives the output of
// the previous stage.
func happyStages(t *testing.T) turn.Stages {
	return turn.Stages{
		Decoder: decodeFunc(func(_ context.Context, b []byte) ([]byte, error) {
			if string(b) != "webm-bytes" {
				t.Errorf("decoder got %q", b)
			}
			return []byte("pcm-wav"), nil
		}),
		Transcriber: transcribeFunc(func(_ context.Context, b []byte) (string, error) {
			if string(b) != "pcm-wav" {
				t.Errorf("transcriber got %q", b)
			}
			return transcript, nil
		}),
		Chatter: replyFunc(func(_ context.Context, s string) (string, error) {
			if s != transcript {
				t.Errorf("chatter got %q", s)
			}
			return reply, nil
		}),
		Synthesizer: synthFunc(func(_ context.Context, s string) ([]byte, error) {
			if s != reply {
				t.Errorf("synthesizer got %q", s)
			}
			return []byte(replyWAV), nil
		}),
	}
}

func newServer(t *testing.T, stages turn.Stages, opts turn.Options) *httptest.Server {
	t.Helper()
	if opts.MaxUploadBytes == 0 {
		opts.MaxUploadBytes = 1 << 20
	}
	if opts.TurnTimeout == 0 {
		opts.TurnTimeout = 5 * time.Second
	}
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	srv := httptest.NewServer(turn.New(stages, opts))
	t.Cleanup(srv.Close)
	return srv
}

func post(t *testing.T, target, body string) *http.Response {
	t.Helper()
	resp, err := http.Post(target, "audio/webm", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST %s: %v", target, err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestTurnSuccess(t *testing.T) {
	var logs bytes.Buffer
	srv := newServer(t, happyStages(t), turn.Options{Logger: slog.New(slog.NewJSONHandler(&logs, nil))})

	resp := post(t, srv.URL+"/v1/turn", "webm-bytes")
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body %q", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "audio/wav" {
		t.Errorf("Content-Type = %q", ct)
	}
	if string(body) != replyWAV {
		t.Errorf("body = %q", body)
	}

	for header, want := range map[string]string{"X-Transcript": transcript, "X-Reply": reply} {
		raw := resp.Header.Get(header)
		for _, r := range raw {
			if r > 127 {
				t.Errorf("%s is not ASCII: %q", header, raw)
				break
			}
		}
		got, err := url.PathUnescape(raw)
		if err != nil || got != want {
			t.Errorf("%s = %q decodes to %q (%v), want %q", header, raw, got, err, want)
		}
	}
	for _, h := range []string{"X-Timing-Decode", "X-Timing-Transcribe", "X-Timing-Chat", "X-Timing-Synthesize", "X-Timing-Total"} {
		var ms int64
		if _, err := fmt.Sscanf(resp.Header.Get(h), "%d", &ms); err != nil || ms < 0 {
			t.Errorf("%s = %q, want a non-negative integer", h, resp.Header.Get(h))
		}
	}

	var entry map[string]any
	if err := json.Unmarshal(logs.Bytes(), &entry); err != nil {
		t.Fatalf("log line %q: %v", logs.String(), err)
	}
	if entry["msg"] != "turn" || entry["status"] != float64(200) ||
		entry["transcript"] != transcript || entry["reply"] != reply {
		t.Errorf("log entry = %v", entry)
	}
	for _, k := range []string{"decode_ms", "transcribe_ms", "chat_ms", "synthesize_ms", "total_ms", "input_bytes", "output_bytes"} {
		if _, ok := entry[k]; !ok {
			t.Errorf("log entry lacks %q: %v", k, entry)
		}
	}
}

func TestStageFailures(t *testing.T) {
	upstream := errors.New("connection refused")
	cases := []struct {
		name   string
		break_ func(*turn.Stages)
		status int
		stage  string
	}{
		{"undecodable audio", func(s *turn.Stages) {
			s.Decoder = decodeFunc(func(context.Context, []byte) ([]byte, error) {
				return nil, fmt.Errorf("%w: invalid data", turn.ErrUndecodable)
			})
		}, http.StatusBadRequest, "decode"},
		{"decoder broken", func(s *turn.Stages) {
			s.Decoder = decodeFunc(func(context.Context, []byte) ([]byte, error) {
				return nil, errors.New("ffmpeg: executable not found")
			})
		}, http.StatusInternalServerError, "decode"},
		{"whisper down", func(s *turn.Stages) {
			s.Transcriber = transcribeFunc(func(context.Context, []byte) (string, error) { return "", upstream })
		}, http.StatusBadGateway, "transcribe"},
		{"no speech", func(s *turn.Stages) {
			s.Transcriber = transcribeFunc(func(context.Context, []byte) (string, error) { return "  ", nil })
		}, http.StatusUnprocessableEntity, "transcribe"},
		{"gateway down", func(s *turn.Stages) {
			s.Chatter = replyFunc(func(context.Context, string) (string, error) { return "", upstream })
		}, http.StatusBadGateway, "chat"},
		{"piper down", func(s *turn.Stages) {
			s.Synthesizer = synthFunc(func(context.Context, string) ([]byte, error) { return nil, upstream })
		}, http.StatusBadGateway, "synthesize"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			stages := happyStages(t)
			c.break_(&stages)
			var logs bytes.Buffer
			srv := newServer(t, stages, turn.Options{Logger: slog.New(slog.NewJSONHandler(&logs, nil))})

			resp := post(t, srv.URL+"/v1/turn", "webm-bytes")
			if resp.StatusCode != c.status {
				t.Fatalf("status = %d, want %d", resp.StatusCode, c.status)
			}
			var e struct {
				Error string `json:"error"`
				Stage string `json:"stage"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&e); err != nil {
				t.Fatalf("decode error body: %v", err)
			}
			if e.Stage != c.stage || e.Error == "" {
				t.Errorf("error body = %+v, want stage %q", e, c.stage)
			}
			if !strings.Contains(logs.String(), `"stage":"`+c.stage+`"`) {
				t.Errorf("log lacks failed stage: %s", logs.String())
			}
		})
	}
}

func TestTimeoutReachesStages(t *testing.T) {
	stages := happyStages(t)
	stages.Chatter = replyFunc(func(ctx context.Context, _ string) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	})
	srv := newServer(t, stages, turn.Options{TurnTimeout: 50 * time.Millisecond})

	resp := post(t, srv.URL+"/v1/turn", "webm-bytes")
	if resp.StatusCode != http.StatusGatewayTimeout {
		t.Errorf("status = %d, want 504", resp.StatusCode)
	}
}

func TestClientCancellationReachesStages(t *testing.T) {
	stopped := make(chan struct{})
	stages := happyStages(t)
	stages.Transcriber = transcribeFunc(func(ctx context.Context, _ []byte) (string, error) {
		<-ctx.Done()
		close(stopped)
		return "", ctx.Err()
	})
	srv := newServer(t, stages, turn.Options{TurnTimeout: time.Minute})

	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/v1/turn", strings.NewReader("webm-bytes"))
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	if resp, err := http.DefaultClient.Do(req); err == nil {
		resp.Body.Close()
	}
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("transcriber context was not cancelled with the client")
	}
}

func TestRejectsLargeUpload(t *testing.T) {
	srv := newServer(t, happyStages(t), turn.Options{MaxUploadBytes: 4})
	resp := post(t, srv.URL+"/v1/turn", "webm-bytes")
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", resp.StatusCode)
	}
}

func TestRejectsEmptyBody(t *testing.T) {
	srv := newServer(t, happyStages(t), turn.Options{})
	resp := post(t, srv.URL+"/v1/turn", "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestRejectsWrongMethod(t *testing.T) {
	srv := newServer(t, happyStages(t), turn.Options{})
	resp, err := http.Get(srv.URL + "/v1/turn")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", resp.StatusCode)
	}
}

func TestHealthz(t *testing.T) {
	srv := newServer(t, happyStages(t), turn.Options{})
	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d", resp.StatusCode)
	}
}
