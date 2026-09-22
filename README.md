# eanbot

Rastreador web («bot tipo Googlebot») en un único binario Go: CLI, API REST y
interfaz Vue 3 embebida. Rastrea un sitio desde una URL semilla respetando
`robots.txt` (grupos por agente, `Allow`/`Disallow` con `*` y `$`,
`Crawl-delay`, `Sitemap:`) y guarda en SQLite cada página: código HTTP, tipo,
tamaño, tiempo, título, description, canonical, meta robots, H1, redirección,
error, y los enlaces salientes y entrantes.

## Uso rápido

Requiere Go 1.27 (Node solo para modificar el frontend).

```sh
git clone https://github.com/xavibj/eanbot.git && cd eanbot
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
`-no-sitemaps`, `-header "Nombre: valor"` (repetible; p. ej. `-header "x-ean-client: XXXX"`
para que Cloudflare no bloquee al bot), `-origin ip[:puerto]` (conecta
directamente al servidor de origen del host de la semilla, saltando Cloudflare;
`Host` y SNI siguen siendo los del sitio, como `curl --resolve`),
`-insecure-tls` (no verificar el certificado, para Origin CA de Cloudflare o
certificados propios), `-json`, `-quiet`. `Ctrl-C` cancela y guarda lo
rastreado. En la API y la web, esas opciones van en `config.headers`,
`config.origin` y `config.insecure_tls`.

```sh
./bin/eanbot crawl https://fo-test.electricautomationnetwork.com \
    -origin 172.16.0.10 -insecure-tls -header "x-ean-client: XXXXXXXX"
```

## API

`GET /api/healthz`, `POST /api/crawls`, `GET /api/crawls`,
`GET /api/crawls/{id}`, `POST /api/crawls/{id}/cancel`,
`DELETE /api/crawls/{id}`, `GET /api/crawls/{id}/pages?status=&q=&limit=&offset=`,
`GET /api/crawls/{id}/pages/{page_id}`, `GET /api/crawls/{id}/broken`.
Contrato completo en `specs/004-api-web.md`.

## Docker

```sh
make docker VERSION=0.1.0        # compila frontend (Vite) y Go en la imagen; FROM scratch, uid 65532
docker volume create eanbot-data
docker run -d --name eanbot -p 8345:8345 -v eanbot-data:/data \
    7u40qj0f.gra7.container-registry.ovh.net/ean/eanbot:0.1.0
docker exec eanbot /eanbot crawl https://xavi.net -db /data/eanbot.db
```

Con un directorio del host en vez de volumen (`-v $PWD/data:/data`), hazlo
escribible por el contenedor: `chown -R 65532:65532 data` (o `--user $(id -u)`).

La imagen define `SQLITE_TMPDIR=/data`: SQLite necesita un directorio temporal
para ordenar tablas grandes (resúmenes, enlaces rotos) y en `scratch` no hay
`/tmp`; sin él las consultas fallan con `disk I/O error (6410)`.

Detalles y verificación en `specs/007-docker.md`.

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

## Licencia

MIT, ver `LICENSE`.
