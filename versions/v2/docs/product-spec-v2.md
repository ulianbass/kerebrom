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

1. **Plug-and-play in Claude Desktop**: when the client exposes Kerebrom tools, any user message in a fresh chat should trigger an automatic `context` call before the model answers, without the user mentioning Kerebrom.
2. **Plug-and-play in Claude Code**: hooks fire on session start, prompt submit, subagent stop, stop, and post-compaction without manual intervention; the agent never asks permission to call memory tools.
3. **Cowork native bootstrap**: when Claude Desktop has local Cowork account storage, setup seeds Cowork's native `memory/CLAUDE.md` with an idempotent Kerebrom authority block.
4. **Self-update**: `kerebrom update` brings the user from any older release to the latest with one command and a single confirmation.
5. **No data loss across upgrades**: schema changes are additive and existing sessions, observations, prompts, and sync chunks continue to import.
6. **Cleanup of v1 leftovers**: a v1 user upgrading to v2 ends with no `mcp__Kerebrom__mem_*` entries lingering in their `permissions.allow`.
7. **Agent-installable repository**: Claude, Codex, Copilot, or another coding agent can read the repository-native install instructions and guide an end user through a safe `versions/v2` install without guessing.
8. **Activation without noisy saves**: Kerebrom should activate through `context` on every user message when tools are available, while `remember` remains limited to durable facts and does not save bare acknowledgements as observations.
9. **Global retrieval despite project drift**: clients that launch without a real workspace must not become isolated under weak projects such as `/` or `default`; reads fall back to cross-project memory and strong-project recalls can still surface better matches from another project.
10. **Project alias stability**: once a project variant is consolidated, future writes through the old name resolve to the canonical project instead of recreating fragmentation.
11. **Session lifecycle hygiene**: active sessions close through explicit summaries/hooks, and stale active sessions are auto-closed after 24 hours without activity.
12. **Chronological truth priority**: retrieval, context, and timeline use `valid_at` as the semantic clock so newer corrections and revalidated memories outrank stale observations, while administrative metadata updates do not make old information look current.
13. **Context Governor**: every `context`/`recall` payload includes an explicit decision contract telling the agent to think, search, analyze, then answer; prefer query matches over generic recency; and use `timeline` when conflicts appear.
14. **Trust Ledger**: every observation has local lifecycle events for creation, update/correction, duplicate reassertion, import, and soft deletion so memory provenance can be audited without storing raw transcripts.
15. **Deep Doctor**: `kerebrom doctor --deep` audits the installed vehicle, SQLite integrity, FTS, semantic clock, trust ledger coverage, active-session hygiene, project aliases, AI client configs, and factory drift.

## Non-goals

- Programmatic modification of Claude Chat account memory, Claude Personal Preferences, or cloud-backed memory controls through private APIs or browser databases.
- Telemetry of any kind.
- Semantic memory rewriting without explicit agent action. Kerebrom can consolidate project names, but it does not rewrite observation content by itself.
- Multi-user or networked sync (v2 sync chunks are still filesystem-based).
- Remote MCP as the default install path. `mcp-http` is advanced and opt-in because it changes the privacy boundary.

## Release contract

Captured in `manifest.json`:

| Field | Value |
|---|---|
| `version_line` | `v2` |
| `semver` | `v2.1.1` |
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
- [../../../docs/AI_AGENT_INSTALL.md](../../../docs/AI_AGENT_INSTALL.md) — end-user install guide for Claude/Codex-style agents.
- [docs/migration-v1-to-v2.md](migration-v1-to-v2.md) — upgrade guide and rollback notes.
- [docs/adr/0003-v2-semantic-surface.md](adr/0003-v2-semantic-surface.md) — decision record for the surface rewrite.
- [docs/release-checklist-v2.md](release-checklist-v2.md) — what to verify before tagging.
