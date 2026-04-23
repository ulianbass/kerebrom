# Kerebrom v2

[Root README](../../README.md) · [AI agent install guide](../../docs/AI_AGENT_INSTALL.md) · [Product spec](docs/product-spec-v2.md) · [Architecture](docs/architecture-v2.md)

> Current stable Kerebrom line: seven semantic memory tools, local SQLite + FTS5 storage, context governance, trust-ledger auditability, plug-and-play setup for AI clients, and self-update from GitHub Releases.

## Install

```bash
git clone https://github.com/ulianbass/kerebrom.git
cd kerebrom/versions/v2
make install-user
```

The install target builds the binary, installs it at `~/local/bin/kerebrom`, links it from `~/.local/bin/kerebrom`, and runs `kerebrom setup auto`.

Force a target when needed:

```bash
make install-user SETUP_AGENT=claude-desktop
make install-user SETUP_AGENT=codex
make install-user SETUP_AGENT=all
```

Supported setup targets:

```text
auto, all, claude, claude-code, claude-desktop, codex, cursor,
gemini-cli, opencode, windsurf, vscode
```

Restart open AI clients after install.

## Install Through An AI Agent

If a user asks Claude, Codex, or another coding agent to install this repo, the agent should read:

- [../../AGENTS.md](../../AGENTS.md)
- [../../CLAUDE.md](../../CLAUDE.md)
- [../../docs/AI_AGENT_INSTALL.md](../../docs/AI_AGENT_INSTALL.md)

The intended agent instruction is:

```text
Use versions/v2, run make install-user, verify kerebrom version and stats,
report configured clients, and do not enable remote HTTP memory unless asked.
```

## Update

```bash
kerebrom update --check
kerebrom update
```

`kerebrom update` downloads the latest GitHub release source tarball, runs `make install-user` from `versions/v2`, and keeps the existing SQLite data intact.

## Memory Cycle

| Moment | Tool |
|---|---|
| Every user message | `context` |
| Search a topic | `recall` |
| Save a durable learning | `remember` |
| Close substantial work or explicit close requests | `summary` |
| Inspect chronology | `timeline` |
| Invalidate wrong memory | `forget` |
| Admin project consolidation and aliases | `projects` |

Activation is every user message when Kerebrom tools are available. Observations are interpreted memories, not raw transcripts. Use `What / Why / Where / Learned` only when there is a durable fact to preserve. Project consolidation persists aliases so old names keep resolving to the canonical project. Retrieval uses `valid_at` as the semantic chronology, so newer corrections and revalidated facts outrank stale memories without erasing history.

Every context/recall payload includes `context_governor`, which tells the agent how to prioritize matches, recency, conflicts, and chronology before answering. Every observation also has trust-ledger events so lifecycle changes are auditable without storing raw chat transcripts.

## Architecture

```text
cmd/kerebrom/             CLI entrypoint
internal/cli/             commands, hooks, TUI
internal/contextgov/      context governance contract
internal/setup/           local AI-client setup and v1 cleanup
internal/store/sqlite/    SQLite + FTS5 memory store
internal/transport/mcp/   semantic MCP server
internal/transport/http/  local HTTP API
internal/sync/            compressed sync chunks
internal/updater/         self-update
docs/                     specs, ADRs, migration, release checklist
```

Runtime data lives in `~/.kerebrom/`.

## Build And Test

```bash
make test
make build
./bin/kerebrom version
./bin/kerebrom doctor --deep
```

## Safety

Kerebrom is local-first by default. Do not expose `mcp-http` or patch Claude Chat cloud memory unless the user explicitly chooses a supported, documented path. Claude Cowork native memory is seeded only through stable local desktop storage when present. For Claude Chat's native cloud memory, use the manual prompt in [../../docs/AI_AGENT_INSTALL.md](../../docs/AI_AGENT_INSTALL.md#optional-claude-chat-native-memory-seed).
