# Kerebrom v2 — Architecture

This document describes the moving parts of v2 in enough detail to maintain or extend the system without reading the entire codebase.

## Goals

1. **Plug-and-play across MCP-capable clients** — install once, restart the client, memory works automatically without the user invoking it.
2. **Self-update** — `kerebrom update` keeps the binary current against GitHub Releases.
3. **Local-first** — every byte stays on the user's machine. No telemetry, no cloud calls beyond the explicit update check.
4. **Backwards-compatible data** — v1 SQLite databases open and write under v2 without migration.

## Component map

```text
┌─────────────────────────────────────────────────────────────────────────┐
│ cmd/kerebrom/main.go                                                    │
│    Single binary entrypoint. Delegates to internal/cli.                  │
└──────────────────────────────┬──────────────────────────────────────────┘
                               │
                  ┌────────────▼────────────┐
                  │ internal/cli/app.go     │  Subcommand dispatcher
                  └─┬───┬───┬───┬───┬───┬───┘
   ┌────────────────┘   │   │   │   │   └─────────────── update
   │             ┌──────┘   │   │   └────────────────── mcp / mcp-http
   │             │     ┌────┘   └────────────────────── setup / hook / serve
   │             │     │                                + ad-hoc CLI tools
   ▼             ▼     ▼
┌──────────┐ ┌───────────┐ ┌──────────────┐ ┌─────────────┐ ┌──────────┐
│ updater  │ │ transport │ │ setup        │ │ store/sqlite │ │ sync     │
│ (NEW)    │ │   /mcp    │ │              │ │              │ │          │
│          │ │   /http   │ │              │ │              │ │          │
└────┬─────┘ └─────┬─────┘ └──────┬───────┘ └──────┬───────┘ └────┬─────┘
     │             │              │                 │              │
     ▼             ▼              ▼                 ▼              ▼
GitHub      MCP clients     Client config       SQLite + FTS5  Compressed
Releases    (Claude         files (Codex,       at             chunks at
API +       Desktop,        Claude, Cursor,     ~/.kerebrom/   ~/.kerebrom/
tarball     Code, Codex,    Gemini, etc.)       kerebrom.db    chunks/
download    Cursor, etc.)
```

Everything compiles into one binary. There is no daemon, no service, no socket beyond the user-launched `mcp` (stdio) or `mcp-http` (loopback by default) processes.

## The MCP surface

Seven tools registered in `internal/transport/mcp/server.go`:

| Tool | Annotations | Handler | Profile |
|---|---|---|---|
| `context` | write, non-destructive, non-idempotent, closed-world | `handleContext` | agent |
| `recall` | read-only, non-destructive, idempotent, closed-world | `handleRecall` | agent |
| `remember` | write, non-destructive, non-idempotent, closed-world | `handleRemember` | agent |
| `summary` | write, non-destructive, non-idempotent, closed-world | `handleSummary` | agent |
| `forget` | write, **destructive**, non-idempotent, closed-world | `handleForget` | agent |
| `timeline` | read-only, non-destructive, idempotent, closed-world | `handleTimeline` | agent |
| `projects` | write, non-destructive, non-idempotent, closed-world | `handleProjects` | admin |

Profile resolution: `kerebrom mcp --tools=agent` (default for setup) registers the first six. `--tools=admin` registers `projects`. `--tools=all` registers everything.

The MCP `instructions` field returned at handshake is the canonical protocol text living in `memoryProtocolText()`. v2 intentionally does not expose MCP prompts or resources for the memory workflow because those surfaces make the protocol feel optional rather than active.

## The cycle

The protocol text encodes a four-step rhythm the model follows automatically:

