# AI Agent Install Guide

Audience: Claude, Codex, Cursor, Copilot, Gemini, or any coding agent a user asks to install Kerebrom from this repository.

Goal: install Kerebrom as an end-user product, not as a development sandbox.

## First Principles

- Use the current stable product line: `versions/v2`.
- Prefer the plug-and-play path: `make install-user`.
- Keep Kerebrom local-first. Do not start or expose `mcp-http` unless the user explicitly asks for remote connector support.
- Do not import legacy backups, delete memory, reset databases, or modify unrelated AI-client configuration.
- After install or update, tell the user to fully restart any open AI client.
- If a command fails because Go or Make is missing, explain the missing dependency and stop with clear next steps.

## Supported Setup Targets

`kerebrom setup` accepts:

```text
auto
all
claude
claude-code
claude-desktop
codex
cursor
gemini-cli
opencode
windsurf
vscode
```

Use `auto` by default. Use a specific target only when the user asks for one, or when you are fixing one known client.

## Fresh Install

Run:

```bash
git clone https://github.com/ulianbass/kerebrom.git
cd kerebrom/versions/v2
make install-user
```

Expected side effects:

- Builds `bin/kerebrom`.
- Installs the executable at `~/local/bin/kerebrom`.
- Creates or refreshes the symlink `~/.local/bin/kerebrom`.
- Runs `kerebrom setup auto` with the installed binary path.
- Writes only marked Kerebrom blocks or MCP entries into supported client config files.

If the repository is already cloned, do not reclone over it. Use the existing checkout:

```bash
cd path/to/kerebrom/versions/v2
git pull --ff-only
make install-user
```

If the user wants every supported client configured, run:

```bash
make install-user SETUP_AGENT=all
```

## Update Existing Install

If `kerebrom` is already installed:

```bash
kerebrom update --check
kerebrom update
```

Use `kerebrom update --yes` only if the user asked for a non-interactive update.

If `kerebrom update` is unavailable because the installed binary is too old, fall back to the fresh install path from `versions/v2`.

## Verify

Run:

```bash
kerebrom version
kerebrom stats
```

Then verify setup output from the install command. It should list files configured for the detected clients.

Recommended user-facing final check:

1. Fully quit and reopen Claude Desktop, Codex, Cursor, or the relevant AI client.
2. Start a fresh chat.
3. Ask: `What do you know about my projects from Kerebrom?`
4. The agent should call `context` or `recall` before answering, when the client exposes MCP tools.

## Client-Specific Notes

| Client | What Kerebrom installs |
|---|---|
| Claude Code | MCP config, lifecycle hooks, auto-approved everyday tools, protocol block in global Claude instructions, hook scripts. |
| Claude Desktop Chat | Local MCP server entry. Claude Chat account memory is cloud-backed, so Kerebrom does not patch private APIs or browser databases. |
| Claude Cowork | Local MCP plus native Cowork `memory/CLAUDE.md` seed when local Cowork account storage exists. |
| Codex | MCP server config, auto-approval for everyday memory tools, protocol block in global `AGENTS.md`. |
| Cursor | MCP config and Kerebrom memory rule. |
| Gemini CLI | MCP config, system prompt, and system-instruction environment flag. |
| OpenCode | MCP config and Kerebrom memory protocol file. |
| Windsurf | MCP config and global rules. |
| VS Code | MCP config and prompt instructions where supported. |

## What To Tell The User

After a successful install, report:

- Installed Kerebrom version.
- Which setup target ran (`auto`, `all`, or a specific client).
- Which client config files were changed, in plain language.
- That they must fully restart open AI apps.
- That runtime memory lives locally in `~/.kerebrom/`.

Do not paste private config file contents unless the user asks for debugging.

## Troubleshooting

| Symptom | Likely cause | Action |
|---|---|---|
| `make: command not found` | Make is not installed. | Ask the user to install command-line build tools for their OS, then rerun. |
| `go: command not found` | Go is not installed. | Ask the user to install Go, then rerun. |
| AI client shows Kerebrom but does not use it | Client cached the old MCP session. | Fully quit and reopen the client. |
| Claude Chat does not retain the native memory hint | Claude Chat account memory is cloud-backed. | Add the Kerebrom authority hint manually through Claude's supported memory UI. |
| User wants remote ChatGPT/Claude web connector support | Local stdio MCP cannot be launched by cloud clients. | Explain the privacy tradeoff before using `mcp-http`; require explicit user consent. |

## Privacy Boundary

Never expose Kerebrom over the network by default. The command below is advanced and should only be used after the user explicitly accepts the risk:

```bash
KEREBROM_REMOTE_TOKEN="change-me" kerebrom mcp-http --addr 127.0.0.1:7437 --path /mcp
```

Non-loopback addresses require an auth token or an explicit unsafe override. Do not suggest unsafe public unauthenticated mode for normal users.
