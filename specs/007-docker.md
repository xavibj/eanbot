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
   `USER 65532:65532`, `VOLUME /data`, `EXPOSE 8345`,
   `ENTRYPOINT ["/eanbot"]`, `CMD ["serve", "-addr", ":8345", "-db",
   "/data/eanbot.db"]`.

`.dockerignore` excluye `.git`, `bin/`, `web/node_modules/`, `*.db*`,
`server/static/` (se regenera en la etapa `web`).

## Makefile

```make
IMAGE   ?= 7u40qj0f.gra7.container-registry.ovh.net/xavi/eanbot
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
    7u40qj0f.gra7.container-registry.ovh.net/xavi/eanbot:0.1.0
docker exec eanbot /eanbot crawl https://xavi.net -db /data/eanbot.db   # CLI dentro del contenedor
```

Bind mount de un directorio del host: `chown 65532:65532` del directorio
(SQLite en WAL necesita escribir `-wal`/`-shm` junto a la BBDD).

## Verificación exigida

`make docker` construye; `docker run` del contenedor responde
`GET /api/healthz` con `{"ok":true}` y `GET /` con `text/html` que contiene
los assets de Vite; un `POST /api/crawls` contra un sitio HTTPS real termina en
`done` con páginas 2xx (prueba los certificados CA); el contenedor corre como
uid 65532 (`docker exec eanbot id` no aplica en scratch: comprobar con
`docker inspect --format '{{.Config.User}}'`).
