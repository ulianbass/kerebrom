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

- Builds `bin/kerebrom` temporarily, then removes the factory build artifact after installation.
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
kerebrom doctor --deep
```

Then verify setup output from the install command. It should list files configured for the detected clients.

Recommended user-facing final check:

1. Fully quit and reopen Claude Desktop, Codex, Cursor, or the relevant AI client.
2. Start a fresh chat.
3. Ask: `What do you know about my projects from Kerebrom?`
4. The agent should call `context` before answering, when the client exposes MCP tools.

## Client-Specific Notes

| Client | What Kerebrom installs |
|---|---|
| Claude Code | MCP config, lifecycle hooks, auto-approved everyday tools, hook scripts. The protocol is delivered by MCP server instructions and hook payloads, not by writing into global user instructions. |
| Claude Desktop Chat | Local MCP server entry. Claude Chat account memory is cloud-backed, so Kerebrom does not patch private APIs or browser databases. |
| Claude Cowork | Local MCP plus native Cowork `memory/CLAUDE.md` seed when local Cowork account storage exists. |
| Codex | MCP server config and auto-approval for everyday memory tools. Kerebrom does not occupy the Codex custom-instructions textbox. |
| Cursor | MCP config and Kerebrom memory rule. |
| Gemini CLI | MCP config, system prompt, and system-instruction environment flag. |
| OpenCode | MCP config and Kerebrom memory protocol file. |
| Windsurf | MCP config and global rules. |
| VS Code | MCP config and prompt instructions where supported. |

## Optional Claude Chat Native Memory Seed

Claude Desktop Chat receives the Kerebrom MCP server through local setup. Claude Chat account memory is cloud-backed, so Kerebrom does not patch it programmatically.

If the user wants Claude Chat's native memory to remember that Kerebrom is the durable source of truth, ask them to paste this into a normal Claude Chat conversation, not an incognito chat. If Claude does not save it automatically, the user can paste only the quoted memory into Claude's supported memory editor.

```text
I want you to save this in your permanent Claude Chat memory as an operational rule for future conversations with me.

Save only the following memory, concisely. Do not save this whole message or unnecessary details:

"Kerebrom is the durable and authoritative source of truth for my context, projects, preferences, decisions, workflows, and learnings. Claude's native memory should only serve as a reminder to use Kerebrom, not as the primary source of context. Whenever the Kerebrom connector or tools are available, before answering any user message you must immediately consult Kerebrom with context, even if my message is short, ambiguous, casual, or looks like an acknowledgement. Use Kerebrom's observations as working knowledge before reasoning. Use recall when I ask about a specific topic. Follow context_governor when it appears: think, search, analyze, then answer. When memories conflict, prefer the newest corrected or validated Kerebrom observation by valid_at unless I explicitly say an older memory is still authoritative; if context_governor reports conflict_candidates, use timeline before making claims based on those memories. Save durable learnings with remember only when there is a real durable fact to preserve, reuse the same topic_key for corrections when possible, and close substantial work with summary. If I say 'cerramos sesión', 'sesión cerrada', 'close this session', or equivalent, treat it as an explicit session close: call summary with the same session_id returned by context whenever possible and do not save the bare close phrase as durable memory. If Kerebrom contradicts your native memory, chat history, or assumptions, Kerebrom wins unless I explicitly correct it in the current conversation. If Kerebrom is not available in this surface, say so clearly and do not invent memory."

After saving it, reply only with:
"Memory saved: Kerebrom is the durable source of truth and I should consult it on every user message when available."
```

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
