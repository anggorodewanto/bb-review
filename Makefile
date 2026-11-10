SHELL := /bin/sh

BIN := bin/bb-review
GO ?= go
NPM ?= npm
LISTEN ?= 127.0.0.1:7777
WEB_HAS_PKG := $(wildcard web/package.json)

SERVICE := bb-review
UNIT_DIR := $(HOME)/.config/systemd/user
SYSTEMCTL := systemctl --user

.PHONY: all build web test lint dev fmt clean help \
	service-install service-uninstall start stop restart status logs logs-recent deploy

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

## help: list make targets.
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/^## /  /'

## service-install: install + enable the systemd user service (starts now and at boot).
service-install: build
	@mkdir -p $(UNIT_DIR)
	sed 's|@BIN@|$(CURDIR)/$(BIN)|' contrib/bb-review.service > $(UNIT_DIR)/$(SERVICE).service
	$(SYSTEMCTL) daemon-reload
	$(SYSTEMCTL) enable --now $(SERVICE)
	loginctl enable-linger "$$USER"
	@$(SYSTEMCTL) --no-pager status $(SERVICE) | head -3

## service-uninstall: stop, disable and remove the systemd user service.
service-uninstall:
	-$(SYSTEMCTL) disable --now $(SERVICE)
	rm -f $(UNIT_DIR)/$(SERVICE).service
	$(SYSTEMCTL) daemon-reload

## start / stop / restart / status: control the running service.
start:
	$(SYSTEMCTL) start $(SERVICE)
stop:
	$(SYSTEMCTL) stop $(SERVICE)
restart:
	$(SYSTEMCTL) restart $(SERVICE)
status:
	@$(SYSTEMCTL) --no-pager status $(SERVICE)

## logs: follow service logs (LINES=n to show more history).
logs:
	journalctl --user -u $(SERVICE) -f -n $(or $(LINES),50)

## logs-recent: print the last LINES (default 100) log lines and exit (safe for agents).
logs-recent:
	@journalctl --user -u $(SERVICE) --no-pager -n $(or $(LINES),100)

## deploy: rebuild, then restart the service so it runs the new binary.
deploy: build restart
	@sleep 2; $(SYSTEMCTL) is-active $(SERVICE)

## clean: remove build output.
clean:
	rm -rf bin web/dist/assets
