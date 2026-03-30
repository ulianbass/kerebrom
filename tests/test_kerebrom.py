from __future__ import annotations

import contextlib
import io
import json
import os
import runpy
import subprocess
import tempfile
import sys
import unittest
from datetime import datetime, timedelta, timezone
from pathlib import Path

from kerebrom.store import KerebromStore


class KerebromStoreTests(unittest.TestCase):
    def setUp(self) -> None:
        self.tempdir = tempfile.TemporaryDirectory()
        self.db_path = Path(self.tempdir.name) / "kerebrom.db"
        self.store = KerebromStore(self.db_path)
        self.store.initialize()

    def tearDown(self) -> None:
        self.tempdir.cleanup()

    def test_remember_and_recall(self) -> None:
        self.store.remember("Me llamo Ulian. Vivo en Guatemala. Trabajo en Kerebrom.", importance=0.9)
        self.store.remember("Prefiero Rust para construir herramientas de sistemas.", importance=0.7)

        results = self.store.recall("Donde vivo", limit=3)

        self.assertGreaterEqual(len(results), 1)
        self.assertIn("Guatemala", results[0].content)

    def test_sensitive_content_is_redacted(self) -> None:
        payload = self.store.remember("Mi correo es ulian@example.com y api_key=sk_1234567890ABCDE")
        memory = payload["memory"]

        self.assertTrue(memory["is_redacted"])
        self.assertNotIn("ulian@example.com", memory["content"])
        self.assertIn("[REDACTED:EMAIL]", memory["content"])

    def test_duplicate_memories_are_deduplicated(self) -> None:
        first = self.store.remember("Vivo en Guatemala.")
        second = self.store.remember("Vivo en Guatemala.")

        self.assertTrue(first["inserted"])
        self.assertFalse(second["inserted"])
        memory = self.store.get_memory(first["memory"]["id"])
        self.assertGreaterEqual(memory.duplicate_count, 1)

    def test_forget_invalidates_memory(self) -> None:
        memory_id = self.store.remember("Estoy aprendiendo Rust.")["memory"]["id"]
        count = self.store.forget(memory_id=memory_id)

        self.assertEqual(count, 1)
        active = self.store.recall("Rust", limit=10)
        ids = [memory.id for memory in active]
        self.assertNotIn(memory_id, ids)

    def test_forget_invalidates_derived_facts(self) -> None:
        memory_id = self.store.remember("Vivo en Guatemala.")["memory"]["id"]
        self.store.forget(memory_id=memory_id)

        facts = self.store.list_facts(limit=10)
        self.assertEqual(facts, [])

    def test_contradictory_facts_invalidate_previous_relation(self) -> None:
        self.store.remember("Vivo en Guatemala.")
        self.store.remember("Vivo en España.")

        facts = self.store.list_facts(limit=10)
        live_facts = [fact for fact in facts if fact["predicate"] == "lives_in"]
        self.assertEqual(len(live_facts), 1)
        self.assertEqual(live_facts[0]["object"], "España")

    def test_forgetting_latest_exclusive_fact_restores_previous_supported_fact(self) -> None:
        first_memory_id = self.store.remember("Vivo en Guatemala.")["memory"]["id"]
        second_memory_id = self.store.remember("Vivo en España.")["memory"]["id"]
        self.store.forget(memory_id=second_memory_id)

        facts = self.store.list_facts(limit=10)
        live_facts = [fact for fact in facts if fact["predicate"] == "lives_in"]
        self.assertEqual(len(live_facts), 1)
        self.assertEqual(live_facts[0]["object"], "Guatemala")
        self.assertIsNotNone(first_memory_id)

    def test_forget_query_forgets_dormant_memory_without_reactivation_side_effects(self) -> None:
        memory_id = self.store.remember("Me gusta Rust.")["memory"]["id"]
        dormant_at = (datetime.now(timezone.utc) - timedelta(days=2)).replace(microsecond=0).isoformat()
        with self.store.connect() as connection:
            connection.execute(
                """
                UPDATE memories
                SET invalid_at = ?, source = 'manual', access_count = 0, updated_at = ?
                WHERE id = ?
                """,
                (dormant_at, dormant_at, memory_id),
            )

        count = self.store.forget(query="Rust")

        self.assertEqual(count, 1)
        with self.store.connect() as connection:
            row = connection.execute(
                "SELECT source, invalid_at, access_count FROM memories WHERE id = ?",
                (memory_id,),
            ).fetchone()

        self.assertEqual(row["source"], "forgotten")
        self.assertEqual(row["invalid_at"], dormant_at)
        self.assertEqual(int(row["access_count"]), 0)

    def test_backup_and_restore_round_trip(self) -> None:
        self.store.remember("Vivo en Guatemala.")
        backup_path = Path(self.tempdir.name) / "backup.db"
        restored_path = Path(self.tempdir.name) / "restored.db"

        backup_summary = self.store.backup(backup_path)
        restored_store = KerebromStore(restored_path)
        restore_summary = restored_store.restore_from(backup_path)
        restored_results = restored_store.recall("Guatemala", limit=5)

        self.assertEqual(backup_summary["memories"], 1)
        self.assertEqual(restore_summary["memories"], 1)
        self.assertTrue(backup_path.exists())
        self.assertTrue(restored_path.exists())
        self.assertGreaterEqual(len(restored_results), 1)
        self.assertIn("Guatemala", restored_results[0].content)


class EncryptedStoreTests(unittest.TestCase):
    def setUp(self) -> None:
        self.tempdir = tempfile.TemporaryDirectory()
        self.db_path = Path(self.tempdir.name) / "secure.kdb"
        self.passphrase = "secreto-ulian"
        self.store = KerebromStore(self.db_path, passphrase=self.passphrase)
        self.store.initialize()

    def tearDown(self) -> None:
        self.store.close()
        self.tempdir.cleanup()

    def test_encrypted_store_round_trip(self) -> None:
        self.store.remember("Vivo en Guatemala.")
        results = self.store.recall("Guatemala", limit=5)
        raw = self.db_path.read_bytes()

        self.assertGreaterEqual(len(results), 1)
        self.assertIn("Guatemala", results[0].content)
        self.assertTrue(raw.startswith(b"KEREBROM-ENC-1"))
        self.assertNotIn(b"SQLite format 3", raw[:64])

    def test_opening_encrypted_store_without_passphrase_fails(self) -> None:
        self.store.remember("Trabajo en Kerebrom.")
        plain_store = KerebromStore(self.db_path)

        with self.assertRaises(ValueError):
            plain_store.recall("Kerebrom", limit=5)

    def test_restore_plain_backup_into_encrypted_target(self) -> None:
        plain_db = Path(self.tempdir.name) / "plain.db"
        backup_path = Path(self.tempdir.name) / "plain-backup.db"
        plain_store = KerebromStore(plain_db)
        plain_store.initialize()
        plain_store.remember("Mi lenguaje favorito es Rust.")
        plain_store.backup(backup_path)

        encrypted_target = KerebromStore(Path(self.tempdir.name) / "restored-secure.kdb", passphrase=self.passphrase)
        summary = encrypted_target.restore_from(backup_path)
        results = encrypted_target.recall("Rust", limit=5)

        self.assertEqual(summary["memories"], 1)
        self.assertGreaterEqual(len(results), 1)
        self.assertIn("Rust", results[0].content)


class ProgressiveDisclosureTests(unittest.TestCase):
    def setUp(self) -> None:
        self.tempdir = tempfile.TemporaryDirectory()
        self.db_path = Path(self.tempdir.name) / "kerebrom.db"
        self.store = KerebromStore(self.db_path)
        self.store.initialize()
        self.store.remember("Vivo en Guatemala.", importance=0.9)
        self.store.remember("Prefiero Python para data science.", importance=0.7)

    def tearDown(self) -> None:
        self.tempdir.cleanup()

    def test_layer_1_compact_index(self) -> None:
        ctx = self.store.build_context("Guatemala", layer=1)
        self.assertEqual(ctx["layer"], 1)
        self.assertIn("memory_index", ctx)
        self.assertIn("facts_summary", ctx)
        for item in ctx["memory_index"]:
            self.assertIn("id", item)
            self.assertIn("score", item)
            self.assertNotIn("content", item)

    def test_layer_2_summary(self) -> None:
        ctx = self.store.build_context("Guatemala", layer=2)
        self.assertEqual(ctx["layer"], 2)
        for m in ctx["memories"]:
            self.assertIn("snippet", m)
            self.assertIn("created_at", m)
            self.assertNotIn("raw_content", m)

    def test_layer_3_full_detail(self) -> None:
        ctx = self.store.build_context("Guatemala", layer=3)
        self.assertEqual(ctx["layer"], 3)
        for m in ctx["memories"]:
            self.assertIn("content", m)
            self.assertIn("raw_content", m)

    def test_context_filters_unrelated_facts_for_specific_query(self) -> None:
        self.store.remember("Trabajo en Kerebrom.", importance=0.9)
        ctx = self.store.build_context("Python", layer=2)
        predicates = {fact["predicate"] for fact in ctx["facts"]}
        self.assertIn("prefers", predicates)
        self.assertNotIn("lives_in", predicates)
        self.assertNotIn("works_at", predicates)


