# eanbot

Rastreador web («bot tipo Googlebot») en un único binario Go: CLI, API REST y
interfaz Vue 3 embebida. Rastrea un sitio desde una URL semilla respetando
`robots.txt` (grupos por agente, `Allow`/`Disallow` con `*` y `$`,
`Crawl-delay`, `Sitemap:`) y guarda en SQLite cada página: código HTTP, tipo,
tamaño, tiempo, título, description, canonical, meta robots, H1, redirección,
error, y los enlaces salientes y entrantes.

## Uso rápido

```sh
make build                                   # bin/eanbot (sin Node: el frontend ya está compilado)
./bin/eanbot crawl https://xavi.net -max-pages 200 -concurrency 2 -delay 300ms
./bin/eanbot crawls                          # lista de rastreos
./bin/eanbot pages 1 -status 4xx             # páginas del rastreo 1 filtradas
./bin/eanbot broken 1                        # enlaces rotos con sus referrers
./bin/eanbot serve                           # http://localhost:8345 (UI + API)
```

Si la semilla redirige a otro host (p. ej. `xavi.net` → `xavibolivar.xavi.net`),
el bot adopta el host destino como ámbito del rastreo.

Flags de `crawl`: `-db`, `-max-pages`, `-max-depth`, `-concurrency`, `-delay`,
`-timeout`, `-user-agent`, `-include-subdomains`, `-ignore-robots`,
`-no-sitemaps`, `-json`, `-quiet`. `Ctrl-C` cancela y guarda lo rastreado.

## API

`GET /api/healthz`, `POST /api/crawls`, `GET /api/crawls`,
`GET /api/crawls/{id}`, `POST /api/crawls/{id}/cancel`,
`DELETE /api/crawls/{id}`, `GET /api/crawls/{id}/pages?status=&q=&limit=&offset=`,
`GET /api/crawls/{id}/pages/{page_id}`, `GET /api/crawls/{id}/broken`.
Contrato completo en `specs/004-api-web.md`.

## Desarrollo

Metodología SDD + TDD: `specs/` es el contrato, `AGENTS.md` las reglas para
agentes. Estructura: `crawler/` (dominio puro, Fetcher inyectable), `store/`
(SQLite via `modernc.org/sqlite`), `server/` (API + estáticos `go:embed`),
`cmd/eanbot/` (CLI), `web/` (Vite + Vue; `make frontend` regenera
`server/static/`, que se commitea).

```sh
make test vet fmt        # suite completa
make frontend            # requiere Node (ruta nvm en el Makefile)
cd web && npm run dev    # dev server con proxy /api → :8345
```
