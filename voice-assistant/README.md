# voice-assistant

[![voice-assistant](https://github.com/informaticadiaz/proyectos-go/actions/workflows/voice-assistant.yml/badge.svg)](https://github.com/informaticadiaz/proyectos-go/actions/workflows/voice-assistant.yml)

An orchestrator written in Go for a local, Spanish-speaking voice
assistant. One push-to-talk turn goes in as recorded audio and comes back
as spoken audio, using only services that run on this machine.

## Status

| Stage | Feature | State |
| --- | --- | --- |
| 1 | `POST /v1/turn`: decode, transcribe, chat, synthesize; timings in headers and logs | Done |
| 2 | Web push-to-talk page (browser `MediaRecorder` → `/v1/turn` → playback) | Planned |
| 3 | Sentence-level streaming: speak the first LLM sentence while the rest is generated | Planned |
| 4 | Open microphone with voice activity detection (no button) | Planned |

## Architecture

```text
client ──POST /v1/turn (webm/ogg/wav/mp3…)──▶ voice-assistant
                                                │
   1. decode      ffmpeg (stdin → stdout)       │  any format → 16 kHz mono s16 WAV
   2. transcribe  whisper-server  POST /inference      → text
   3. chat        llm-gateway     POST /v1/chat/completions → reply text
   4. synthesize  Piper           POST /synthesize     → WAV
                                                │
client ◀──200 audio/wav + X-Transcript, X-Reply, X-Timing-*──┘
```

- `internal/turn` owns the pipeline. Each stage is a one-method interface
  (`Decoder`, `Transcriber`, `Chatter`, `Synthesizer`); the HTTP handler
  composes them and maps failures to status codes.
- `internal/audio`, `internal/whisper`, `internal/chat` and
  `internal/piper` are the adapters. Whisper and Piper run as resident
  servers so their models are loaded once, not on every turn.
- Standard library only.

## API

`POST /v1/turn` — the request body is the recorded audio, in any format
ffmpeg can read from a pipe. `Content-Type` is only logged.

| Status | When | Body |
| --- | --- | --- |
| `200` | Turn completed | `audio/wav` (Piper's output, 22.05 kHz mono) |
| `400` | Empty body, or audio ffmpeg cannot decode | JSON error |
| `405` | Any method other than `POST` | — |
| `413` | Body larger than `VOICE_MAX_UPLOAD_BYTES` | JSON error |
| `422` | No speech recognised (silence, or only tags such as `[MÚSICA]`) | JSON error |
| `500` | The decoder itself is broken (for example ffmpeg missing) | JSON error |
| `502` | whisper, the gateway or Piper failed or returned nothing usable | JSON error |
| `504` | The turn exceeded `VOICE_TURN_TIMEOUT` | JSON error |

Errors look like `{"error":"transcribe failed","stage":"transcribe"}`.

Response headers on `200`:

| Header | Content |
| --- | --- |
| `X-Transcript` | What the user said |
| `X-Reply` | What the assistant answered |
| `X-Timing-Decode`, `X-Timing-Transcribe`, `X-Timing-Chat`, `X-Timing-Synthesize`, `X-Timing-Total` | Milliseconds per stage |

HTTP headers must be ASCII, so `X-Transcript` and `X-Reply` are
**percent-encoded UTF-8** (RFC 3986, spaces as `%20`). Decode them with
`decodeURIComponent` in a browser or `url.PathUnescape` in Go.

Every turn, successful or not, writes one JSON log line (`"msg":"turn"`)
with status, content type, input/output bytes, the per-stage `*_ms`
timings, transcript and reply; failures add `stage` and `err`.

`GET /healthz` returns `200 ok`. It does not probe the upstream servers.

## Configuration

| Variable | Default | Description |
| --- | --- | --- |
| `VOICE_GATEWAY_KEY_FILE` | — (required) | File holding the llm-gateway API key. The key is never read from the environment |
| `VOICE_ADDR` | `127.0.0.1:8095` | Listen address |
| `VOICE_WHISPER_URL` | `http://127.0.0.1:8092` | whisper-server base URL |
| `VOICE_GATEWAY_URL` | `http://127.0.0.1:8090` | llm-gateway base URL |
| `VOICE_PIPER_URL` | `http://127.0.0.1:8093` | Piper HTTP server base URL |
| `VOICE_MODEL` | `llama3.2:3b` | Chat model requested from the gateway |
| `VOICE_LANGUAGE` | `es` | Language passed to whisper (`auto` to detect) |
| `VOICE_MAX_TOKENS` | `200` | `max_tokens` for the reply |
| `VOICE_MAX_UPLOAD_BYTES` | `10485760` | Largest accepted request body |
| `VOICE_TURN_TIMEOUT` | `2m` | Deadline for a whole turn (Go duration) |
| `VOICE_FFMPEG` | `ffmpeg` | ffmpeg binary |

## Run locally

Start the three resident servers, each bound to `127.0.0.1`:

```sh
# 1. whisper.cpp server (the shared libraries live next to the binary)
W=~/voice/vendor/whisper.cpp
LD_LIBRARY_PATH=$W/build/bin $W/build/bin/whisper-server \
  -m $W/models/ggml-small-q5_0.bin --host 127.0.0.1 --port 8092 -t 8

# 2. llm-gateway, already running as a user service on 127.0.0.1:8090.
#    Create a key for this client and store it in a 0600 file:
#    go run ../llm-gateway/cmd/gateway keygen voice-assistant

# 3. Piper HTTP server (needs Flask: pip install 'piper-tts[http]')
~/generacion-audio-texto/.venv/bin/python -m piper.http_server \
  --host 127.0.0.1 --port 8093 \
  -m ~/generacion-audio-texto/data/models/piper/es_AR-daniela-high.onnx
```

Then the orchestrator and a turn:

```sh
VOICE_GATEWAY_KEY_FILE=~/.config/voice-assistant/gateway.key go run ./cmd/voice-assistant

curl -s -o reply.wav -D - -X POST -H 'Content-Type: audio/webm' \
  --data-binary @question.webm http://127.0.0.1:8095/v1/turn
```

`SIGINT`/`SIGTERM` stop accepting turns and let in-flight ones finish (up
to `VOICE_TURN_TIMEOUT` + 5 s).

## Design notes

- ffmpeg reads stdin and writes raw PCM to stdout, so no temporary files
  are created. The orchestrator writes the WAV header itself, because
  ffmpeg cannot fill in the sizes when its output is a pipe. Reading from
  a pipe also means ffmpeg cannot seek: WebM, Ogg, WAV, MP3 and fragmented
  MP4 work, a non-fragmented MP4/M4A with its index at the end does not.
- A non-zero ffmpeg exit is a client error (`400`); a missing binary or a
  cancelled turn is not.
- whisper.cpp's API is `POST /inference` (multipart: `file`,
  `response_format=json`, `language`), answering `{"text": "..."}`. Its
  `/health` endpoint reports readiness. On silence it often returns only a
  tag such as `[MÚSICA]`; bracketed and parenthesised tags are removed, so
  silence ends as `422` instead of a reply about music.
- The chat request is non-streaming, with a short Spanish system prompt
  asking for one to three spoken sentences without markdown. Leftover
  markdown markers (`**`, `#`, backticks) are stripped before synthesis.
- Piper's API is `POST /synthesize` with `{"text": "..."}`, answering WAV
  bytes labelled `text/html`; the client checks for a `RIFF` header
  instead of trusting the content type. Server defaults are used.
- The `es_AR-daniela-high` voice peaks at full scale (0 dB): every smoke
  test reply reached 32767. Piper 1.6 has a `volume` synthesis setting,
  but its HTTP server does not expose it; lowering the level would need a
  gain stage here or a server change.
- The turn context (client disconnect or `VOICE_TURN_TIMEOUT`) reaches
  ffmpeg (killed) and every HTTP call.

## Measured timings

Smoke test on 2026-10-09 on this server (CPU only, 12 threads), whisper
`ggml-small-q5_0` with 8 threads, `llama3.2:3b` through the llm-gateway,
Piper `es_AR-daniela-high`. Inputs are 5 s OpenSLR SLR61 (es-AR) clips.

| Turn | Input | Decode | Transcribe | Chat | Synthesize | Total |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| 1 (cold LLM) | WAV 48 kHz | 49 ms | 2291 ms | 35166 ms | 2187 ms | 39.7 s |
| 2 | WebM/Opus | 42 ms | 2274 ms | 3980 ms | 1602 ms | 7.9 s |
| 3 | Ogg/Opus | 41 ms | 2289 ms | 4558 ms | 1711 ms | 8.6 s |
| 4 | WAV 48 kHz | 50 ms | 2358 ms | 5311 ms | 1452 ms | 9.2 s |
| 5 | WebM/Opus | 40 ms | 2296 ms | 7262 ms | 2604 ms | 12.2 s |
| 6 | Ogg/Opus | 41 ms | 2297 ms | 5006 ms | 1800 ms | 9.1 s |

The first turn loads `llama3.2:3b` into Ollama (roughly 30 s of the 35 s);
whisper and Piper were already resident. Warm turns take 8–12 s, mostly
the LLM. Every transcript matched the reference sentence (for example
"Hace 17 grados y está nublado."). Sentence-level streaming (stage 3) is
the main lever left on CPU.

## Test

```sh
go test -race ./...
```

Unit tests fake every stage and use `httptest` servers for the whisper,
gateway and Piper clients. ffmpeg is faked with a shell script; two tests
use the real ffmpeg and skip when it is not installed.