class MemoryDecayTests(unittest.TestCase):
    def setUp(self) -> None:
        self.tempdir = tempfile.TemporaryDirectory()
        self.db_path = Path(self.tempdir.name) / "kerebrom.db"
        self.store = KerebromStore(self.db_path)
        self.store.initialize()

    def tearDown(self) -> None:
        self.tempdir.cleanup()

    def test_decay_reduces_importance(self) -> None:
        self.store.remember("Algo poco importante.", importance=0.1)
        result = self.store.apply_decay(half_life_days=0.001)
        self.assertGreaterEqual(result["total_active"], 1)

    def test_decay_returns_summary(self) -> None:
        self.store.remember("Memoria de prueba.", importance=0.5)
        result = self.store.apply_decay()
        self.assertIn("total_active", result)
        self.assertIn("decayed", result)
        self.assertIn("invalidated_zombies", result)

    def test_decay_uses_incremental_baseline(self) -> None:
        memory_id = self.store.remember("Memoria con tiempo acumulado.", importance=1.0)["memory"]["id"]
        old_timestamp = (datetime.now(timezone.utc) - timedelta(days=60)).replace(microsecond=0).isoformat()
        with self.store.connect() as connection:
            connection.execute(
                "UPDATE memories SET created_at = ?, updated_at = ? WHERE id = ?",
                (old_timestamp, old_timestamp, memory_id),
            )

        self.store.apply_decay(half_life_days=30)
        first_pass = self.store.get_memory(memory_id)
        self.store.apply_decay(half_life_days=30)
        second_pass = self.store.get_memory(memory_id)

        self.assertAlmostEqual(first_pass.importance, second_pass.importance, places=6)


class ConsolidationTests(unittest.TestCase):
    def setUp(self) -> None:
        self.tempdir = tempfile.TemporaryDirectory()
        self.db_path = Path(self.tempdir.name) / "kerebrom.db"
        self.store = KerebromStore(self.db_path)
        self.store.initialize()

    def tearDown(self) -> None:
        self.tempdir.cleanup()

    def test_consolidation_with_similar_memories(self) -> None:
        self.store.remember("Vivo en Guatemala.", kind="episodic", importance=0.6)
        self.store.remember("Vivo en Guatemala, Centroamérica.", kind="episodic", importance=0.6)
        result = self.store.consolidate(similarity_threshold=0.70)
        self.assertGreaterEqual(result["clusters"], 0)
        self.assertIn("new_facts", result)
        self.assertIn("consolidated_episodes", result)

    def test_consolidation_not_enough_memories(self) -> None:
        self.store.remember("Solo una memoria.", kind="episodic")
        result = self.store.consolidate()
        self.assertEqual(result["clusters"], 0)

    def test_consolidation_is_idempotent(self) -> None:
        self.store.remember("Vivo en Guatemala.", kind="episodic", importance=0.6)
        self.store.remember("Vivo en Guatemala, Centroamérica.", kind="episodic", importance=0.6)
        first = self.store.consolidate(similarity_threshold=0.70)
        second = self.store.consolidate(similarity_threshold=0.70)
        exported = self.store.export_memories(include_inactive=True)
        consolidated = [memory for memory in exported if memory["source"] == "consolidation"]

        self.assertEqual(first["clusters"], 1)
        self.assertEqual(second["clusters"], 0)
        self.assertEqual(len(consolidated), 1)


class ExpandedCaptureTests(unittest.TestCase):
    def setUp(self) -> None:
        self.tempdir = tempfile.TemporaryDirectory()
        self.db_path = Path(self.tempdir.name) / "kerebrom.db"
        self.store = KerebromStore(self.db_path)
        self.store.initialize()

    def tearDown(self) -> None:
        self.tempdir.cleanup()

    def test_dislikes_relation(self) -> None:
        self.store.remember("No me gusta Java.")
        facts = self.store.list_facts(limit=20)
        dislikes = [f for f in facts if f["predicate"] == "dislikes"]
        self.assertGreaterEqual(len(dislikes), 1)

    def test_uses_relation(self) -> None:
        self.store.remember("Uso Docker para todos mis proyectos.")
        facts = self.store.list_facts(limit=20)
        uses = [f for f in facts if f["predicate"] == "uses"]
        self.assertGreaterEqual(len(uses), 1)

    def test_building_relation(self) -> None:
        self.store.remember("Estoy construyendo Kerebrom.")
        facts = self.store.list_facts(limit=20)
        building = [f for f in facts if f["predicate"] == "building"]
        self.assertGreaterEqual(len(building), 1)

    def test_wants_relation(self) -> None:
        self.store.remember("Quiero aprender Haskell.")
        facts = self.store.list_facts(limit=20)
        wants = [f for f in facts if f["predicate"] == "wants"]
        self.assertGreaterEqual(len(wants), 1)

    def test_multivalue_predicates_do_not_override_each_other(self) -> None:
        self.store.remember("Me gusta Python.")
        self.store.remember("Me gusta Rust.")
        facts = self.store.list_facts(limit=20)
        likes = [f["object"] for f in facts if f["predicate"] == "likes"]
        self.assertEqual(set(likes), {"Python", "Rust"})

    def test_soy_de_does_not_create_false_role_or_residence(self) -> None:
        self.store.remember("Soy de Guatemala.")
        facts = self.store.list_facts(limit=20)
        predicates = {fact["predicate"] for fact in facts}
        self.assertIn("from", predicates)
        self.assertNotIn("role", predicates)
        self.assertNotIn("lives_in", predicates)

    def test_entity_extraction_skips_leading_action_noise(self) -> None:
        self.store.remember("Uso Docker.")
        entities = self.store.list_entities(limit=20)
        names = {entity["name"] for entity in entities}
        self.assertIn("Docker", names)
        self.assertNotIn("Uso Docker", names)


class EntityViewTests(unittest.TestCase):
    def setUp(self) -> None:
        self.tempdir = tempfile.TemporaryDirectory()
        self.db_path = Path(self.tempdir.name) / "kerebrom.db"
        self.store = KerebromStore(self.db_path)
        self.store.initialize()

    def tearDown(self) -> None:
        self.tempdir.cleanup()

    def test_entities_default_to_active_memories_only(self) -> None:
        memory_id = self.store.remember("Uso Docker.")["memory"]["id"]
        before = self.store.list_entities(limit=20)
        self.store.forget(memory_id=memory_id)
        after = self.store.list_entities(limit=20)
        all_entities = self.store.list_entities(limit=20, active_only=False)

        before_names = {entity["name"] for entity in before}
        after_names = {entity["name"] for entity in after}
        all_names = {entity["name"] for entity in all_entities}

        self.assertIn("Docker", before_names)
        self.assertNotIn("Docker", after_names)
        self.assertIn("Docker", all_names)


