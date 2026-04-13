# Kerebrom Clean-Room Audit Against Engram v1.12.0

Baseline: Engram `v1.12.0`, tag commit `9f87509a3fa09e892c9a59408ccbdbf28c2c199b`. Engram `origin/main` was also checked during the audit and only added README changes beyond that tag.

## Closed In This Pass

- MCP tool profiles: `kerebrom mcp --tools=agent`, `--tools=admin`, `--tools=all`, and explicit comma-separated tool allowlists are implemented. Agent setup now uses `--tools=agent` to reduce tool context.
- MCP default project fallback: empty project inputs now fall back to `--project`, then `KEREBROM_PROJECT`, then cwd/git detection, and finally `default`.
- MCP session fallback: `mem_save`, `mem_save_prompt`, `mem_session_summary`, and passive capture now use a stable `mcp:<project>` session when MCP-only clients omit `session_id`.
- MCP activity nudges: the server tracks per-session tool activity and save activity, then returns save reminders/activity score when a long active MCP session has not persisted durable learnings.
- Store consistency: `EndSession` is retry-safe, `SearchObservations` resolves exact `topic_key` before FTS fallback, duplicate observation repair backs a partial unique index on active `normalized_hash`, and project merge is transactional.
- Prompt noise filter: exact casual-only prompts are filtered without dropping short but meaningful commands like `Hazlo` or `Borra eso`.
- Claude prompt-hook reminder: save reminders now look at the current session rather than the whole project, so another agent's recent save cannot silence this session.
- VS Code setup: user-level config paths are OS-aware instead of macOS-only.

## Still Not Claimed As Full Visual/Product Parity

- OpenCode remains MCP plus instructions, not Engram's event plugin model with native `chat.message`, compaction, and tool-output capture hooks.
- The `tui` command is a terminal dashboard with an interactive command loop, not a full Bubble Tea visual TUI equivalent.
- Engram v1.12.0 includes Obsidian export/plugin work. Kerebrom v1 does not claim Obsidian parity unless that surface is explicitly accepted into the Kerebrom roadmap.

## Verification

```bash
go test ./...
```

Result during this audit: passing.
