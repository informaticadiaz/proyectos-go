# AGENTS.md - proyectos-go

## Identidad

Workspace independiente para proyectos escritos en Go: aprendizaje del
lenguaje, herramientas de línea de comandos, servicios y prototipos.

## Misión

Concentrar los proyectos Go del usuario en un único lugar, con una estructura
común y convenciones compartidas, separado del `GOPATH` (`/home/ignacio/go`).

## Alcance

- Un subdirectorio por proyecto, cada uno con su propio módulo (`go.mod`).
- Convenciones comunes: estructura de carpetas, testing, formato y linting.
- Notas de aprendizaje del lenguaje cuando aporten a los proyectos.

## Reglas

- Responder en español salvo que el código o el repo requieran inglés. El
  código, los identificadores y los comentarios van en inglés.
- Mantener el contexto persistente en `CODEX_STATE.md`.
- No crear proyectos dentro de `/home/ignacio/go`: es el `GOPATH` (caché de
  módulos en `pkg/mod` y binarios de `go install` en `bin/`).
- Cada proyecto es un módulo independiente; usar `go work` sólo si dos
  proyectos necesitan desarrollarse juntos.
- Formatear con `gofmt` y acompañar el código con tests (`go test ./...`).
- Usar `data/` sólo para datos de ejecución: salidas, bases locales, binarios
  compilados y reportes generados.
- No instalar herramientas, exponer servicios ni crear unidades systemd sin
  confirmación explícita.

## Relación con el resto del sistema

- Padre directo: `root` (`/home/ignacio`).
- Coordina con `servidor/` antes de operar un servicio en este host y con
  `cloudflare/` antes de exponer cualquier servicio públicamente.
- Si un proyecto crece y necesita contexto propio, se promueve a subagente
  siguiendo la política de hilos del `AGENTS.md` raíz.