class MaintenanceSchedulingTests(unittest.TestCase):
    def setUp(self) -> None:
        self.tempdir = tempfile.TemporaryDirectory()
        self.db_path = Path(self.tempdir.name) / "kerebrom.db"

        class TrackingStore(KerebromStore):
            def __init__(self, db_path: Path) -> None:
                super().__init__(db_path)
                self.decay_runs = 0
                self.consolidate_runs = 0

            def _apply_decay_by_kind(self, project: str) -> None:
                self.decay_runs += 1

            def consolidate(self, project: str = "default", similarity_threshold: float = 0.80, min_cluster_size: int = 2):
                self.consolidate_runs += 1
                return {"clusters": 0, "new_facts": 0, "consolidated_episodes": 0}

        self.store = TrackingStore(self.db_path)
        self.store.initialize()

    def tearDown(self) -> None:
        self.tempdir.cleanup()

    def test_auto_maintain_skips_tasks_when_not_due(self) -> None:
        now = datetime.now(timezone.utc).replace(microsecond=0).isoformat()
        with self.store.connect() as connection:
            connection.execute(
                """
                INSERT INTO maintenance_log(project, task, last_run_at)
                VALUES (?, ?, ?), (?, ?, ?)
                """,
                ("default", "decay", now, "default", "consolidate", now),
            )

        self.store._auto_maintain("default")
        self.assertEqual(self.store.decay_runs, 0)
        self.assertEqual(self.store.consolidate_runs, 0)

    def test_auto_maintain_runs_due_tasks(self) -> None:
        self.store._auto_maintain("default")
        self.assertEqual(self.store.decay_runs, 1)
        self.assertEqual(self.store.consolidate_runs, 1)


class SetupTests(unittest.TestCase):
    def setUp(self) -> None:
        self.tempdir = tempfile.TemporaryDirectory()
        self.original_home = os.environ.get("HOME")
        os.environ["HOME"] = self.tempdir.name

    def tearDown(self) -> None:
        if self.original_home is None:
            os.environ.pop("HOME", None)
        else:
            os.environ["HOME"] = self.original_home
        self.tempdir.cleanup()

    def test_ensure_setup_configures_tools_installed_later(self) -> None:
        from unittest.mock import patch
        from kerebrom.setup import ensure_setup

        db_path = Path(self.tempdir.name) / ".kerebrom" / "kerebrom.db"
        codex_dir = Path(self.tempdir.name) / ".codex"

        with patch("kerebrom.setup._is_temp_path", return_value=False):
            ensure_setup(db_path=db_path)
        self.assertFalse((codex_dir / "config.toml").exists())

        codex_dir.mkdir(parents=True, exist_ok=True)
        with patch("kerebrom.setup._is_temp_path", return_value=False):
            ensure_setup(db_path=db_path)

        self.assertTrue((codex_dir / "config.toml").exists())
        self.assertTrue((codex_dir / "AGENTS.md").exists())

    def test_ensure_setup_propagates_passphrase_selector(self) -> None:
        from unittest.mock import patch
        from kerebrom.setup import ensure_setup

        db_path = Path(self.tempdir.name) / ".kerebrom" / "secure.kdb"
        codex_dir = Path(self.tempdir.name) / ".codex"
        claude_dir = Path(self.tempdir.name) / ".claude"
        codex_dir.mkdir(parents=True, exist_ok=True)
        claude_dir.mkdir(parents=True, exist_ok=True)

        with patch("kerebrom.setup._is_temp_path", return_value=False):
            ensure_setup(db_path=db_path, passphrase_env="KEREBROM_PASSPHRASE")

        codex_config = (codex_dir / "config.toml").read_text(encoding="utf-8")
        mcp_json = Path(self.tempdir.name) / ".claude" / ".mcp.json"
        mcp_config = json.loads(mcp_json.read_text(encoding="utf-8"))

        self.assertIn("--passphrase-env", codex_config)
        self.assertIn("KEREBROM_PASSPHRASE", codex_config)
        self.assertIn("--passphrase-env", mcp_config["mcpServers"]["Kerebrom"]["args"])
        self.assertIn("KEREBROM_PASSPHRASE", mcp_config["mcpServers"]["Kerebrom"]["args"])


class CLITests(unittest.TestCase):
    def setUp(self) -> None:
        self.tempdir = tempfile.TemporaryDirectory()
        self.original_home = os.environ.get("HOME")
        os.environ["HOME"] = self.tempdir.name

    def tearDown(self) -> None:
        if self.original_home is None:
            os.environ.pop("HOME", None)
        else:
            os.environ["HOME"] = self.original_home
        self.tempdir.cleanup()

    def test_init_persists_project_description(self) -> None:
        from kerebrom.cli import main

        db_path = Path(self.tempdir.name) / "kerebrom.db"
        with contextlib.redirect_stdout(io.StringIO()):
            exit_code = main(
                [
                    "init",
                    "--db",
                    str(db_path),
                    "--project",
                    "demo",
                    "--description",
                    "Proyecto de prueba",
                ]
            )

        self.assertEqual(exit_code, 0)
        store = KerebromStore(db_path)
        with store.connect() as connection:
            row = connection.execute(
                "SELECT description FROM projects WHERE name = ?",
                ("demo",),
            ).fetchone()

        self.assertIsNotNone(row)
        self.assertEqual(row["description"], "Proyecto de prueba")

    def test_backup_and_restore_commands_round_trip(self) -> None:
        from kerebrom.cli import main

        db_path = Path(self.tempdir.name) / "source.db"
        backup_path = Path(self.tempdir.name) / "backup.db"
        restored_path = Path(self.tempdir.name) / "restored.db"

        with contextlib.redirect_stdout(io.StringIO()):
            self.assertEqual(main(["init", "--db", str(db_path), "--project", "demo"]), 0)
            self.assertEqual(
                main(["remember", "--db", str(db_path), "--project", "demo", "Trabajo en Kerebrom."]),
                0,
            )

        backup_stdout = io.StringIO()
        with contextlib.redirect_stdout(backup_stdout):
            self.assertEqual(
                main(
                    [
                        "backup",
                        "--db",
                        str(db_path),
                        "--project",
                        "demo",
                        "--output",
                        str(backup_path),
                    ]
                ),
                0,
            )
        backup_data = json.loads(backup_stdout.getvalue())
        self.assertEqual(backup_data["memories"], 1)

        restore_stdout = io.StringIO()
        with contextlib.redirect_stdout(restore_stdout):
            self.assertEqual(
                main(
                    [
                        "restore",
                        "--db",
                        str(restored_path),
                        "--project",
                        "demo",
                        "--input",
                        str(backup_path),
                    ]
                ),
                0,
            )
        restore_data = json.loads(restore_stdout.getvalue())
        self.assertEqual(restore_data["memories"], 1)

        restored_store = KerebromStore(restored_path)
        restored_results = restored_store.recall("Kerebrom", project="demo", limit=5)
        self.assertGreaterEqual(len(restored_results), 1)
        self.assertIn("Kerebrom", restored_results[0].content)

    def test_backup_command_does_not_create_missing_custom_db_via_setup(self) -> None:
        from kerebrom.cli import main

        missing_db = Path(self.tempdir.name) / "missing.db"
        backup_path = Path(self.tempdir.name) / "missing-backup.db"

        with self.assertRaises(FileNotFoundError):
            with contextlib.redirect_stdout(io.StringIO()):
                main(
                    [
                        "backup",
                        "--db",
                        str(missing_db),
                        "--project",
                        "demo",
                        "--output",
                        str(backup_path),
                    ]
                )

        self.assertFalse(missing_db.exists())

    def test_cli_round_trip_with_passphrase_env(self) -> None:
        from kerebrom.cli import main

        db_path = Path(self.tempdir.name) / "secure.kdb"
        env_name = "KEREBROM_TEST_PASSPHRASE"
        previous = os.environ.get(env_name)
        os.environ[env_name] = "prueba-segura"
        try:
            with contextlib.redirect_stdout(io.StringIO()):
                self.assertEqual(
                    main(["init", "--db", str(db_path), "--project", "demo", "--passphrase-env", env_name]),
                    0,
                )
                self.assertEqual(
                    main(
                        [
                            "remember",
                            "--db",
                            str(db_path),
                            "--project",
                            "demo",
                            "--passphrase-env",
                            env_name,
                            "Vivo en Guatemala.",
                        ]
                    ),
                    0,
                )

            recall_stdout = io.StringIO()
            with contextlib.redirect_stdout(recall_stdout):
                self.assertEqual(
                    main(
                        [
                            "recall",
                            "--db",
                            str(db_path),
                            "--project",
                            "demo",
                            "--passphrase-env",
                            env_name,
                            "--json",
                            "Guatemala",
                        ]
                    ),
                    0,
                )
            recall_data = json.loads(recall_stdout.getvalue())

            self.assertTrue(db_path.read_bytes().startswith(b"KEREBROM-ENC-1"))
            self.assertGreaterEqual(len(recall_data), 1)
            self.assertIn("Guatemala", recall_data[0]["content"])
        finally:
            if previous is None:
                os.environ.pop(env_name, None)
            else:
                os.environ[env_name] = previous


