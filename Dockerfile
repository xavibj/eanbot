# syntax=docker/dockerfile:1

# ---- 1. Frontend: Vite build -------------------------------------------------
FROM node:24-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
# vite.config.js writes to ../server/static; the directory must exist for emptyOutDir
RUN mkdir -p /src/server/static && npm run build

# ---- 2. Go build (cross-compiled, no emulation) -----------------------------
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Replace the committed static output with the one built in this image.
RUN rm -rf server/static && mkdir -p server/static
COPY --from=web /src/server/static/ server/static/
RUN go vet ./... && go test ./...
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags='-s -w' -o /out/eanbot ./cmd/eanbot
RUN mkdir -p /out/tmp

# ---- 3. Runtime ----------------------------------------------------------------
FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/eanbot /eanbot
# SQLite spills large sorts (summaries, broken links) to temp files; scratch has
# no /tmp and / is not writable by the runtime user, so provide both a /tmp and
# an explicit temp dir on the data volume.
COPY --from=build --chown=65532:65532 --chmod=1777 /out/tmp /tmp
ENV SQLITE_TMPDIR=/data
USER 65532:65532
VOLUME ["/data"]
EXPOSE 8345
ENTRYPOINT ["/eanbot"]
CMD ["serve", "-addr", ":8345", "-db", "/data/eanbot.db"]
