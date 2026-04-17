# Kerebrom

[Español](README.es.md) · [Install for AI agents](docs/AI_AGENT_INSTALL.md) · [Release v2.0.4](https://github.com/ulianbass/kerebrom/releases/tag/v2.0.4) · [Repository history](docs/BRANCHES.md)

> Local-first persistent memory for AI agents.
> Install once, then Claude, Codex, Cursor, Gemini CLI, OpenCode, Windsurf, VS Code, and any MCP-capable client can share one durable memory layer.

Kerebrom is a private memory engine for people who work with more than one AI assistant. It runs as one Go binary, stores memory locally in SQLite + FTS5, exposes a semantic MCP server, and installs the strongest available memory lifecycle for each supported client.

It is designed to feel automatic: agents consult prior context, remember durable decisions, summarize substantial work, and recover project context without the user repeating "use memory" in every chat.

![Kerebrom memory flow](docs/assets/kerebrom-memory-flow.svg)

## Why It Exists

AI clients usually remember in separate silos. Claude Code, Claude Desktop Chat, Cowork, Codex, Cursor, and CLI agents may all know different pieces of the same project. Kerebrom gives them one local source of truth:

| Problem | Kerebrom behavior |
|---|---|
| Context disappears between chats | `context` activates on every user message and retrieves prior project observations before the agent answers. |
| Agents save noisy transcripts | `remember` stores distilled observations, not raw conversation dumps. |
| Claude/Codex know different things | All configured clients read and write the same local SQLite store. |
| Setup is fragile | `setup auto` detects installed clients and writes their native config surfaces. |
| Cloud memory is risky | Local stdio MCP and local hooks are the default; remote MCP is opt-in only. |

## Install

```bash
git clone https://github.com/ulianbass/kerebrom.git
cd kerebrom/versions/v2
make install-user
```

That command builds the binary, installs it to `~/local/bin/kerebrom`, links it from `~/.local/bin/kerebrom`, and runs:

```bash
kerebrom setup auto
```

`setup auto` configures only clients that already have local config on the machine. If no known client is detected, it falls back to Claude Desktop so a first-time user still gets a working MCP entry.

To force a specific target:

```bash
make install-user SETUP_AGENT=claude-desktop
make install-user SETUP_AGENT=codex
make install-user SETUP_AGENT=all
```

After install, fully restart any open AI client so it reloads the MCP server and native instruction files.

## Ask An AI Agent To Install It

Kerebrom includes repo-native instructions for AI installers:

| Agent surface | File it should read |
|---|---|
| Codex / OpenAI-style coding agents | [AGENTS.md](AGENTS.md) |
| Claude Code / Claude-aware agents | [CLAUDE.md](CLAUDE.md) |
| Any agent or human installer | [docs/AI_AGENT_INSTALL.md](docs/AI_AGENT_INSTALL.md) |

You can paste this into Claude, Codex, or another coding agent after pointing it at the repo:

```text
Please install this repository for me as an end-user product.
Read AGENTS.md or CLAUDE.md first, then follow docs/AI_AGENT_INSTALL.md.
Use the current stable line in versions/v2, run the plug-and-play install path, verify the binary, and tell me which AI clients were configured.
Do not enable remote/HTTP memory unless I explicitly ask for it.
```

![Kerebrom AI installer flow](docs/assets/kerebrom-agent-install-flow.svg)

## What Gets Configured

| Client | Setup behavior |
|---|---|
| Claude Code | MCP entry, lifecycle hooks, auto-approved everyday tools, global `CLAUDE.md` protocol block, hook scripts. |
| Claude Desktop Chat | Local MCP server entry. Account memory is cloud-backed, so Kerebrom does not patch private Claude APIs or browser databases. |
| Claude Cowork | Local MCP plus a native `memory/CLAUDE.md` authority seed when the desktop app has local Cowork account storage. |
| Codex | MCP server in config, auto-approval for everyday memory tools, global `AGENTS.md` protocol block. |
| Cursor | MCP entry plus Kerebrom memory rule. |
| Gemini CLI | MCP entry, system prompt, environment flag for system instructions. |
| OpenCode | MCP entry plus Kerebrom memory protocol file. |
| Windsurf | MCP config plus global rules. |
| VS Code | MCP entry and prompt instructions where supported. |

## The Memory Cycle

Kerebrom v2 exposes seven semantic tools. The first six are everyday agent tools; `projects` is administrative.

| Moment | Tool | Purpose |
|---|---|---|
| Every user message | `context` | Open/resume a session, save the prompt only when appropriate, and retrieve useful prior observations before the agent answers. |
| Need a specific topic | `recall` | Search memory by natural-language query. |
| Durable fact appears | `remember` | Save an interpreted observation using `What / Why / Where / Learned`. |
| Work is ending or compacting | `summary` | Persist goals, decisions, changes, risks, files, and next steps. |
| Inspect chronology | `timeline` | Review recent sessions and observations. |
| User says memory is wrong | `forget` | Invalidate an obsolete observation. |
| Project names need consolidation | `projects` | Admin-only project maintenance. |

The user should not have to say "use Kerebrom" or "save this" for every turn. Activation happens every user message; durable saving still happens only when the message contains something worth preserving.

## Local-First Security Model

Kerebrom is private by default:

- Runtime data lives in `~/.kerebrom/`.
- The memory database is local SQLite + FTS5.
- There is no telemetry, cloud database, hosted account, or background daemon.
- `kerebrom update` contacts GitHub Releases only when the user runs it.
- `kerebrom mcp-http` exists for advanced remote connector workflows, but non-loopback exposure requires explicit authentication or an explicit unsafe override.
- Claude Chat account memory is not modified through private APIs. If the user wants a native Claude Chat memory hint, use the copy-paste prompt in [docs/AI_AGENT_INSTALL.md](docs/AI_AGENT_INSTALL.md#optional-claude-chat-native-memory-seed).

## Commands

```bash
kerebrom version
kerebrom update --check
kerebrom update
kerebrom setup auto
kerebrom setup all
kerebrom stats
kerebrom context --project my-project "what matters here?"
kerebrom search "release decision"
kerebrom tui
kerebrom export --output memory-export.json
kerebrom sync --status
```

Advanced local HTTP MCP transport:

```bash
KEREBROM_REMOTE_TOKEN="change-me" kerebrom mcp-http --addr 127.0.0.1:7437 --path /mcp
```

## Product Lines

| Line | Status | Purpose |
|---|---:|---|
| `v2` | current | Semantic seven-tool surface, self-update, stronger Claude Desktop/Cowork behavior. |
| `v1` | maintained | Prior `mem_*` tool surface; critical fixes only. |
| `history/legacy-main-2026-04-10` | tag | Pre-reset legacy history. |
| `history/go-rewrite-2026-04-10` | tag | Previous Go rewrite experiment. |

Each line lives under `versions/vN/` so future releases can evolve without rewriting history. See [docs/BRANCHES.md](docs/BRANCHES.md).

## Build And Test

```bash
cd versions/v2
make test
make build
./bin/kerebrom version
```

## Architecture

```text
versions/v2/
  cmd/kerebrom/             CLI entrypoint
  internal/cli/             commands, hooks, TUI
  internal/setup/           local AI-client setup and v1 cleanup
  internal/store/sqlite/    SQLite + FTS5 memory store
  internal/transport/mcp/   MCP server with semantic tools
  internal/transport/http/  local HTTP API
  internal/sync/            compressed sync chunks
  internal/updater/         self-update via GitHub Releases
  docs/                     specs, ADRs, migration, release checklist
```

Canonical docs:

- [AI agent install guide](docs/AI_AGENT_INSTALL.md)
- [v2 product spec](versions/v2/docs/product-spec-v2.md)
- [v2 architecture](versions/v2/docs/architecture-v2.md)
- [v1 to v2 migration](versions/v2/docs/migration-v1-to-v2.md)
- [semantic MCP surface ADR](versions/v2/docs/adr/0003-v2-semantic-surface.md)

## License

Source-available proprietary software. See [LICENSE](LICENSE).