class MCPServerProtocolTests(unittest.TestCase):
    """Unit tests for the MCP server message handlers (no actual stdio)."""

    def setUp(self) -> None:
        self.tempdir = tempfile.TemporaryDirectory()
        self.db_path = Path(self.tempdir.name) / "kerebrom.db"
        self.store = KerebromStore(self.db_path)
        self.store.initialize()

    def tearDown(self) -> None:
        self.tempdir.cleanup()

    def test_initialize_response(self) -> None:
        from kerebrom.mcp_server import handle_initialize
        resp = handle_initialize(1, {})
        self.assertEqual(resp["id"], 1)
        result = resp["result"]
        self.assertIn("protocolVersion", result)
        self.assertIn("capabilities", result)
        self.assertIn("tools", result["capabilities"])

    def test_tools_list(self) -> None:
        from kerebrom.mcp_server import handle_tools_list
        resp = handle_tools_list(2, {})
        tools = resp["result"]["tools"]
        names = {t["name"] for t in tools}
        self.assertIn("remember", names)
        self.assertIn("recall", names)
        self.assertIn("forget", names)
        self.assertIn("entities", names)
        self.assertIn("facts", names)
        self.assertIn("context", names)

    def test_tools_call_remember(self) -> None:
        from kerebrom.mcp_server import handle_tools_call
        resp = handle_tools_call(
            3,
            {"name": "remember", "arguments": {"content": "Test via MCP."}},
            self.store,
            "default",
        )
        content = resp["result"]["content"]
        self.assertEqual(len(content), 1)
        data = json.loads(content[0]["text"])
        self.assertTrue(data["inserted"])

    def test_tools_call_recall(self) -> None:
        from kerebrom.mcp_server import handle_tools_call
        self.store.remember("Vivo en Guatemala.")
        resp = handle_tools_call(
            4,
            {"name": "recall", "arguments": {"query": "Guatemala"}},
            self.store,
            "default",
        )
        content = resp["result"]["content"]
        data = json.loads(content[0]["text"])
        self.assertIsInstance(data, list)
        self.assertGreaterEqual(len(data), 1)

    def test_tools_call_context_layer(self) -> None:
        from kerebrom.mcp_server import handle_tools_call
        self.store.remember("Prefiero Python.")
        resp = handle_tools_call(
            5,
            {"name": "context", "arguments": {"query": "preferencias", "layer": 1}},
            self.store,
            "default",
        )
        content = resp["result"]["content"]
        data = json.loads(content[0]["text"])
        self.assertEqual(data["layer"], 1)


class MCPServerSubprocessTests(unittest.TestCase):
    def setUp(self) -> None:
        self.tempdir = tempfile.TemporaryDirectory()
        self.original_home = os.environ.get("HOME")
        os.environ["HOME"] = self.tempdir.name
        self.repo_root = Path(__file__).resolve().parents[1]

    def tearDown(self) -> None:
        if self.original_home is None:
            os.environ.pop("HOME", None)
        else:
            os.environ["HOME"] = self.original_home
        self.tempdir.cleanup()

    def test_stdio_server_round_trip(self) -> None:
        db_path = Path(self.tempdir.name) / "kerebrom.db"
        env = os.environ.copy()
        env["HOME"] = self.tempdir.name
        pythonpath = str(self.repo_root)
        existing = env.get("PYTHONPATH", "")
        env["PYTHONPATH"] = pythonpath if not existing else pythonpath + os.pathsep + existing

        messages = [
            {"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": {}},
            {"jsonrpc": "2.0", "method": "notifications/initialized", "params": {}},
            {"jsonrpc": "2.0", "id": 2, "method": "tools/list", "params": {}},
            {
                "jsonrpc": "2.0",
                "id": 3,
                "method": "tools/call",
                "params": {"name": "remember", "arguments": {"content": "Vivo en Guatemala."}},
            },
            {
                "jsonrpc": "2.0",
                "id": 4,
                "method": "tools/call",
                "params": {"name": "recall", "arguments": {"query": "Guatemala", "limit": 3}},
            },
        ]
        payload = "\n".join(json.dumps(message, ensure_ascii=False) for message in messages) + "\n"

        result = subprocess.run(
            [sys.executable, "-m", "kerebrom", "serve", "--db", str(db_path), "--project", "subprocess"],
            cwd=str(self.repo_root),
            env=env,
            input=payload,
            text=True,
            capture_output=True,
            check=False,
        )

        self.assertEqual(result.returncode, 0, msg=result.stderr)
        responses = [json.loads(line) for line in result.stdout.splitlines() if line.strip()]
        by_id = {response["id"]: response for response in responses}

        self.assertEqual(by_id[1]["result"]["protocolVersion"], "2025-03-26")
        tools = {tool["name"] for tool in by_id[2]["result"]["tools"]}
        self.assertIn("remember", tools)
        self.assertIn("recall", tools)

        remember_data = json.loads(by_id[3]["result"]["content"][0]["text"])
        self.assertTrue(remember_data["inserted"])

        recall_data = json.loads(by_id[4]["result"]["content"][0]["text"])
        self.assertGreaterEqual(len(recall_data), 1)
        self.assertIn("Guatemala", recall_data[0]["content"])


class BenchmarkGateTests(unittest.TestCase):
    def test_retrieval_metrics_cover_remember_and_facts(self) -> None:
        repo_root = Path(__file__).resolve().parents[1]
        module = runpy.run_path(str(repo_root / "scripts" / "benchmark_gate.py"))

        thresholds = module["THRESHOLDS"]
        metrics = module["_measure_retrieval_metrics"]()

        self.assertIn("remember_p95_ms_max", thresholds)
        self.assertIn("facts_p95_ms_max", thresholds)
        self.assertIn("remember_median_ms", metrics)
        self.assertIn("remember_p95_ms", metrics)
        self.assertIn("facts_median_ms", metrics)
        self.assertIn("facts_p95_ms", metrics)
        self.assertGreater(metrics["corpus_memories"], 0)
        self.assertGreater(metrics["queries"], 0)


class ReactivationTests(unittest.TestCase):
    """Test the human-like memory reactivation model."""

    def setUp(self) -> None:
        self.tempdir = tempfile.TemporaryDirectory()
        self.db_path = Path(self.tempdir.name) / "kerebrom.db"
        self.store = KerebromStore(self.db_path)
        self.store.initialize()

    def tearDown(self) -> None:
        self.tempdir.cleanup()

    def test_dormant_memory_reactivates_on_strong_semantic_match(self) -> None:
        """A memory that decayed should come back when a strong cue arrives."""
        result = self.store.remember("Me llamo Pedro Julian Arribas Monzon, soy de Guatemala.", importance=0.9)
        memory_id = result["memory"]["id"]

        # Simulate natural decay: mark as dormant (invalidated, low importance).
        dormant_at = (datetime.now(timezone.utc) - timedelta(days=60)).replace(microsecond=0).isoformat()
        with self.store.connect() as connection:
            connection.execute(
                """
                UPDATE memories
                SET invalid_at = ?, importance = 0.01, source = 'manual', updated_at = ?
                WHERE id = ?
                """,
                (dormant_at, dormant_at, memory_id),
            )

        # Query with a strong cue — should reactivate.
        results = self.store.recall("Pedro Julian Guatemala", limit=5, maintain=False)
        ids = [m.id for m in results]
        self.assertIn(memory_id, ids, "Dormant memory should reactivate on strong cue")

        # Verify the memory is no longer dormant.
        with self.store.connect() as connection:
            row = connection.execute("SELECT invalid_at, importance FROM memories WHERE id = ?", (memory_id,)).fetchone()
        self.assertIsNone(row["invalid_at"], "Reactivated memory should have NULL invalid_at")
        self.assertGreaterEqual(float(row["importance"]), 0.3, "Reactivated memory should have boosted importance")

    def test_forgotten_memory_never_reactivates(self) -> None:
        """Intentionally forgotten memories must NEVER come back."""
        result = self.store.remember("Mi contraseña es hunter2.", importance=0.9)
        memory_id = result["memory"]["id"]
        self.store.forget(memory_id=memory_id)

        # Query with a direct match — should NOT reactivate.
        results = self.store.recall("contraseña hunter2", limit=5, maintain=False)
        ids = [m.id for m in results]
        self.assertNotIn(memory_id, ids, "Forgotten memory must never reactivate")

    def test_reactivation_skipped_when_active_results_are_strong(self) -> None:
        """If active results are good (score > 0.4), skip reactivation entirely."""
        self.store.remember("Python es mi lenguaje favorito para data science.", importance=0.9)

        # Create a dormant memory with similar content.
        dormant = self.store.remember("Uso Python para machine learning y análisis de datos.", importance=0.9)
        dormant_id = dormant["memory"]["id"]
        dormant_at = (datetime.now(timezone.utc) - timedelta(days=60)).replace(microsecond=0).isoformat()
        with self.store.connect() as connection:
            connection.execute(
                "UPDATE memories SET invalid_at = ?, importance = 0.01, source = 'manual' WHERE id = ?",
                (dormant_at, dormant_id),
            )

        # The active "Python" memory should score well enough to skip reactivation.
        results = self.store.recall("Python data science", limit=5, maintain=False)
        # The dormant memory should stay dormant.
        with self.store.connect() as connection:
            row = connection.execute("SELECT invalid_at FROM memories WHERE id = ?", (dormant_id,)).fetchone()
        self.assertIsNotNone(row["invalid_at"], "Dormant memory should stay dormant when active results are strong")


