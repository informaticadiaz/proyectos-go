# llm-gateway

[![llm-gateway](https://github.com/informaticadiaz/proyectos-go/actions/workflows/llm-gateway.yml/badge.svg)](https://github.com/informaticadiaz/proyectos-go/actions/workflows/llm-gateway.yml)

An HTTP gateway written in Go that sits in front of [Ollama](https://ollama.com)
and exposes its OpenAI-compatible API, adding the concerns Ollama does not
handle on its own.

## Status

| Stage | Feature | State |
| --- | --- | --- |
| 1 | Reverse proxy for `/v1/*`, SSE streaming, health check, native API blocked | Done |
| 2 | API key authentication | Done |
| 3 | Per-client rate limiting (token bucket) | Done |
| 4 | Structured request logs and Prometheus metrics (latency, TTFB, tokens, model) | Done |

## Run

Create a key for each client. The key is printed once; only its SHA-256
hash goes into the keys file:

```sh
go run ./cmd/gateway keygen alice
# API key (shown once, give it to the client): gw_...
# Keys file line: alice:3f1c...

echo 'alice:3f1c...' >> keys.txt
GATEWAY_KEYS_FILE=keys.txt go run ./cmd/gateway
```

| Variable | Default | Description |
| --- | --- | --- |
| `GATEWAY_KEYS_FILE` | — (required) | Keys file, one `name:sha256hex` per line; `#` comments allowed |
| `GATEWAY_ADDR` | `127.0.0.1:8090` | Listen address |
| `OLLAMA_URL` | `http://127.0.0.1:11434` | Ollama base URL |
| `GATEWAY_RATE_PER_MINUTE` | `60` | Average requests per minute per client |
| `GATEWAY_RATE_BURST` | `10` | Requests a client may send at once |
| `GATEWAY_METRICS_ADDR` | — (disabled) | Listen address for `GET /metrics`, e.g. `127.0.0.1:9100` |

Any OpenAI client works by pointing its base URL at the gateway and using
the key as its API key:

```sh
curl -N http://127.0.0.1:8090/v1/chat/completions \
  -H "Authorization: Bearer $GATEWAY_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"model":"qwen2.5-coder:7b","stream":true,"messages":[{"role":"user","content":"Hi"}]}'
```

## Design

- Only `/v1/*` is forwarded. Ollama's native API (`/api/*`) can pull and
  delete models, so the gateway answers `404` for it.
- `httputil.ReverseProxy` flushes `text/event-stream` responses immediately,
  so streamed tokens are not buffered.
- An unreachable upstream returns `502 Bad Gateway`.
- `/v1/*` requires `Authorization: Bearer <key>`; `/healthz` stays public.
  Rejections use the OpenAI error envelope (`invalid_api_key`), so SDKs
  report them clearly.
- Keys are looked up by their SHA-256 hash: the file never holds a usable
  secret, and hashing before the map lookup avoids timing leaks.
- The gateway refuses to start without a keys file or with an empty one.
- The `Authorization` header is removed before forwarding, so client keys
  never reach Ollama. The client name travels in the request context for
  the rate-limiting and logging stages.
- Each client has a token bucket of `GATEWAY_RATE_BURST` tokens refilled at
  `GATEWAY_RATE_PER_MINUTE`. An empty bucket returns `429` with
  `Retry-After` and the OpenAI `rate_limit_exceeded` error. Buckets live in
  memory behind a mutex, one per configured client.
- Authentication runs before rate limiting, so requests with a wrong key
  never spend a client's tokens.
- Logs are JSON on stdout. Each authenticated request produces one
  `request` entry with client, status, model, `stream`, `duration_ms`,
  `ttfb_ms` (time to first byte, the latency a user perceives) and token
  usage. Rejected keys produce a `rejected request` warning.
- Model and usage are read from the upstream body while the proxy copies
  it, so observation never buffers a stream. Streamed responses only carry
  usage when the client sends `"stream_options":{"include_usage":true}`;
  the gateway does not inject it, since that would alter the stream.
- Metrics use the Prometheus text format, written without dependencies:
  `gateway_requests_total`, `gateway_tokens_total` and
  `gateway_request_duration_seconds`. They are served on a separate
  listener because their labels reveal client names.
- `SIGINT`/`SIGTERM` trigger a graceful shutdown that lets in-flight
  streams finish (up to 30 s).

## Deploy

The gateway runs as a systemd user service. `deploy/install.sh` runs the
tests, builds the binary into `~/proyectos-go/data/llm-gateway/`, installs
`deploy/llm-gateway.service` and restarts it. Re-run it after every change.

Client keys live outside the repository, in `~/.config/llm-gateway/`
(directory `0700`, files `0600`). The service does not start until
`keys.txt` exists:

```sh
install -d -m 0700 ~/.config/llm-gateway
go run ./cmd/gateway keygen alice   # copy the "Keys file line" into keys.txt
chmod 600 ~/.config/llm-gateway/keys.txt
deploy/install.sh
```

Adding or revoking a client is an edit to `keys.txt` followed by
`systemctl --user restart llm-gateway`.

| Item | Value |
| --- | --- |
| API | `http://127.0.0.1:8090/v1` |
| Metrics | `http://127.0.0.1:8091/metrics` |
| Logs | `journalctl --user -u llm-gateway` |

## Test

```sh
go test -race ./...
```
