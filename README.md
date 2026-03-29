# Kerebrom

Kerebrom is a local-first persistent memory engine for AI workflows. This repository now contains a real phase-1 prototype based on the research document in [RESEARCH.md](/Users/ulianbass/Documents/Kerebrom/RESEARCH.md): SQLite-backed storage, keyword search with FTS5, deterministic local semantic embeddings, entity extraction, contradiction-aware facts, progressive disclosure, automatic maintenance, and an MCP server over stdio.

## What works today

- Single-file SQLite storage with WAL enabled.
- Automatic memory capture through `remember`.
- Sensitive value scrubbing before content is stored.
- Hybrid retrieval using keyword ranks, semantic similarity, recency, access frequency, and entity overlap.
- Entity graph with active fact invalidation, multivalue predicates, and support restoration after `forget`.
- Progressive-disclosure context layers for compact or full recall.
- Optional encrypted-at-rest database container protected by passphrase.
- Automatic decay and episodic-to-semantic consolidation with maintenance scheduling.
- MCP server via `python3 -m kerebrom serve` plus `setup` for tool configuration.
- Commands for `init`, `remember`, `recall`, `forget`, `entities`, `facts`, `context`, `export`, `serve`, `consolidate`, `decay`, and `setup`.
- Automated tests using the Python standard library.

## Why this MVP is honest

This build avoids pretending to have capabilities that are not implemented locally. There is no external vector database, no hosted services, and no LLM required for the core loop. Semantic search currently uses deterministic feature hashing rather than a neural embedding model, which keeps the engine offline and self-contained while still making retrieval work today.

## Quick start

```bash
python3 -m kerebrom init --db kerebrom.db
python3 -m kerebrom remember --db kerebrom.db "Me llamo Ulian. Vivo en Guatemala. Trabajo en Kerebrom."
python3 -m kerebrom remember --db kerebrom.db "Prefiero Rust para construir herramientas de sistemas."
python3 -m kerebrom recall --db kerebrom.db "donde vivo"
python3 -m kerebrom facts --db kerebrom.db
python3 -m kerebrom context --db kerebrom.db "perfil del usuario" --layer 2
python3 -m kerebrom serve --db kerebrom.db
```

## CLI examples

```bash
python3 -m kerebrom forget --db kerebrom.db --memory-id 2
python3 -m kerebrom forget --db kerebrom.db --sensitive
python3 -m kerebrom entities --db kerebrom.db --json
python3 -m kerebrom entities --db kerebrom.db --all --json
python3 -m kerebrom consolidate --db kerebrom.db
python3 -m kerebrom decay --db kerebrom.db
python3 -m kerebrom export --db kerebrom.db --active-only
python3 -m kerebrom backup --db kerebrom.db --output backups/kerebrom.db
python3 -m kerebrom restore --db restored.db --input backups/kerebrom.db
python3 -m kerebrom setup --db ~/.kerebrom/kerebrom.db
export KEREBROM_PASSPHRASE="cambia-esto"
python3 -m kerebrom init --db ~/.kerebrom/secure.kdb --passphrase-env KEREBROM_PASSPHRASE
python3 -m kerebrom remember --db ~/.kerebrom/secure.kdb --passphrase-env KEREBROM_PASSPHRASE "Vivo en Guatemala."
```

## Encrypted-at-rest mode

Kerebrom can protect the database file at rest by wrapping SQLite inside an encrypted container. The current implementation is:

- Whole-file encryption with passphrase support via `--passphrase-env` or `--passphrase-file`.
- Compatible with normal CLI commands, `serve`, `backup`, `restore`, and `setup`.
- Validated by automated tests and by the release gate.

Example:

```bash
export KEREBROM_PASSPHRASE="cambia-esto"
python3 -m kerebrom init --db ~/.kerebrom/secure.kdb --passphrase-env KEREBROM_PASSPHRASE
python3 -m kerebrom context --db ~/.kerebrom/secure.kdb --passphrase-env KEREBROM_PASSPHRASE "perfil" --layer 2
python3 -m kerebrom setup --db ~/.kerebrom/secure.kdb --passphrase-env KEREBROM_PASSPHRASE
```

Current limits of encrypted mode:

- It protects the database at rest, but a plaintext runtime SQLite file exists while a process is actively using it.
- It prioritizes safety and portability over multi-process concurrency, so encrypted mode is stricter than plain SQLite/WAL mode.
- It currently relies on the system `openssl` binary rather than SQLCipher.

## Single-machine release smoke

If you only have one computer, you can still validate Kerebrom like a fresh install by using isolated temporary HOME directories on the same machine:

```bash
python3 scripts/local_release_smoke.py
```

This smoke runner validates three critical flows end-to-end:

- Fresh CLI usage plus opportunistic setup for Claude Code and Codex.
- Delayed tool installation on the same machine after Kerebrom was already used once.
- MCP stdio server round-trip (`initialize`, `tools/list`, `remember`, `recall`, `context`).

## Automated release gate

For a stricter end-to-end pass, run the release gate:

```bash
python3 scripts/release_gate.py
```

This executes:

- The full unit test suite.
- The single-machine smoke runner.
- An isolated `venv` install via `pip install .` that validates the packaged `kerebrom` entrypoint plus MCP startup from an installed environment.
- Native `backup` / `restore` validation.
- Encrypted-container validation for passphrase-protected databases.
- A benchmark gate for setup/install latency, smoke duration, `remember`, `facts`, `recall`, `context`, MCP round-trip time, and simple retrieval quality proxies.

If you want to run only the local performance gate, use:

```bash
python3 scripts/benchmark_gate.py
```

If you want to validate the real installed Codex client on this machine as well, run:

```bash
python3 scripts/release_gate.py --with-real-codex
```

That optional phase uses the actual `codex exec` client, requires a working local Codex installation with authentication, and spends real model tokens.

If you want to validate the real Claude Code runtime as well, run:

```bash
python3 scripts/release_gate.py --with-real-claude
```

That phase looks for either `claude` on `PATH` or the binary bundled inside the Claude desktop app. On this machine the bundled binary is present, but `claude auth status` currently reports `loggedIn: false`, so the phase fails with an explicit authentication blocker instead of claiming Claude is missing.

## Known limits in this revision

- Retrieval uses deterministic hash embeddings, not neural embeddings or sqlite-vec yet.
- Heuristic capture is intentionally conservative and still benefits from future pattern expansion.
- Encrypted mode protects the database at rest but still uses a plaintext runtime file while the process is active.
- Encrypted mode currently uses the system `openssl` binary instead of SQLCipher.
- Real Claude client automation is wired into the release gate, but on this machine the Claude runtime is not authenticated yet.

## Next step that makes sense

The next serious milestone is to improve retrieval quality with real local embeddings plus sqlite-vec, then carry this Python foundation into the Rust rewrite for distribution and protection.