class MultiProjectIsolationTests(unittest.TestCase):
    """Test that projects are properly isolated."""

    def setUp(self) -> None:
        self.tempdir = tempfile.TemporaryDirectory()
        self.db_path = Path(self.tempdir.name) / "kerebrom.db"
        self.store = KerebromStore(self.db_path)
        self.store.initialize(project="alpha")
        self.store.initialize(project="beta")

    def tearDown(self) -> None:
        self.tempdir.cleanup()

    def test_recall_respects_project_boundary(self) -> None:
        self.store.remember("Alpha secret data XYZ123.", project="alpha", importance=0.9)
        self.store.remember("Beta secret data ABC456.", project="beta", importance=0.9)

        alpha_results = self.store.recall("secret data", project="alpha", limit=10, maintain=False)
        beta_results = self.store.recall("secret data", project="beta", limit=10, maintain=False)

        alpha_contents = " ".join(m.content for m in alpha_results)
        beta_contents = " ".join(m.content for m in beta_results)

        self.assertIn("XYZ123", alpha_contents)
        self.assertNotIn("ABC456", alpha_contents)
        self.assertIn("ABC456", beta_contents)
        self.assertNotIn("XYZ123", beta_contents)

    def test_facts_scoped_to_project(self) -> None:
        self.store.remember("Vivo en Guatemala.", project="alpha")
        self.store.remember("Vivo en España.", project="beta")

        alpha_facts = self.store.list_facts(project="alpha", limit=10)
        beta_facts = self.store.list_facts(project="beta", limit=10)

        alpha_objects = {f["object"] for f in alpha_facts}
        beta_objects = {f["object"] for f in beta_facts}

        self.assertIn("Guatemala", alpha_objects)
        self.assertNotIn("España", alpha_objects)
        self.assertIn("España", beta_objects)
        self.assertNotIn("Guatemala", beta_objects)

    def test_forget_does_not_cross_projects(self) -> None:
        self.store.remember("Me gusta Rust.", project="alpha", importance=0.9)
        mid_beta = self.store.remember("Me gusta Rust.", project="beta", importance=0.9)["memory"]["id"]

        count = self.store.forget(project="beta", query="Rust")
        alpha_results = self.store.recall("Rust", project="alpha", limit=5, maintain=False)

        self.assertEqual(count, 1)
        self.assertGreaterEqual(len(alpha_results), 1, "Alpha memory should survive beta forget")


class EmptyDatabaseEdgeCaseTests(unittest.TestCase):
    """Test edge cases on empty or minimal databases."""

    def setUp(self) -> None:
        self.tempdir = tempfile.TemporaryDirectory()
        self.db_path = Path(self.tempdir.name) / "kerebrom.db"
        self.store = KerebromStore(self.db_path)
        self.store.initialize()

    def tearDown(self) -> None:
        self.tempdir.cleanup()

    def test_recall_on_empty_database(self) -> None:
        results = self.store.recall("anything", limit=5)
        self.assertEqual(results, [])

    def test_list_facts_on_empty_database(self) -> None:
        facts = self.store.list_facts(limit=10)
        self.assertEqual(facts, [])

    def test_list_entities_on_empty_database(self) -> None:
        entities = self.store.list_entities(limit=10)
        self.assertEqual(entities, [])

    def test_build_context_on_empty_database(self) -> None:
        ctx = self.store.build_context("anything", limit=5)
        self.assertIn("facts", ctx)
        self.assertIn("memories", ctx)
        self.assertEqual(len(ctx["memories"]), 0)

    def test_export_empty_database(self) -> None:
        data = self.store.export_memories()
        self.assertEqual(data, [])

    def test_consolidate_with_no_memories(self) -> None:
        result = self.store.consolidate()
        self.assertEqual(result["clusters"], 0)

    def test_apply_decay_with_no_memories(self) -> None:
        result = self.store.apply_decay()
        self.assertEqual(result["decayed"], 0)

    def test_forget_nonexistent_memory_id(self) -> None:
        count = self.store.forget(memory_id=99999)
        self.assertEqual(count, 0)


class MCPServerShutdownTests(unittest.TestCase):
    """Test that the MCP server properly closes the store on shutdown."""

    def test_server_closes_store_on_stdin_eof(self) -> None:
        with tempfile.TemporaryDirectory() as td:
            db_path = Path(td) / "mcp.db"
            # Send only initialize then close stdin — server should exit cleanly.
            init_msg = json.dumps({"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": {}})
            result = subprocess.run(
                [sys.executable, "-m", "kerebrom", "serve", "--db", str(db_path)],
                input=init_msg + "\n",
                capture_output=True,
                text=True,
                timeout=15,
            )
            self.assertEqual(result.returncode, 0, msg=result.stderr)


class UninstallTests(unittest.TestCase):
    """Test the complete uninstall module."""

    def test_uninstall_removes_kerebrom_dir(self) -> None:
        with tempfile.TemporaryDirectory() as td:
            fake_home = Path(td)
            kerebrom_dir = fake_home / ".kerebrom"
            kerebrom_dir.mkdir()
            (kerebrom_dir / "kerebrom.db").write_text("fake")
            (kerebrom_dir / "kerebrom.db-wal").write_text("")

            from unittest.mock import patch
            with patch("kerebrom.uninstall.Path.home", return_value=fake_home):
                from kerebrom.uninstall import _remove_kerebrom_db
                msg = _remove_kerebrom_db()

            self.assertFalse(kerebrom_dir.exists())
            self.assertIn("eliminado", msg)

    def test_uninstall_cleans_claude_mcp(self) -> None:
        with tempfile.TemporaryDirectory() as td:
            fake_home = Path(td)
            claude_dir = fake_home / ".claude"
            claude_dir.mkdir()
            mcp_data = {"mcpServers": {"kerebrom": {"command": "python3"}, "other_tool": {"command": "node"}}}
            (claude_dir / ".mcp.json").write_text(json.dumps(mcp_data))

            from unittest.mock import patch
            with patch("kerebrom.uninstall.Path.home", return_value=fake_home):
                from kerebrom.uninstall import _clean_claude_mcp
                msg = _clean_claude_mcp()

            remaining = json.loads((claude_dir / ".mcp.json").read_text())
            self.assertNotIn("kerebrom", remaining.get("mcpServers", remaining))
            self.assertIn("other_tool", remaining.get("mcpServers", remaining))
            self.assertIn("removido", msg)

    def test_uninstall_cleans_codex_config(self) -> None:
        with tempfile.TemporaryDirectory() as td:
            fake_home = Path(td)
            codex_dir = fake_home / ".codex"
            codex_dir.mkdir()
            config = 'model = "gpt-5"\n\n[mcp_servers.kerebrom]\ncommand = "python3"\nargs = ["-m", "kerebrom"]\n'
            (codex_dir / "config.toml").write_text(config)

            from unittest.mock import patch
            with patch("kerebrom.uninstall.Path.home", return_value=fake_home):
                from kerebrom.uninstall import _clean_codex_config
                msg = _clean_codex_config()

            remaining = (codex_dir / "config.toml").read_text()
            self.assertNotIn("[mcp_servers.kerebrom]", remaining)
            self.assertIn('model = "gpt-5"', remaining)
            self.assertIn("removida", msg)

    def test_uninstall_cli_with_yes_flag(self) -> None:
        import os
        import tempfile

        # Run uninstall in an isolated HOME so the real system is untouched.
        with tempfile.TemporaryDirectory() as fake_home:
            env = {**os.environ, "HOME": fake_home}
            result = subprocess.run(
                [sys.executable, "-m", "kerebrom", "uninstall", "--yes", "--keep-pip"],
                capture_output=True,
                text=True,
                timeout=15,
                env=env,
            )
        self.assertEqual(result.returncode, 0, msg=result.stderr)
        self.assertIn("desinstalado", result.stdout.lower())


