# 001 — Plan técnico

Sigue el patrón «binario Go único con frontend Vue embebido, API REST y CLI»
(documentador, doc 18) y la metodología SDD + TDD (doc 12).

## Stack cerrado

| Pieza | Elección | Motivo |
|---|---|---|
| Lenguaje | Go 1.27, `CGO_ENABLED=0` | Binario estático, cross-compile |
| HTTP | `net/http` stdlib (`http.ServeMux` con patrones de método y `{id}`) | Sin framework |
| HTML | `golang.org/x/net/html` | Parser tolerante |
| SQLite | `modernc.org/sqlite` vía `database/sql` | Puro Go, sin CGO |
| Frontend | Vue 3 (Composition API, SFC `<script setup>`) + Vite | Build verifica el JS |
| Tests | stdlib `testing`, `httptest`, BBDD real en `t.TempDir()` | |

**No se añaden dependencias** fuera de esta tabla sin actualizar esta spec.
`go.mod`/`go.sum` ya están fijados; ningún agente los toca (tampoco `go mod tidy`).

## Paquetes

```
cmd/eanbot/   CLI: main.go delega en run(args, stdout, stderr) int
crawler/      dominio del rastreo: normalización de URL, ámbito, robots.txt,
              extracción de enlaces, sitemaps, frontier y motor (Fetcher inyectable)
store/        TODO el acceso a SQLite (crawls, pages, links, resúmenes)
server/       Handler(): API REST + estáticos embebidos (go:embed static/) +
              Manager que ejecuta rastreos en segundo plano
web/          proyecto Vite (fuente Vue); su build escribe server/static/
specs/        estas especificaciones
```

Dependencias entre paquetes (flecha = importa): `cmd/eanbot → server, store,
crawler`; `server → store, crawler`; `store → (nada interno)`; `crawler →
(nada interno)`. Nunca al revés.

## Convenciones

- Código, identificadores y comentarios en inglés. Textos de UI (CLI y web) en
  español.
- Errores envueltos con `%w`. `store.ErrNotFound` como centinela.
- JSON: nombres de campo `snake_case` fijados en las specs.
- Tiempos en RFC 3339 UTC en JSON; duraciones en milisegundos enteros.
- `gofmt`, `go vet ./...` y `go test ./...` limpios en todo el repo antes de dar
  nada por terminado.
- Tests table-driven; un caso de spec = al menos un test, incluidos negativos.

## Build

```make
frontend:  cd web && npm ci && npm run build      # escribe server/static/
build:     CGO_ENABLED=0 go build -o bin/eanbot ./cmd/eanbot
build-linux: CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o bin/eanbot-linux-amd64 ./cmd/eanbot
test:      go test ./...
vet:       go vet ./...
```

`server/static/` (salida compilada de Vite) **se commitea**, de modo que `go
build` produce el binario completo sin Node. Node solo hace falta para cambiar
el frontend. Node vive en `~/.nvm/versions/node/v24.21.0/bin`; el Makefile lo
añade al PATH explícitamente.

## Valores por defecto

| Parámetro | Valor |
|---|---|
| `max_pages` | 500 |
| `max_depth` | 10 |
| `concurrency` | 4 |
| `delay_ms` | 500 |
| `timeout_ms` (por petición) | 15000 |
| `max_body_bytes` | 2 MiB (2097152) |
| `user_agent` | `EANBot/0.1 (+https://xavi.net)` |
| token robots | `eanbot` |
| `include_subdomains` | false |
| `ignore_robots` | false |
| `use_sitemaps` | true |
| puerto `serve` | `:8345` |
| fichero BBDD | `eanbot.db` en el directorio actual |
