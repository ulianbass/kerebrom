#!/usr/bin/env sh
set -eu

ROOT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
INSTALL_BIN_DIR="${KEREBROM_INSTALL_BIN_DIR:-"$HOME/local/bin"}"
LINK_BIN_DIR="${KEREBROM_LINK_BIN_DIR:-"$HOME/.local/bin"}"
SETUP_AGENT="${KEREBROM_SETUP_AGENT:-auto}"
ASSUME_YES="${KEREBROM_ASSUME_YES:-}"
RUN_DOCTOR=1
REQUIRED_GO_MAJOR=1
REQUIRED_GO_MINOR=26
RECOMMENDED_GO_VERSION="${KEREBROM_RECOMMENDED_GO_VERSION:-go1.26.2}"

usage() {
	cat <<'EOF'
Kerebrom user installer

Usage:
  ./scripts/install-user.sh [options]

Options:
  -a, --agent TARGET       Setup target: auto, all, codex, claude, claude-code,
                           claude-desktop, cursor, gemini-cli, opencode,
                           windsurf, or vscode.
  -y, --yes               Do not ask interactive questions; use defaults.
      --all               Configure every supported client.
      --install-bin-dir   Directory for the installed binary.
      --link-bin-dir      Directory for the convenience symlink.
      --no-doctor         Skip final doctor --deep verification.
  -h, --help              Show this help.

Environment:
  KEREBROM_SETUP_AGENT, KEREBROM_INSTALL_BIN_DIR, KEREBROM_LINK_BIN_DIR,
  KEREBROM_ASSUME_YES=1, KEREBROM_RECOMMENDED_GO_VERSION=go1.26.2

Default:
  Install to ~/local/bin/kerebrom, link from ~/.local/bin/kerebrom, and run
  setup auto.
EOF
}

die() {
	printf '%s\n' "error: $*" >&2
	exit 1
}

ask_yes_no() {
	prompt="$1"
	default="${2:-N}"
	if ! is_interactive; then
		return 1
	fi
	if [ "$default" = "Y" ]; then
		printf '%s [Y/n]: ' "$prompt"
	else
		printf '%s [y/N]: ' "$prompt"
	fi
	read answer
	answer="${answer:-$default}"
	case "$answer" in
		y|Y|yes|YES) return 0 ;;
		*) return 1 ;;
	esac
}

go_install_guidance() {
	cat <<EOF
Kerebrom builds from source and requires Go ${REQUIRED_GO_MAJOR}.${REQUIRED_GO_MINOR} or newer.
Recommended for this release: ${RECOMMENDED_GO_VERSION}.
Without Go, this installer cannot build or install Kerebrom.

Manual options:
  - Official Go downloads: https://go.dev/dl/
  - macOS with Homebrew: brew install go

After installing Go, rerun:
  ./scripts/install-user.sh
EOF
}

install_go_with_brew() {
	if ! command -v brew >/dev/null 2>&1; then
		return 1
	fi
	if command -v go >/dev/null 2>&1; then
		brew upgrade go || brew install go
	else
		brew install go
	fi
}

handle_go_requirement() {
	reason="$1"
	printf '\n%s\n' "$reason"
	go_install_guidance
	if command -v brew >/dev/null 2>&1; then
		if ask_yes_no "Install or upgrade Go now with Homebrew?" "N"; then
			install_go_with_brew || die "Homebrew could not install or upgrade Go. Install Go manually from https://go.dev/dl/ and rerun this installer."
			return 0
		fi
		die "Go was not installed. Kerebrom cannot be installed until Go ${REQUIRED_GO_MAJOR}.${REQUIRED_GO_MINOR}+ is available."
	fi
	die "Go is not ready and no supported automatic installer was found. Install Go ${REQUIRED_GO_MAJOR}.${REQUIRED_GO_MINOR}+ manually, then rerun this installer."
}

require_go_version() {
	if ! command -v go >/dev/null 2>&1; then
		handle_go_requirement "Go was not found on PATH."
	fi

	go_version="$(go env GOVERSION 2>/dev/null || true)"
	if [ "$go_version" = "" ]; then
		go_version="$(go version 2>/dev/null | awk '{print $3}')"
	fi
	major_minor="$(printf '%s\n' "$go_version" | sed -n 's/^go\([0-9][0-9]*\)\.\([0-9][0-9]*\).*/\1 \2/p')"
	if [ "$major_minor" = "" ]; then
		printf '%s\n' "warning: could not parse Go version '$go_version'; continuing and letting go build validate it." >&2
		return 0
	fi
	set -- $major_minor
	major="$1"
	minor="$2"
	if [ "$major" -lt "$REQUIRED_GO_MAJOR" ] || { [ "$major" -eq "$REQUIRED_GO_MAJOR" ] && [ "$minor" -lt "$REQUIRED_GO_MINOR" ]; }; then
		handle_go_requirement "Go ${REQUIRED_GO_MAJOR}.${REQUIRED_GO_MINOR}+ is required. Detected $go_version."
		require_go_version
	fi
}

is_interactive() {
	[ -t 0 ] && [ -t 1 ] && [ "${ASSUME_YES:-}" = "" ]
}

validate_agent() {
	case "$1" in
	auto|all|codex|claude|claude-code|claude-desktop|cursor|gemini-cli|opencode|windsurf|vscode)
		return 0
		;;
	gemini)
		SETUP_AGENT="gemini-cli"
		return 0
		;;
	code|vs-code)
		SETUP_AGENT="vscode"
		return 0
		;;
	*)
		die "unsupported setup target '$1'. Use auto, all, codex, claude, claude-code, claude-desktop, cursor, gemini-cli, opencode, windsurf, or vscode."
		;;
	esac
}