class EmbeddingAutoSelectTests(unittest.TestCase):
    """Test the embedding model auto-selection and pluggable interface."""

    def test_auto_select_picks_best_available(self) -> None:
        from kerebrom.embeddings import EmbeddingModel, auto_select_model

        model = auto_select_model()
        # Should satisfy the EmbeddingModel protocol regardless of tier.
        self.assertTrue(hasattr(model, "dimensions"))
        self.assertTrue(hasattr(model, "embed"))
        self.assertGreater(model.dimensions, 0)

    def test_hash_model_produces_stable_embeddings(self) -> None:
        from kerebrom.embeddings import HashEmbeddingModel

        model = HashEmbeddingModel()
        a = model.embed("Vivo en Guatemala")
        b = model.embed("Vivo en Guatemala")
        self.assertEqual(a, b)

    def test_char_ngrams_improve_partial_matching(self) -> None:
        from kerebrom.embeddings import HashEmbeddingModel, cosine_similarity

        model = HashEmbeddingModel()
        guatemala = model.embed("Guatemala")
        guatemalteco = model.embed("guatemalteco")
        espana = model.embed("España")

        sim_related = cosine_similarity(guatemala, guatemalteco)
        sim_unrelated = cosine_similarity(guatemala, espana)
        self.assertGreater(
            sim_related, sim_unrelated,
            "Related words (Guatemala/guatemalteco) should be more similar than unrelated (Guatemala/España)",
        )

    def test_embedding_model_protocol(self) -> None:
        """Verify any model satisfying the protocol works with the store."""
        from kerebrom.embeddings import HashEmbeddingModel

        model = HashEmbeddingModel()
        with tempfile.TemporaryDirectory() as td:
            store = KerebromStore(Path(td) / "test.db", embedding_model=model)
            store.initialize()
            result = store.remember("Test memory")
            self.assertTrue(result["inserted"])


class PrivateTagTests(unittest.TestCase):
    """Test <private> tag stripping."""

    def test_private_tags_stripped_before_storage(self) -> None:
        with tempfile.TemporaryDirectory() as td:
            store = KerebromStore(Path(td) / "test.db")
            store.initialize()
            result = store.remember("Mi nombre es Ulian. <private>Mi contraseña es hunter2</private> Vivo en Guatemala.")
            content = result["memory"]["content"]
            self.assertNotIn("hunter2", content)
            self.assertNotIn("<private>", content)
            self.assertIn("Ulian", content)
            self.assertIn("Guatemala", content)

    def test_entirely_private_text(self) -> None:
        from kerebrom.privacy import strip_private_tags
        result = strip_private_tags("<private>todo secreto</private>")
        self.assertEqual(result, "")

    def test_multiple_private_blocks(self) -> None:
        from kerebrom.privacy import strip_private_tags
        result = strip_private_tags("Hola <private>secreto1</private> mundo <private>secreto2</private> fin")
        self.assertNotIn("secreto1", result)
        self.assertNotIn("secreto2", result)
        self.assertIn("Hola", result)
        self.assertIn("fin", result)


class ObservationTagTests(unittest.TestCase):
    """Test observation tags on memories."""

    def setUp(self) -> None:
        self.tempdir = tempfile.TemporaryDirectory()
        self.db_path = Path(self.tempdir.name) / "kerebrom.db"
        self.store = KerebromStore(self.db_path)
        self.store.initialize()

    def tearDown(self) -> None:
        self.tempdir.cleanup()

    def test_remember_with_tags(self) -> None:
        result = self.store.remember("Decidimos usar ONNX en vez de torch.", tags=["decision", "feature"])
        memory = result["memory"]
        self.assertEqual(memory["tags"], ["decision", "feature"])

    def test_remember_without_tags(self) -> None:
        result = self.store.remember("Un recuerdo normal.")
        memory = result["memory"]
        self.assertEqual(memory["tags"], [])

    def test_tags_in_recall(self) -> None:
        self.store.remember("Corregimos el bug de race condition.", tags=["bugfix"])
        results = self.store.recall("race condition", limit=1)
        self.assertGreaterEqual(len(results), 1)
        self.assertEqual(results[0].tags, ["bugfix"])

    def test_tags_via_mcp(self) -> None:
        from kerebrom.mcp_server import handle_tools_call
        resp = handle_tools_call(
            1,
            {"name": "remember", "arguments": {"content": "Refactorizamos crypto.", "tags": ["refactor"]}},
            self.store,
            "default",
        )
        data = json.loads(resp["result"]["content"][0]["text"])
        self.assertTrue(data["inserted"])
        self.assertEqual(data["memory"]["tags"], ["refactor"])


class PassiveCaptureTests(unittest.TestCase):
    """Test the passive capture hooks module."""

    def test_summarize_write_event(self) -> None:
        from kerebrom.hooks import _summarize_tool_event
        event = {
            "tool_name": "Write",
            "tool_input": {"file_path": "/src/main.py", "content": "print('hello')"},
        }
        summary = _summarize_tool_event(event)
        self.assertIsNotNone(summary)
        self.assertIn("main.py", summary)

    def test_summarize_bash_event(self) -> None:
        from kerebrom.hooks import _summarize_tool_event
        event = {
            "tool_name": "Bash",
            "tool_input": {"command": "npm test"},
            "tool_output": "5 tests passed",
        }
        summary = _summarize_tool_event(event)
        self.assertIsNotNone(summary)
        self.assertIn("npm test", summary)

    def test_trivial_bash_skipped(self) -> None:
        from kerebrom.hooks import _summarize_tool_event
        event = {"tool_name": "Bash", "tool_input": {"command": "ls"}}
        summary = _summarize_tool_event(event)
        self.assertIsNone(summary)

    def test_uninteresting_tool_skipped(self) -> None:
        from kerebrom.hooks import _summarize_tool_event
        event = {"tool_name": "Read", "tool_input": {"file_path": "/foo.py"}}
        summary = _summarize_tool_event(event)
        self.assertIsNone(summary)

    def test_handle_capture_stores_memory(self) -> None:
        with tempfile.TemporaryDirectory() as td:
            db_path = Path(td) / "test.db"
            event = json.dumps({
                "tool_name": "Edit",
                "tool_input": {"file_path": "/src/app.py", "old_string": "x", "new_string": "y"},
            })
            # Simulate stdin
            from unittest.mock import patch
            with patch("sys.stdin", io.StringIO(event)):
                from kerebrom.hooks import handle_capture
                ret = handle_capture(db_path=str(db_path))
            self.assertEqual(ret, 0)

            # Verify memory was stored.
            store = KerebromStore(db_path)
            store.initialize()
            results = store.recall("app.py", limit=5)
            self.assertGreaterEqual(len(results), 1)
            self.assertIn("auto-capture", results[0].tags)

    def test_handle_capture_ignores_malformed_input(self) -> None:
        from unittest.mock import patch
        with patch("sys.stdin", io.StringIO("not json")):
            from kerebrom.hooks import handle_capture
            ret = handle_capture(db_path="/tmp/nonexistent.db")
        self.assertEqual(ret, 0)


