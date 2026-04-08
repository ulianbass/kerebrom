#!/usr/bin/env bash
# Kerebrom — one-command installer
# Usage: curl -fsSL https://raw.githubusercontent.com/ulianbass/kerebrom/main/scripts/install.sh | bash
set -euo pipefail

GREEN='\033[0;32m'
BLUE='\033[0;34m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
RESET='\033[0m'

echo ""
echo -e "${BLUE}╔════════════════════════════════════════╗${RESET}"
echo -e "${BLUE}║${RESET}  ${GREEN}K${RESET} Kerebrom — installer                 ${BLUE}║${RESET}"
echo -e "${BLUE}║${RESET}    Local-first memory for AI tools     ${BLUE}║${RESET}"
echo -e "${BLUE}╚════════════════════════════════════════╝${RESET}"
echo ""

# ── Requirements ──────────────────────────────────────────
if ! command -v python3 >/dev/null 2>&1; then
    echo -e "${RED}✗${RESET} Python 3.9+ is required but not found."
    echo "  Install it from https://www.python.org/downloads/ and re-run."
    exit 1
fi

PY_VERSION=$(python3 -c 'import sys; print("{}.{}".format(sys.version_info[0], sys.version_info[1]))')
PY_MAJOR=$(echo "$PY_VERSION" | cut -d. -f1)
PY_MINOR=$(echo "$PY_VERSION" | cut -d. -f2)
if [ "$PY_MAJOR" -lt 3 ] || { [ "$PY_MAJOR" -eq 3 ] && [ "$PY_MINOR" -lt 9 ]; }; then
    echo -e "${RED}✗${RESET} Python 3.9+ required. Found: $PY_VERSION"
    exit 1
fi
echo -e "${GREEN}✓${RESET} Python $PY_VERSION detected"

if ! command -v git >/dev/null 2>&1; then
    echo -e "${RED}✗${RESET} git is required but not found."
    exit 1
fi
echo -e "${GREEN}✓${RESET} git detected"

# ── Clone or update ──────────────────────────────────────
INSTALL_DIR="${KEREBROM_INSTALL_DIR:-$HOME/.kerebrom/source}"
REPO_URL="https://github.com/ulianbass/kerebrom.git"

if [ -d "$INSTALL_DIR/.git" ]; then
    echo -e "${YELLOW}→${RESET} Updating existing installation at $INSTALL_DIR"
    git -C "$INSTALL_DIR" pull --quiet
else
    echo -e "${YELLOW}→${RESET} Cloning to $INSTALL_DIR"
    mkdir -p "$(dirname "$INSTALL_DIR")"
    git clone --quiet "$REPO_URL" "$INSTALL_DIR"
fi
echo -e "${GREEN}✓${RESET} Source ready"

# ── Install Python package ───────────────────────────────
echo -e "${YELLOW}→${RESET} Installing Python package"
cd "$INSTALL_DIR"
python3 -m pip install --quiet --user . 2>&1 | grep -v "WARNING" || true
echo -e "${GREEN}✓${RESET} Package installed"

# ── Run setup ────────────────────────────────────────────
echo -e "${YELLOW}→${RESET} Running kerebrom setup"
mkdir -p "$HOME/.kerebrom"
python3 -m kerebrom setup --db "$HOME/.kerebrom/kerebrom.db"
echo ""

# ── Done ─────────────────────────────────────────────────
echo -e "${GREEN}╔════════════════════════════════════════╗${RESET}"
echo -e "${GREEN}║${RESET}  Kerebrom is ready                      ${GREEN}║${RESET}"
echo -e "${GREEN}╚════════════════════════════════════════╝${RESET}"
echo ""
echo "  Next steps:"
echo "    • Restart your AI tools to pick up the MCP config"
echo "    • Run: python3 -m kerebrom graph     (interactive dashboard)"
echo "    • Run: python3 -m kerebrom benchmark (measure savings)"
echo ""
echo "  Docs:  https://github.com/ulianbass/kerebrom"
echo ""
