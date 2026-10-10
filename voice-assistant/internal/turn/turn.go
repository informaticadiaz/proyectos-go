// Package turn runs one push-to-talk turn of the voice assistant: it decodes
// the recorded audio, transcribes it, asks the language model for a reply
// and synthesizes that reply as speech.
//
// Each stage is a small interface so the HTTP handler can be tested with
// fakes and each adapter (ffmpeg, whisper.cpp, llm-gateway, Piper) can be
// replaced independently.
package turn

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Decoder converts audio in any supported container into 16 kHz mono 16-bit
// PCM WAV.
type Decoder interface {
	Decode(ctx context.Context, audio []byte) ([]byte, error)
}

// Transcriber turns WAV speech into text.
type Transcriber interface {
	Transcribe(ctx context.Context, wav []byte) (string, error)
}

// Chatter answers a user utterance with a short spoken-style reply.
type Chatter interface {
	Reply(ctx context.Context, text string) (string, error)
}

// Synthesizer turns text into WAV speech.
type Synthesizer interface {
	Synthesize(ctx context.Context, text string) ([]byte, error)
}

// ErrUndecodable marks input audio that is not valid media. Decoders wrap it
// so the handler can tell a client error from a broken decoder.
var ErrUndecodable = errors.New("audio could not be decoded")

// Stages are the four steps of a turn, in order.
type Stages struct {
	Decoder     Decoder
	Transcriber Transcriber
	Chatter     Chatter
	Synthesizer Synthesizer
}

// Options tune the handler.
type Options struct {
	// MaxUploadBytes caps the request body; larger bodies get 413.
	MaxUploadBytes int64
	// TurnTimeout bounds the whole pipeline; exceeding it returns 504.
	TurnTimeout time.Duration
	// Logger receives one "turn" entry per request.
	Logger *slog.Logger
}

// Stage names, used in timing headers, logs and error bodies.
const (
	stageRead       = "read"
	stageDecode     = "decode"
	stageTranscribe = "transcribe"
	stageChat       = "chat"
	stageSynthesize = "synthesize"
)

// New returns the HTTP handler serving POST /v1/turn and GET /healthz.
func New(stages Stages, opts Options) http.Handler {
	h := &handler{stages: stages, opts: opts}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, "ok\n")
	})
	mux.HandleFunc("POST /v1/turn", h.serveTurn)
	return mux
}

type handler struct {
	stages Stages
	opts   Options
}

// record collects what a turn did, for the response headers and the log.
type record struct {
	start                     time.Time
	inputBytes, outputBytes   int
	decode, transcribe        time.Duration
	chat, synthesize          time.Duration
	transcript, reply         string
	status                    int
	failedStage, errorMessage string
}

// failure is a stage error already mapped to an HTTP status.
type failure struct {
	stage  string
	status int
	msg    string
	err    error
}

func (h *handler) serveTurn(w http.ResponseWriter, r *http.Request) {
	rec := &record{start: time.Now()}
	defer h.log(r, rec)

	ctx, cancel := context.WithTimeout(r.Context(), h.opts.TurnTimeout)
	defer cancel()

	wav, f := h.run(ctx, w, r, rec)
	if f != nil {
		rec.status, rec.failedStage = f.status, f.stage
		if f.err != nil {
			rec.errorMessage = f.err.Error()
		}
		if r.Context().Err() != nil {
			// The client went away; nobody is left to read a response.
			return
		}
		writeError(w, f)
		return
	}

	hdr := w.Header()
	hdr.Set("Content-Type", "audio/wav")
	hdr.Set("Content-Length", strconv.Itoa(len(wav)))
	// Headers must be ASCII, so free text travels percent-encoded (UTF-8,
	// RFC 3986): decodeURIComponent in browsers, url.PathUnescape in Go.
	hdr.Set("X-Transcript", url.PathEscape(rec.transcript))
	hdr.Set("X-Reply", url.PathEscape(rec.reply))
	hdr.Set("X-Timing-Decode", ms(rec.decode))
	hdr.Set("X-Timing-Transcribe", ms(rec.transcribe))
	hdr.Set("X-Timing-Chat", ms(rec.chat))
	hdr.Set("X-Timing-Synthesize", ms(rec.synthesize))
	hdr.Set("X-Timing-Total", ms(time.Since(rec.start)))
	rec.status, rec.outputBytes = http.StatusOK, len(wav)
	w.WriteHeader(http.StatusOK)
	w.Write(wav)
}

