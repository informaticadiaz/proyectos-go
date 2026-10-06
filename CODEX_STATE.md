# CODEX_STATE.md - proyectos-go

## Estado actual

- Workspace creado el 2026-10-05 a pedido del usuario para proyectos en Go.
- Toolchain disponible: Go 1.26.0 (linux/amd64), `GOPATH` en
  `/home/ignacio/go`.
- Primer proyecto: [`llm-gateway/`](llm-gateway/README.md), gateway HTTP en
  Go delante de Ollama con API compatible con OpenAI, elegido el 2026-10-05
  como pieza de portafolio. Etapa 1 (proxy `/v1/*`, streaming SSE,
  `/healthz`, `/api/*` bloqueado, apagado ordenado) hecha con tests y
  probada contra el Ollama local (0.31.1, `qwen2.5-coder:7b`).
- `llm-gateway` etapa 2 (2026-10-05): API keys con `Authorization: Bearer`,
  archivo `GATEWAY_KEYS_FILE` obligatorio con hashes SHA-256 (`name:hex`),
  subcomando `gateway keygen <name>`, header removido antes de Ollama y
  nombre del cliente en el `context`. Probada contra Ollama.
- `llm-gateway` etapa 3 (2026-10-05): rate limiting por cliente con token
  bucket propio (sin dependencias), `sync.Mutex`, reloj inyectado, 429 con
  `Retry-After`; `GATEWAY_RATE_PER_MINUTE` (60) y `GATEWAY_RATE_BURST` (10).
  Corre después de la autenticación. Probada contra Ollama.
- `llm-gateway` etapa 4 (2026-10-05): logs JSON por request (cliente,
  status, modelo, latencia, TTFB, tokens) y métricas Prometheus sin
  dependencias en un puerto aparte opcional (`GATEWAY_METRICS_ADDR`). El
  `usage` se lee del body mientras pasa, sin bufferizar streams; en
  streaming sólo existe si el cliente pide `include_usage`. Probada contra
  Ollama. Las cuatro etapas planificadas están completas.
- CI (2026-10-05): GitHub Actions en `.github/workflows/llm-gateway.yml`
  (gofmt, `go vet`, `go test -race`) con Go del `go.mod` (1.25) y `stable`,
  sólo cuando cambia `llm-gateway/`. Badge en el README.

## Decisiones

- Mantener este espacio como contexto independiente con padre directo `root`.
- Ubicar los proyectos fuera del `GOPATH`, un módulo por subdirectorio.
- Guardar información de ejecución en `data/`.
- 2026-10-05: la carpeta completa es un repositorio git propio, público, en
  https://github.com/informaticadiaz/proyectos-go (rama `main`). El repo
  `root` la ignora (`/proyectos-go/` en su `.gitignore`).

## Hilos abiertos

- `llm-gateway`: sin servicio systemd ni exposición pública todavía.

## Próximos pasos

- `llm-gateway`: decidir siguiente paso (despliegue como servicio o nuevas
  funciones).
