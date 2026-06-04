# go-cli-template — shared build conventions for the m-cli Go toolchain.
# Every toolchain Go repo inherits this: static (CGO_ENABLED=0), -trimpath,
# version stamped via -ldflags, cross-compile matrix, lint, test, schema.

BIN     ?= m-ydb
PKG     := github.com/vista-cloud-dev/m-ydb
LDPKG   := $(PKG)/clikit
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%d)
LDFLAGS := -s -w -X $(LDPKG).Version=$(VERSION) -X $(LDPKG).Commit=$(COMMIT) -X $(LDPKG).Date=$(DATE)

# Static, no-libc, reproducible (spec §10).
GOFLAGS := -trimpath
export CGO_ENABLED := 0

PLATFORMS := linux/amd64 linux/arm64 darwin/arm64 windows/amd64

.PHONY: all build run lint test tidy schema dist clean

all: lint test build

build:
	go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o dist/$(BIN) .

run: build
	./dist/$(BIN) $(ARGS)

lint:
	golangci-lint run ./...

test:
	go test $(GOFLAGS) -race -cover ./...

# Real-engine integration tier (gated). Needs a running YottaDB container.
# CONTAINER defaults to the shared dev engine m-test-engine.
CONTAINER ?= m-test-engine
test-it:
	M_YDB_IT=1 M_YDB_CONTAINER=$(CONTAINER) go test $(GOFLAGS) -count=1 -run Real ./internal/transport/ ./internal/source/ -v

tidy:
	go mod tidy

# Emit the machine schema (the §5.5 contract) — also a CI conformance artifact.
schema: build
	./dist/$(BIN) schema

# Cross-compile the pinned matrix into dist/.
dist:
	@mkdir -p dist
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; ext=""; [ "$$os" = "windows" ] && ext=".exe"; \
		echo "  $$os/$$arch"; \
		GOOS=$$os GOARCH=$$arch go build $(GOFLAGS) -ldflags "$(LDFLAGS)" \
			-o dist/$(BIN)-$$os-$$arch$$ext . ; \
	done

clean:
	rm -rf dist
