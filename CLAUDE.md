# Kerebrom Instructions For Claude

This repository contains Kerebrom, a local-first persistent memory layer for AI agents.

## If The User Asks Claude To Install This Repo

Follow [docs/AI_AGENT_INSTALL.md](docs/AI_AGENT_INSTALL.md). Use `versions/v2` as the current stable product line.

Default install:

```bash
cd versions/v2
make install-user
```

If `make` is unavailable but Go is installed:

```bash
./scripts/install-user.sh --yes --agent auto
```

If Go is missing or older than 1.26, explain that Kerebrom cannot build without Go 1.26+, ask the user whether they want Go installed or upgraded, and only proceed after explicit approval. Do not install Go, Homebrew, package-manager dependencies, or use elevated permissions without consent.

Verify:

```bash
kerebrom version
kerebrom stats
kerebrom doctor --deep
```

Then tell the user to fully restart Claude Desktop, Claude Code, Codex, Cursor, or any other open AI client so the new MCP server and instruction files are loaded.

## Important Product Boundaries

- Kerebrom is local-first. Runtime memory lives in `~/.kerebrom/`.
- Do not enable `mcp-http` or expose `serve` beyond loopback unless the user explicitly asks for remote connector support and accepts the privacy tradeoff.
- Do not modify Claude Chat account memory through private APIs or browser databases. If a native Claude Chat memory hint is needed, point the user to the copy-paste prompt in [docs/AI_AGENT_INSTALL.md](docs/AI_AGENT_INSTALL.md#optional-claude-chat-native-memory-seed).
- Do not import, reset, or delete existing Kerebrom memory unless the user explicitly asks.
- Do not mix v1 and v2 implementation lines. The current product root is `versions/v2`.

## Kerebrom Protocol

When Kerebrom is installed in an AI client, the agent should treat it as the durable source of truth for prior project context:

- Call `context` before answering every user message when Kerebrom tools are available, including short or ambiguous messages.
- Call `recall` when the user asks what is known about a topic.
- Call `remember` only when a durable decision, preference, bugfix, configuration change, or non-obvious learning appears.
- Call `summary` before ending substantial work or after compaction.
- If the user says "cerramos sesión", "sesión cerrada", "close this session", or equivalent, treat it as an explicit close request: call `summary` with the current session id and do not save the bare close phrase as durable memory.
- Use `forget` only when the user says memory is wrong or obsolete.
- Treat `valid_at` as the semantic memory clock. When observations conflict, prefer the newest corrected or revalidated observation unless the user explicitly says an older memory is still authoritative.
- Follow `context_governor` when it appears in context/recall payloads: think, search, analyze, then answer; use `timeline` before claiming anything that depends on conflict candidates.

The saved observation format should be concise and interpreted:

```text
What: durable fact or change.
Why: why it matters.
Where: project, file, workflow, or context.
Learned: implication, gotcha, or next useful connection.
```
