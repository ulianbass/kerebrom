#!/usr/bin/env sh
set -eu

ROOT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
INSTALL_BIN_DIR="${KEREBROM_INSTALL_BIN_DIR:-"$HOME/local/bin"}"
LINK_BIN_DIR="${KEREBROM_LINK_BIN_DIR:-"$HOME/.local/bin"}"
SETUP_AGENT="${KEREBROM_SETUP_AGENT:-auto}"
INSTALLED_BINARY="$INSTALL_BIN_DIR/kerebrom"

cd "$ROOT_DIR"

mkdir -p bin "$INSTALL_BIN_DIR" "$LINK_BIN_DIR"
COMMIT="$(git rev-parse --short=12 HEAD 2>/dev/null || echo none)"
BUILD_DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
LDFLAGS="-X github.com/ulianbass/kerebrom/internal/version.Commit=$COMMIT -X github.com/ulianbass/kerebrom/internal/version.BuildDate=$BUILD_DATE"
go build -ldflags "$LDFLAGS" -o bin/kerebrom ./cmd/kerebrom
tmp_binary="$INSTALL_BIN_DIR/.kerebrom.$$"
cp bin/kerebrom "$tmp_binary"
chmod 0755 "$tmp_binary"
mv -f "$tmp_binary" "$INSTALLED_BINARY"
ln -sf "$INSTALLED_BINARY" "$LINK_BIN_DIR/kerebrom"

"$INSTALLED_BINARY" setup "$SETUP_AGENT" --binary-path "$INSTALLED_BINARY"
rm -f bin/kerebrom
"$INSTALLED_BINARY" version
"$INSTALLED_BINARY" stats
