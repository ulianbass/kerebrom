GO ?= go
BINARY ?= bin/kerebrom
INSTALL_BIN_DIR ?= $(HOME)/local/bin
LINK_BIN_DIR ?= $(HOME)/.local/bin
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

install-user: build
	@mkdir -p "$(INSTALL_BIN_DIR)" "$(LINK_BIN_DIR)"
	tmp_binary="$(INSTALL_BIN_DIR)/.kerebrom.$$$$"; \
	cp "$(BINARY)" "$$tmp_binary"; \
	chmod 0755 "$$tmp_binary"; \
	mv -f "$$tmp_binary" "$(INSTALL_BIN_DIR)/kerebrom"
	ln -sf "$(INSTALL_BIN_DIR)/kerebrom" "$(LINK_BIN_DIR)/kerebrom"
	"$(INSTALL_BIN_DIR)/kerebrom" setup all --binary-path "$(INSTALL_BIN_DIR)/kerebrom"
