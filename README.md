# Kerebrom

[Leer en Español](README.es.md)

> **Local-first persistent memory for AI coding tools.**
> Claude and Codex stop asking you the same questions. Your projects stay in context. Your tokens stop bleeding.

**No cloud. No API keys. No data leaves your machine.** Just a SQLite file that Claude Code, Codex, and Claude Desktop share as a long-term brain.

![Kerebrom stats dashboard — measurable token savings](docs/stats-dashboard.png)

---

## Why it matters

Every AI coding assistant forgets. Every new conversation starts from zero. You re-explain your project, your preferences, your decisions. You re-read files. You pay tokens for context the model should already know.

Kerebrom fixes that. One persistent brain, shared across your AI tools, **running 100% on your machine**.

### Measured impact

Real numbers from `kerebrom benchmark` on a 150-memory project (April 2026):

| Metric | Value |
|---|---|
| Average recall latency | **60ms** |
| Tokens input per query | **~15** |
| Tokens output per recall | **~2,400** |
| **Estimated savings ratio** | **~500×** |

> Savings are **conservative estimates**, not measured A/B tests. The multiplier (recall × 3.0) assumes the model would otherwise re-read ~3× more content to find the same information. Your real savings depend on the size of your project and how often you reuse context.

Reproduce it yourself:

```bash
kerebrom benchmark --queries 10 --output my-benchmark.json
```

---

## Install

```bash
git clone https://github.com/ulianbass/kerebrom.git
cd kerebrom
pip install .
kerebrom setup
```

`kerebrom setup` auto-detects Claude Code, Claude Desktop, and Codex on your machine and wires Kerebrom as an MCP server into each of them. It's idempotent — run it anytime to repair configs.

---

## What you get

### For every AI tool you use
- **One shared brain.** Claude learns something, Codex knows it next time.
- **Persistent across restarts.** Nothing lives in a chat window.
- **Auto-consolidation.** A scheduled agent (Sopor) reads your transcripts and distills them into structured memories — never copying your words literally, always rewriting as facts.

### For your privacy
- **100% local.** SQLite file in `~/.kerebrom/kerebrom.db`. Nothing sent anywhere.
- **Sensitive value scrubbing.** API keys, emails, and known PII patterns get redacted before storage.
- **Optional encryption at rest.** AES-256 via system `openssl`.

### For your wallet
- **Token tracking built in.** Every `recall`, `context`, and `query` is counted.
- **Stats dashboard.** Open `kerebrom graph` to see tokens saved by day, by operation, and cumulative.
- **Reproducible benchmarks.** `kerebrom benchmark` exports JSON you can publish.

### For your brain
- **Interactive knowledge graph.** D3-force visualization of every entity and memory in your database, with filters, search, zoom, and click-to-focus.
- **Structured queries.** Filter by `kind`, `tags`, `importance`, date ranges, and arbitrary JSON metadata.
- **Entity inference.** People, locations, organizations, and concepts are classified automatically.

---

## Commands

```bash
kerebrom setup              # configure AI tools (idempotent)
kerebrom serve              # start MCP server over stdio
kerebrom graph              # open the interactive knowledge graph + stats dashboard

kerebrom remember "…"       # save a memory intentionally
kerebrom recall "query"     # hybrid search (semantic + keyword + graph)
kerebrom context "query"    # progressive disclosure bundle
kerebrom query --kind core  # structured filters
kerebrom facts              # list semantic triples
kerebrom entities           # list known people, places, concepts
kerebrom gaps               # list knowledge gaps (referenced but unknown)

kerebrom stats              # terminal report of token savings
kerebrom benchmark          # run a measurable benchmark
kerebrom sopor              # manually consolidate transcripts

kerebrom snapshot           # portable .kbk backup
kerebrom revive             # restore from .kbk
kerebrom uninstall          # remove everything
```

---

## The interactive graph

![Interactive knowledge graph with D3 force-directed layout](docs/graph-view.png)

## The stats dashboard

When you run `kerebrom graph`, the web UI has two tabs:

**Grafo** — D3-force interactive visualization of your memory graph:
- Color-coded by type (person, location, organization, concept, memory kind)
- Search, filter, zoom, minimap, keyboard shortcuts (⌘K, Esc, F, ?)
- Click a node to see its memories and connections
- Auto label-collision avoidance

**Estadísticas** — live dashboard of what Kerebrom is saving you:
- 4 stat cards: total saved, this month, this week, today
- 30-day timeline bar chart (SVG, no external libs)
- Breakdown by operation (recall, context, query, remember)
- Transparent formula box explaining every multiplier

---

## Architecture

```
~/.kerebrom/
  kerebrom.db          single SQLite file (memories, entities, facts,
                       embeddings, token_stats)
  reports/             versioned Sopor maintenance reports
  backups/             versioned .kbk snapshots
  Kerebrom.app/        macOS bundle for the auto-repair LaunchAgent
```

Every tool (Claude Code, Claude Desktop, Codex) connects to the same MCP server and sees the same database. There's no sync, no server, no cloud — it's one file.

### The auto-repair layer

On macOS, `kerebrom setup` installs a LaunchAgent that periodically ensures the MCP configs stay registered. If Claude Code rewrites `settings.json` or a tool update breaks a config, the agent restores it automatically. No cron, no Docker, no systemd.

### Sopor — the consolidation agent

Sopor runs as a scheduled task inside Claude Code **and** as an automation inside Codex. Both versions share a single generic prompt that:

1. Reads recent transcripts from `~/.claude/projects` and `~/.codex/sessions`
2. **Distills** ideas — never copies the user's words literally
3. Validates each idea against the "will this matter in 30 days?" filter
4. Merges partial memories into stronger unified ones
5. Writes a report to `~/.kerebrom/reports/`

The prompt is installed automatically by `kerebrom setup` — nothing to configure.

---

## Honest limitations

- **"Tokens saved" is an estimate.** It uses conservative multipliers per operation type (recall × 3, context × 5, query × 2). It's not an A/B measurement. The dashboard, stats output, and benchmark report all say this explicitly.
- **Embeddings default to hash** if you don't have `onnxruntime` or `sentence-transformers` installed. Hash embeddings are deterministic but don't capture semantic similarity as well. Install `onnxruntime` for proper local neural embeddings.
- **Encrypted mode uses system `openssl`**, not SQLCipher. While a process is actively using the database, a temporary plaintext file exists in `/tmp`.
- **Sopor runs on Claude Code and Codex.** If you use neither, the consolidation step is manual (`kerebrom sopor --all`).
- **macOS is a first-class target.** Linux and Windows work for the core engine and MCP server, but the LaunchAgent auto-repair is macOS-only.

---

## Tests

```bash
python3 -m pytest tests/test_kerebrom.py
```

111 tests cover the store, MCP server, setup, token tracker, Sopor consolidation, backup/restore, and encryption.

---

## Author

Created by **Ulian Bass** — Pedro Julián Arribas Monzón.

Copyright © 2026 Ulian Bass. All rights reserved.

## License

Proprietary — source is public for auditing and local use. See [LICENSE](LICENSE) for terms.
