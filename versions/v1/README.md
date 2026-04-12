# Kerebrom v1

This directory contains the source tree for the `v1` line inside the Kerebrom repository.

## Build And Test

```bash
make build
make test
go run ./cmd/kerebrom version
go run ./cmd/kerebrom mcp
```

## User Install

```bash
make install-user
```

The user install builds Kerebrom from the current checkout, places the binary at `~/local/bin/kerebrom`, links it from `~/.local/bin/kerebrom`, and runs `kerebrom setup auto`. `auto` configures clients with existing local config and falls back to Claude Desktop when no client config is detected. Use `make install-user SETUP_AGENT=all` to configure every supported client, or `make install-user SETUP_AGENT=cursor` / `claude-desktop` / `codex` to force one client.

Claude Code also gets lifecycle hooks under `~/.kerebrom/hooks/claude-code/` for automatic session start, user prompt ingest, post-compaction recovery, subagent passive capture, and session stop. Other AI clients receive the strongest local integration they expose: MCP plus a mandatory memory protocol prompt/resource when no hook API is available. Claude Desktop is in this MCP-only category: Kerebrom can expose tools, prompts, and resources there, but cannot install Claude Code's per-turn lifecycle hooks into Claude Desktop.

Kerebrom stores two different layers: `mem_save_prompt` keeps user intent history, while `mem_save` stores agent-distilled observations using `What / Why / Where / Learned`. Canonical memories should be interpreted summaries, not raw transcript copies.

## Runtime Contract

- Single Go binary.
- Shared local memory across supported agents.
- SQLite + FTS5 store.
- HTTP + MCP + CLI surfaces.
- 15 `mem_*` MCP tools.
- MCP prompt/resource memory protocol for Claude Desktop and other MCP-only clients.
- Retrieval commands like `context`, `search`, `timeline`, and `tui`.
- Lifecycle hook runner for hook-capable AI clients.
- Export/import and compressed sync chunks under `.kerebrom/`.
- Idempotent setup for Codex, Claude Code, Claude Desktop, Gemini CLI, OpenCode, Cursor, Windsurf, and VS Code.
- Selective default installer via `setup auto`; explicit `setup all` remains available.

## Layout

```text
cmd/kerebrom/           CLI entrypoint
internal/               Product code
docs/                   v1 architecture and release docs
scripts/                Local install and operational scripts
test/                   Contract and e2e suites
```

## Canonical v1 Docs

- [Product Spec](docs/product-spec-v1.md)
- [Parity Matrix](docs/parity-matrix-engram.md)
- [Release Checklist](docs/release-checklist-v1.md)