```
                 ┌──────────────────────┐
   user prompt ──▶│ 1. context           │  every user message: open/resume
                 │    (always first)    │  session, fetch prior observations
                 └─────────┬────────────┘
                           │ working knowledge
                           ▼
                 ┌──────────────────────┐
                 │ 2. remember          │  durable fact appears
                 │    (when applicable) │  → save with What/Why/Where/Learned
                 └─────────┬────────────┘
                           │
                           ▼
                 ┌──────────────────────┐
                 │ 3. recall            │  user asks about a topic
                 │    (when asked)      │  → search by natural-language query
                 └─────────┬────────────┘
                           │
                           ▼
                 ┌──────────────────────┐
                 │ 4. summary           │  closing substantial work
                 │    (at end / compact)│  → goals, decisions, changes, risks
                 └──────────────────────┘
```

`context` is the activation step and should run on every user message when the client exposes Kerebrom tools. Prompt persistence remains filtered inside `context`, so short acknowledgements can activate memory without becoming durable observations. `forget`, `timeline`, and `projects` are specialized — invoked only when explicitly needed.

## Project lookup behavior

Project names organize memory; they must not become hard walls that hide relevant context from another AI client. v2.0.4 treats weak project identities such as `/`, `.`, `default`, `home`, or the user's home-folder name as **unknown** for read paths. When `context`, `recall`, `timeline`, HTTP context/timeline, or Claude Code hook context receive an unknown project, they use a cross-project lookup so clients launched outside a workspace still see the latest durable memories.

When a strong project is supplied explicitly, Kerebrom still searches that project first, but `context` and `recall` also merge broad cross-project matches. This prevents generic local hits from hiding a more relevant observation in another project, while preserving project metadata for organization, summaries, prompts, and explicit project filters.

v2.0.6 adds persistent project aliases. `kerebrom projects consolidate --target proyecto-falage --sources falage` now does two things: it moves existing sessions, observations, and prompts to the canonical project, and it stores `falage -> proyecto-falage` so future writes through the old name resolve to the canonical project instead of recreating the fragment. `kerebrom projects alias` can add alias rules without moving historical rows.

## Chronological Memory Validity

Observations carry three clocks:

- `created_at`: when the observation row first entered the store.
- `updated_at`: when the row was last maintained, including administrative metadata changes.
- `valid_at`: when the memory content was last semantically asserted, corrected, or revalidated.

Retrieval uses `valid_at` as the chronology of truth. `context`, `recall`, `timeline`, CLI context/search, HTTP context/search, and Claude/Codex hook context sort observations by semantic validity rather than raw row maintenance time. This prevents old facts from becoming "new" only because a project merge, import, duplicate repair, or other metadata operation touched the row.

When an agent saves a correction with the same `topic_key`, Kerebrom updates the canonical observation and refreshes `valid_at` while keeping `created_at` for auditability. When the same observation is saved again as a duplicate, `last_seen_at` and `valid_at` refresh because the memory has been reasserted. Project consolidation updates project metadata but does not change `valid_at`.

## Context Governor

v2.1.0 adds `internal/contextgov`, a small policy layer shared by MCP, HTTP, CLI context, and Claude/Codex hook context. It does not add another MCP tool; instead, every context-bearing payload includes `context_governor` with:

- the required sequence: think -> search -> analyze -> answer;
- `valid_at` as the primary clock;
- match-vs-recency decision rules;
- whether the project filter was strict or relaxed cross-project;
- canonical topic counts and conflict candidates when multiple returned observations share a `topic_key`.

This makes the agent's memory contract explicit in the returned data, not only in setup instructions. If `conflict_candidates` are present, the agent should call `timeline` before making a claim that depends on the corrected history.

## Trust Ledger

Observations are not raw transcripts, but they still need provenance. v2.1.0 adds `observation_events`, an append-only local ledger linked to each observation. Events currently include:

- `created` when a distilled observation is inserted;
- `updated` when content, type, title, topic, or metadata changes;
- `reasserted` when a duplicate durable memory refreshes `duplicate_count` and `valid_at`;
- `imported` when export/import or sync brings an observation into a store;
- `soft_deleted` when `forget` invalidates a memory without removing audit history.

