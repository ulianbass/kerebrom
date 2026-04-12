# Kerebrom v1 Release Checklist

## Foundation

- Clean-room ADRs accepted.
- Shared local memory ADR accepted.
- Repo structure stable.
- Product docs aligned with actual contract.

## Runtime

- [x] SQLite schema and migrations implemented.
- [x] FTS5 retrieval implemented.
- [x] MCP contract implemented with 15 `mem_*` tools.
- [x] MCP prompt/resource memory protocol implemented for Claude Desktop and other MCP-only clients.
- [x] HTTP API implemented for sessions, observations, prompts, context, timeline, stats, export/import, and projects.
- [x] CLI contract implemented for setup, serve, mcp, tui, search, save, context, timeline, stats, export, import, sync, and projects.
- [x] TUI functional for dashboard, recent observations, search, observation detail, timeline, sessions, and prompts.
- [x] Store-layer private tag redaction implemented.
- [x] Hook runner implemented for lifecycle-capable clients.
- [x] Repeated lifecycle hooks are idempotent and do not reactivate completed sessions.
- [x] Store init repairs legacy sessions where `ended_at` is populated but status is still active.

## Integrations

- [x] Codex setup works.
- [x] Claude / Claude Code / Claude Desktop setup works through MCP and memory protocol files.
- [x] `setup auto` configures detected clients and avoids creating every client config by default.
- [x] Claude Code lifecycle hooks are installed for session start, user prompt ingest, subagent passive capture, stop, and post-compaction recovery.
- [x] Cursor setup works.
- [x] Gemini CLI setup works.
- [x] OpenCode setup works through MCP plus instruction registration.
- [x] Windsurf setup works.
- [x] VS Code setup works.

## Quality gates

- [x] Contract tests for `mem_*` tools pass.
- [x] Contract tests for MCP prompt/resource memory protocol pass.
- [x] End-to-end lifecycle tests pass.
- [x] Cross-agent recall works for the same normalized project against the same local store.
- [x] Hook smoke verifies automatic session, prompt ingest, passive capture, and session close without polluting the production DB.
- [x] Export/import and sync are deterministic enough for v1 backup and git-chunk sharing.
- [x] A clean install can go from binary to working memory with `kerebrom setup <agent>`.
- [x] Default user install uses `kerebrom setup auto`; `setup all` remains explicit.

## Verification commands

```bash
go test ./...
go build -o bin/kerebrom ./cmd/kerebrom
```
