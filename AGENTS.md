# Kerebrom Repository Instructions For AI Agents

These instructions are for AI coding agents operating inside this repository.

## If The User Asks You To Install Kerebrom

Treat the task as an end-user product install, not a development task.

1. Read [docs/AI_AGENT_INSTALL.md](docs/AI_AGENT_INSTALL.md).
2. Use the current stable line: `versions/v2`.
3. Run the default install path:

```bash
cd versions/v2
make install-user
```

4. Verify:

```bash
kerebrom version
kerebrom stats
```

5. Tell the user to fully restart open AI clients so they reload MCP and native instruction files.

Use `SETUP_AGENT=all` only when the user asks to configure every supported client. Use a specific setup target only when the user asks for that client or when repairing it.

## Product Defaults

- Current branch: `v2`.
- Current product root: `versions/v2`.
- Runtime data: local `~/.kerebrom/`.
- Binary install path: `~/local/bin/kerebrom` with symlink from `~/.local/bin/kerebrom`.
- Default setup: `kerebrom setup auto`.
- Default memory transport: local stdio MCP.

## Safety Rules

- Do not enable `mcp-http` or expose memory over a network unless the user explicitly asks for remote connector support and accepts the privacy tradeoff.
- Do not delete, reset, import, or migrate user memory unless the user explicitly asks.
- Do not patch Claude Chat account memory through private APIs or browser databases. If the user wants a native Claude Chat memory hint, point them to the copy-paste prompt in [docs/AI_AGENT_INSTALL.md](docs/AI_AGENT_INSTALL.md#optional-claude-chat-native-memory-seed).
- Do not copy private machine paths, config contents, tokens, memories, or backups into public docs, commits, issues, or release notes.
- Do not mix `versions/v1` and `versions/v2` implementation lines. v2 is the current line; v1 is maintained history.
- Do not rewrite release tags or historical refs without explicit user approval.

## If You Are Maintaining The Repo

- Use `versions/v2` for new stable work.
- Keep root README files as product landing pages.
- Keep [docs/AI_AGENT_INSTALL.md](docs/AI_AGENT_INSTALL.md), [CLAUDE.md](CLAUDE.md), and this file aligned whenever setup behavior changes.
- For docs-only profile updates, do not create a release unless the user asks.
- Before committing, run at least:

```bash
git diff --check
cd versions/v2
go test ./...
```

## Product Promise

Kerebrom is a local-first persistent memory layer for AI agents. The intended user experience is:

```text
User -> AI client -> Kerebrom MCP -> local SQLite + FTS5 -> recovered project context -> AI client
```

The user should not need to say "use Kerebrom" in every conversation. Configured agents should call `context` before answering every user message when Kerebrom tools are available, then call `recall`, `remember`, and `summary` according to the Kerebrom protocol. Activation is every turn; durable saving is only when there is something real to preserve.
