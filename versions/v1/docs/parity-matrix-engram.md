# Engram Parity Matrix

| Surface | Engram behavior target | Kerebrom v1 target |
| --- | --- | --- |
| Binary install | Single binary | Match |
| Local storage | SQLite + FTS5 | Match |
| MCP tools | 15 `mem_*` tools | Match: save/search/update/delete/topic/prompt/context/stats/timeline/get/session/passive/merge |
| MCP prompts/resources | Memory protocol surfaced to compatible clients | Match: `kerebrom_memory_protocol` prompt and `kerebrom://memory-protocol` resource |
| HTTP API | Local loopback service | Match: sessions, observations, prompts, context, timeline, stats, export/import, projects |
| CLI | setup/serve/mcp/tui/search/save/context/timeline/stats/export/import/sync/projects | Match |
| TUI | Browse/search/detail/timeline/sessions | Match via terminal dashboard + interactive search/show/timeline/sessions/prompts |
| Agent setup | One-command setup where feasible | Match for Codex, Claude Code, Claude Desktop, Gemini CLI, OpenCode, Cursor, Windsurf, VS Code |
| Compaction recovery | Prompt or hook-based | Match through Claude Code lifecycle hook plus installed memory protocol and recovery prompts/rules elsewhere |
| Passive capture | Extract `## Key Learnings` | Match via Claude Code `SubagentStop` hook and `mem_capture_passive` |
| Privacy tags | Strip `<private>...</private>` | Match at store layer |
| Sync | Git chunks + manifest | Match with `.kerebrom/manifest.json` and gzipped JSONL chunks |
| Cross-agent memory | Same store, same project | Match |
| Remote-only desktop clients | Not core | Out of scope for v1 |

## Contract notes

- Kerebrom keeps the `mem_*` namespace in v1 for compatibility.
- Behavioral parity matters more than internal file structure parity.
- Kerebrom v1 does not copy Engram plugin code. Agent setup is clean-room and config/protocol based.
- Automation depth is client-dependent: lifecycle hooks where supported, MCP plus strict protocol where the native app exposes no hook surface. Claude Desktop is explicitly MCP-only in v1; Claude Code gets per-turn hooks.
- Any future Kerebrom-only enhancements must not compromise the v1 compatibility surface.
