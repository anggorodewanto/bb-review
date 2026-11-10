SHELL := /bin/sh

BIN := bin/bb-review
GO ?= go
NPM ?= npm
LISTEN ?= 127.0.0.1:7777
WEB_HAS_PKG := $(wildcard web/package.json)

.PHONY: all build web test lint dev fmt clean

all: build

## build: build the SPA (when web/package.json exists) then the Go binary.
build: web
	$(GO) build -o $(BIN) ./cmd/bb-review

## web: install and build the frontend; no-op while web/package.json is absent.
web:
	@if [ -f web/package.json ]; then \
		$(NPM) --prefix web ci && $(NPM) --prefix web run build; \
	else \
		echo "web/package.json not found: skipping frontend build"; \
	fi

## test: go tests plus the frontend tests when they exist.
test:
	$(GO) test ./...
	@if [ -f web/package.json ]; then $(NPM) --prefix web test; fi

## lint: go vet.
lint:
	$(GO) vet ./...

## dev: Go server on :7777 plus the Vite dev server (proxies /api to :7777).
dev:
	@if [ -f web/package.json ]; then \
		$(NPM) --prefix web run dev & \
		WEB_PID=$$!; \
		trap 'kill $$WEB_PID 2>/dev/null' EXIT INT TERM; \
	fi; \
	$(GO) run ./cmd/bb-review -listen $(LISTEN)

## fmt: gofmt the tree.
fmt:
	$(GO) fmt ./...

## clean: remove build output.
clean:
	rm -rf bin web/dist/assets
