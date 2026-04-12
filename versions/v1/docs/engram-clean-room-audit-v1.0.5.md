# Kerebrom v1.0.5 Clean-Room Engram Audit

Date: 2026-04-12

Scope: behavior parity review against the local clean reference at `/tmp/engram-readonly`, commit `326f937`, with focus on lifecycle hooks, MCP surface, setup, session handling, prompt capture, context retrieval, passive ingest, sync/import/export, and dashboard/TUI behavior.

This audit compares responsibilities and observable behavior. It does not copy Engram implementation code.

## Executive Finding

Kerebrom v1.0.5 is stronger than v1.0.4 on Claude Code lifecycle semantics: `UserPromptSubmit` now captures prompts every turn without behaving like repeated `SessionStart`. This matches the important Engram distinction:

- `SessionStart` bootstraps session context.
- `UserPromptSubmit` runs every user prompt, but is mostly silent after the first prompt.
- Repeated session ensures must be idempotent and must not inflate sessions.

Paridad cerrada should not yet be claimed for every client. The main remaining functional gap is OpenCode: Engram installs an event plugin that passively captures prompts, tool activity, compaction context, and passive Task learnings. Kerebrom currently configures OpenCode through MCP plus instructions, not a first-class event plugin.

## Claude Code Lifecycle

Engram reference:

- `plugin/claude-code/hooks/hooks.json` registers `SessionStart`, `UserPromptSubmit`, `SubagentStop`, and `Stop`.
- `SessionStart` uses `startup|clear` and `compact` matchers with status messages.
- `user-prompt-submit.sh` always exits successfully with valid JSON.
- First prompt injects bootstrap instructions.
- Later prompts return `{}` unless a save reminder is warranted.
- Reminder logic waits until the session is older than five minutes and the last save is older than fifteen minutes.

Kerebrom v1.0.5:

- `SessionStart`, `post-compaction`, `UserPromptSubmit`, `SubagentStop`, and `Stop` are registered in `~/.claude/settings.json` through `setupClaudeCode`.
- `SessionStart` creates or refreshes the session and injects protocol/context.
- `UserPromptSubmit` saves prompts on every non-empty prompt.
- `UserPromptSubmit` only injects the strong bootstrap context on the first prompt for that session.
- Later prompts return `{}` unless the save-reminder window is reached.
- Session creation is guarded with `SessionExists` and prompt counting; missing sessions are created lazily, but existing sessions are not repeatedly restarted.

Status: aligned for the issue that triggered this audit.

## Session Semantics

Engram reference:

- OpenCode adapter uses `ensureSession(sessionId)` before DB writes.
- It keeps an in-memory `knownSessions` set so repeated calls do not repeatedly hit the server.
- The server path is idempotent.
- Completed/deleted/subagent sessions should not inflate top-level session counts.

Kerebrom v1.0.5:

- Store-level `StartSession` is idempotent for the same `session_id`.
- Completed sessions are not reactivated by later starts.
- Store init repairs sessions with populated `ended_at` but stale `active` status.
- Claude Code prompt hook now checks existence before creating a missing session.

Gap:

- Kerebrom does not yet suppress subagent-created sessions generically across every event-capable client. Claude `SubagentStop` captures learnings, but OpenCode-style subagent session suppression is not implemented because Kerebrom does not yet have the OpenCode event plugin.

## Prompt Capture

Engram reference:

- Claude Code: `UserPromptSubmit` first-message bootstrap plus later save reminder.
- OpenCode: `chat.message` event extracts user text and POSTs prompt history for non-trivial prompts.
- Prompt capture is separate from canonical observation memory.

Kerebrom v1.0.5:

- Claude Code: prompt capture now aligns with the first-message/silent-later pattern.
- MCP-only clients: `mem_save_prompt` is exposed and emphasized in protocol text.
- Codex: global instructions require `mem_save_prompt` at the start of non-trivial turns.

Gap:

- OpenCode does not yet have event-level automatic prompt capture. It relies on MCP instructions.

## MCP Tool Surface

Engram reference exposes fifteen `mem_*` tools:

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

Kerebrom v1.0.5 exposes the same fifteen tool names.

Gaps:

- Engram supports `mcp --tools=agent`, `mcp --tools=admin`, and explicit tool-name allowlists to reduce tool context. Kerebrom currently exposes all tools through `kerebrom mcp`.
- Engram has default project detection for MCP calls with empty project input. Kerebrom mainly relies on explicit project arguments and store normalization.

## Project Detection

Engram reference:

- Project detection priority is git remote repository name, then git root basename, then cwd basename.
- This reduces project fragmentation across agents and worktrees.

Kerebrom v1.0.5:

- Store normalization is stable: lowercase, underscore-to-dash, whitespace-to-dash.
- Hook project detection uses explicit project if provided, otherwise git remote repository name, git root basename, then cwd basename.

Remaining gap:

- MCP calls with no explicit project should also get a configurable default project, similar to Engram's MCP default project handling.

## Passive Capture

Engram reference:

- Claude Code `SubagentStop` hook and OpenCode `Task` output capture `## Key Learnings`.
- `mem_capture_passive` is also available as a direct MCP tool.

Kerebrom v1.0.5:

- `SubagentStop` extracts key learnings.
- `mem_capture_passive` extracts key learnings through MCP.

Gap:

- OpenCode Task-output passive capture is missing until an event plugin exists.

## Context Retrieval

Engram reference:

- `mem_context` returns recent project memory and context.
- Search results are previews; `mem_get_observation` retrieves full content.

Kerebrom v1.0.5:

- `mem_context` returns stats, recent sessions, recent prompts, recent observations, and optional search matches.
- `mem_get_observation` retrieves full content.

Status: aligned at the behavior level.

## Setup and User Experience

Engram reference:

- Agent setup writes the strongest available integration for each supported client.
- For supported MCP agents, setup uses `--tools=agent` to reduce context footprint.
- Claude Code has hooks, MCP, and protocol instructions.
- OpenCode has an event plugin plus MCP.

Kerebrom v1.0.5:

- `setup auto` detects existing client configs and installs Kerebrom only where relevant.
- Claude Code gets hooks and MCP config.
- Claude Desktop gets MCP server plus prompt/resource protocol.
- Codex, Gemini CLI, Cursor, Windsurf, VS Code, and OpenCode get MCP/config/instructions.

Gaps:

- OpenCode event plugin is not yet equivalent to Engram.
- MCP tool profile allowlisting is not yet implemented.
- Project detection is weaker than Engram.

## Dashboard / TUI

Engram reference:

- Has a richer Bubble Tea TUI with dashboard, recent, search, detail, and timeline screens.

Kerebrom v1.0.5:

- Has a terminal dashboard and interactive command loop for recent observations, search, detail, timeline, sessions, and prompts.

Gap:

- Functional coverage exists, but visual/interaction parity is not exact. If "Engram experience" includes the richer Bubble Tea feel, this remains a UX parity gap.

## Release Decision

v1.0.5 is valid as a lifecycle bugfix release.

It should not be described as final closed parity across every client. The next parity-closing work should be:

1. Add Engram-equivalent MCP tool profiles and update setup configs to use `mcp --tools=agent` where appropriate.
2. Add MCP default project handling for empty project input.
3. Add a clean-room OpenCode event plugin for prompt capture, session ensure, compaction recovery, and Task passive capture.
4. Decide whether the TUI parity target is functional-only or includes Engram's richer Bubble Tea UX.
