# Engram Parity Matrix

| Surface | Engram behavior target | Kerebrom v1 target |
| --- | --- | --- |
| Binary install | Single binary | Match |
| Local storage | SQLite + FTS5 | Match |
| MCP tools | 15 `mem_*` tools plus optional `--tools` profiles | Partial: save/search/update/delete/topic/prompt/context/stats/timeline/get/session/passive/merge exist; profile allowlisting remains open |
| MCP prompts/resources | Memory protocol surfaced to compatible clients | Match: `kerebrom_memory_protocol` prompt and `kerebrom://memory-protocol` resource |
| HTTP API | Local loopback service | Match: sessions, observations, prompts, context, timeline, stats, export/import, projects |
| CLI | setup/serve/mcp/tui/search/save/context/timeline/stats/export/import/sync/projects | Match |
| TUI | Browse/search/detail/timeline/sessions | Partial: terminal dashboard + interactive search/show/timeline/sessions/prompts exist; richer Engram-style Bubble Tea UX remains open if visual parity is required |
| Agent setup | One-command setup where feasible | Match for Codex, Claude Code, Claude Desktop, Gemini CLI, Cursor, Windsurf, VS Code; partial for OpenCode until event plugin parity exists |
| Selective install | Avoid unnecessary client config | Match: `setup auto` by default, explicit `setup all` for power users |
| Compaction recovery | Prompt or hook-based | Match through Claude Code lifecycle hook plus installed memory protocol and recovery prompts/rules elsewhere |
| Passive capture | Extract `## Key Learnings` | Match via Claude Code `SubagentStop` hook and `mem_capture_passive`; partial for OpenCode until Task-output event capture exists |
| Privacy tags | Strip `<private>...</private>` | Match at store layer |
| Sync | Git chunks + manifest | Match with `.kerebrom/manifest.json` and gzipped JSONL chunks |
| Cross-agent memory | Same store, same project | Partial: same store exists and Claude hooks detect git remote/root project identity; MCP default project handling remains open |
| Session idempotency | Repeated hooks should not inflate session counts | Match: repeated `session_id` starts do not duplicate or reactivate completed sessions |
| Remote-only desktop clients | Not core | Out of scope for v1 |

## Contract notes

- Kerebrom keeps the `mem_*` namespace in v1 for compatibility.
- Behavioral parity matters more than internal file structure parity.
- Kerebrom v1 does not copy Engram plugin code. Agent setup is clean-room and config/protocol based.
- Automation depth is client-dependent: lifecycle hooks where supported, MCP plus strict protocol where the native app exposes no hook surface. Claude Desktop is explicitly MCP-only in v1; Claude Code gets per-turn hooks.
- Any future Kerebrom-only enhancements must not compromise the v1 compatibility surface.
- As of v1.0.5, Claude Code hook lifecycle parity is aligned for first-prompt bootstrap and quiet subsequent prompt capture. Full cross-client parity is not closed until the gaps documented in `engram-clean-room-audit-v1.0.5.md` are resolved.
