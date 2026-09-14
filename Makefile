NODE_BIN ?= $(HOME)/.nvm/versions/node/v24.21.0/bin
export PATH := $(NODE_BIN):$(PATH)

IMAGE   ?= 7u40qj0f.gra7.container-registry.ovh.net/xavi/eanbot
VERSION ?= dev

.PHONY: build build-linux frontend test vet fmt run docker push docker-run

build:
	CGO_ENABLED=0 go build -o bin/eanbot ./cmd/eanbot

build-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o bin/eanbot-linux-amd64 ./cmd/eanbot

frontend:
	cd web && npm ci && npm run build

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -l .

run: build
	./bin/eanbot serve

docker:
	docker build --platform linux/amd64 -t $(IMAGE):$(VERSION) -t $(IMAGE):latest .

push:
	docker push $(IMAGE):$(VERSION)
	docker push $(IMAGE):latest

docker-run:
	docker run --rm -p 8345:8345 -v eanbot-data:/data $(IMAGE):$(VERSION)
