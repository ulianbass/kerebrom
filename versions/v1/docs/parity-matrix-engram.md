# Engram Parity Matrix

| Surface | Engram behavior target | Kerebrom v1 target |
| --- | --- | --- |
| Binary install | Single binary | Match |
| Local storage | SQLite + FTS5 | Match |
| MCP tools | 15 `mem_*` tools plus optional `--tools` profiles | Match: all 15 tools exist; `--tools=agent`, `--tools=admin`, `--tools=all`, and explicit tool allowlists are supported |
| MCP prompts/resources | Memory protocol surfaced to compatible clients | Match: `kerebrom_memory_protocol` prompt and `kerebrom://memory-protocol` resource |
| HTTP API | Local loopback service | Match: sessions, observations, prompts, context, timeline, stats, export/import, projects |
| CLI | setup/serve/mcp/mcp-http/tui/search/save/context/timeline/stats/export/import/sync/projects | Match |
| TUI | Browse/search/detail/timeline/sessions | Partial: terminal dashboard + interactive search/show/timeline/sessions/prompts exist; richer Engram-style Bubble Tea UX remains open if visual parity is required |
| Agent setup | One-command setup where feasible | Match for Codex, Claude Code, Claude Desktop, Gemini CLI, Cursor, Windsurf, VS Code; partial for OpenCode until event plugin parity exists |
| Selective install | Avoid unnecessary client config | Match: `setup auto` by default, explicit `setup all` for power users |
| Compaction recovery | Prompt or hook-based | Match through Claude Code lifecycle hook plus installed memory protocol and recovery prompts/rules elsewhere |
| Passive capture | Extract `## Key Learnings` | Match via Claude Code `SubagentStop` hook and `mem_capture_passive`; partial for OpenCode until Task-output event capture exists |
| Privacy tags | Strip `<private>...</private>` | Match at store layer |
| Sync | Git chunks + manifest | Match with `.kerebrom/manifest.json` and gzipped JSONL chunks |
| Cross-agent memory | Same store, same project | Match for shared local store plus default MCP project fallback from `--project`, `KEREBROM_PROJECT`, or cwd/git detection; clients should still pass explicit project when known |
| Session idempotency | Repeated hooks should not inflate session counts | Match: repeated `session_id` starts do not duplicate or reactivate completed sessions |
| Remote/cloud clients | Remote MCP where supported | Partial: `mcp-http` exposes Streamable HTTP MCP, but hosted HTTPS/OAuth onboarding for Claude Chat/Cowork and ChatGPT is not yet packaged |

## Contract notes

- Kerebrom keeps the `mem_*` namespace in v1 for compatibility.
- Behavioral parity matters more than internal file structure parity.
- Kerebrom v1 does not copy Engram plugin code. Agent setup is clean-room and config/protocol based.
- Automation depth is client-dependent: lifecycle hooks where supported, MCP plus strict protocol where the native app exposes no hook surface. Claude Desktop local chat is MCP-only; Claude Code gets per-turn hooks; cloud surfaces need remote MCP.
- Any future Kerebrom-only enhancements must not compromise the v1 compatibility surface.
- As of `v1.0.8`, MCP profile allowlisting, default-project fallback, and MCP initialize-time memory authority instructions are implemented. Full cross-client parity is still not closed until OpenCode event-plugin parity and richer visual TUI parity are either implemented or explicitly declared out of scope.
