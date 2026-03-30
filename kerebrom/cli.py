"""Command-line interface for Kerebrom."""

from __future__ import annotations

import argparse
import json
import os
import sys
from pathlib import Path
from typing import Any, Iterable, Optional

from .store import DEFAULT_PROJECT, KerebromStore


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(prog="kerebrom", description="Local-first persistent memory for AI workflows.")
    subparsers = parser.add_subparsers(dest="command", required=True)

    def add_crypto_arguments(target: argparse.ArgumentParser) -> None:
        target.add_argument("--passphrase-env", help="Read the database passphrase from this environment variable.")
        target.add_argument("--passphrase-file", help="Read the database passphrase from this local file.")

    def add_common_arguments(target: argparse.ArgumentParser) -> None:
        target.add_argument("--db", default="kerebrom.db", help="Path to the SQLite database.")
        target.add_argument("--project", default=DEFAULT_PROJECT, help="Logical project namespace.")
        add_crypto_arguments(target)

    init_parser = subparsers.add_parser("init", help="Initialize the database.")
    add_common_arguments(init_parser)
    init_parser.add_argument("--description", default="", help="Optional project description.")

    remember_parser = subparsers.add_parser("remember", help="Store a memory.")
    add_common_arguments(remember_parser)
    remember_parser.add_argument("text", nargs="?", help="Memory content. If omitted, stdin is used.")
    remember_parser.add_argument("--kind", default="episodic", choices=["core", "episodic", "semantic", "procedural"])
    remember_parser.add_argument("--source", default="manual")
    remember_parser.add_argument("--importance", type=float, default=0.5)
    remember_parser.add_argument("--confidence", type=float, default=0.8)
    remember_parser.add_argument("--tags", nargs="*", default=[], help="Observation tags (decision, bugfix, feature, refactor, discovery, change).")

    recall_parser = subparsers.add_parser("recall", help="Search memories.")
    add_common_arguments(recall_parser)
    recall_parser.add_argument("query", nargs="?", help="Search query. If omitted, stdin is used.")
    recall_parser.add_argument("--limit", type=int, default=5)
    recall_parser.add_argument("--include-inactive", action="store_true")
    recall_parser.add_argument("--json", action="store_true")

    forget_parser = subparsers.add_parser("forget", help="Invalidate memories.")
    add_common_arguments(forget_parser)
    forget_group = forget_parser.add_mutually_exclusive_group(required=True)
    forget_group.add_argument("--memory-id", type=int)
    forget_group.add_argument("--query")
    forget_group.add_argument("--sensitive", action="store_true")

    entities_parser = subparsers.add_parser("entities", help="List known entities.")
    add_common_arguments(entities_parser)
    entities_parser.add_argument("--limit", type=int, default=20)
    entities_parser.add_argument("--all", action="store_true", help="Include entities supported only by inactive memories.")
    entities_parser.add_argument("--json", action="store_true")

    facts_parser = subparsers.add_parser("facts", help="List active semantic facts.")
    add_common_arguments(facts_parser)
    facts_parser.add_argument("--limit", type=int, default=20)
    facts_parser.add_argument("--all", action="store_true", help="Include invalidated facts.")
    facts_parser.add_argument("--json", action="store_true")

    context_parser = subparsers.add_parser("context", help="Build recall context for a query.")
    add_common_arguments(context_parser)
    context_parser.add_argument("query", nargs="?", help="Query. If omitted, stdin is used.")
    context_parser.add_argument("--limit", type=int, default=5)
    context_parser.add_argument("--layer", type=int, default=3, choices=[1, 2, 3], help="Progressive disclosure layer (1=compact, 2=summary, 3=full).")
    context_parser.add_argument("--json", action="store_true")

    export_parser = subparsers.add_parser("export", help="Export memories as JSON.")
    add_common_arguments(export_parser)
    export_parser.add_argument("--active-only", action="store_true")

    backup_parser = subparsers.add_parser("backup", help="Create a consistent SQLite backup.")
    add_common_arguments(backup_parser)
    backup_parser.add_argument("--output", required=True, help="Destination path for the backup database.")
    backup_parser.add_argument("--force", action="store_true", help="Overwrite the destination if it already exists.")

    restore_parser = subparsers.add_parser("restore", help="Restore a database from a backup copy.")
    add_common_arguments(restore_parser)
    restore_parser.add_argument("--input", required=True, help="Source backup database to restore from.")
    restore_parser.add_argument("--force", action="store_true", help="Overwrite the target database if it already exists.")

    serve_parser = subparsers.add_parser("serve", help="Start MCP server over stdio.")
    add_common_arguments(serve_parser)

    consolidate_parser = subparsers.add_parser("consolidate", help="Run episodic → semantic consolidation.")
    add_common_arguments(consolidate_parser)
    consolidate_parser.add_argument("--similarity", type=float, default=0.80, help="Clustering threshold (0-1).")
    consolidate_parser.add_argument("--min-cluster", type=int, default=2, help="Minimum cluster size.")

    decay_parser = subparsers.add_parser("decay", help="Apply memory importance decay (zombie cleanup).")
    add_common_arguments(decay_parser)
    decay_parser.add_argument("--half-life", type=float, default=30.0, help="Half-life in days.")
    decay_parser.add_argument("--min-importance", type=float, default=0.05, help="Threshold below which memories are invalidated.")

    setup_parser = subparsers.add_parser("setup", help="Auto-configure Kerebrom in all detected AI tools.")
    setup_parser.add_argument("--db", default=str(Path.home() / ".kerebrom" / "kerebrom.db"), help="Path to the SQLite database.")
    add_crypto_arguments(setup_parser)

    capture_parser = subparsers.add_parser("capture", help="Passive capture — receives hook event JSON on stdin and stores a memory.")
    capture_parser.add_argument("--db", default=str(Path.home() / ".kerebrom" / "kerebrom.db"), help="Path to the SQLite database.")
    capture_parser.add_argument("--project", default=DEFAULT_PROJECT, help="Logical project namespace.")
    add_crypto_arguments(capture_parser)

    snapshot_parser = subparsers.add_parser("snapshot", help="Create a portable .kbk backup of all memories.")
    add_common_arguments(snapshot_parser)
    snapshot_parser.add_argument("--output", help="Output path for the .kbk file (default: ~/Documents/).")

    revive_parser = subparsers.add_parser("revive", help="Restore memories organically from a .kbk backup.")
    add_common_arguments(revive_parser)
    revive_parser.add_argument("--input", help="Path to a .kbk file. If omitted, searches the computer automatically.")
    revive_parser.add_argument("--no-dedup", action="store_true", help="Skip duplicate checking (faster but may create duplicates).")

    sopor_parser = subparsers.add_parser("sopor", help="Run Sopor Plenus — consolidate transcript memories.")
    add_common_arguments(sopor_parser)
    sopor_group = sopor_parser.add_mutually_exclusive_group()
    sopor_group.add_argument("--session", help="Session ID or path to a specific .jsonl transcript.")
    sopor_group.add_argument("--latest", action="store_true", help="Process only the most recent transcript.")
    sopor_group.add_argument("--all", action="store_true", help="Process all transcripts.")
    sopor_parser.add_argument("--project-path", help="Filesystem path of the project (to find its transcripts).")
    sopor_parser.add_argument("--threshold", type=float, default=0.35, help="Score threshold for extraction (0-1).")

    uninstall_parser = subparsers.add_parser("uninstall", help="Remove all traces of Kerebrom from this machine.")
    uninstall_parser.add_argument("--keep-pip", action="store_true", help="Skip pip uninstall (keep the Python package).")
    uninstall_parser.add_argument("--yes", "-y", action="store_true", help="Skip confirmation prompt.")

    return parser


