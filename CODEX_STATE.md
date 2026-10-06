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
- Despliegue (2026-10-05, con autorización del usuario): servicio systemd de
  usuario `llm-gateway.service`, habilitado y activo (linger activo). API en
  `127.0.0.1:8090`, métricas en `127.0.0.1:8091`; sin exposición pública.
  Binario en `data/llm-gateway/gateway`; unit y script versionados en
  `llm-gateway/deploy/` (`install.sh` testea, compila, instala y reinicia).
  Keys en `~/.config/llm-gateway/` (`0700`/`0600`): `keys.txt` con el
  cliente `ignacio` y su key en `ignacio.key`, fuera del repo. Verificado
  contra Ollama: 401 sin key, 200 con key, métricas y logs en el journal.
- Scraping (2026-10-06, con autorización del usuario): el Prometheus del
  servidor (snap 2.37, `:9090`) tiene el job `llm-gateway` sobre
  `127.0.0.1:8091`; target `up` y series `gateway_*` verificadas. Comando y
  explicación en `llm-gateway/deploy/prometheus.md`; backup de la config
  previa en `/var/snap/prometheus/current/prometheus.yml.bak-2026-10-06`.
  Retención por defecto: 15 días.

## Decisiones

- Mantener este espacio como contexto independiente con padre directo `root`.
- Ubicar los proyectos fuera del `GOPATH`, un módulo por subdirectorio.
- Guardar información de ejecución en `data/`.
- 2026-10-05: la carpeta completa es un repositorio git propio, público, en
  https://github.com/informaticadiaz/proyectos-go (rama `main`). El repo
  `root` la ignora (`/proyectos-go/` en su `.gitignore`).
- Forma de trabajo en `llm-gateway`: etapas chicas, TDD (tests primero,
  verificar rojo, implementar), `go test -race`, prueba de humo contra el
  Ollama real y documentación (README + este archivo) antes de cada commit.
  Sin dependencias de terceros salvo decisión explícita.
- Commits: conventional commits, separando código (`feat`/`ci`) de estado
  (`docs`); commit y push sólo con aprobación del usuario en cada etapa.

## Notas operativas

- `gh` usa un token fine-grained: un repo nuevo da 403 al hacer push hasta
  agregarlo al acceso del token.
- Actualizar el servicio: `llm-gateway/deploy/install.sh`. Logs:
  `journalctl --user -u llm-gateway -f`.
- Las pruebas de humo usan puertos `18090`/`18091` y keys temporales en el
  scratchpad, nunca las keys reales.

## Hilos abiertos

- `llm-gateway`: corre como servicio local; sin exposición pública. Sus
  métricas se guardan en el Prometheus del servidor.
- Idea (2026-10-06): asistente de voz "micrófono vivo". Página web en el
  teléfono vía internet → orquestador en Go → `whisper.cpp` (en
  `ia-local/voice/`) → `llm-gateway` → Piper `es_AR-daniela-high` (de
  `generacion-audio-texto/`). Sin GPU la latencia medida es de 15 a 30 s
  (4.4 tokens/s con el modelo de 7B); para que se sienta en vivo hace falta una
  GPU (propuesta: RTX 3060 12 GB). Bloqueado por el relevamiento de la fuente
  en `servidor/mantenimiento/`. Alternativa sin compra: prototipo con un
  modelo de 1.5B–3B. Exponerlo requiere coordinar con `cloudflare/`.
- En el repo `root` queda sin commit el registro de este workspace
  (`.gitignore`, `AGENTS.md`, `REGISTRO_AGENTES.md`, `CODEX_STATE.md`),
  mezclado con otros cambios pendientes del usuario en esos archivos.

## Próximos pasos

- Opcional: ampliar la retención de Prometheus más allá de 15 días (flags del
  snap), coordinando con `servidor/`.
- Alternativas: nuevos clientes del gateway o funciones nuevas.
