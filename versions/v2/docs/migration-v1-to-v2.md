# Migrating from Kerebrom v1 to v2

v2 is a clean evolution of v1. The data store is unchanged, so all your existing observations, sessions, and prompts continue to work without conversion. What changes is the **MCP tool surface** the agents see and a few **client configuration files** that setup rewrites.

## TL;DR

```bash
# From an existing v1 install:
kerebrom update
# (when prompted, confirm)

# Restart Claude Desktop, Claude Code, Codex, and any other AI client.
# That's it.
```

## What changes

### MCP tools

| v1 (deprecated) | v2 |
|---|---|
| `mem_bootstrap` | `context` |
| `mem_context` | `context` |
| `mem_save_prompt` | `context` |
| `mem_session_start` | `context` |
| `mem_search` / `recall` | `recall` |
| `mem_save` | `remember` |
| `mem_capture_passive` | `remember` (call directly with distilled content) |
| `mem_update` | `remember` (re-save with the same `topic_key` + `forget` the old id) |
| `mem_session_summary` | `summary` |
| `mem_session_end` | `summary` (passing `content` ends the session) |
| `mem_delete` | `forget` |
| `mem_timeline` | `timeline` |
| `mem_get_observation` | `timeline` with `observation_id` |
| `mem_stats` | included in `context` payload |
| `mem_suggest_topic_key` | (removed; the model picks topic keys) |
| `mem_merge_projects` | `projects` |

The seven new tools are not aliases — `mem_*` is removed entirely. If you have scripts or external MCP clients that hardcoded `mem_*` names, see "Staying on v1" below.

### Client configuration files

`kerebrom setup` (run automatically by `kerebrom update`) rewrites:

- `~/.claude/settings.json` — adds the seven new `mcp__Kerebrom__*` entries to `permissions.allow` and **removes** any leftover `mcp__Kerebrom__mem_*` entries from v1. Other entries you added are preserved.
- `~/.claude/CLAUDE.md` — the `<!-- KEREBROM:START -->` block is replaced with the v2 protocol text.
- `~/.claude/mcp.json` — the `Kerebrom` MCP server entry is updated to use the new binary path (no behavior change).
- `~/.codex/config.toml` — the `[mcp_servers.kerebrom]` block is regenerated with auto-approval for the six agent tools instead of the eleven v1 tools.
- `~/.codex/AGENTS.md` — Kerebrom block replaced with the v2 cycle text.
- `~/Library/Application Support/Claude/claude_desktop_config.json` — MCP server entry refreshed (binary path).
- `~/.gemini/{settings.json, system.md, .env}` — protocol and MCP entry updated.
- `~/.config/opencode/{opencode.json, kerebrom-memory.md}` — same.
- `~/.cursor/{mcp.json, rules/kerebrom.mdc}` — same.
- `~/.codeium/windsurf/mcp_config.json` and `~/.windsurfrules` — same.
- `~/Library/Application Support/Code/User/{mcp.json, prompts/kerebrom-memory.instructions.md}` — same.

`~/.kerebrom/hooks/claude-code/*.sh` — the five hook scripts are rewritten with the new binary path. The hook script content itself is unchanged because the hooks call `kerebrom hook <name>` and the subcommand names are the same.

### Database

`~/.kerebrom/kerebrom.db` is **not touched**. Same schema, same data, same FTS5 indexes. v2 reads and writes the same tables v1 did.

### Sync chunks

`~/.kerebrom/chunks/*.jsonl.gz` and `~/.kerebrom/manifest.json` are **not touched**. The `kerebrom sync` format is unchanged.

## Step by step

### From an existing v1.x install

```bash
kerebrom update
```

You'll see:

```
kerebrom update available: v1.1.0 → v2.0.0
release notes: https://github.com/ulianbass/kerebrom/releases/tag/v2.0.0
Install v2.0.0 now? [y/N]: y
downloading https://github.com/ulianbass/kerebrom/archive/refs/tags/v2.0.0.tar.gz
extracting source
running make install-user in /tmp/kerebrom-update-XYZ/kerebrom-2.0.0/versions/v2
...
kerebrom installed: v2.0.0
restart any running clients (Claude Desktop, Code, Codex, etc.) to pick up the new MCP server.
```

Then **fully quit and reopen** every AI client you use:
- Claude Desktop: ⌘Q (not just close window). Reopen.
- Claude Code: close any open `claude` CLI session and start a new one.
- Codex: restart the desktop app.
- Cursor / Windsurf / VS Code / Gemini CLI / OpenCode: same.

That's all. The first message you send to a client should now trigger an automatic `context` call — no need to mention memory.

### From a fresh machine

```bash
git clone https://github.com/ulianbass/kerebrom.git
cd kerebrom/versions/v2
make install-user
```

Then restart any AI client.

## Verifying v2 is active

```bash
kerebrom version
# → v2.0.0 (commit=..., build_date=...)
```

In Claude Desktop, open a fresh chat and ask "What do you know about my projects?". Check `~/Library/Logs/Claude/mcp-server-Kerebrom.log` — you should see a `tools/call` for `context` immediately after the user's message. If you only see `initialize`, `tools/list`, and silence, Claude Desktop is still using a cached MCP session — fully quit (⌘Q) and reopen.

## Staying on v1

If you have integrations that depend on the `mem_*` tool names and you cannot migrate them yet:

```bash
cd kerebrom/versions/v1
make install-user
```

v1 keeps running independently. The `versions/v1/` source tree stays in the repository. v1 will continue to receive critical fixes — the `manifest.json` for v1 declares it as maintained.

If you want to run **both** v1 and v2 in parallel for testing, that is supported but unusual: each client config block uses one binary path. You'd need to point different clients at different binaries. Most users should just run `kerebrom update` and move on.

## Rollback

`make install-user` writes the new binary atomically. If something goes wrong:

```bash
# Check that ~/local/bin/kerebrom.bak.* exists from a previous install
ls -lt ~/local/bin/kerebrom*

# Restore the previous binary
mv ~/local/bin/kerebrom.bak.<timestamp> ~/local/bin/kerebrom
```

(Note: `make install-user` does not currently keep automatic backups; future versions may. To preserve a known-good binary, copy `~/local/bin/kerebrom` somewhere before running `kerebrom update`.)

Then re-run `kerebrom setup auto` from the v1 source to revert the client config files.

## Questions answered by the data

- **Does my data move?** No.
- **Do I need to re-run setup?** No, `kerebrom update` runs it automatically.
- **Do I lose anything in `permissions.allow`?** Only the seventeen `mcp__Kerebrom__mem_*` entries from v1. Anything else you put there stays.
- **Will Claude Desktop start using memory automatically?** That is exactly the point of v2. If not, fully quit (⌘Q) and reopen — the in-memory MCP session from before the upgrade is sticky.
- **Can I downgrade?** Yes, by reinstalling from `versions/v1/` as shown above.
