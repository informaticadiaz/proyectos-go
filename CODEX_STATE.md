# CODEX_STATE.md - proyectos-go

## Estado actual

- Workspace creado el 2026-10-05 a pedido del usuario para proyectos en Go.
- Toolchain disponible: Go 1.26.0 (linux/amd64), `GOPATH` en
  `/home/ignacio/go`.
- Todavía no hay proyectos.

## Decisiones

- Mantener este espacio como contexto independiente con padre directo `root`.
- Ubicar los proyectos fuera del `GOPATH`, un módulo por subdirectorio.
- Guardar información de ejecución en `data/`.
- 2026-10-05: la carpeta completa es un repositorio git propio, público, en
  https://github.com/informaticadiaz/proyectos-go (rama `main`). El repo
  `root` la ignora (`/proyectos-go/` en su `.gitignore`).

## Hilos abiertos

- Definir el primer proyecto (tipo: CLI, API web, bot u otro).

## Próximos pasos

- Elegir el primer proyecto y crear su módulo con `go mod init`.
