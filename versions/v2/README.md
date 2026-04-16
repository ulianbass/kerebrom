# Kerebrom v2

> Local-first persistent memory for AI agents — one durable brain shared across Claude, Codex, Cursor, Gemini CLI, OpenCode, Windsurf, VS Code, and any MCP-capable client.

Kerebrom v2 is a clean-room rewrite that replaces the v1 `mem_*` tool surface with seven semantic verbs the model picks up by intuition: **context**, **recall**, **remember**, **summary**, **forget**, **timeline**, and **projects**. The user no longer has to say "use Kerebrom" — the agent calls memory on its own when it sees the right tool name.

## Install

```bash
git clone https://github.com/ulianbass/kerebrom.git
cd kerebrom/versions/v2
make install-user
```

That single command:

1. Builds the `kerebrom` binary with embedded version metadata.
2. Installs it to `~/local/bin/kerebrom` and links it from `~/.local/bin/kerebrom`.
3. Runs `kerebrom setup auto` to wire every detected AI client (Claude Desktop, Claude Code, Codex, Cursor, Gemini CLI, OpenCode, Windsurf, VS Code).

Restart any open AI client so it picks up the new MCP server. From that point on, memory is automatic.

## Update

```bash
kerebrom update
```

Fetches the latest GitHub release, downloads its source tarball, and runs `make install-user` against `versions/v2`. Use `--check` to inspect without installing or `--yes` to skip the confirmation prompt.

## Architecture in one screen

```text
versions/v2/
  cmd/kerebrom/             CLI entrypoint
  internal/cli/             commands, hooks, TUI
  internal/setup/           local AI-client setup with cleanup of v1 entries
  internal/store/sqlite/    SQLite + FTS5 memory store (schema unchanged from v1)
  internal/transport/mcp/   MCP server: 7 semantic tools, no aliases
  internal/transport/http/  local HTTP API
  internal/sync/            compressed sync chunks
  internal/updater/         self-update via GitHub Releases
  docs/                     ADRs, architecture, migration, release checklist
```

The DB lives at `~/.kerebrom/kerebrom.db`. v2 reads and writes the same schema as v1, so upgrading does not touch your data.

## The cycle

The six day-to-day tools plus one explicit admin tool compose the memory rhythm:

| When | Tool |
|---|---|
| Start of any non-trivial conversation | `context` |
| Need to look up a topic | `recall` |
| Durable fact, decision, or learning appears | `remember` |
| Closing substantial work | `summary` |
| Inspect history | `timeline` |
| User says something is wrong | `forget` |
| Consolidate project name variants after the user explicitly asks | `projects` (admin profile) |

Detail in [docs/architecture-v2.md](docs/architecture-v2.md). Migration notes in [docs/migration-v1-to-v2.md](docs/migration-v1-to-v2.md).

## Build and test

```bash
cd versions/v2
make test
make build
./bin/kerebrom version
```

## License

Source-available proprietary. See [LICENSE](../../LICENSE).
