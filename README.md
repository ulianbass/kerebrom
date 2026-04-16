# Kerebrom

[Español](README.es.md) · [Release v2.0.1](https://github.com/ulianbass/kerebrom/releases/tag/v2.0.1) · [Repository history](docs/BRANCHES.md)

> Local-first persistent memory for AI agents.
> One durable memory layer for Claude, Codex, Cursor, Gemini CLI, OpenCode, Windsurf, VS Code, and any other MCP-capable client.

Kerebrom gives coding agents a shared local memory without sending your project context to a cloud service. It runs as a single Go binary, stores data in SQLite with FTS5, exposes an MCP server with a clean semantic surface, and installs the strongest available memory workflow for each supported AI client.

![Kerebrom memory flow](docs/assets/kerebrom-memory-flow.svg)

## Product Lines

| Line | Status | Purpose |
|---|---:|---|
| `v2` | **current branch** | Clean semantic surface (7 verb-named tools), self-update command, plug-and-play in Claude Desktop |
| `v1` | maintained | Prior `mem_*` tool surface; still receives critical fixes |
| `history/legacy-main-2026-04-10` | tag | Legacy default-branch history before the v1 reset |
| `history/go-rewrite-2026-04-10` | tag | Previous Go rewrite experiment |

Each line lives under its own `versions/vN/` directory so future versions evolve without rewriting history. See [docs/BRANCHES.md](docs/BRANCHES.md) for the full repository map.

## What v2 Provides

- **Single Go binary** with no service stack to maintain.
- **Seven semantic MCP tools** (`context`, `recall`, `remember`, `summary`, `forget`, `timeline`, `projects`) — verb names that the model picks up by intuition, no `mem_*` prefixes to learn.
- **Self-update** with `kerebrom update` — fetches the latest GitHub release, downloads source, and reinstalls in place.
- **Auto-approve in Claude Code** for the six everyday tools, so the agent never has to ask permission to use memory.
- **Cleanup of v1 leftovers** during setup: any old `mcp__Kerebrom__mem_*` entries lingering in `permissions.allow` are removed.
- **Same SQLite + FTS5 store as v1** — upgrading does not touch your data.
- **Streamable HTTP MCP transport** for remote connectors such as Claude Chat/Cowork and ChatGPT when the client cannot launch a local stdio MCP server.
- **CLI, HTTP API, terminal dashboard, export/import, compressed sync chunks** — everything v1 had, with the v2 vocabulary.
- **Claude Code lifecycle hooks** for session start, prompt ingest, passive capture, compaction recovery, and session close.
- **Setup for Codex, Claude Desktop, Cursor, Gemini CLI, OpenCode, Windsurf, and VS Code** with the new protocol text.
- **Distilled memory framework**: prompts are intent history; observations are agent-interpreted `What / Why / Where / Learned` memories.

## Install

```bash
git clone https://github.com/ulianbass/kerebrom.git
cd kerebrom/versions/v2
make install-user
```

This builds `kerebrom`, installs it to `~/local/bin/kerebrom`, links it from `~/.local/bin/kerebrom`, and runs:

```bash
kerebrom setup auto
```

`setup auto` configures only clients with existing local config and falls back to Claude Desktop when no client config is detected. To force one client:

```bash
make install-user SETUP_AGENT=cursor
make install-user SETUP_AGENT=claude-desktop
make install-user SETUP_AGENT=all
```

Restart any open AI client so it picks up the new MCP server.

## Update

```bash
kerebrom update
```

Fetches the latest GitHub release, downloads the source tarball for that tag, and runs `make install-user` against `versions/v2`. Use `--check` to see whether an update is available without installing, `--yes` to skip the confirmation prompt, or `--pre-release` to consider pre-release tags.

## The Cycle

The six day-to-day tools plus one explicit admin tool compose the memory rhythm:

| When | Tool |
|---|---|
| Start of any non-trivial conversation | `context` |
| Need to look up a specific topic | `recall` |
| Durable fact, decision, preference, bugfix, or learning appears | `remember` |
| Closing substantial work or after compaction | `summary` |
| Inspect chronological history | `timeline` |
| User says something is wrong or obsolete | `forget` |
| Consolidate project name variants after the user explicitly asks | `projects` (admin profile) |

The user never has to say "use Kerebrom" or "save this in memory". The agent uses the tools on its own when the names match the intent.

## Everyday Commands

```bash
kerebrom version
kerebrom update
kerebrom setup auto
kerebrom setup all
kerebrom stats
kerebrom context --project my-project "what matters here?"
kerebrom search "release decision"
KEREBROM_REMOTE_TOKEN="change-me" kerebrom mcp-http --addr 127.0.0.1:7437 --path /mcp
kerebrom tui
kerebrom export --output memory-export.json
kerebrom sync --status
```

## Memory Model

Kerebrom separates four concepts:

| Object | Purpose |
|---|---|
| Project | Normalized workspace or domain boundary |
| Session | Lifecycle of a working conversation or agent run |
| Prompt | User intent history |
| Observation | Canonical durable memory, distilled by an AI agent |

Observations should not be raw transcripts. They should be concise, interpreted, and useful for future work — distilled with the **What / Why / Where / Learned** framework.

## Architecture

```text
versions/v2/
  cmd/kerebrom/             CLI entrypoint
  internal/cli/             commands, hooks, TUI
  internal/setup/           local AI-client setup with v1 cleanup
  internal/store/sqlite/    SQLite + FTS5 memory store (schema unchanged)
  internal/transport/mcp/   MCP server with the 7 semantic tools
  internal/transport/http/  local HTTP API
  internal/sync/            compressed sync chunks
  internal/updater/         self-update via GitHub Releases
  docs/                     ADRs, architecture, migration, release checklist
```

Runtime data lives in `~/.kerebrom/`. The repository does not contain user memory, backups, machine-specific configuration, or migration staging material.

## Build And Test

```bash
cd versions/v2
make test
make build
./bin/kerebrom version
```

## Provenance

Kerebrom v2 is the clean evolution of the v1 product line. It preserves the product direction, the public repository identity, and the SQLite schema, while replacing the technical `mem_*` tool surface with semantic verb names that the model invokes automatically. v1 remains in `versions/v1/` for users who do not want to migrate yet. See [docs/BRANCHES.md](docs/BRANCHES.md) for the repository map and [versions/v2/docs/migration-v1-to-v2.md](versions/v2/docs/migration-v1-to-v2.md) for the upgrade guide.

## License

Source-available proprietary software. See [LICENSE](LICENSE).