// run executes the pipeline and returns the reply audio or the first
// failure.
func (h *handler) run(ctx context.Context, w http.ResponseWriter, r *http.Request, rec *record) ([]byte, *failure) {
	audio, err := io.ReadAll(http.MaxBytesReader(w, r.Body, h.opts.MaxUploadBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return nil, &failure{stageRead, http.StatusRequestEntityTooLarge,
				"audio exceeds " + strconv.FormatInt(tooLarge.Limit, 10) + " bytes", err}
		}
		return nil, &failure{stageRead, http.StatusBadRequest, "could not read request body", err}
	}
	rec.inputBytes = len(audio)
	if len(audio) == 0 {
		return nil, &failure{stageRead, http.StatusBadRequest, "request body is empty; send the recorded audio", nil}
	}

	start := time.Now()
	wav, err := h.stages.Decoder.Decode(ctx, audio)
	rec.decode = time.Since(start)
	if err != nil {
		if errors.Is(err, ErrUndecodable) && ctx.Err() == nil {
			return nil, &failure{stageDecode, http.StatusBadRequest, "audio could not be decoded", err}
		}
		return nil, stageFailure(ctx, stageDecode, http.StatusInternalServerError, err)
	}

	start = time.Now()
	text, err := h.stages.Transcriber.Transcribe(ctx, wav)
	rec.transcribe = time.Since(start)
	if err != nil {
		return nil, stageFailure(ctx, stageTranscribe, http.StatusBadGateway, err)
	}
	rec.transcript = strings.TrimSpace(text)
	if rec.transcript == "" {
		return nil, &failure{stageTranscribe, http.StatusUnprocessableEntity, "no speech detected", nil}
	}

	start = time.Now()
	reply, err := h.stages.Chatter.Reply(ctx, rec.transcript)
	rec.chat = time.Since(start)
	if err != nil {
		return nil, stageFailure(ctx, stageChat, http.StatusBadGateway, err)
	}
	rec.reply = strings.TrimSpace(reply)
	if rec.reply == "" {
		return nil, &failure{stageChat, http.StatusBadGateway, "the language model returned an empty reply", nil}
	}

	start = time.Now()
	out, err := h.stages.Synthesizer.Synthesize(ctx, rec.reply)
	rec.synthesize = time.Since(start)
	if err != nil {
		return nil, stageFailure(ctx, stageSynthesize, http.StatusBadGateway, err)
	}
	return out, nil
}

// stageFailure maps a stage error to a status: a turn that ran out of time
// is a 504 whatever the stage, anything else gets the stage's own status.
func stageFailure(ctx context.Context, stage string, status int, err error) *failure {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return &failure{stage, http.StatusGatewayTimeout, "turn timed out during " + stage, err}
	}
	return &failure{stage, status, stage + " failed", err}
}

func writeError(w http.ResponseWriter, f *failure) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(f.status)
	json.NewEncoder(w).Encode(struct {
		Error string `json:"error"`
		Stage string `json:"stage"`
	}{f.msg, f.stage})
}

func (h *handler) log(r *http.Request, rec *record) {
	attrs := []any{
		"status", rec.status,
		"remote", r.RemoteAddr,
		"content_type", r.Header.Get("Content-Type"),
		"input_bytes", rec.inputBytes,
		"output_bytes", rec.outputBytes,
		"decode_ms", rec.decode.Milliseconds(),
		"transcribe_ms", rec.transcribe.Milliseconds(),
		"chat_ms", rec.chat.Milliseconds(),
		"synthesize_ms", rec.synthesize.Milliseconds(),
		"total_ms", time.Since(rec.start).Milliseconds(),
		"transcript", rec.transcript,
		"reply", rec.reply,
	}
	if rec.failedStage != "" {
		attrs = append(attrs, "stage", rec.failedStage, "err", rec.errorMessage)
		h.opts.Logger.Warn("turn", attrs...)
		return
	}
	h.opts.Logger.Info("turn", attrs...)
}

func ms(d time.Duration) string { return strconv.FormatInt(d.Milliseconds(), 10) }
