# 007 — Imagen Docker

Imagen mínima para ejecutar `eanbot serve` en un servidor. Misma filosofía que
documentador: solo el binario (`FROM scratch`, usuario no root 65532), estado en
el fichero SQLite montado en `/data`.

## Dockerfile (raíz del repo), tres etapas

1. **`web`** (`node:24-alpine`): `npm ci` + `npm run build` en `web/`. La salida
   (`server/static/`) se genera **dentro de la imagen**, sin depender de la copia
   commiteada.
2. **`build`** (`golang:1.27-alpine`, `--platform=$BUILDPLATFORM` para
   cross-compilar sin emulación): copia el módulo, sustituye `server/static/`
   por la salida de la etapa `web`, y compila con
   `CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath
   -ldflags='-s -w' -o /out/eanbot ./cmd/eanbot`. Ejecuta `go vet` y
   `go test ./...` antes del build (una imagen no se construye con la suite en
   rojo).
3. **Runtime** (`scratch`): binario en `/eanbot`, certificados CA de Alpine en
   `/etc/ssl/certs/ca-certificates.crt` (imprescindibles para rastrear HTTPS),
   `/tmp` vacío con permisos `1777`, `ENV SQLITE_TMPDIR=/data` (SQLite
   necesita un directorio temporal para ordenar tablas grandes; sin él falla con
   `disk I/O error (6410)`), `USER 65532:65532`, `VOLUME /data`, `EXPOSE 8345`,
   `ENTRYPOINT ["/eanbot"]`, `CMD ["serve", "-addr", ":8345", "-db",
   "/data/eanbot.db"]`.

`.dockerignore` excluye `.git`, `bin/`, `web/node_modules/`, `*.db*`,
`server/static/` (se regenera en la etapa `web`).

## Makefile

```make
IMAGE   ?= eanbot            # nombre local; para subir: make push IMAGE=<registry>/<ns>/eanbot
VERSION ?= dev
docker:      docker build --platform linux/amd64 -t $(IMAGE):$(VERSION) -t $(IMAGE):latest .
push:        docker push $(IMAGE):$(VERSION) && docker push $(IMAGE):latest
docker-run:  docker run --rm -p 8345:8345 -v eanbot-data:/data $(IMAGE):$(VERSION)
```

## Uso

```sh
make docker VERSION=0.1.0
docker volume create eanbot-data
docker run -d --name eanbot -p 8345:8345 -v eanbot-data:/data \
    eanbot:0.1.0
docker exec eanbot /eanbot crawl https://xavi.net -db /data/eanbot.db   # CLI dentro del contenedor
```

Bind mount de un directorio del host (`-v $PWD/data:/data`): el directorio
debe ser escribible por el uid 65532 del contenedor: `chown -R 65532:65532
data` (SQLite en WAL necesita escribir `-wal`/`-shm` junto a la BBDD). Si no,
el binario termina con `error: store: el directorio de la base de datos
"/data" no es escribible por el uid 65532 (...)` y código 1. Alternativa sin
chown: `docker run --user $(id -u):$(id -g) ...`.

## Verificación exigida

`make docker` construye; `docker run` del contenedor responde
`GET /api/healthz` con `{"ok":true}` y `GET /` con `text/html` que contiene
los assets de Vite; un `POST /api/crawls` contra un sitio HTTPS real termina en
`done` con páginas 2xx (prueba los certificados CA); el contenedor corre como
uid 65532 (`docker exec eanbot id` no aplica en scratch: comprobar con
`docker inspect --format '{{.Config.User}}'`).