Existing observations are backfilled with a `created` migration event on first init after upgrade. The ledger is exported and synced with memory data, but it stores lifecycle metadata only; it does not store private raw transcript.

## Session lifecycle behavior

A session is active only until a client closes it through `summary`, `session-end`, or a native stop hook. Clients without reliable stop events can leave rows marked active, so v2.0.6 auto-closes active sessions after 24 hours without prompts or observations. The summary is explicit: `Auto-closed by Kerebrom after 24h without activity.` This keeps `active_sessions` meaningful without deleting prompts, observations, or session history.

## Deep Doctor

`kerebrom doctor --deep` is the operational readiness check. It verifies the runtime DB file, schema, `PRAGMA integrity_check`, `PRAGMA foreign_key_check`, missing `valid_at`, trust-ledger coverage, active duplicate hashes, FTS row availability, stale active sessions, project alias cycles, installed binary paths, Codex/Claude config blocks, factory version alignment, public-doc private path leaks, and ignored binary artifacts. It exits non-zero only on FAIL; WARN means the install is usable but deserves attention.

## Self-update flow

`kerebrom update` runs the following pipeline:

1. **Check** — `GET https://api.github.com/repos/<repo>/releases/latest` and compare the returned tag against `version.Version`. If the candidate is not strictly newer, exit cleanly.
2. **Confirm** — print release notes URL and (unless `--yes`) ask the user.
3. **Download** — fetch `https://github.com/<repo>/archive/refs/tags/<tag>.tar.gz` into a per-run temp directory.
4. **Extract** — gzip + tar into the temp directory, validating against path traversal and unsafe symlinks.
5. **Install** — `cd <tmp>/<repo>-<tag>/versions/v2 && make install-user`. The `install-user` target uses `cp` to a temporary file and then `mv -f` for atomic replacement; on macOS/Linux the running process keeps executing from its open file descriptor until exit.
6. **Cleanup** — remove the temp directory.

Requirements: Git is **not** required. `make` and `go` are required (same requirement as the original install). `--check` runs only steps 1–2 with exit code 10 if an update is available, useful for scripting.

## Setup flow

`kerebrom setup auto` detects each client's local config and writes:

- **Claude Code** (`~/.claude/`): MCP server entry in `mcp.json`, hooks in `settings.json`, the six agent-profile `mcp__Kerebrom__*` entries in `permissions.allow` (and removes any stale Kerebrom permissions outside the active agent surface), the protocol block in `CLAUDE.md`, and five hook scripts in `~/.kerebrom/hooks/claude-code/`.
- **Claude Desktop** (`~/Library/Application Support/Claude/` on macOS): MCP server entry in `claude_desktop_config.json` and, when Cowork local account storage exists, an idempotent Kerebrom block in `local-agent-mode-sessions/<account>/<org>/memory/CLAUDE.md`. Claude Chat account memory is cloud-backed and is not patched through private APIs or browser databases.
- **Codex** (`~/.codex/`): MCP server in `config.toml` with auto-approval for the six agent tools, plus the protocol block in `AGENTS.md`.
- **Cursor** (`~/.cursor/`): MCP entry in `mcp.json`, protocol rule in `rules/kerebrom.mdc`.
- **Gemini CLI** (`~/.gemini/`): MCP entry in `settings.json`, protocol in `system.md`, env var enabling system.md.
- **OpenCode** (`~/.config/opencode/`): MCP entry in `opencode.json`, protocol in `kerebrom-memory.md`.
- **Windsurf** (`~/.codeium/windsurf/`): MCP config + global `~/.windsurfrules`.
- **VS Code** (`~/Library/Application Support/Code/User/` on macOS): MCP entry, protocol prompt instructions.

`setup auto` falls back to `claude-desktop` when no client config is detected. `setup all` covers everything explicitly. `setup <agent>` targets one client.

