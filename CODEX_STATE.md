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
- Segundo proyecto: [`voice-assistant/`](voice-assistant/README.md),
  orquestador en Go de un asistente de voz local (push-to-talk). Etapa 1
  (2026-10-09): `POST /v1/turn` recibe audio en cualquier formato que
  ffmpeg lea por pipe (WebM/Ogg/WAV/MP3), lo pasa a WAV 16 kHz mono con
  `ffmpeg` (stdin/stdout, header WAV armado en Go), transcribe con
  `whisper-server` residente (`POST /inference`), responde con
  `llm-gateway` (`llama3.2:3b`, sin streaming, prompt breve en español) y
  sintetiza con Piper `es_AR-daniela-high` (`POST /synthesize`). Devuelve
  `audio/wav` con `X-Transcript`/`X-Reply` (percent-encoding UTF-8) y
  `X-Timing-*` en ms; una línea de log JSON por turno. Etapas como
  interfaces en `internal/turn`; 400/413/422/502/504 según la falla.
  Config `VOICE_*` (puerto `127.0.0.1:8095`), key del gateway leída de un
  archivo (`VOICE_GATEWAY_KEY_FILE`). Sólo biblioteca estándar; tests con
  fakes y `httptest`, CI en `.github/workflows/voice-assistant.yml`.
- Prueba de humo de `voice-assistant` (2026-10-09), todo en puertos
  temporales `1809x` y apagado al terminar: gateway temporal con key
  descartable, `whisper-server` con `ggml-small-q5_0` (8 hilos) y Piper.
  Primer turno 39,7 s (35,2 s de chat por la carga de `llama3.2:3b` en
  Ollama); turnos en caliente 7,9–12,2 s: decode ~40 ms, transcripción
  ~2,3 s, chat 4–7 s, síntesis 1,5–2,6 s. Transcripciones correctas en
  WAV, WebM/Opus y Ogg/Opus. El silencio producía `[MÚSICA]`; ahora se
  filtran esas etiquetas y responde 422.

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
  teléfono vía internet → orquestador en Go → `whisper.cpp` →
  `llm-gateway` → Piper `es_AR-daniela-high` (de
  `generacion-audio-texto/`). El workspace de voz (whisper.cpp, datasets)
  está en `/home/ignacio/voice/`, no en `ia-local/voice/`. Se avanzó por la
  alternativa sin compra: el orquestador es el módulo
  [`voice-assistant/`](voice-assistant/README.md), etapa 1 hecha con
  `llama3.2:3b` (8–12 s por turno en caliente, sólo CPU). La GPU
  (propuesta: RTX 3060 12 GB) sigue bloqueada por el relevamiento de la
  fuente en `servidor/mantenimiento/`. Exponerlo requiere coordinar con
  `cloudflare/`.
- `voice-assistant`: el 2026-10-09, con autorización del usuario, se
  instaló `piper-tts[http]==1.6.0` (Flask 3.1.3) en el venv de
  `generacion-audio-texto/`; `piper.http_server` real verificado
  (`POST /synthesize` → WAV válido en ~0,3 s). `whisper-server` necesita
  `LD_LIBRARY_PATH=build/bin`. La voz satura a 0 dB (picos 32767) y el
  servidor de Piper no expone `volume`. Etapa 1 commiteada y pusheada.
- En el repo `root` queda sin commit el registro de este workspace
  (`.gitignore`, `AGENTS.md`, `REGISTRO_AGENTES.md`, `CODEX_STATE.md`),
  mezclado con otros cambios pendientes del usuario en esos archivos.

## Próximos pasos

- `voice-assistant` etapa 2: página web push-to-talk (`MediaRecorder` →
  `/v1/turn` → reproducción). Etapa 3: streaming por oración LLM → TTS.
  Etapa 4: micrófono abierto con VAD.

- Opcional: ampliar la retención de Prometheus más allá de 15 días (flags del
  snap), coordinando con `servidor/`.
- Alternativas: nuevos clientes del gateway o funciones nuevas.
