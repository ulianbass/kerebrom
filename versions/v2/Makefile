GO ?= go
BINARY ?= bin/kerebrom
INSTALL_BIN_DIR ?= $(HOME)/local/bin
LINK_BIN_DIR ?= $(HOME)/.local/bin
SETUP_AGENT ?= auto
COMMIT ?= $(shell git rev-parse --short=12 HEAD 2>/dev/null || echo none)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS ?= -X github.com/ulianbass/kerebrom/internal/version.Commit=$(COMMIT) -X github.com/ulianbass/kerebrom/internal/version.BuildDate=$(BUILD_DATE)

.PHONY: build test run fmt install-user

build:
	@mkdir -p bin
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/kerebrom

test:
	$(GO) test ./...

run:
	$(GO) run ./cmd/kerebrom

fmt:
	$(GO) fmt ./...

install-user:
	@KEREBROM_INSTALL_BIN_DIR="$(INSTALL_BIN_DIR)" \
	KEREBROM_LINK_BIN_DIR="$(LINK_BIN_DIR)" \
	KEREBROM_SETUP_AGENT="$(SETUP_AGENT)" \
	./scripts/install-user.sh --yes --agent "$(SETUP_AGENT)"
