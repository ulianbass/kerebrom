# ADR-0003: v2 semantic MCP surface

- **Status**: Accepted (2026-04-16)
- **Supersedes**: implicit `mem_*` namespace from v1.x
- **Related**: ADR-0001 (clean-room), ADR-0002 (shared local memory)

## Context

Kerebrom v1 exposed seventeen MCP tools, all prefixed `mem_*`: `mem_bootstrap`, `mem_save`, `mem_save_prompt`, `mem_search`, `mem_context`, `mem_session_start`, `mem_session_end`, `mem_session_summary`, `mem_capture_passive`, `mem_get_observation`, `mem_stats`, `mem_suggest_topic_key`, `mem_update`, `mem_delete`, `mem_timeline`, `mem_merge_projects`, plus a `recall` alias.

Field experience in Claude Desktop and Claude Chat showed the model would not invoke memory tools proactively even after the v1.1.0 hardening pass that strengthened the protocol text and added auto-approval. Investigation in April 2026 confirmed the cause: clients like Pieces, claude-mem, and Engram succeed because their tool names describe the action (`ask_pieces_ltm`, `recall`, `remember`, `context`), so the model picks the right one from the name alone. Kerebrom's `mem_*` prefix forced the model to read seventeen long descriptions to disambiguate, and in MCP-only clients it simply skipped the call.

The user requirement was explicit:
- A new release must feel "like the product was always this way".
- v1 instalations must keep working without breaking, but the new release should not carry visible v1 baggage.
- The agent should never need to be reminded that memory exists.

## Decision

Kerebrom v2 replaces the seventeen `mem_*` tools with a clean surface of seven semantic verbs:

| Tool | Replaces | Purpose |
|---|---|---|
| `context` | `mem_bootstrap`, `mem_context`, `mem_save_prompt`, `mem_session_start` | Open or resume a session, save the current prompt when substantive, return prior observations |
| `recall` | `mem_search`, `recall` | Search persisted observations by natural-language query |
| `remember` | `mem_save`, `mem_capture_passive`, `mem_update` | Persist a distilled observation (What/Why/Where/Learned) |
| `summary` | `mem_session_summary`, `mem_session_end` | Close substantial work with a structured summary |
| `forget` | `mem_delete` | Soft-delete an obsolete observation |
| `timeline` | `mem_timeline`, `mem_get_observation`, `mem_stats` | Inspect chronological history or recent observations |
| `projects` | `mem_merge_projects` | Administrative consolidation of project name variants |

`mem_*` tools are **removed entirely** from v2 — they are not registered as aliases. The setup command actively cleans `mcp__Kerebrom__mem_*` entries from any user `permissions.allow` it finds, so a fresh `kerebrom update` from v1.x leaves no residual surface.

The MCP `WithInstructions` text and every per-tool description are rewritten in terms of the new vocabulary and a four-step cycle:

1. `context` — activation step before answering every user message when tools are available.
2. `remember` — when a durable fact appears.
3. `recall` — when the user asks about a topic.
4. `summary` — closing substantial work.

The other three (`forget`, `timeline`, `projects`) are specialized and only invoked when explicitly relevant.

## Consequences

**Positive**
- The agent invokes memory automatically in MCP-only clients (Claude Desktop, Chat, Cowork) because tool names are intuition-friendly.
- The protocol text is 40 % shorter and reads as a coherent rhythm rather than a list of mandatory rules.
- The `permissions.allow` list shrinks from 17 to 7 entries.
- The Codex AGENTS.md block, the CLAUDE.md block, and every other client-facing instruction page are written in one consistent vocabulary.
- The SQLite schema is unchanged, so v1 data is fully accessible from v2 without migration.

**Negative / accepted**
- Breaking change for any external integration that hardcoded `mem_*` tool names. v1 stays maintained in `versions/v1/` for users who cannot migrate.
- A few capabilities that lived in their own v1 tool (e.g., `mem_suggest_topic_key`, `mem_capture_passive`) are no longer exposed as standalone MCP tools — the underlying behavior is still available via the consolidated tools or via the CLI.
- Users running `kerebrom setup` after upgrading will see their `permissions.allow` mutate (legacy entries removed). Documented in the migration guide.

## Alternatives considered

1. **Aliases bridge** — register the seven new names alongside the seventeen `mem_*` aliases as transparent shims. Rejected: the user explicitly asked for a clean release without v1 residue, and aliases would inflate `tools/list` to 24 entries, which dilutes the model's attention again.
2. **Rename in place inside v1.x** — bump to v1.2.0 with renames. Rejected: changing the declared `mcp_namespace` and `mcp_tool_count` in the manifest breaks the v1 release contract, which is a semver-major event.
3. **Expose only one tool** like Pieces' `ask_pieces_ltm`. Rejected: Kerebrom's domain (sessions, observations, projects, summaries) genuinely has multiple distinct verbs. Collapsing them into one would push complexity into the tool's parameters and lose clarity.

## Validation

- Tests verify the seven tools register with correct annotations, the agent profile exposes six (omitting the admin-only `projects`), and the four-step cycle works end to end (context → remember → recall → summary).
- The setup test verifies Codex auto-approves six entries (the agent profile), and the permissions-allow test verifies legacy `mcp__Kerebrom__mem_*` entries get removed on upgrade.
- The updater test verifies semver comparison and the check-only path against a mocked GitHub Releases endpoint.
- Smoke test in Claude Desktop will confirm that `context` is called automatically on the first turn of a new chat without the user mentioning Kerebrom.