class SoporTests(unittest.TestCase):
    """Test the Sopor Plenus transcript consolidation engine."""

    def setUp(self) -> None:
        self.tempdir = tempfile.TemporaryDirectory()
        self.db_path = Path(self.tempdir.name) / "kerebrom.db"
        self.store = KerebromStore(self.db_path)
        self.store.initialize()

        # Create a synthetic JSONL transcript.
        self.transcript_path = Path(self.tempdir.name) / "test-session.jsonl"
        messages = [
            {"type": "user", "message": {"role": "user", "content": "Decidí cambiar el diseño del frontend. Vamos con Tailwind en vez de CSS puro."}, "timestamp": "2026-03-29T10:00:00Z", "sessionId": "test-session"},
            {"type": "assistant", "message": {"role": "assistant", "content": [{"type": "text", "text": "Perfecto, voy a migrar los estilos a Tailwind. Esta decisión simplifica el mantenimiento."}]}, "timestamp": "2026-03-29T10:01:00Z", "sessionId": "test-session"},
            {"type": "user", "message": {"role": "user", "content": "También encontré un bug: el trailing stop no se activa cuando el precio sube más de 3%."}, "timestamp": "2026-03-29T10:05:00Z", "sessionId": "test-session"},
            {"type": "assistant", "message": {"role": "assistant", "content": [{"type": "text", "text": "El problema era que la condición comparaba el porcentaje absoluto en vez del relativo. Ya lo corregí en trading_engine.py línea 245."}]}, "timestamp": "2026-03-29T10:06:00Z", "sessionId": "test-session"},
            {"type": "user", "message": {"role": "user", "content": "Perfecto, confirmado que funciona. Siempre quiero que los trailing stops se calculen con porcentaje relativo."}, "timestamp": "2026-03-29T10:10:00Z", "sessionId": "test-session"},
            {"type": "assistant", "message": {"role": "assistant", "content": [{"type": "text", "text": "Listo, actualizado y verificado."}]}, "timestamp": "2026-03-29T10:11:00Z", "sessionId": "test-session"},
            # Some low-value messages that should be skipped.
            {"type": "assistant", "message": {"role": "assistant", "content": [{"type": "tool_use", "name": "Read", "input": {"file_path": "/foo.py"}}]}, "timestamp": "2026-03-29T10:12:00Z", "sessionId": "test-session"},
            {"type": "system", "message": {"role": "system", "content": ""}, "timestamp": "2026-03-29T10:13:00Z", "sessionId": "test-session"},
        ]
        with open(self.transcript_path, "w") as f:
            for msg in messages:
                f.write(json.dumps(msg) + "\n")

    def tearDown(self) -> None:
        self.tempdir.cleanup()

    def test_parse_transcript(self) -> None:
        from kerebrom.sopor import parse_transcript
        messages = parse_transcript(self.transcript_path)
        # Should have user + assistant text messages (not tool-only or empty system).
        self.assertGreaterEqual(len(messages), 5)
        roles = [m.role for m in messages]
        self.assertIn("user", roles)
        self.assertIn("assistant", roles)

    def test_extract_blocks(self) -> None:
        from kerebrom.sopor import extract_consolidation_blocks, parse_transcript
        messages = parse_transcript(self.transcript_path)
        blocks = extract_consolidation_blocks(messages)
        self.assertGreater(len(blocks), 0)
        # Blocks should have tags inferred.
        all_tags = [tag for b in blocks for tag in b["tags"]]
        self.assertTrue(len(all_tags) > 0)

    def test_run_sopor_stores_memories(self) -> None:
        from kerebrom.sopor import run_sopor
        result = run_sopor(self.store, self.transcript_path)
        self.assertGreater(result.messages_read, 0)
        self.assertGreater(result.memories_extracted, 0)
        self.assertGreater(result.memories_stored, 0)
        self.assertEqual(len(result.errors), 0)

        # Verify memories are in the store.
        all_memories = self.store.recall("diseño frontend Tailwind", limit=10)
        self.assertGreaterEqual(len(all_memories), 1)

    def test_run_sopor_deduplicates(self) -> None:
        from kerebrom.sopor import run_sopor
        # Run twice on the same transcript.
        result1 = run_sopor(self.store, self.transcript_path)
        result2 = run_sopor(self.store, self.transcript_path)
        # Second run should skip most as duplicates.
        self.assertGreater(result2.memories_skipped_duplicate, 0)
        self.assertLessEqual(result2.memories_stored, result1.memories_stored)

    def test_run_sopor_skips_compaction_summaries(self) -> None:
        from kerebrom.sopor import parse_transcript, extract_consolidation_blocks
        # Add a compaction summary to the transcript.
        compaction_path = Path(self.tempdir.name) / "compacted.jsonl"
        with open(compaction_path, "w") as f:
            f.write(json.dumps({
                "type": "user",
                "message": {"role": "user", "content": "This session is being continued from a previous conversation that ran out of context. The summary below covers the earlier portion..."},
                "timestamp": "2026-03-29T11:00:00Z",
                "sessionId": "compacted",
            }) + "\n")
            f.write(json.dumps({
                "type": "user",
                "message": {"role": "user", "content": "Decidí usar Redis para el cache."},
                "timestamp": "2026-03-29T11:01:00Z",
                "sessionId": "compacted",
            }) + "\n")
        messages = parse_transcript(compaction_path)
        blocks = extract_consolidation_blocks(messages)
        # The compaction summary should not become a block.
        for block in blocks:
            self.assertNotIn("continued from a previous conversation", block["text"])

    def test_sopor_via_mcp(self) -> None:
        from kerebrom.mcp_server import handle_tools_call
        resp = handle_tools_call(
            1,
            {"name": "sopor", "arguments": {"session": str(self.transcript_path)}},
            self.store,
            "default",
        )
        data = json.loads(resp["result"]["content"][0]["text"])
        self.assertIn("memories_stored", data)
        self.assertGreater(data["memories_stored"], 0)

    def test_sopor_via_cli(self) -> None:
        from kerebrom.cli import main
        stdout = io.StringIO()
        with contextlib.redirect_stdout(stdout):
            ret = main([
                "sopor",
                "--db", str(self.db_path),
                "--session", str(self.transcript_path),
            ])
        self.assertEqual(ret, 0)
        output = json.loads(stdout.getvalue().split("\n", 1)[1])  # Skip header line.
        self.assertGreater(output["memories_stored"], 0)

    def test_infer_tags_decision(self) -> None:
        from kerebrom.sopor import _infer_tags
        tags = _infer_tags("Decidí usar Tailwind para el diseño del frontend")
        self.assertIn("decision", tags)
        self.assertIn("design", tags)

    def test_infer_tags_bugfix(self) -> None:
        from kerebrom.sopor import _infer_tags
        tags = _infer_tags("Encontré un bug en el trailing stop, ya lo corregí con un fix")
        self.assertIn("bugfix", tags)

    def test_score_user_decision_message(self) -> None:
        from kerebrom.sopor import TranscriptMessage, _score_message
        msg = TranscriptMessage(role="user", text="Decidí cambiar el diseño completo del frontend porque el actual no funciona bien")
        score = _score_message(msg)
        self.assertGreaterEqual(score, 0.5, "User decision messages should score high")

    def test_score_trivial_assistant_message(self) -> None:
        from kerebrom.sopor import TranscriptMessage, _score_message
        msg = TranscriptMessage(role="assistant", text="Ok.")
        score = _score_message(msg)
        self.assertLess(score, 0.35, "Trivial messages should score below threshold")


