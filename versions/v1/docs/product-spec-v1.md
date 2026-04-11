# Kerebrom v1 Product Spec

## Product goal

Deliver a serious, local-first persistent memory product that matches the user-facing behavior of Engram where that behavior is compatible with Kerebrom's ownership and clean-room constraints.

## v1 scope

- Single Go binary.
- Shared local memory store.
- SQLite + FTS5 retrieval.
- CLI, MCP, HTTP, and TUI surfaces.
- Setup flows for supported local agents.
- Session lifecycle, proactive save, passive capture, and compaction recovery.
- Export/import and git-based sync.
- Store-layer privacy redaction for `<private>...</private>`.
- Lifecycle hooks where an AI client exposes a local hook API.

## Non-goals

- SaaS, auth, or multi-tenant cloud.
- Remote-only client support.
- Vector search in v1.
- Raw tool-call persistence.
- Copying Engram plugin implementation code.

## Core product behaviors

### Installation

Kerebrom installs as one binary with no runtime dependency stack beyond the OS.

### Agent setup

`kerebrom setup <agent>` configures MCP, local prompts/rules, and recovery instructions idempotently for:

- `codex`
- `claude` / `claude-code` / `claude-desktop`
- `gemini-cli`
- `opencode`
- `cursor`
- `windsurf`
- `vscode`
- `all`

On macOS, Claude setup writes both Claude Code config under `~/.claude/` and Claude Desktop config under `~/Library/Application Support/Claude/claude_desktop_config.json`, preserving existing MCP servers and desktop preferences.

### Automation model

Kerebrom v1 uses the strongest integration each AI client exposes:

- Hook-capable clients get active lifecycle automation: session start, prompt ingest, compaction recovery, subagent passive capture, and session stop.
- MCP-only clients get MCP registration plus mandatory memory protocol instructions that force proactive `mem_context`, `mem_search`, `mem_save`, and `mem_session_summary` behavior.
- No client gets raw transcript scraping by default; durable memory is still stored through Kerebrom's explicit memory store and redaction pipeline.
- Durable observations are agent-distilled, not transcript copies. `mem_save` uses the `What / Why / Where / Learned` framework so future agents recover context quickly. `mem_save_prompt` stores user prompts separately as intent history, not as canonical observation memory.

Claude Code currently receives full lifecycle hooks through `~/.claude/settings.json` and scripts under `~/.kerebrom/hooks/claude-code/`. Codex Desktop, Claude Desktop, Cursor, Windsurf, VS Code, Gemini CLI, and OpenCode receive the highest local automation available through their current MCP/prompt/config surfaces.

### Lifecycle

Sessions must be startable, summarizable, recoverable after compaction, and closable without losing learnings.

### Retrieval

Recall must work across agents when they share the same normalized project identity and local store.

### MCP contract

v1 exposes the full 15-tool `mem_*` compatibility surface:

- `mem_save`
- `mem_search`
- `mem_update`
- `mem_delete`
- `mem_suggest_topic_key`
- `mem_save_prompt`
- `mem_context`
- `mem_stats`
- `mem_timeline`
- `mem_get_observation`
- `mem_session_summary`
- `mem_session_start`
- `mem_session_end`
- `mem_capture_passive`
- `mem_merge_projects`

`mem_context` returns recent sessions, prompts, observations, stats, and optional search matches.

### Sync and portability

`kerebrom export` writes portable JSON. `kerebrom import` imports it idempotently. `kerebrom sync` writes append-friendly gzipped JSONL chunks with a `.kerebrom/manifest.json` index and tracks imported chunks in SQLite.

### Migration

Legacy memory is not bulk-restored into v1. It is reviewed and re-ingested from staged export material.

## v1 release final criteria

- `go test ./...` passes.
- `go build -o bin/kerebrom ./cmd/kerebrom` passes.
- Smoke test covers lifecycle, save/search/context, export, sync, projects, TUI, and `setup all`.
- No Kerebrom legacy runtime or old DB is required for a clean install.
