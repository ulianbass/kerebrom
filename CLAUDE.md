# Kerebrom Instructions For Claude

This repository contains Kerebrom, a local-first persistent memory layer for AI agents.

## If The User Asks Claude To Install This Repo

Follow [docs/AI_AGENT_INSTALL.md](docs/AI_AGENT_INSTALL.md). Use `versions/v2` as the current stable product line.

Default install:

```bash
cd versions/v2
make install-user
```

Verify:

```bash
kerebrom version
kerebrom stats
```

Then tell the user to fully restart Claude Desktop, Claude Code, Codex, Cursor, or any other open AI client so the new MCP server and instruction files are loaded.

## Important Product Boundaries

- Kerebrom is local-first. Runtime memory lives in `~/.kerebrom/`.
- Do not enable `mcp-http` unless the user explicitly asks for remote connector support and accepts the privacy tradeoff.
- Do not modify Claude Chat account memory through private APIs or browser databases. If a native Claude Chat memory hint is needed, point the user to the copy-paste prompt in [docs/AI_AGENT_INSTALL.md](docs/AI_AGENT_INSTALL.md#optional-claude-chat-native-memory-seed).
- Do not import, reset, or delete existing Kerebrom memory unless the user explicitly asks.
- Do not mix v1 and v2 implementation lines. The current product root is `versions/v2`.

## Kerebrom Protocol

When Kerebrom is installed in an AI client, the agent should treat it as the durable source of truth for prior project context:

- Call `context` before answering every user message when Kerebrom tools are available, including short or ambiguous messages.
- Call `recall` when the user asks what is known about a topic.
- Call `remember` only when a durable decision, preference, bugfix, configuration change, or non-obvious learning appears.
- Call `summary` before ending substantial work or after compaction.
- Use `forget` only when the user says memory is wrong or obsolete.

The saved observation format should be concise and interpreted:

```text
What: durable fact or change.
Why: why it matters.
Where: project, file, workflow, or context.
Learned: implication, gotcha, or next useful connection.
```