The public repository also carries installer-facing agent instructions:

- Root `AGENTS.md` for Codex/OpenAI-style agents.
- Root `CLAUDE.md` for Claude-aware agents.
- Root `docs/AI_AGENT_INSTALL.md` for any agent or human installer.

Those files are documentation surfaces, but they are part of the install contract: a user should be able to ask an AI agent to install the repository and have the agent land on the same `versions/v2 + make install-user + verify + restart clients` path as the README.

## Storage

`internal/store/sqlite` owns the only persistent state. Schema is additive across releases:

```sql
sessions(id PK, project, directory, started_at, ended_at, summary)
observations(id PK, session_id FK, type, title, content, project, scope,
             topic_key, tool_name, normalized_hash, created_at, updated_at,
             valid_at, deleted_at, last_seen_at, duplicate_count, revision_count)
observation_events(id PK, observation_id FK, event_type, actor, reason,
                   related_observation_id, created_at)
prompts(id PK, session_id FK, project, content, created_at)
observations_fts (FTS5 virtual table mirroring observations content)
prompts_fts (FTS5 virtual table mirroring prompts content)
```

PRAGMA: `journal_mode=WAL`, `synchronous=NORMAL`, `foreign_keys=ON`, `busy_timeout=5000`. Triggers keep FTS5 in sync. Idempotent migrations via `IF NOT EXISTS` in `Init()`.

## Sync

`internal/sync` writes append-only chunks to `~/.kerebrom/chunks/<chunkid>.jsonl.gz` with a `manifest.json` index. The chunk id is the truncated SHA-256 of the chunk's raw JSONL content. Import is idempotent via `MarkSyncChunkImported`. Designed for filesystem-level sync (iCloud, Dropbox, git), not for cross-user sharing.

## Delete Safety

`forget` is available in the default agent profile for soft-delete invalidation when the user says a memory is wrong. Permanent hard delete is blocked in the default MCP agent profile and requires an MCP profile that includes the admin surface, such as `--tools=all` or an explicit allowlist with both `forget` and `projects`. This keeps everyday memory cleanup reversible by default.

## Updater testing

The updater is testable end-to-end:
- `parseSemver` and `isNewer` are pure functions with table-driven tests.
- `Run` accepts overridable `ReleasesAPI`, `TarballURLTemplate`, `HTTPClient`, and IO streams. Tests use `httptest.NewServer` to mock GitHub responses and validate the check-only path and the up-to-date path.
- The full install path requires `make` and `go` and writes to the user's filesystem; it is exercised by the smoke test (manual) rather than the unit suite.

## What is intentionally NOT in v2

- **Aliases for v1 `mem_*` tools.** v2 is a clean break. v1 remains in `versions/v1/` for users who need the old names.
- **Cloud sync.** Sync stays filesystem-based.
- **Programmatic update of Claude Personal Preferences.** Anthropic does not expose a public API for it.
- **Telemetry.** No anonymized usage data, no crash reports, nothing.

## Where to extend

| You want to … | Touch |
|---|---|
| Add a new MCP tool | `internal/transport/mcp/server.go` (registerTools + handler) and add to `profileAgentTools` or `profileAdminTools` |
| Add a new client to setup | `internal/setup/setup.go` (new `setup<Client>` function + entry in `Run`/`SupportedAgents`/`setupAll`) |
| Change the protocol text | `memoryProtocolText` in `internal/transport/mcp/server.go` and `memoryProtocolBlock`/`codexAGENTSBlock` in `internal/setup/setup.go` |
| Change the update behavior | `internal/updater/update.go` (Config struct + Run flow) |
| Add a new CLI command | `internal/cli/app.go` (case in `Run` + `runFoo` function) |
| Change end-user installation behavior | `Makefile`, `internal/setup/setup.go`, root `AGENTS.md`, root `CLAUDE.md`, and `docs/AI_AGENT_INSTALL.md` |