prompt_agent() {
	printf '\nWhich AI clients should Kerebrom configure?\n'
	printf '  1) Auto-detect installed clients (recommended)\n'
	printf '  2) All supported clients\n'
	printf '  3) Codex\n'
	printf '  4) Claude Desktop\n'
	printf '  5) Type a target manually\n'
	printf 'Press Enter for 1: '
	read choice
	case "${choice:-1}" in
		1) SETUP_AGENT="auto" ;;
		2) SETUP_AGENT="all" ;;
		3) SETUP_AGENT="codex" ;;
		4) SETUP_AGENT="claude-desktop" ;;
		5)
			printf 'Target: '
			read typed_agent
			SETUP_AGENT="$typed_agent"
			;;
		*)
			die "invalid selection '$choice'"
			;;
	esac
}

run_doctor_check() {
	if [ "$RUN_DOCTOR" -ne 1 ]; then
		printf '\nSkipping Doctor verification because --no-doctor was passed.\n'
		return 0
	fi

	printf '\nVerifying installation with Doctor...\n'
	if "$INSTALLED_BINARY" doctor --deep --project-dir "$ROOT_DIR"; then
		return 0
	fi

	printf '\nDoctor found drift after install.\n'
	if is_interactive; then
		printf 'Run Doctor Health Mode repair now? [Y/n]: '
		read answer
		case "${answer:-Y}" in
			y|Y|yes|YES)
				"$INSTALLED_BINARY" doctor heal --project-dir "$ROOT_DIR" --setup-agent "$SETUP_AGENT" --binary-path "$INSTALLED_BINARY"
				"$INSTALLED_BINARY" doctor --deep --project-dir "$ROOT_DIR"
				return 0
				;;
		esac
	fi

	printf 'Run this when ready:\n'
	printf '  %s doctor heal --project-dir %s --setup-agent %s --binary-path %s\n' "$INSTALLED_BINARY" "$ROOT_DIR" "$SETUP_AGENT" "$INSTALLED_BINARY"
	return 1
}

while [ "$#" -gt 0 ]; do
	case "$1" in
		-a|--agent)
			[ "$#" -ge 2 ] || die "$1 requires a setup target"
			SETUP_AGENT="$2"
			shift 2
			;;
		--all)
			SETUP_AGENT="all"
			shift
			;;
		-y|--yes)
			ASSUME_YES=1
			shift
			;;
		--install-bin-dir)
			[ "$#" -ge 2 ] || die "$1 requires a directory"
			INSTALL_BIN_DIR="$2"
			shift 2
			;;
		--link-bin-dir)
			[ "$#" -ge 2 ] || die "$1 requires a directory"
			LINK_BIN_DIR="$2"
			shift 2
			;;
		--no-doctor)
			RUN_DOCTOR=0
			shift
			;;
		-h|--help)
			usage
			exit 0
			;;
		*)
			die "unknown option '$1'. Run ./scripts/install-user.sh --help."
			;;
	esac
done

require_go_version

if is_interactive && [ "$SETUP_AGENT" = "auto" ]; then
	prompt_agent
fi
validate_agent "$SETUP_AGENT"

INSTALLED_BINARY="$INSTALL_BIN_DIR/kerebrom"

printf 'Kerebrom installer\n'
printf 'Factory: %s\n' "$ROOT_DIR"
printf 'Setup target: %s\n' "$SETUP_AGENT"
printf 'Binary: %s\n' "$INSTALLED_BINARY"
printf 'Symlink: %s\n' "$LINK_BIN_DIR/kerebrom"

cd "$ROOT_DIR"

mkdir -p bin "$INSTALL_BIN_DIR" "$LINK_BIN_DIR"
if command -v git >/dev/null 2>&1; then
	COMMIT="$(git rev-parse --short=12 HEAD 2>/dev/null || echo none)"
else
	COMMIT="none"
fi
BUILD_DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
LDFLAGS="-X github.com/ulianbass/kerebrom/internal/version.Commit=$COMMIT -X github.com/ulianbass/kerebrom/internal/version.BuildDate=$BUILD_DATE"

printf '\nBuilding Kerebrom...\n'
go build -ldflags "$LDFLAGS" -o bin/kerebrom ./cmd/kerebrom

tmp_binary="$INSTALL_BIN_DIR/.kerebrom.$$"
cp bin/kerebrom "$tmp_binary"
chmod 0755 "$tmp_binary"
mv -f "$tmp_binary" "$INSTALLED_BINARY"
ln -sf "$INSTALLED_BINARY" "$LINK_BIN_DIR/kerebrom"

printf '\nConfiguring AI clients...\n'
"$INSTALLED_BINARY" setup "$SETUP_AGENT" --binary-path "$INSTALLED_BINARY"
rm -f bin/kerebrom

printf '\nInstalled version:\n'
"$INSTALLED_BINARY" version

printf '\nMemory stats:\n'
"$INSTALLED_BINARY" stats

run_doctor_check

printf '\nKerebrom is installed.\n'
printf '%s\n' '- Restart open AI clients so they reload MCP and hook configuration.'
printf '%s\n' "- Runtime memory stays local in ${KEREBROM_DATA_DIR:-"$HOME/.kerebrom"}"
printf '%s\n' '- Doctor WARN means the install is usable but has an item worth reviewing; FAIL means it needs repair.'
case ":$PATH:" in
	*":$LINK_BIN_DIR:"*) ;;
	*) printf '%s\n' "- Add $LINK_BIN_DIR to PATH if the 'kerebrom' command is not found in new terminals." ;;
esac
printf '%s\n' '- Quick check after restart: ask your agent, "What do you know about my projects from Kerebrom?"'