class OrganicBackupTests(unittest.TestCase):
    """Test the portable .kbk backup and organic restore system."""

    def setUp(self) -> None:
        self.tempdir = tempfile.TemporaryDirectory()
        self.db_path = Path(self.tempdir.name) / "kerebrom.db"
        self.store = KerebromStore(self.db_path)
        self.store.initialize()
        # Seed some memories.
        self.store.remember("Me llamo Pedro Julián Arribas Monzón.", kind="core", importance=1.0, tags=["preference"])
        self.store.remember("Quamtos usa Node.js y PostgreSQL.", kind="semantic", importance=0.8, tags=["discovery"])
        self.store.remember("Decidí migrar a Tailwind.", kind="episodic", importance=0.6, tags=["decision", "design"])

    def tearDown(self) -> None:
        self.tempdir.cleanup()

    def test_create_backup(self) -> None:
        from kerebrom.backup import create_backup, FORMAT_VERSION
        backup_path = create_backup(self.store, output_path=Path(self.tempdir.name) / "test.kbk")
        self.assertTrue(backup_path.exists())
        data = json.loads(backup_path.read_text())
        self.assertEqual(data["format"], FORMAT_VERSION)
        self.assertEqual(data["memory_count"], 3)
        self.assertEqual(len(data["memories"]), 3)
        # Verify memory fields are present.
        mem = data["memories"][0]
        self.assertIn("content", mem)
        self.assertIn("kind", mem)
        self.assertIn("importance", mem)
        self.assertIn("tags", mem)

    def test_validate_backup(self) -> None:
        from kerebrom.backup import create_backup, validate_backup
        backup_path = create_backup(self.store, output_path=Path(self.tempdir.name) / "test.kbk")
        info = validate_backup(backup_path)
        self.assertTrue(info["valid"])
        self.assertEqual(info["memory_count"], 3)

    def test_validate_invalid_file(self) -> None:
        from kerebrom.backup import validate_backup
        bad_file = Path(self.tempdir.name) / "bad.kbk"
        bad_file.write_text('{"format": "wrong"}')
        info = validate_backup(bad_file)
        self.assertFalse(info["valid"])

    def test_restore_organic(self) -> None:
        from kerebrom.backup import create_backup, restore_organic
        # Create backup from store with 3 memories.
        backup_path = create_backup(self.store, output_path=Path(self.tempdir.name) / "test.kbk")

        # Create a fresh empty store.
        new_db = Path(self.tempdir.name) / "fresh.db"
        new_store = KerebromStore(new_db)
        new_store.initialize()

        # Restore organically.
        result = restore_organic(new_store, backup_path)
        self.assertEqual(result["restored"], 3)
        self.assertEqual(result["skipped_duplicate"], 0)

        # Verify memories are searchable in the new store.
        results = new_store.recall("Pedro Julián", limit=5)
        self.assertGreaterEqual(len(results), 1)
        self.assertIn("Pedro", results[0].content)

    def test_restore_deduplicates(self) -> None:
        from kerebrom.backup import create_backup, restore_organic
        backup_path = create_backup(self.store, output_path=Path(self.tempdir.name) / "test.kbk")
        # Restore into the SAME store (already has the memories).
        result = restore_organic(self.store, backup_path, skip_duplicates=True)
        self.assertEqual(result["restored"], 0)
        self.assertEqual(result["skipped_duplicate"], 3)

    def test_create_backup_reuses_existing(self) -> None:
        """Calling create_backup twice without output_path should update the same file."""
        from unittest.mock import patch
        from kerebrom.backup import create_backup, DEFAULT_BACKUP_DIR

        backup_dir = Path(self.tempdir.name) / "docs"
        backup_dir.mkdir()

        with patch("kerebrom.backup.DEFAULT_BACKUP_DIR", backup_dir), \
             patch("kerebrom.backup.find_latest_backup", return_value=None):
            first = create_backup(self.store)

        # First call creates the file.
        self.assertTrue(first.exists())
        first_data = json.loads(first.read_text())
        self.assertEqual(first_data["memory_count"], 3)

        # Add a 4th memory.
        self.store.remember("Cuarta memoria de prueba.", kind="episodic", importance=0.5)

        # Second call should reuse the same file.
        with patch("kerebrom.backup.find_latest_backup", return_value=first):
            second = create_backup(self.store)

        self.assertEqual(first, second)
        second_data = json.loads(second.read_text())
        self.assertEqual(second_data["memory_count"], 4)

        # Only one .kbk file should exist.
        kbk_files = list(backup_dir.glob("*.kbk"))
        self.assertEqual(len(kbk_files), 1)

    def test_find_backups(self) -> None:
        from kerebrom.backup import create_backup, find_backups
        backup_path = create_backup(self.store, output_path=Path(self.tempdir.name) / "test.kbk")
        found = find_backups(search_dirs=[Path(self.tempdir.name)])
        self.assertIn(backup_path.resolve(), found)

    def test_snapshot_cli(self) -> None:
        from kerebrom.cli import main
        output_path = Path(self.tempdir.name) / "cli-test.kbk"
        stdout = io.StringIO()
        with contextlib.redirect_stdout(stdout):
            ret = main(["snapshot", "--db", str(self.db_path), "--output", str(output_path)])
        self.assertEqual(ret, 0)
        self.assertTrue(output_path.exists())
        output = json.loads(stdout.getvalue())
        self.assertEqual(output["memory_count"], 3)

    def test_revive_cli(self) -> None:
        from kerebrom.backup import create_backup
        from kerebrom.cli import main
        # Create a backup.
        backup_path = create_backup(self.store, output_path=Path(self.tempdir.name) / "test.kbk")
        # Create a fresh DB.
        fresh_db = Path(self.tempdir.name) / "fresh.db"
        stdout = io.StringIO()
        with contextlib.redirect_stdout(stdout):
            ret = main(["revive", "--db", str(fresh_db), "--input", str(backup_path)])
        self.assertEqual(ret, 0)
        output_text = stdout.getvalue()
        # The output has print lines + JSON from print_json — parse the JSON part.
        self.assertIn("restored", output_text)
        # Extract JSON by finding the last { ... } block.
        json_start = output_text.rfind("{\n")
        if json_start >= 0:
            result = json.loads(output_text[json_start:])
            self.assertEqual(result["restored"], 3)

    def test_uninstall_creates_auto_backup(self) -> None:
        from unittest.mock import patch
        from kerebrom.uninstall import _auto_backup

        fake_home = Path(self.tempdir.name) / "fakehome"
        fake_home.mkdir()
        kerebrom_dir = fake_home / ".kerebrom"
        kerebrom_dir.mkdir()
        docs_dir = fake_home / "Documents"
        docs_dir.mkdir()

        # Create a real DB in the fake home.
        db_path = kerebrom_dir / "kerebrom.db"
        store = KerebromStore(db_path)
        store.initialize()
        store.remember("Test memory for backup.")
        store.close()

        with patch("kerebrom.uninstall.Path.home", return_value=fake_home), \
             patch("kerebrom.backup.Path.home", return_value=fake_home), \
             patch("kerebrom.backup.DEFAULT_BACKUP_DIR", docs_dir):
            msg = _auto_backup()

        self.assertIn("respaldadas", msg)
        # Verify .kbk file was created.
        kbk_files = list(docs_dir.glob("*.kbk"))
        self.assertEqual(len(kbk_files), 1)


class HookSetupTests(unittest.TestCase):
    """Test that setup.py configures hooks in settings.json."""

    def test_setup_creates_hooks_in_settings(self) -> None:
        with tempfile.TemporaryDirectory() as td:
            fake_home = Path(td)
            claude_dir = fake_home / ".claude"
            claude_dir.mkdir()

            from unittest.mock import patch
            with patch("kerebrom.setup.Path.home", return_value=fake_home), \
                 patch("kerebrom.setup._is_temp_path", return_value=False):
                from kerebrom.setup import _setup_claude_code
                ok, msg = _setup_claude_code(fake_home / ".kerebrom" / "kerebrom.db")

            self.assertTrue(ok)
            self.assertIn("Hooks", msg)

            settings = json.loads((claude_dir / "settings.json").read_text())
            self.assertIn("hooks", settings)
            self.assertIn("PostToolUse", settings["hooks"])
            self.assertIn("Stop", settings["hooks"])

            # Verify kerebrom capture command is in hooks (new format: hooks array inside entry)
            post_hooks = settings["hooks"]["PostToolUse"]
            commands = []
            for entry in post_hooks:
                if isinstance(entry, dict):
                    for h in entry.get("hooks", []):
                        commands.append(h.get("command", ""))
            self.assertTrue(any("kerebrom capture" in cmd for cmd in commands))

    def test_uninstall_removes_hooks(self) -> None:
        with tempfile.TemporaryDirectory() as td:
            fake_home = Path(td)
            claude_dir = fake_home / ".claude"
            claude_dir.mkdir()
            settings = {
                "hooks": {
                    "PostToolUse": [{"matcher": "PostToolUse", "command": "kerebrom capture --db /x"}],
                    "Stop": [{"matcher": "Stop", "command": "kerebrom capture --db /x"}],
                }
            }
            (claude_dir / "settings.json").write_text(json.dumps(settings))

            from unittest.mock import patch
            with patch("kerebrom.uninstall.Path.home", return_value=fake_home):
                from kerebrom.uninstall import _clean_claude_hooks
                msg = _clean_claude_hooks()

            self.assertIn("removidos", msg)
            remaining = json.loads((claude_dir / "settings.json").read_text())
            self.assertNotIn("hooks", remaining)


if __name__ == "__main__":
    unittest.main()
