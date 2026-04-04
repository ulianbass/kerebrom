# Kerebrom

[Leer en Espanol](README.es.md)

Kerebrom is a local-first persistent memory engine for AI coding tools. It gives Claude Code, Codex, Cursor, and Claude Desktop a shared long-term memory that survives across conversations — no cloud services, no external databases, just a single SQLite file on your machine.

## Features

- **Single-file SQLite** storage with WAL for concurrent access.
- **Hybrid retrieval** combining keyword search (FTS5), semantic similarity, recency, access frequency, and entity graph overlap.
- **Entity graph** with fact triples, contradiction detection, and automatic invalidation.
- **Progressive disclosure** — compact, summary, or full-detail context layers.
- **Privacy-first** — sensitive value scrubbing before storage; optional encrypted-at-rest mode.
- **Automatic maintenance** — memory decay, episodic-to-semantic consolidation, and scheduled cleanup.
- **MCP server** over stdio — works with any tool that speaks Model Context Protocol.
- **Auto-setup** — one command detects and configures Claude Code, Codex, Claude Desktop, and Cursor.
- **Portable backups** — `.kbk` snapshot files for disaster recovery and migration.

## Install

```bash
pip install .
```

## Quick start

```bash
# Auto-configure all detected AI tools
python3 -m kerebrom setup --db ~/.kerebrom/kerebrom.db

# Store memories
python3 -m kerebrom remember --db ~/.kerebrom/kerebrom.db "The API uses JWT tokens with 24h expiry."
python3 -m kerebrom remember --db ~/.kerebrom/kerebrom.db "User prefers dark mode and Spanish language."

# Search memories
python3 -m kerebrom recall --db ~/.kerebrom/kerebrom.db "authentication"

# View entity graph
python3 -m kerebrom facts --db ~/.kerebrom/kerebrom.db

# Get structured context for an AI prompt
python3 -m kerebrom context --db ~/.kerebrom/kerebrom.db "project architecture" --layer 2

# Start MCP server
python3 -m kerebrom serve --db ~/.kerebrom/kerebrom.db
```

## CLI commands

| Command | Description |
|---------|-------------|
| `setup` | Auto-detect AI tools and configure MCP + hooks |
| `remember` | Store a new memory |
| `recall` | Search memories by query |
| `forget` | Invalidate a memory by ID or scrub sensitive data |
| `context` | Get a structured context bundle (facts + memories) |
| `entities` | List known entities in the knowledge graph |
| `facts` | List active semantic triples |
| `consolidate` | Merge related episodic memories into semantic ones |
| `decay` | Apply importance decay to old memories |
| `backup` | Create a SQLite database copy |
| `snapshot` | Create a portable `.kbk` backup file |
| `revive` | Restore memories from a `.kbk` file |
| `export` | Export memories as JSON |
| `serve` | Start the MCP stdio server |
| `sopor` | Consolidate transcript sessions into memories |

## How setup works

`kerebrom setup` auto-detects installed AI tools and configures each one:

- **Claude Code** — registers MCP server in `.mcp.json`, adds usage instructions to `CLAUDE.md`, disables built-in file-based memory, installs passive capture hooks.
- **Claude Desktop** — configures MCP in `claude_desktop_config.json` (Chat + Cowork).
- **Codex** — registers MCP in `config.toml`, adds instructions to `AGENTS.md`.
- **Cursor** — registers MCP in `mcp.json`.
- **LaunchAgent** (macOS) — installs a periodic agent that auto-repairs configs if removed.

## Architecture

```
~/.kerebrom/
  kerebrom.db        # SQLite database (memories, entities, facts, embeddings)
  reports/           # Versioned maintenance reports
  backups/           # Versioned .kbk snapshot files
  Kerebrom           # Wrapper script for LaunchAgent
```

The database is the single source of truth. All AI tools connect to the same MCP server and share the same memory pool.

## Encrypted-at-rest mode

```bash
export KEREBROM_PASSPHRASE="your-passphrase"
python3 -m kerebrom init --db ~/.kerebrom/secure.kdb --passphrase-env KEREBROM_PASSPHRASE
python3 -m kerebrom setup --db ~/.kerebrom/secure.kdb --passphrase-env KEREBROM_PASSPHRASE
```

Wraps SQLite inside an encrypted container using the system `openssl` binary. Compatible with all CLI commands and the MCP server.

## Testing

```bash
# Unit tests
python3 -m pytest tests/test_kerebrom.py

# Single-machine release smoke
python3 scripts/local_release_smoke.py

# Full release gate (tests + smoke + venv install + benchmarks)
python3 scripts/release_gate.py
```

## Known limits

- Retrieval uses deterministic hash embeddings, not neural embeddings yet.
- Encrypted mode uses the system `openssl` binary rather than SQLCipher.
- Encrypted mode creates a temporary plaintext file while a process is actively using the database.

## License

MIT
