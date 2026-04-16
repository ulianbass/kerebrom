# Kerebrom v1 Product Spec

## Product goal

Deliver a serious, local-first persistent memory product that matches the user-facing behavior of Engram where that behavior is compatible with Kerebrom's ownership and clean-room constraints.

## v1 scope

- Single Go binary.
- Shared local memory store.
- SQLite + FTS5 retrieval.
- CLI, MCP, HTTP, and terminal dashboard surfaces.
- Setup flows for supported local agents.
- Session lifecycle, proactive save, passive capture, and compaction recovery.
- Export/import and git-based sync.
- Store-layer privacy redaction for `<private>...</private>`.
- Lifecycle hooks where an AI client exposes a local hook API.
- MCP prompt/resource protocol for MCP-only clients such as Claude Desktop.
- Streamable HTTP MCP transport for remote connector clients that cannot execute a local `stdio` MCP server.

## Non-goals

- SaaS or multi-tenant cloud hosting.
- Full OAuth remote connector onboarding.
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
- `auto`
- `all`

On macOS, explicit `claude` setup writes both Claude Code config under `~/.claude/` and Claude Desktop config under `~/Library/Application Support/Claude/claude_desktop_config.json`, preserving existing MCP servers and desktop preferences. Explicit `claude-code` only configures Claude Code hooks/config. Explicit `claude-desktop` only configures Claude Desktop MCP.

`setup auto` is the default user-install path. It configures clients with existing local config and falls back to Claude Desktop when no client config is detected. `setup all` remains available for users who intentionally want every supported client registered.

### Automation model

Kerebrom v1 uses the strongest integration each AI client exposes:

- Hook-capable clients get active lifecycle automation: session start, prompt ingest, compaction recovery, subagent passive capture, and session stop.
- MCP-only clients get MCP registration plus a mandatory memory protocol exposed as MCP prompt/resource and reinforced in tool descriptions. This instructs proactive `mem_save_prompt`, `mem_context`, `mem_search`, `mem_save`, and `mem_session_summary` behavior.
- Cloud clients that cannot run local `stdio` MCP get a Streamable HTTP MCP endpoint via `kerebrom mcp-http`. They still require a reachable HTTPS URL and connector registration in the host product.
- No client gets raw transcript scraping by default; durable memory is still stored through Kerebrom's explicit memory store and redaction pipeline.
- Durable observations are agent-distilled, not transcript copies. `mem_save` uses the `What / Why / Where / Learned` framework so future agents recover context quickly. `mem_save_prompt` stores user prompts separately as intent history, not as canonical observation memory.

Claude Code currently receives full lifecycle hooks through `~/.claude/settings.json` and scripts under `~/.kerebrom/hooks/claude-code/`. Claude Desktop receives MCP server registration plus the Kerebrom memory protocol prompt/resource because Anthropic's Claude Desktop MCP surface exposes tools, prompts, and resources, not the Claude Code per-turn hook lifecycle. Codex Desktop, Cursor, Windsurf, VS Code, Gemini CLI, and OpenCode receive the highest local automation available through their current MCP/prompt/config surfaces. OpenCode is MCP/instruction-based in v1; event-plugin parity remains a separate product decision.

Claude Chat/Cowork and ChatGPT-style cloud surfaces cannot launch a user's local Kerebrom binary by reading local config files. For those surfaces, Kerebrom must be reachable as a remote MCP server over Streamable HTTP. `mcp-http` provides the transport; user-hosted HTTPS exposure and OAuth-grade onboarding remain a separate production deployment concern.

### Lifecycle

Sessions must be startable, summarizable, recoverable after compaction, and closable without losing learnings.

Session start is idempotent: repeated lifecycle hooks for the same `session_id` must not create duplicate sessions or reactivate a session that has already been completed.

Store init repairs legacy lifecycle inconsistencies by marking sessions with a populated `ended_at` as completed.

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

The MCP server also exposes:

- Prompt: `kerebrom_memory_protocol`
- Resource: `kerebrom://memory-protocol`

Both carry the same mandatory memory workflow for MCP-only clients.

For agent setup, Kerebrom uses `kerebrom mcp --tools=agent` to expose the 11 tools agents need during normal work. `--tools=admin`, `--tools=all`, and explicit comma-separated tool names remain available for curation and advanced workflows.

For remote MCP connector setup, Kerebrom uses:

```bash
KEREBROM_REMOTE_TOKEN="change-me" kerebrom mcp-http --addr 127.0.0.1:7437 --path /mcp --tools=agent
```

Non-loopback addresses require either `--auth-token`, `KEREBROM_REMOTE_TOKEN`, or an explicit `--allow-public-unauthenticated` flag. This prevents accidental public exposure of private local memory.

### Sync and portability

`kerebrom export` writes portable JSON. `kerebrom import` imports it idempotently. `kerebrom sync` writes append-friendly gzipped JSONL chunks with a `.kerebrom/manifest.json` index and tracks imported chunks in SQLite.

### Migration

Legacy memory is not bulk-restored into v1. It is reviewed and re-ingested from staged export material.

## v1 release final criteria

- `go test ./...` passes.
- `go build -o bin/kerebrom ./cmd/kerebrom` passes.
- Smoke test covers lifecycle, save/search/context, export, sync, projects, TUI, `setup auto`, `setup all`, and Streamable HTTP MCP.
- No Kerebrom legacy runtime or old DB is required for a clean install.