def read_text(value: Optional[str]) -> str:
    if value:
        return value
    piped = sys.stdin.read().strip()
    if piped:
        return piped
    raise SystemExit("Expected text argument or piped stdin.")


def print_json(data: Any) -> None:
    print(json.dumps(data, ensure_ascii=False, indent=2))


def resolve_passphrase(args: Any) -> Optional[str]:
    passphrase_env = getattr(args, "passphrase_env", None)
    passphrase_file = getattr(args, "passphrase_file", None)
    if passphrase_env and passphrase_file:
        raise SystemExit("Use only one of --passphrase-env or --passphrase-file.")
    if passphrase_env:
        payload = os.environ.get(passphrase_env)
        if not payload:
            raise SystemExit("Environment variable {} is empty or unset.".format(passphrase_env))
        return payload
    if passphrase_file:
        payload = Path(passphrase_file).expanduser().read_text(encoding="utf-8").strip()
        if not payload:
            raise SystemExit("Passphrase file is empty: {}".format(passphrase_file))
        return payload
    return None


def main(argv: Optional[Iterable[str]] = None) -> int:
    parser = build_parser()
    args = parser.parse_args(list(argv) if argv is not None else None)

    # Capture is lightweight — reads stdin JSON, stores memory, exits.
    if args.command == "capture":
        from .hooks import handle_capture
        passphrase = resolve_passphrase(args)
        return handle_capture(db_path=args.db, project=args.project, passphrase=passphrase)

    # Uninstall is special — it must NOT run setup or create a store.
    if args.command == "uninstall":
        from .uninstall import run_uninstall

        if not args.yes:
            print("Esto eliminará TODA la memoria, configuración y el paquete de Kerebrom.")
            print("Esta acción es IRREVERSIBLE.\n")
            try:
                answer = input("¿Continuar? [s/N] ").strip().lower()
            except (EOFError, KeyboardInterrupt):
                answer = ""
            if answer not in ("s", "si", "sí", "y", "yes"):
                print("Cancelado.")
                return 0

        print("\nKerebrom — Desinstalación completa\n")
        run_uninstall(skip_pip=args.keep_pip)
        return 0

    passphrase = None if args.command == "setup" else resolve_passphrase(args)

    # Opportunistic auto-setup — configures detected AI tools silently.
    # For normal CLI usage we keep setup pinned to the default shared DB so
    # read-only commands against custom paths do not create files by surprise.
    from .setup import DEFAULT_DB as DEFAULT_SETUP_DB, ensure_setup

    setup_db_path: Optional[Path] = None
    setup_passphrase_env = getattr(args, "passphrase_env", None)
    setup_passphrase_file = getattr(args, "passphrase_file", None)
    if args.command in {"serve", "setup"} and hasattr(args, "db"):
        setup_db_path = Path(args.db)
    else:
        setup_db_path = Path(DEFAULT_SETUP_DB)
        setup_passphrase_env = None
        setup_passphrase_file = None
    ensure_setup(
        db_path=setup_db_path,
        passphrase_env=setup_passphrase_env,
        passphrase_file=setup_passphrase_file,
    )

    store = KerebromStore(Path(args.db), passphrase=passphrase)

    try:
        if args.command == "init":
            store.initialize(project=args.project, description=args.description)
            print("Initialized {} for project '{}'.".format(args.db, args.project))
            return 0

        if args.command == "remember":
            payload = store.remember(
                content=read_text(args.text),
                project=args.project,
                kind=args.kind,
                source=args.source,
                importance=args.importance,
                confidence=args.confidence,
                tags=args.tags,
            )
            print_json(payload)
            return 0

        if args.command == "recall":
            results = store.recall(
                query=read_text(args.query),
                project=args.project,
                limit=args.limit,
                include_inactive=args.include_inactive,
            )
            if args.json:
                print_json([result.to_dict() for result in results])
            else:
                for result in results:
                    print("[{}] score={:.3f} {}".format(result.id, result.score, result.content))
            return 0

        if args.command == "forget":
            count = store.forget(
                project=args.project,
                memory_id=args.memory_id,
                query=args.query,
                sensitive=args.sensitive,
            )
            print("Invalidated {} memories.".format(count))
            return 0

        if args.command == "entities":
            data = store.list_entities(project=args.project, limit=args.limit, active_only=not args.all)
            if args.json:
                print_json(data)
            else:
                for row in data:
                    print("{name} [{entity_type}] x{memory_count}".format(**row))
            return 0

        if args.command == "facts":
            data = store.list_facts(project=args.project, active_only=not args.all, limit=args.limit)
            if args.json:
                print_json(data)
            else:
                for row in data:
                    suffix = "" if row["invalid_at"] is None else " (invalidated)"
                    print("{subject} --{predicate}--> {object} [{confidence:.2f}]{suffix}".format(suffix=suffix, **row))
            return 0

        if args.command == "context":
            data = store.build_context(query=read_text(args.query), project=args.project, limit=args.limit, layer=args.layer)
            if args.json:
                print_json(data)
            else:
                layer = data.get("layer", 3)
                if layer == 1:
                    print("Facts ({} total):".format(data.get("fact_count", 0)))
                    for s in data.get("facts_summary", []):
                        print("  " + s)
                    print("Memories:")
                    for m in data.get("memory_index", []):
                        print("  id={id} score={score} kind={kind}".format(**m))
                elif layer == 2:
                    print("Facts:")
                    for f in data.get("facts", []):
                        print("- {subject} --{predicate}--> {object} [{confidence:.2f}]".format(**f))
                    print("Memories:")
                    for m in data.get("memories", []):
                        print("- [{id}] ({kind}, {created_at}) {snippet}".format(**m))
                else:
                    print("Facts:")
                    for fact in data["facts"]:
                        print("- {subject} --{predicate}--> {object} [{confidence:.2f}]".format(**fact))
                    print("Memories:")
                    for memory in data["memories"]:
                        print("- [{id}] {content}".format(**memory))
            return 0

        if args.command == "export":
            data = store.export_memories(project=args.project, include_inactive=not args.active_only)
            print_json(data)
            return 0

        if args.command == "backup":
            data = store.backup(output_path=args.output, overwrite=args.force)
            print_json(data)
            return 0

        if args.command == "restore":
            data = store.restore_from(input_path=args.input, overwrite=args.force)
            print_json(data)
            return 0

        if args.command == "serve":
            from .mcp_server import run_server
            run_server(db_path=args.db, project=args.project, passphrase=passphrase)
            return 0


        if args.command == "consolidate":
            data = store.consolidate(
                project=args.project,
                similarity_threshold=args.similarity,
                min_cluster_size=args.min_cluster,
            )
            print_json(data)
            return 0

        if args.command == "decay":
            data = store.apply_decay(
                project=args.project,
                half_life_days=args.half_life,
                min_importance=args.min_importance,
            )
            print_json(data)
            return 0

        if args.command == "snapshot":
            from .backup import create_backup
            output = Path(args.output) if args.output else None
            backup_path = create_backup(store, output_path=output, project=args.project)
            memories = store.export_memories(project=args.project, include_inactive=False)
            print_json({
                "backup_path": str(backup_path),
                "memory_count": len(memories),
                "message": "Snapshot creado exitosamente.",
            })
            return 0

        if args.command == "revive":
            from .backup import find_latest_backup, restore_organic, validate_backup
            if args.input:
                backup_path = Path(args.input)
                if not backup_path.exists():
                    raise SystemExit("Archivo no encontrado: {}".format(args.input))
            else:
                print("Buscando backups en la computadora...")
                backup_path = find_latest_backup()
                if not backup_path:
                    raise SystemExit("No se encontró ningún archivo .kbk de Kerebrom.")
                info = validate_backup(backup_path)
                if not info.get("valid"):
                    raise SystemExit("Backup inválido: {}".format(info.get("error")))
                print("Encontrado: {} ({} memorias, {})".format(
                    backup_path.name, info["memory_count"], info["created_at"],
                ))

            print("Restaurando orgánicamente...")
            result = restore_organic(
                store, backup_path,
                project=args.project,
                skip_duplicates=not args.no_dedup,
            )
            print_json(result)
            return 0

        if args.command == "sopor":
            from .sopor import find_transcripts, run_sopor, run_sopor_all, run_sopor_latest
            print("Kerebrom — Sopor Plenus\n")

            if args.session:
                # Direct path or session ID.
                session_path = Path(args.session)
                if not session_path.exists():
                    # Try finding it in the projects dir.
                    candidates = list(Path.home().joinpath(".claude", "projects").rglob("{}.jsonl".format(args.session)))
                    if not candidates:
                        raise SystemExit("Transcript not found: {}".format(args.session))
                    session_path = candidates[0]
                result = run_sopor(store, session_path, project=args.project, score_threshold=args.threshold)
                print_json({
                    "session_id": result.session_id,
                    "messages_read": result.messages_read,
                    "memories_extracted": result.memories_extracted,
                    "memories_stored": result.memories_stored,
                    "memories_skipped_duplicate": result.memories_skipped_duplicate,
                    "errors": result.errors,
                })
            elif args.all:
                results = run_sopor_all(store, project_path=args.project_path, project=args.project, score_threshold=args.threshold)
                summary = {
                    "transcripts_processed": len(results),
                    "total_messages": sum(r.messages_read for r in results),
                    "total_extracted": sum(r.memories_extracted for r in results),
                    "total_stored": sum(r.memories_stored for r in results),
                    "total_skipped": sum(r.memories_skipped_duplicate for r in results),
                    "errors": [e for r in results for e in r.errors],
                }
                print_json(summary)
            else:
                # Default: --latest
                result = run_sopor_latest(store, project_path=args.project_path, project=args.project, score_threshold=args.threshold)
                if result is None:
                    print("No transcripts found.")
                    return 0
                print_json({
                    "session_id": result.session_id,
                    "messages_read": result.messages_read,
                    "memories_extracted": result.memories_extracted,
                    "memories_stored": result.memories_stored,
                    "memories_skipped_duplicate": result.memories_skipped_duplicate,
                    "errors": result.errors,
                })
            return 0

        if args.command == "setup":
            from .setup import run_setup
            print("Kerebrom — Auto-setup\n")
            results = run_setup(
                db_path=Path(args.db),
                passphrase_env=args.passphrase_env,
                passphrase_file=args.passphrase_file,
            )
            print_json(results)
            return 0

        parser.print_help()
        return 1
    finally:
        store.close()
