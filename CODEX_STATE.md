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

## Decisiones

- Mantener este espacio como contexto independiente con padre directo `root`.
- Ubicar los proyectos fuera del `GOPATH`, un módulo por subdirectorio.
- Guardar información de ejecución en `data/`.
- 2026-10-05: la carpeta completa es un repositorio git propio, público, en
  https://github.com/informaticadiaz/proyectos-go (rama `main`). El repo
  `root` la ignora (`/proyectos-go/` en su `.gitignore`).

## Hilos abiertos

- `llm-gateway`: etapas 2 (API keys), 3 (rate limiting por key) y 4 (logs
  y métricas). Sin servicio systemd ni exposición pública todavía.

## Próximos pasos

- `llm-gateway` etapa 2: autenticación con API keys como middleware.
