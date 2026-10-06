# llm-gateway

An HTTP gateway written in Go that sits in front of [Ollama](https://ollama.com)
and exposes its OpenAI-compatible API, adding the concerns Ollama does not
handle on its own.

## Status

| Stage | Feature | State |
| --- | --- | --- |
| 1 | Reverse proxy for `/v1/*`, SSE streaming, health check, native API blocked | Done |
| 2 | API key authentication | Planned |
| 3 | Per-key rate limiting | Planned |
| 4 | Structured request logs and metrics (latency, tokens, model) | Planned |

## Run

```sh
go run ./cmd/gateway
```

| Variable | Default | Description |
| --- | --- | --- |
| `GATEWAY_ADDR` | `127.0.0.1:8090` | Listen address |
| `OLLAMA_URL` | `http://127.0.0.1:11434` | Ollama base URL |

Any OpenAI client works by pointing its base URL at the gateway:

```sh
curl -N http://127.0.0.1:8090/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{"model":"qwen2.5-coder:7b","stream":true,"messages":[{"role":"user","content":"Hi"}]}'
```

## Design

- Only `/v1/*` is forwarded. Ollama's native API (`/api/*`) can pull and
  delete models, so the gateway answers `404` for it.
- `httputil.ReverseProxy` flushes `text/event-stream` responses immediately,
  so streamed tokens are not buffered.
- An unreachable upstream returns `502 Bad Gateway`.
- `SIGINT`/`SIGTERM` trigger a graceful shutdown that lets in-flight
  streams finish (up to 30 s).

## Test

```sh
go test -race ./...
```
