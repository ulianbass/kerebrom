# Kerebrom v2 — Product Spec

## What it is

Kerebrom v2 is a single Go binary that gives AI coding agents shared persistent memory on the user's local machine. It exposes seven semantic MCP tools (`context`, `recall`, `remember`, `summary`, `forget`, `timeline`, `projects`) and installs the strongest available memory workflow into every supported AI client during setup.

## What it is not

- A cloud service. Nothing leaves the machine except the explicit GitHub Releases check during `kerebrom update`.
- A vector database. Storage is SQLite + FTS5 — fast, durable, and operable without ML infrastructure.
- A team-shared memory layer. v2 is single-user. Sharing across users is out of scope.
- A replacement for project-specific configuration files. `CLAUDE.md`, `AGENTS.md`, etc. continue to hold project-specific guidance; Kerebrom adds a marked block but does not replace the file.

## Who it is for

A solo developer or operator who uses multiple AI clients (Claude Desktop, Claude Code, Codex, Cursor, Gemini CLI, OpenCode, Windsurf, VS Code) and wants every client to share the same persistent context across conversations and across days.

## Success criteria

1. **Plug-and-play in Claude Desktop**: opening a fresh chat and asking "what do you know about my projects?" triggers an automatic `context` call without the user mentioning Kerebrom.
2. **Plug-and-play in Claude Code**: hooks fire on session start, prompt submit, subagent stop, stop, and post-compaction without manual intervention; the agent never asks permission to call memory tools.
3. **Self-update**: `kerebrom update` brings the user from any older release to the latest with one command and a single confirmation.
4. **No data loss across upgrades**: SQLite schema unchanged from v1, sync chunks unchanged.
5. **Cleanup of v1 leftovers**: a v1 user upgrading to v2 ends with no `mcp__Kerebrom__mem_*` entries lingering in their `permissions.allow`.

## Non-goals

- Programmatic modification of Claude Personal Preferences (no public API exists).
- Telemetry of any kind.
- Server-side memory consolidation beyond what the agent itself does in `summary`.
- Multi-user or networked sync (v2 sync chunks are still filesystem-based).

## Release contract

Captured in `manifest.json`:

| Field | Value |
|---|---|
| `version_line` | `v2` |
| `semver` | `v2.0.1` |
| `binary_name` | `kerebrom` |
| `storage_mode` | `local-first` |
| `store` | `sqlite+fts5` |
| `mcp_surface` | `semantic` |
| `mcp_tool_count` | `7` |
| `agent_profile_tools` | `6` |
| `admin_profile_tools` | `1` |
| `self_update_command` | `kerebrom update` |
| `sync_layout` | `.kerebrom/manifest.json + .kerebrom/chunks/*.jsonl.gz` |

Any change to these fields is a semver-major event.

## References

- [docs/architecture-v2.md](architecture-v2.md) — component map, MCP surface details, update flow, setup flow.
- [docs/migration-v1-to-v2.md](migration-v1-to-v2.md) — upgrade guide and rollback notes.
- [docs/adr/0003-v2-semantic-surface.md](adr/0003-v2-semantic-surface.md) — decision record for the surface rewrite.
- [docs/release-checklist-v2.md](release-checklist-v2.md) — what to verify before tagging.
