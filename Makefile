NODE_BIN ?= $(HOME)/.nvm/versions/node/v24.21.0/bin
export PATH := $(NODE_BIN):$(PATH)

.PHONY: build build-linux frontend test vet fmt run

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
