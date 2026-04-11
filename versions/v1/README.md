# Kerebrom v1

This directory is the source tree for the `v1` line inside the Kerebrom
factory repository.

## Build and test

```bash
make build
make test
go run ./cmd/kerebrom version
go run ./cmd/kerebrom mcp
```

## User install

```bash
make install-user
```

The user install builds Kerebrom, places the binary at `~/local/bin/kerebrom`, links it from `~/.local/bin/kerebrom`, and runs `kerebrom setup all`. On macOS this configures both Claude Code under `~/.claude/` and Claude Desktop under `~/Library/Application Support/Claude/claude_desktop_config.json`, preserving existing MCP servers.

Claude Code also gets lifecycle hooks under `~/.kerebrom/hooks/claude-code/` for automatic session start, user prompt ingest, post-compaction recovery, subagent passive capture, and session stop. Other AI clients receive the strongest local integration they expose: MCP plus mandatory memory protocol instructions when no hook API is available.

Kerebrom stores two different layers: `mem_save_prompt` keeps user intent history, while `mem_save` stores agent-distilled observations using `What / Why / Where / Learned`. Canonical memories should be interpreted summaries, not raw transcript copies.

## Runtime contract

- single Go binary
- shared local memory across supported agents
- SQLite + FTS5 store
- HTTP + MCP + CLI surfaces
- 15 Engram-compatible `mem_*` MCP tools
- retrieval commands like `context`, `search`, `timeline`, and `tui`
- lifecycle hook runner for hook-capable AI clients
- export/import and compressed sync chunks under `.kerebrom/`
- idempotent setup for Codex, Claude Code, Claude Desktop, Gemini CLI, OpenCode, Cursor, Windsurf, and VS Code

## Layout

```text
cmd/kerebrom/           CLI entrypoint
internal/               Product code
docs/                   v1 architectural and release docs
scripts/                Local install and operational scripts
test/                   Contract and e2e suites
```

## Canonical v1 docs

- [Product Spec](docs/product-spec-v1.md)
- [Parity Matrix](docs/parity-matrix-engram.md)
- [Release Checklist](docs/release-checklist-v1.md)
