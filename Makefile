SHELL := /usr/bin/env bash

BIN_DIR := bin
WEB_DIR := web
EMBED_DIR := internal/staticfs/dist
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: web go build test check fixtures dev clean

web:
	cd $(WEB_DIR) && pnpm install --frozen-lockfile && pnpm run build
	rm -rf $(EMBED_DIR)
	mkdir -p $(EMBED_DIR)
	cp -r $(WEB_DIR)/build/. $(EMBED_DIR)/

go:
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/gammich ./cmd/gammich

build: web go

test:
	go test ./...

check:
	test -z "$$(gofmt -l cmd internal)"
	go vet ./...
	cd $(WEB_DIR) && pnpm check

fixtures:
	./scripts/make-fixtures.sh _fixtures

# Local dev against the demo library; loopback is trusted so edits work
# without a password.
dev: fixtures
	GAMMICH_VIDEOS=$$PWD/_fixtures GAMMICH_DB=data/dev.db GAMMICH_CACHE=data/dev-cache \
		GAMMICH_TRUST_LOOPBACK=1 go run ./cmd/gammich -addr 127.0.0.1:8080

clean:
	rm -rf $(BIN_DIR) $(EMBED_DIR)
	mkdir -p $(EMBED_DIR)
	git checkout -- $(EMBED_DIR)/index.html
