# Copyright (c) 2026 Ulian Bass. All rights reserved.
# This software is proprietary. See LICENSE for terms.

"""SQLite-backed Kerebrom core engine."""

from __future__ import annotations

import atexit
import fcntl
import json
import math
import os
import sqlite3
import sys
import tempfile
from contextlib import contextmanager
from dataclasses import dataclass
from datetime import datetime, timezone
from pathlib import Path
from typing import Any, Dict, Iterable, List, Optional, Sequence

from .capture import (
    canonicalize_entity,
    extract_entities,
    extract_relation_candidates,
)
from .crypto import decrypt_database_file, encrypt_database_file, is_encrypted_container
from .embeddings import EmbeddingModel, auto_select_model, average_embedding, cosine_similarity, tokenize
from .privacy import scrub_sensitive

DEFAULT_PROJECT = "default"
DEFAULT_USER_ENTITY = "user"
EXCLUSIVE_PREDICATES = {"name", "lives_in", "from", "works_at", "role"}

# ── Auto-maintenance constants ───────────────────────────────────────
# How often automatic decay/consolidation runs (in seconds).
_AUTO_MAINTENANCE_INTERVAL_SECS = 86400  # once per day
# Decay half-life per memory kind (in days).
_HALF_LIFE_BY_KIND = {
    "core": 365 * 100,       # core memories: virtually immortal (100 years)
    "semantic": 365.0,       # consolidated knowledge: 1 year
    "procedural": 180.0,     # how-to knowledge: 6 months
    "episodic": 30.0,        # episodes: 30 days (default)
}
# Minimum similarity to reactivate a dormant (invalidated) memory.
_REACTIVATION_THRESHOLD = 0.55
# Importance floor below which memories become dormant.
_DORMANT_IMPORTANCE = 0.05


def utc_now() -> str:
    return datetime.now(timezone.utc).replace(microsecond=0).isoformat()


def normalize_score(value: float, lower: float = 0.0, upper: float = 1.0) -> float:
    return max(lower, min(upper, value))


def build_fts_query(text: str) -> Optional[str]:
    tokens = tokenize(text)
    if not tokens:
        return None
    return " OR ".join('"{}"'.format(token.replace('"', "")) for token in tokens[:8])


@dataclass
class MemoryRecord:
    id: int
    project: str
    content: str
    raw_content: str
    kind: str
    source: str
    importance: float
    confidence: float
    access_count: int
    duplicate_count: int
    is_redacted: bool
    pii_hits: List[str]
    tags: List[str]
    metadata: Dict[str, Any]
    created_at: str
    updated_at: str
    valid_at: str
    invalid_at: Optional[str]
    score: float = 0.0
    semantic_similarity: float = 0.0
    graph_score: float = 0.0
    keyword_score: float = 0.0
    fusion_score: float = 0.0

    @classmethod
    def from_row(cls, row: sqlite3.Row) -> "MemoryRecord":
        return cls(
            id=int(row["id"]),
            project=row["project"],
            content=row["content"],
            raw_content=row["raw_content"],
            kind=row["kind"],
            source=row["source"],
            importance=float(row["importance"]),
            confidence=float(row["confidence"]),
            access_count=int(row["access_count"]),
            duplicate_count=int(row["duplicate_count"]),
            is_redacted=bool(row["is_redacted"]),
            pii_hits=json.loads(row["pii_hits"]),
            tags=json.loads(row["tags"]) if row["tags"] else [],
            metadata=json.loads(row["metadata"]) if row["metadata"] else {},
            created_at=row["created_at"],
            updated_at=row["updated_at"],
            valid_at=row["valid_at"],
            invalid_at=row["invalid_at"],
        )

    def to_dict(self) -> Dict[str, Any]:
        return {
            "id": self.id,
            "project": self.project,
            "content": self.content,
            "raw_content": self.raw_content,
            "kind": self.kind,
            "source": self.source,
            "importance": self.importance,
            "confidence": self.confidence,
            "access_count": self.access_count,
            "duplicate_count": self.duplicate_count,
            "is_redacted": self.is_redacted,
            "pii_hits": self.pii_hits,
            "tags": self.tags,
            "metadata": self.metadata,
            "created_at": self.created_at,
            "updated_at": self.updated_at,
            "valid_at": self.valid_at,
            "invalid_at": self.invalid_at,
            "score": round(self.score, 4),
            "semantic_similarity": round(self.semantic_similarity, 4),
            "graph_score": round(self.graph_score, 4),
            "keyword_score": round(self.keyword_score, 4),
            "fusion_score": round(self.fusion_score, 4),
        }


class KerebromStore:
    def __init__(
        self,
        db_path: str | Path,
        embedding_model: Optional[EmbeddingModel] = None,
        passphrase: Optional[str] = None,
    ) -> None:
        self.db_path = str(Path(db_path).expanduser())
        self.embedding_model = embedding_model or auto_select_model()
        self.passphrase = passphrase
        self._runtime_dir: Optional[Path] = None
        self._runtime_db_path: Optional[Path] = None
        self._lock_handle: Optional[Any] = None
        self._cleanup_registered = False

    @contextmanager
    def connect(self) -> Iterable[sqlite3.Connection]:
        sqlite_path = self._prepare_sqlite_path()
        connection = sqlite3.connect(str(sqlite_path), timeout=30.0)
        connection.row_factory = sqlite3.Row
        connection.execute("PRAGMA foreign_keys = ON")
        connection.execute("PRAGMA journal_mode = WAL")
        try:
            yield connection
            connection.commit()
        except Exception:
            connection.rollback()
            raise
        finally:
            connection.close()
            if self.passphrase:
                self._sync_encrypted_runtime()

    def initialize(self, project: str = DEFAULT_PROJECT, description: str = "") -> None:
        Path(self.db_path).parent.mkdir(parents=True, exist_ok=True)
        with self.connect() as connection:
            connection.executescript(
                """
                CREATE TABLE IF NOT EXISTS projects (
                    id INTEGER PRIMARY KEY AUTOINCREMENT,
                    name TEXT NOT NULL UNIQUE,
                    description TEXT NOT NULL DEFAULT '',
                    created_at TEXT NOT NULL
                );

                CREATE TABLE IF NOT EXISTS memories (
                    id INTEGER PRIMARY KEY AUTOINCREMENT,
                    project TEXT NOT NULL,
                    content TEXT NOT NULL,
                    raw_content TEXT NOT NULL,
                    kind TEXT NOT NULL,
                    source TEXT NOT NULL,
                    importance REAL NOT NULL DEFAULT 0.5,
                    confidence REAL NOT NULL DEFAULT 0.8,
                    access_count INTEGER NOT NULL DEFAULT 0,
                    duplicate_count INTEGER NOT NULL DEFAULT 0,
                    is_redacted INTEGER NOT NULL DEFAULT 0,
                    pii_hits TEXT NOT NULL DEFAULT '[]',
                    embedding TEXT NOT NULL,
                    created_at TEXT NOT NULL,
                    updated_at TEXT NOT NULL,
                    last_accessed_at TEXT,
                    last_decay_at TEXT,
                    valid_at TEXT NOT NULL,
                    invalid_at TEXT,
                    consolidated_at TEXT,
                    tags TEXT NOT NULL DEFAULT '[]',
                    metadata TEXT NOT NULL DEFAULT '{}'
                );

                CREATE VIRTUAL TABLE IF NOT EXISTS memories_fts
                USING fts5(
                    content,
                    content='memories',
                    content_rowid='id',
                    tokenize='unicode61 remove_diacritics 2'
                );

                CREATE TABLE IF NOT EXISTS entities (
                    id INTEGER PRIMARY KEY AUTOINCREMENT,
                    project TEXT NOT NULL,
                    name TEXT NOT NULL,
                    canonical_name TEXT NOT NULL,
                    entity_type TEXT NOT NULL DEFAULT 'concept',
                    created_at TEXT NOT NULL,
                    updated_at TEXT NOT NULL,
                    UNIQUE(project, canonical_name)
                );

                CREATE TABLE IF NOT EXISTS memory_entities (
                    memory_id INTEGER NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
                    entity_id INTEGER NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
                    role TEXT NOT NULL DEFAULT 'mention',
                    PRIMARY KEY(memory_id, entity_id)
                );

                CREATE TABLE IF NOT EXISTS relations (
                    id INTEGER PRIMARY KEY AUTOINCREMENT,
                    project TEXT NOT NULL,
                    subject_entity_id INTEGER NOT NULL REFERENCES entities(id),
                    predicate TEXT NOT NULL,
                    object_entity_id INTEGER NOT NULL REFERENCES entities(id),
                    confidence REAL NOT NULL DEFAULT 0.7,
                    source_memory_id INTEGER NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
                    valid_at TEXT NOT NULL,
                    invalid_at TEXT,
                    created_at TEXT NOT NULL,
                    updated_at TEXT NOT NULL
                );

                CREATE TABLE IF NOT EXISTS relation_supports (
                    id INTEGER PRIMARY KEY AUTOINCREMENT,
                    project TEXT NOT NULL,
                    subject_entity_id INTEGER NOT NULL REFERENCES entities(id),
                    predicate TEXT NOT NULL,
                    object_entity_id INTEGER NOT NULL REFERENCES entities(id),
                    confidence REAL NOT NULL DEFAULT 0.7,
                    source_memory_id INTEGER NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
                    created_at TEXT NOT NULL,
                    updated_at TEXT NOT NULL,
                    UNIQUE(project, subject_entity_id, predicate, object_entity_id, source_memory_id)
                );

                CREATE INDEX IF NOT EXISTS idx_memories_project_valid
                ON memories(project, invalid_at, created_at DESC);

                CREATE INDEX IF NOT EXISTS idx_entities_project_name
                ON entities(project, canonical_name);

                CREATE INDEX IF NOT EXISTS idx_relations_project_predicate
                ON relations(project, predicate, invalid_at);

                CREATE INDEX IF NOT EXISTS idx_memory_entities_entity
                ON memory_entities(entity_id, memory_id);

                CREATE INDEX IF NOT EXISTS idx_relation_supports_subject_predicate
                ON relation_supports(project, subject_entity_id, predicate);

                CREATE INDEX IF NOT EXISTS idx_relation_supports_source_memory
                ON relation_supports(source_memory_id);

                CREATE TRIGGER IF NOT EXISTS memories_ai AFTER INSERT ON memories BEGIN
                    INSERT INTO memories_fts(rowid, content) VALUES (new.id, new.content);
                END;

                CREATE TRIGGER IF NOT EXISTS memories_ad AFTER DELETE ON memories BEGIN
                    INSERT INTO memories_fts(memories_fts, rowid, content) VALUES('delete', old.id, old.content);
                END;

                CREATE TRIGGER IF NOT EXISTS memories_au AFTER UPDATE ON memories BEGIN
                    INSERT INTO memories_fts(memories_fts, rowid, content) VALUES('delete', old.id, old.content);
                    INSERT INTO memories_fts(rowid, content) VALUES (new.id, new.content);
                END;
                """
            )
            self._ensure_column(connection, "memories", "last_decay_at", "TEXT")
            self._ensure_column(connection, "memories", "consolidated_at", "TEXT")
            self._ensure_column(connection, "memories", "tags", "TEXT NOT NULL DEFAULT '[]'")
            # Migración: columna metadata para datos adicionales en formato JSON.
            self._ensure_column(connection, "memories", "metadata", "TEXT NOT NULL DEFAULT '{}'")

            # Tabla de referencias inversas entidad→memoria (grafo bidireccional).
            connection.executescript("""
                CREATE TABLE IF NOT EXISTS entity_references (
                    entity_id INTEGER NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
                    memory_id INTEGER NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
                    direction TEXT NOT NULL DEFAULT 'mention',
                    created_at TEXT NOT NULL,
                    PRIMARY KEY(entity_id, memory_id, direction)
                );
                CREATE INDEX IF NOT EXISTS idx_entity_refs_memory
                ON entity_references(memory_id);
            """)

            # Tabla de referencias no resueltas (huecos de conocimiento).
            connection.executescript("""
                CREATE TABLE IF NOT EXISTS unresolved_references (
                    id INTEGER PRIMARY KEY AUTOINCREMENT,
                    project TEXT NOT NULL,
                    reference_text TEXT NOT NULL,
                    source_memory_id INTEGER NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
                    created_at TEXT NOT NULL,
                    resolved_at TEXT,
                    resolved_entity_id INTEGER REFERENCES entities(id),
                    UNIQUE(project, reference_text, source_memory_id)
                );
            """)

            # Metadata table for auto-maintenance scheduling.
            connection.execute("""
                CREATE TABLE IF NOT EXISTS maintenance_log (
                    project TEXT NOT NULL,
                    task TEXT NOT NULL,
                    last_run_at TEXT NOT NULL,
                    PRIMARY KEY(project, task)
                )
            """)
            connection.execute(
                """
                INSERT INTO projects(name, description, created_at)
                VALUES (?, ?, ?)
                ON CONFLICT(name) DO UPDATE SET
                    description = CASE
                        WHEN excluded.description != '' THEN excluded.description
                        ELSE projects.description
                    END
                """,
                (project, description, utc_now()),
            )
            self._ensure_entity(connection, project, DEFAULT_USER_ENTITY, "person")

    def _container_path(self) -> Path:
        return Path(self.db_path).expanduser().resolve()

    def _lock_path(self) -> Path:
        return Path(str(self._container_path()) + ".lock")

    def _prepare_sqlite_path(self) -> Path:
        container_path = self._container_path()
        if not self.passphrase:
            if container_path.exists() and is_encrypted_container(container_path):
                raise ValueError(
                    "Database is encrypted at rest. Provide a passphrase via --passphrase-env or --passphrase-file."
                )
            return container_path

        if self._runtime_db_path is not None:
            return self._runtime_db_path

        self._acquire_runtime_lock()
        runtime_root = Path(tempfile.mkdtemp(prefix="kerebrom-runtime-"))
        runtime_db_path = runtime_root / "runtime.sqlite3"
        try:
            if container_path.exists():
                if not is_encrypted_container(container_path):
                    raise ValueError(
                        "Encrypted mode was requested, but the existing database is plaintext. "
                        "Migrate it with backup/restore into an encrypted target."
                    )
                decrypt_database_file(container_path, runtime_db_path, self.passphrase)
        except Exception:
            if runtime_db_path.exists():
                runtime_db_path.unlink()
            runtime_root.rmdir()
            self._release_runtime_lock()
            raise

        self._runtime_dir = runtime_root
        self._runtime_db_path = runtime_db_path
        if not self._cleanup_registered:
            atexit.register(self._cleanup_encrypted_runtime)
            self._cleanup_registered = True
        return runtime_db_path

    def _acquire_runtime_lock(self) -> None:
        if self._lock_handle is not None:
            return
        lock_path = self._lock_path()
        lock_path.parent.mkdir(parents=True, exist_ok=True)
        handle = lock_path.open("a+b")
        try:
            fcntl.flock(handle.fileno(), fcntl.LOCK_EX)
        except Exception:
            handle.close()
            raise
        self._lock_handle = handle

    def _release_runtime_lock(self) -> None:
        if self._lock_handle is None:
            return
        try:
            fcntl.flock(self._lock_handle.fileno(), fcntl.LOCK_UN)
        finally:
            self._lock_handle.close()
            self._lock_handle = None

    def _sync_encrypted_runtime(self) -> None:
        if not self.passphrase or self._runtime_db_path is None or not self._runtime_db_path.exists():
            return

        runtime_db_path = self._runtime_db_path
        runtime_dir = self._runtime_dir
        if runtime_dir is None:
            return

        snapshot_path = runtime_dir / "snapshot.sqlite3"
        temp_container = runtime_dir / "container.enc"
        self._remove_sqlite_files(snapshot_path)
        if temp_container.exists():
            temp_container.unlink()

        source_connection = sqlite3.connect(str(runtime_db_path))
        try:
            snapshot_connection = sqlite3.connect(str(snapshot_path))
            try:
                source_connection.backup(snapshot_connection)
            finally:
                snapshot_connection.close()
        finally:
            source_connection.close()

        encrypt_database_file(snapshot_path, temp_container, self.passphrase)
        self._container_path().parent.mkdir(parents=True, exist_ok=True)
        os.replace(temp_container, self._container_path())
        self._remove_sqlite_files(snapshot_path)

    def _cleanup_encrypted_runtime(self) -> None:
        if self.passphrase and self._runtime_db_path is not None and self._runtime_db_path.exists():
            try:
                self._sync_encrypted_runtime()
            except Exception:
                print("WARNING: Failed to re-encrypt database during cleanup.", file=sys.stderr)

        if self._runtime_dir is not None and self._runtime_dir.exists():
            for candidate in sorted(self._runtime_dir.iterdir(), reverse=True):
                if candidate.is_file():
                    candidate.unlink()
            self._runtime_dir.rmdir()

        self._runtime_db_path = None
        self._runtime_dir = None
        self._release_runtime_lock()

    def close(self) -> None:
        if self.passphrase and self._runtime_db_path is not None:
            try:
                self._sync_encrypted_runtime()
            except Exception:
                print("WARNING: Failed to re-encrypt database on close.", file=sys.stderr)
        self._cleanup_encrypted_runtime()

    def remember(
        self,
        content: str,
        project: str = DEFAULT_PROJECT,
        kind: str = "episodic",
        source: str = "manual",
        importance: float = 0.5,
        confidence: float = 0.8,
        tags: Optional[List[str]] = None,
        metadata: Optional[Dict[str, Any]] = None,
    ) -> Dict[str, Any]:
        self.initialize(project=project)
        now = utc_now()
        scrubbed_content, sensitive_matches = scrub_sensitive(content)
        embedding = self.embedding_model.embed(scrubbed_content)

        with self.connect() as connection:
            duplicate = self._find_duplicate(connection, project, scrubbed_content, embedding)
            if duplicate is not None:
                connection.execute(
                    """
                    UPDATE memories
                    SET duplicate_count = duplicate_count + 1,
                        access_count = access_count + 1,
                        importance = MAX(importance, ?),
                        updated_at = ?,
                        last_accessed_at = ?
                    WHERE id = ?
                    """,
                    (importance, now, now, duplicate["id"]),
                )
                return {
                    "inserted": False,
                    "memory": self._get_memory_from_connection(connection, int(duplicate["id"])).to_dict(),
                }

            cursor = connection.execute(
                """
                INSERT INTO memories (
                    project, content, raw_content, kind, source, importance, confidence,
                    is_redacted, pii_hits, tags, metadata, embedding,
                    created_at, updated_at, valid_at
                ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
                """,
                (
                    project,
                    scrubbed_content,
                    content,
                    kind,
                    source,
                    importance,
                    confidence,
                    int(bool(sensitive_matches)),
                    json.dumps([match.label for match in sensitive_matches]),
                    json.dumps(tags or []),
                    json.dumps(metadata or {}),
                    json.dumps(embedding),
                    now,
                    now,
                    now,
                ),
            )
            memory_id = int(cursor.lastrowid)

            entity_names = extract_entities(scrubbed_content)
            for entity_name in entity_names:
                entity_id = self._ensure_entity(connection, project, entity_name, self._infer_entity_type(entity_name))
                connection.execute(
                    "INSERT OR IGNORE INTO memory_entities(memory_id, entity_id, role) VALUES (?, ?, ?)",
                    (memory_id, entity_id, "mention"),
                )
                # Índice inverso: referencia entidad→memoria.
                connection.execute(
                    "INSERT OR IGNORE INTO entity_references(entity_id, memory_id, direction, created_at) VALUES (?, ?, ?, ?)",
                    (entity_id, memory_id, "mention", now),
                )

            user_entity_id = self._ensure_entity(connection, project, DEFAULT_USER_ENTITY, "person")
            # Conjunto de nombres canónicos conocidos para detectar referencias no resueltas.
            known_canonical = {
                row["canonical_name"]
                for row in connection.execute(
                    "SELECT canonical_name FROM entities WHERE project = ?", (project,)
                ).fetchall()
            }
            relation_keys = set()
            for relation in extract_relation_candidates(scrubbed_content):
                # Verificar si el objeto de la relación ya existe como entidad canónica.
                candidate_canonical = canonicalize_entity(relation.object_value).lower()
                if candidate_canonical not in known_canonical:
                    # Registrar referencia no resuelta (hueco de conocimiento).
                    connection.execute(
                        """
                        INSERT OR IGNORE INTO unresolved_references(
                            project, reference_text, source_memory_id, created_at
                        ) VALUES (?, ?, ?, ?)
                        """,
                        (project, relation.object_value, memory_id, now),
                    )
                object_entity_id = self._ensure_entity(
                    connection,
                    project,
                    relation.object_value,
                    relation.entity_type,
                )
                # Actualizar known_canonical después de crear la entidad.
                known_canonical.add(candidate_canonical)
                connection.execute(
                    "INSERT OR IGNORE INTO memory_entities(memory_id, entity_id, role) VALUES (?, ?, ?)",
                    (memory_id, object_entity_id, "relation_object"),
                )
                # Índice inverso: referencia entidad→memoria (objeto de relación).
                connection.execute(
                    "INSERT OR IGNORE INTO entity_references(entity_id, memory_id, direction, created_at) VALUES (?, ?, ?, ?)",
                    (object_entity_id, memory_id, "relation_object", now),
                )
                self._record_relation_support(
                    connection=connection,
                    project=project,
                    subject_entity_id=user_entity_id,
                    predicate=relation.predicate,
                    object_entity_id=object_entity_id,
                    confidence=relation.confidence,
                    source_memory_id=memory_id,
                    now=now,
                )
                relation_keys.add((user_entity_id, relation.predicate))

            for subject_entity_id, predicate in relation_keys:
                self._refresh_relation_state(connection, project, subject_entity_id, predicate, now)

        return {
            "inserted": True,
            "memory": self.get_memory(memory_id).to_dict(),
        }

    def get_memory(self, memory_id: int) -> MemoryRecord:
        self.initialize()
        with self.connect() as connection:
            return self._get_memory_from_connection(connection, memory_id)

    def recall(
        self,
        query: str,
        project: str = DEFAULT_PROJECT,
        limit: int = 5,
        include_inactive: bool = False,
        touch: bool = True,
        reactivate: bool = True,
        maintain: bool = True,
    ) -> List[MemoryRecord]:
        self.initialize(project=project)
        if maintain:
            # Auto-maintenance: decay + consolidation run transparently on recall.
            self._auto_maintain(project)
        query_embedding = self.embedding_model.embed(query)
        fts_query = build_fts_query(query)
        query_entities = {canonicalize_entity(entity).lower() for entity in extract_entities(query)}

        with self.connect() as connection:
            base_sql = "SELECT * FROM memories WHERE project = ?"
            params: List[Any] = [project]
            if not include_inactive:
                base_sql += " AND invalid_at IS NULL"
            rows = connection.execute(base_sql, params).fetchall()
            memories = [MemoryRecord.from_row(row) for row in rows]
            memory_by_id = {memory.id: memory for memory in memories}
            embeddings = {
                int(row["id"]): json.loads(row["embedding"])
                for row in connection.execute(
                    "SELECT id, embedding FROM memories WHERE project = ?",
                    (project,),
                ).fetchall()
            }

            fts_ranks: Dict[int, int] = {}
            if fts_query:
                sql = """
                    SELECT m.id
                    FROM memories_fts f
                    JOIN memories m ON m.id = f.rowid
                    WHERE memories_fts MATCH ? AND m.project = ?
                """
                rank_params: List[Any] = [fts_query, project]
                if not include_inactive:
                    sql += " AND m.invalid_at IS NULL"
                sql += " ORDER BY bm25(memories_fts) LIMIT 50"
                for rank, row in enumerate(connection.execute(sql, rank_params).fetchall(), start=1):
                    fts_ranks[int(row["id"])] = rank

            graph_counts: Dict[int, int] = {}
            if query_entities:
                entity_rows = connection.execute(
                    """
                    SELECT me.memory_id, e.canonical_name
                    FROM memory_entities me
                    JOIN entities e ON e.id = me.entity_id
                    JOIN memories m ON m.id = me.memory_id
                    WHERE m.project = ?
                    """
                    + ("" if include_inactive else " AND m.invalid_at IS NULL"),
                    (project,),
                ).fetchall()
                for row in entity_rows:
                    if row["canonical_name"].lower() in query_entities:
                        graph_counts[int(row["memory_id"])] = graph_counts.get(int(row["memory_id"]), 0) + 1

            semantic_scores = {
                memory.id: normalize_score((cosine_similarity(query_embedding, embeddings.get(memory.id, [])) + 1.0) / 2.0)
                for memory in memories
            }
            semantic_rank_by_id = {
                memory_id: rank
                for rank, (memory_id, _score) in enumerate(
                    sorted(semantic_scores.items(), key=lambda item: item[1], reverse=True),
                    start=1,
                )
            }
            graph_rank_by_id = {
                memory_id: rank
                for rank, (memory_id, _score) in enumerate(
                    sorted(
                        ((memory.id, graph_counts.get(memory.id, 0)) for memory in memories),
                        key=lambda item: item[1],
                        reverse=True,
                    ),
                    start=1,
                )
            }

            scored: List[MemoryRecord] = []
            for memory in memories:
                memory.semantic_similarity = semantic_scores.get(memory.id, 0.0)
                if query_entities:
                    memory.graph_score = normalize_score(graph_counts.get(memory.id, 0) / float(len(query_entities)))
                else:
                    memory.graph_score = 0.0

                keyword_rank = fts_ranks.get(memory.id)
                memory.keyword_score = 1.0 / (10.0 + keyword_rank) if keyword_rank else 0.0
                memory.fusion_score = memory.keyword_score
                if memory.semantic_similarity > 0:
                    memory.fusion_score += 1.0 / (10.0 + semantic_rank_by_id.get(memory.id, len(memories) + 1))
                if memory.graph_score > 0:
                    memory.fusion_score += 1.0 / (10.0 + graph_rank_by_id.get(memory.id, len(memories) + 1))

                memory.score = self._final_score(memory)
                scored.append(memory)

            scored.sort(key=lambda item: item.score, reverse=True)
            selected = scored[: max(1, limit)]

            if touch:
                now = utc_now()
                for memory in selected:
                    connection.execute(
                        """
                        UPDATE memories
                        SET access_count = access_count + 1,
                            last_accessed_at = ?,
                            updated_at = ?
                        WHERE id = ?
                        """,
                        (now, now, memory.id),
                    )

            refreshed = [self._get_memory_from_connection(connection, memory.id) for memory in selected]
            final_lookup = {memory.id: memory for memory in refreshed}
            for previous in selected:
                current = final_lookup[previous.id]
                current.score = previous.score
                current.semantic_similarity = previous.semantic_similarity
                current.graph_score = previous.graph_score
                current.keyword_score = previous.keyword_score
                current.fusion_score = previous.fusion_score

        # Reactivation: if active results are weak, search dormant memories.
        # Models the human "oh wait, now I remember!" experience.
        if reactivate and not include_inactive:
            refreshed = self._try_reactivate(query_embedding, project, limit, refreshed)

        return refreshed

    def forget(
        self,
        project: str = DEFAULT_PROJECT,
        memory_id: Optional[int] = None,
        query: Optional[str] = None,
        sensitive: bool = False,
    ) -> int:
        self.initialize(project=project)
        now = utc_now()
        target_ids: List[int] = []
        if memory_id is not None:
            target_ids = [memory_id]
        elif sensitive:
            with self.connect() as connection:
                rows = connection.execute(
                    """
                    SELECT id
                    FROM memories
                    WHERE project = ? AND is_redacted = 1 AND invalid_at IS NULL
                    """,
                    (project,),
                ).fetchall()
                target_ids = [int(row["id"]) for row in rows]
        elif query:
            target_ids = [
                memory.id
                for memory in self.recall(
                    query=query,
                    project=project,
                    limit=50,
                    include_inactive=True,
                    touch=False,
                    reactivate=False,
                    maintain=False,
                )
            ]

        if not target_ids:
            return 0

        with self.connect() as connection:
            placeholders = ", ".join("?" for _ in target_ids)
            impacted_rows = connection.execute(
                """
                SELECT DISTINCT subject_entity_id, predicate
                FROM relation_supports
                WHERE project = ? AND source_memory_id IN ({})
                """.format(placeholders),
                [project, *target_ids],
            ).fetchall()
            cursor = connection.execute(
                """
                UPDATE memories
                SET invalid_at = COALESCE(invalid_at, ?),
                    updated_at = ?,
                    source = 'forgotten'
                WHERE id IN ({}) AND project = ?
                  AND (invalid_at IS NULL OR source != 'forgotten')
                """.format(placeholders),
                [now, now, *target_ids, project],
            )
            for row in impacted_rows:
                self._refresh_relation_state(
                    connection,
                    project,
                    int(row["subject_entity_id"]),
                    row["predicate"],
                    now,
                )
            return cursor.rowcount

    def list_entities(
        self,
        project: str = DEFAULT_PROJECT,
        limit: int = 20,
        active_only: bool = True,
    ) -> List[Dict[str, Any]]:
        self.initialize(project=project)
        with self.connect() as connection:
            rows = connection.execute(
                """
                SELECT
                    e.id,
                    e.name,
                    e.entity_type,
                    COUNT(DISTINCT CASE WHEN m.invalid_at IS NULL THEN me.memory_id END) AS active_memory_count,
                    COUNT(DISTINCT me.memory_id) AS total_memory_count
                FROM entities e
                LEFT JOIN memory_entities me ON me.entity_id = e.id
                LEFT JOIN memories m ON m.id = me.memory_id
                WHERE e.project = ?
                GROUP BY e.id, e.name, e.entity_type
                """,
                (project,),
            ).fetchall()
            entities: List[Dict[str, Any]] = []
            for row in rows:
                memory_count = int(row["active_memory_count"] if active_only else row["total_memory_count"])
                if active_only and memory_count == 0:
                    continue
                entities.append(
                    {
                        "id": int(row["id"]),
                        "name": row["name"],
                        "entity_type": row["entity_type"],
                        "memory_count": memory_count,
                        "active_memory_count": int(row["active_memory_count"]),
                        "total_memory_count": int(row["total_memory_count"]),
                    }
                )
            entities.sort(key=lambda row: (-row["memory_count"], row["name"]))
            return entities[: max(1, limit)]

    def list_facts(self, project: str = DEFAULT_PROJECT, active_only: bool = True, limit: int = 20) -> List[Dict[str, Any]]:
        self.initialize(project=project)
        with self.connect() as connection:
            sql = """
                SELECT
                    r.id,
                    s.name AS subject,
                    r.predicate,
                    o.name AS object,
                    r.confidence,
                    r.valid_at,
                    r.invalid_at
                FROM relations r
                JOIN entities s ON s.id = r.subject_entity_id
                JOIN entities o ON o.id = r.object_entity_id
                WHERE r.project = ?
            """
            params: List[Any] = [project]
            if active_only:
                sql += " AND r.invalid_at IS NULL"
            sql += " ORDER BY r.updated_at DESC LIMIT ?"
            params.append(limit)
            rows = connection.execute(sql, params).fetchall()
            return [dict(row) for row in rows]

    def _search_facts(self, query: str, project: str = DEFAULT_PROJECT, limit: int = 20) -> List[Dict[str, Any]]:
        self.initialize(project=project)
        query_tokens = set(tokenize(query))
        query_entities = {canonicalize_entity(entity).lower() for entity in extract_entities(query)}
        query_embedding = self.embedding_model.embed(query)

        with self.connect() as connection:
            rows = connection.execute(
                """
                SELECT
                    r.id,
                    s.name AS subject,
                    s.canonical_name AS subject_canonical,
                    r.predicate,
                    o.name AS object,
                    o.canonical_name AS object_canonical,
                    r.confidence,
                    r.valid_at,
                    r.invalid_at,
                    r.source_memory_id
                FROM relations r
                JOIN entities s ON s.id = r.subject_entity_id
                JOIN entities o ON o.id = r.object_entity_id
                WHERE r.project = ? AND r.invalid_at IS NULL
                """,
                (project,),
            ).fetchall()

        ranked: List[tuple[float, Dict[str, Any]]] = []
        for row in rows:
            relation_text = "{} {} {}".format(
                row["subject"],
                row["predicate"].replace("_", " "),
                row["object"],
            )
            relation_tokens = set(tokenize(relation_text))
            token_overlap = 0.0
            if query_tokens:
                token_overlap = len(query_tokens & relation_tokens) / float(len(query_tokens))

            entity_matches = 0
            if query_entities:
                if row["subject_canonical"].lower() in query_entities:
                    entity_matches += 1
                if row["object_canonical"].lower() in query_entities:
                    entity_matches += 1
            entity_overlap = entity_matches / float(len(query_entities)) if query_entities else 0.0

            semantic_similarity = normalize_score(
                (cosine_similarity(query_embedding, self.embedding_model.embed(relation_text)) + 1.0) / 2.0
            )
            score = 0.55 * entity_overlap + 0.30 * token_overlap + 0.15 * semantic_similarity

            if entity_overlap > 0 or token_overlap > 0 or score >= 0.20:
                ranked.append(
                    (
                        score,
                        {
                            "id": int(row["id"]),
                            "subject": row["subject"],
                            "predicate": row["predicate"],
                            "object": row["object"],
                            "confidence": float(row["confidence"]),
                            "valid_at": row["valid_at"],
                            "invalid_at": row["invalid_at"],
                            "score": round(score, 4),
                            "source_memory_id": int(row["source_memory_id"]),
                        },
                    )
                )

        ranked.sort(key=lambda item: (item[0], item[1]["confidence"]), reverse=True)
        return [item[1] for item in ranked[: max(1, limit)]]

    def build_context(
        self,
        query: str,
        project: str = DEFAULT_PROJECT,
        limit: int = 5,
        layer: int = 3,
    ) -> Dict[str, Any]:
        """Progressive-disclosure context builder.

        Layer 1 — compact index (~50-100 tokens): IDs, scores, entity names.
        Layer 2 — chronological summary (~200-500 tokens): timeline + facts.
        Layer 3 — full detail (~500-1000 tokens): complete memory content.
        """
        raw_memories = self.recall(
            query=query,
            project=project,
            limit=max(limit * 3, limit),
            touch=False,
        )
        memories = self._filter_context_memories(query, raw_memories, limit)
        facts = self._search_facts(query=query, project=project, limit=limit)
        if not facts:
            facts = self.list_facts(project=project, active_only=True, limit=limit)

        if layer <= 1:
            return {
                "query": query,
                "layer": 1,
                "fact_count": len(facts),
                "facts_summary": [
                    "{} --{}--> {}".format(f["subject"], f["predicate"], f["object"])
                    for f in facts
                ],
                "memory_index": [
                    {"id": m.id, "score": round(m.score, 3), "kind": m.kind}
                    for m in memories
                ],
            }

        if layer <= 2:
            return {
                "query": query,
                "layer": 2,
                "facts": [
                    {
                        "subject": f["subject"],
                        "predicate": f["predicate"],
                        "object": f["object"],
                        "confidence": f["confidence"],
                    }
                    for f in facts
                ],
                "memories": [
                    {
                        "id": m.id,
                        "score": round(m.score, 3),
                        "kind": m.kind,
                        "created_at": m.created_at,
                        "snippet": m.content[:120],
                    }
                    for m in memories
                ],
            }

        # Layer 3 — full detail (original behaviour)
        return {
            "query": query,
            "layer": 3,
            "facts": facts,
            "memories": [memory.to_dict() for memory in memories],
        }

    # ------------------------------------------------------------------
    # Automatic maintenance — runs transparently on recall
    # ------------------------------------------------------------------

    def _should_run_maintenance(self, connection: sqlite3.Connection, project: str, task: str) -> bool:
        """Check if a maintenance task is due based on the last run time."""
        row = connection.execute(
            "SELECT last_run_at FROM maintenance_log WHERE project = ? AND task = ?",
            (project, task),
        ).fetchone()
        if row is None:
            return True
        last_run = datetime.fromisoformat(row["last_run_at"])
        elapsed = (datetime.now(timezone.utc) - last_run).total_seconds()
        return elapsed >= _AUTO_MAINTENANCE_INTERVAL_SECS

    def _mark_maintenance_done(self, connection: sqlite3.Connection, project: str, task: str) -> None:
        connection.execute(
            """
            INSERT INTO maintenance_log(project, task, last_run_at)
            VALUES (?, ?, ?)
            ON CONFLICT(project, task)
            DO UPDATE SET last_run_at = excluded.last_run_at
            """,
            (project, task, utc_now()),
        )

    def _auto_maintain(self, project: str) -> None:
        """Run maintenance tasks only when they are due.

        The check-then-run pattern marks the task done *after* execution
        completes, so a crash mid-maintenance will simply retry next time.
        """
        run_decay = False
        with self.connect() as connection:
            if self._should_run_maintenance(connection, project, "decay"):
                run_decay = True
        if run_decay:
            self._apply_decay_by_kind(project)
            with self.connect() as connection:
                self._mark_maintenance_done(connection, project, "decay")

        run_consolidate = False
        with self.connect() as connection:
            if self._should_run_maintenance(connection, project, "consolidate"):
                run_consolidate = True
        if run_consolidate:
            self.consolidate(project=project)
            with self.connect() as connection:
                self._mark_maintenance_done(connection, project, "consolidate")

    def _apply_decay_by_kind(self, project: str) -> None:
        """Apply decay with different half-lives per memory kind.

        Core memories barely decay (100 year half-life).
        Episodic memories decay normally (30 day half-life).
        Semantic/procedural sit in between.
        """
        for kind, half_life in _HALF_LIFE_BY_KIND.items():
            self.apply_decay(
                project=project,
                half_life_days=half_life,
                min_importance=_DORMANT_IMPORTANCE,
                kind_filter=kind,
            )

    def _try_reactivate(
        self,
        query_embedding: list,
        project: str,
        limit: int,
        active_results: List[MemoryRecord],
    ) -> List[MemoryRecord]:
        """Search dormant memories and reactivate any that match well.

        This models the human experience of: "I didn't remember, but now
        that you mention it..." — a strong enough cue brings back a
        memory that had faded from easy recall.
        """
        # Only try reactivation if active results are weak.
        if active_results and active_results[0].score > 0.4:
            return active_results

        with self.connect() as connection:
            # Only reactivate memories that faded naturally (decay).
            # Memories with source='forgotten' were intentionally removed
            # by the user and must NOT come back.
            rows = connection.execute(
                """
                SELECT * FROM memories
                WHERE project = ? AND invalid_at IS NOT NULL
                  AND source != 'forgotten'
                ORDER BY importance DESC
                LIMIT 200
                """,
                (project,),
            ).fetchall()

            if not rows:
                return active_results

            reactivated: List[MemoryRecord] = []
            now = utc_now()

            for row in rows:
                embedding = json.loads(row["embedding"])
                similarity = (cosine_similarity(query_embedding, embedding) + 1.0) / 2.0
                if similarity >= _REACTIVATION_THRESHOLD:
                    # Reactivate: clear invalid_at, boost importance back up.
                    new_importance = max(0.3, float(row["importance"]) * 2.0)
                    connection.execute(
                        """
                        UPDATE memories
                        SET invalid_at = NULL,
                            importance = ?,
                            access_count = access_count + 1,
                            last_accessed_at = ?,
                            updated_at = ?
                        WHERE id = ?
                        """,
                        (new_importance, now, now, row["id"]),
                    )
                    memory = self._get_memory_from_connection(connection, int(row["id"]))
                    memory.score = similarity
                    memory.semantic_similarity = similarity
                    reactivated.append(memory)

            if reactivated:
                reactivated.sort(key=lambda m: m.score, reverse=True)

            # Merge: active results first, then reactivated, respect limit.
            combined = active_results + reactivated
            combined.sort(key=lambda m: m.score, reverse=True)
            return combined[:max(1, limit)]

    def query(
        self,
        project: str = DEFAULT_PROJECT,
        kind: Optional[str] = None,
        tags: Optional[List[str]] = None,
        importance_min: Optional[float] = None,
        importance_max: Optional[float] = None,
        created_after: Optional[str] = None,
        created_before: Optional[str] = None,
        source: Optional[str] = None,
        metadata_filter: Optional[Dict[str, Any]] = None,
        limit: int = 20,
        order_by: str = "created_at",
        order_dir: str = "DESC",
    ) -> List[MemoryRecord]:
        """Consulta estructurada de memorias con filtros combinables.

        Construye una consulta SQL con cláusulas WHERE según los filtros
        proporcionados. Para tags usa extracción JSON; para metadata_filter
        usa json_extract().
        """
        self.initialize(project=project)
        # Columnas válidas para ordenamiento.
        allowed_order = {"created_at", "updated_at", "importance", "confidence", "access_count"}
        if order_by not in allowed_order:
            order_by = "created_at"
        if order_dir.upper() not in ("ASC", "DESC"):
            order_dir = "DESC"

        sql = "SELECT * FROM memories WHERE project = ? AND invalid_at IS NULL"
        params: List[Any] = [project]

        if kind is not None:
            sql += " AND kind = ?"
            params.append(kind)

        if source is not None:
            sql += " AND source = ?"
            params.append(source)

        if importance_min is not None:
            sql += " AND importance >= ?"
            params.append(importance_min)

        if importance_max is not None:
            sql += " AND importance <= ?"
            params.append(importance_max)

        if created_after is not None:
            sql += " AND created_at >= ?"
            params.append(created_after)

        if created_before is not None:
            sql += " AND created_at <= ?"
            params.append(created_before)

        # Filtro por tags: verificar si algún tag coincide usando json_each.
        if tags:
            tag_conditions = []
            for tag in tags:
                tag_conditions.append(
                    "EXISTS (SELECT 1 FROM json_each(memories.tags) WHERE json_each.value = ?)"
                )
                params.append(tag)
            sql += " AND (" + " OR ".join(tag_conditions) + ")"

        # Filtro por metadata: usar json_extract para cada clave/valor.
        if metadata_filter:
            for key, value in metadata_filter.items():
                sql += " AND json_extract(metadata, ?) = ?"
                params.append("$.{}".format(key))
                params.append(json.dumps(value) if isinstance(value, (dict, list)) else value)

        sql += " ORDER BY {} {} LIMIT ?".format(order_by, order_dir.upper())
        params.append(limit)

        with self.connect() as connection:
            rows = connection.execute(sql, params).fetchall()
            return [MemoryRecord.from_row(row) for row in rows]

    def get_entity_references(self, entity_id: int, limit: int = 20) -> List[Dict[str, Any]]:
        """Obtener todas las memorias que referencian una entidad dada, con dirección."""
        self.initialize()
        with self.connect() as connection:
            rows = connection.execute(
                """
                SELECT er.entity_id, er.memory_id, er.direction, er.created_at,
                       m.content, m.kind, m.importance
                FROM entity_references er
                JOIN memories m ON m.id = er.memory_id
                WHERE er.entity_id = ? AND m.invalid_at IS NULL
                ORDER BY er.created_at DESC
                LIMIT ?
                """,
                (entity_id, limit),
            ).fetchall()
            return [
                {
                    "entity_id": int(row["entity_id"]),
                    "memory_id": int(row["memory_id"]),
                    "direction": row["direction"],
                    "created_at": row["created_at"],
                    "content": row["content"],
                    "kind": row["kind"],
                    "importance": float(row["importance"]),
                }
                for row in rows
            ]

    def list_unresolved_references(
        self, project: str = DEFAULT_PROJECT, limit: int = 20
    ) -> List[Dict[str, Any]]:
        """Listar referencias no resueltas (huecos de conocimiento)."""
        self.initialize(project=project)
        with self.connect() as connection:
            rows = connection.execute(
                """
                SELECT ur.id, ur.project, ur.reference_text, ur.source_memory_id,
                       ur.created_at, ur.resolved_at, ur.resolved_entity_id,
                       m.content AS source_content
                FROM unresolved_references ur
                JOIN memories m ON m.id = ur.source_memory_id
                WHERE ur.project = ? AND ur.resolved_at IS NULL
                ORDER BY ur.created_at DESC
                LIMIT ?
                """,
                (project, limit),
            ).fetchall()
            return [
                {
                    "id": int(row["id"]),
                    "project": row["project"],
                    "reference_text": row["reference_text"],
                    "source_memory_id": int(row["source_memory_id"]),
                    "created_at": row["created_at"],
                    "resolved_at": row["resolved_at"],
                    "resolved_entity_id": int(row["resolved_entity_id"]) if row["resolved_entity_id"] else None,
                    "source_content": row["source_content"],
                }
                for row in rows
            ]

    def _filter_context_memories(
        self,
        query: str,
        memories: Sequence[MemoryRecord],
        limit: int,
    ) -> List[MemoryRecord]:
        query_tokens = set(tokenize(query))
        query_entities = {canonicalize_entity(entity).lower() for entity in extract_entities(query)}
        if not memories:
            return []

        top_semantic = max(memory.semantic_similarity for memory in memories)
        filtered: List[MemoryRecord] = []
        for memory in memories:
            has_keyword_match = memory.keyword_score > 0.0
            has_graph_match = memory.graph_score > 0.0
            strong_semantic = memory.semantic_similarity >= max(0.60, top_semantic - 0.05)

            if not query_tokens and not query_entities:
                filtered.append(memory)
                continue

            if has_keyword_match or has_graph_match or strong_semantic:
                filtered.append(memory)

        if not filtered:
            return list(memories[: max(1, limit)])
        return filtered[: max(1, limit)]

    # ------------------------------------------------------------------
    # Memory decay — FSRS-inspired lifecycle management
    # ------------------------------------------------------------------

    def apply_decay(
        self,
        project: str = DEFAULT_PROJECT,
        half_life_days: float = 30.0,
        min_importance: float = 0.05,
        kind_filter: Optional[str] = None,
    ) -> Dict[str, Any]:
        """Apply exponential importance decay to active memories.

        Memories that are accessed frequently decay slower (their access_count
        acts as a reinforcement signal, inspired by FSRS / spaced repetition).
        Memories whose importance drops below *min_importance* are auto-
        invalidated (marked dormant, NOT deleted — they can be reactivated
        later if a strong enough cue arrives, just like human memory).

        Returns a summary of what happened.
        """
        self.initialize(project=project)
        now_str = utc_now()
        now_dt = datetime.now(timezone.utc)
        decay_constant = math.log(2) / max(half_life_days, 1e-6)

        with self.connect() as connection:
            sql = """
                SELECT id, importance, access_count, created_at, updated_at, last_decay_at
                FROM memories
                WHERE project = ? AND invalid_at IS NULL
            """
            params: List[Any] = [project]
            if kind_filter:
                sql += " AND kind = ?"
                params.append(kind_filter)
            rows = connection.execute(sql, params).fetchall()

            decayed = 0
            invalidated = 0
            impacted_relations = set()

            for row in rows:
                baseline_text = row["last_decay_at"] or row["created_at"]
                baseline = datetime.fromisoformat(baseline_text)
                age_days = max(0.0, (now_dt - baseline).total_seconds() / 86400.0)
                reinforcement = 1.0 + 0.15 * math.log1p(row["access_count"])
                new_importance = float(row["importance"]) * math.exp(-decay_constant * age_days / reinforcement)
                new_importance = round(max(0.0, new_importance), 6)

                if new_importance < min_importance:
                    relation_rows = connection.execute(
                        """
                        SELECT DISTINCT subject_entity_id, predicate
                        FROM relation_supports
                        WHERE project = ? AND source_memory_id = ?
                        """,
                        (project, row["id"]),
                    ).fetchall()
                    for relation_row in relation_rows:
                        impacted_relations.add((int(relation_row["subject_entity_id"]), relation_row["predicate"]))
                    connection.execute(
                        """
                        UPDATE memories
                        SET invalid_at = ?, importance = ?, updated_at = ?, last_decay_at = ?
                        WHERE id = ?
                        """,
                        (now_str, new_importance, now_str, now_str, row["id"]),
                    )
                    invalidated += 1
                elif abs(new_importance - float(row["importance"])) > 0.001:
                    connection.execute(
                        """
                        UPDATE memories
                        SET importance = ?, updated_at = ?, last_decay_at = ?
                        WHERE id = ?
                        """,
                        (new_importance, now_str, now_str, row["id"]),
                    )
                    decayed += 1
                else:
                    connection.execute(
                        "UPDATE memories SET last_decay_at = ? WHERE id = ?",
                        (now_str, row["id"]),
                    )

            for subject_entity_id, predicate in impacted_relations:
                self._refresh_relation_state(connection, project, subject_entity_id, predicate, now_str)

        return {
            "total_active": len(rows),
            "decayed": decayed,
            "invalidated_zombies": invalidated,
            "half_life_days": half_life_days,
            "min_importance": min_importance,
        }

    # ------------------------------------------------------------------
    # Consolidation — episodic → semantic (bio-inspired hippocampus)
    # ------------------------------------------------------------------

    def consolidate(
        self,
        project: str = DEFAULT_PROJECT,
        similarity_threshold: float = 0.80,
        min_cluster_size: int = 2,
    ) -> Dict[str, Any]:
        """Cluster unconsolidated episodic memories and distill semantic facts.

        Steps (per RESEARCH.md §7.3):
        1. Gather active episodic memories.
        2. Greedily cluster by embedding similarity.
        3. For each cluster, extract recurring entities/relations.
        4. Create/update semantic facts with boosted confidence.
        5. Reduce importance of already-consolidated episodes (don't delete).
        """
        self.initialize(project=project)
        now = utc_now()

        with self.connect() as connection:
            rows = connection.execute(
                """
                SELECT id, content, embedding, importance
                FROM memories
                WHERE project = ? AND kind = 'episodic' AND invalid_at IS NULL
                  AND consolidated_at IS NULL
                ORDER BY created_at ASC
                """,
                (project,),
            ).fetchall()

            if len(rows) < min_cluster_size:
                return {"clusters": 0, "new_facts": 0, "consolidated_episodes": 0}

            # Build embeddings map
            items = [(int(r["id"]), r["content"], json.loads(r["embedding"]), float(r["importance"])) for r in rows]

            # Greedy clustering
            assigned = set()
            clusters: List[List[int]] = []  # list of (id-groups)

            for i, (id_i, _content_i, emb_i, _imp_i) in enumerate(items):
                if id_i in assigned:
                    continue
                cluster = [i]
                assigned.add(id_i)
                for j, (id_j, _content_j, emb_j, _imp_j) in enumerate(items):
                    if id_j in assigned:
                        continue
                    if cosine_similarity(emb_i, emb_j) >= similarity_threshold:
                        cluster.append(j)
                        assigned.add(id_j)
                if len(cluster) >= min_cluster_size:
                    clusters.append(cluster)

            new_facts = 0
            consolidated_episodes = 0

            user_entity_id = self._ensure_entity(connection, project, DEFAULT_USER_ENTITY, "person")

            for cluster_indices in clusters:
                # Combine content of cluster members
                cluster_items = [items[idx] for idx in cluster_indices]
                combined_text = " ".join(item[1] for item in cluster_items)
                summary_parts = []
                for item in cluster_items:
                    snippet = item[1][:100].strip()
                    if snippet:
                        summary_parts.append(snippet)
                summary = "[consolidated] " + " | ".join(summary_parts)
                cluster_embedding = average_embedding([item[2] for item in cluster_items])
                summary_cursor = connection.execute(
                    """
                    INSERT INTO memories (
                        project, content, raw_content, kind, source, importance, confidence,
                        is_redacted, pii_hits, embedding, created_at, updated_at, valid_at
                    ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
                    """,
                    (
                        project,
                        summary,
                        summary,
                        "semantic",
                        "consolidation",
                        min(1.0, max(item[3] for item in cluster_items) + 0.1),
                        0.85,
                        0,
                        "[]",
                        json.dumps(cluster_embedding),
                        now,
                        now,
                        now,
                    ),
                )
                summary_memory_id = int(summary_cursor.lastrowid)

                for entity_name in extract_entities(summary):
                    entity_id = self._ensure_entity(connection, project, entity_name, self._infer_entity_type(entity_name))
                    connection.execute(
                        "INSERT OR IGNORE INTO memory_entities(memory_id, entity_id, role) VALUES (?, ?, ?)",
                        (summary_memory_id, entity_id, "mention"),
                    )

                # Extract relations from combined text
                relations = extract_relation_candidates(combined_text)
                relation_keys = set()
                for relation in relations:
                    boosted_confidence = min(1.0, relation.confidence + 0.1 * (len(cluster_items) - 1))
                    object_entity_id = self._ensure_entity(
                        connection, project, relation.object_value, relation.entity_type,
                    )
                    connection.execute(
                        "INSERT OR IGNORE INTO memory_entities(memory_id, entity_id, role) VALUES (?, ?, ?)",
                        (summary_memory_id, object_entity_id, "relation_object"),
                    )
                    self._record_relation_support(
                        connection=connection,
                        project=project,
                        subject_entity_id=user_entity_id,
                        predicate=relation.predicate,
                        object_entity_id=object_entity_id,
                        confidence=boosted_confidence,
                        source_memory_id=summary_memory_id,
                        now=now,
                    )
                    new_facts += 1
                    relation_keys.add((user_entity_id, relation.predicate))

                for subject_entity_id, predicate in relation_keys:
                    self._refresh_relation_state(connection, project, subject_entity_id, predicate, now)

                # Reduce importance of consolidated episodes
                for item in cluster_items:
                    connection.execute(
                        """
                        UPDATE memories
                        SET importance = importance * 0.5,
                            updated_at = ?,
                            consolidated_at = ?
                        WHERE id = ?
                        """,
                        (now, now, item[0]),
                    )
                    consolidated_episodes += 1

        return {
            "clusters": len(clusters),
            "new_facts": new_facts,
            "consolidated_episodes": consolidated_episodes,
        }

    def export_memories(self, project: str = DEFAULT_PROJECT, include_inactive: bool = True) -> List[Dict[str, Any]]:
        self.initialize(project=project)
        with self.connect() as connection:
            sql = "SELECT * FROM memories WHERE project = ?"
            params: List[Any] = [project]
            if not include_inactive:
                sql += " AND invalid_at IS NULL"
            sql += " ORDER BY created_at ASC"
            rows = connection.execute(sql, params).fetchall()
            return [MemoryRecord.from_row(row).to_dict() for row in rows]

    def backup(self, output_path: str | Path, overwrite: bool = False) -> Dict[str, Any]:
        source_path = Path(self.db_path).resolve()
        destination_path = Path(output_path).expanduser().resolve()
        if not source_path.exists():
            raise FileNotFoundError("Source database does not exist: {}".format(source_path))
        if destination_path == source_path:
            raise ValueError("Backup destination must be different from the source database.")
        if destination_path.exists() and not overwrite:
            raise FileExistsError("Backup destination already exists: {}".format(destination_path))

        destination_path.parent.mkdir(parents=True, exist_ok=True)
        self._remove_sqlite_files(destination_path)

        with self.connect() as source_connection:
            destination_connection = sqlite3.connect(destination_path)
            try:
                source_connection.backup(destination_connection)
            finally:
                destination_connection.close()

        summary = self._database_summary(destination_path)
        summary.update(
            {
                "source_db": str(source_path),
                "backup_db": str(destination_path),
            }
        )
        return summary

    def restore_from(self, input_path: str | Path, overwrite: bool = False) -> Dict[str, Any]:
        backup_path = Path(input_path).expanduser().resolve()
        target_path = self._container_path()
        if not backup_path.exists():
            raise FileNotFoundError("Backup source does not exist: {}".format(backup_path))
        if backup_path == target_path:
            raise ValueError("Restore source must be different from the target database.")
        if target_path.exists() and not overwrite:
            raise FileExistsError("Target database already exists: {}".format(target_path))

        target_path.parent.mkdir(parents=True, exist_ok=True)
        plaintext_target = self._prepare_sqlite_path() if self.passphrase else target_path
        self._remove_sqlite_files(plaintext_target)

        source_connection = sqlite3.connect(str(backup_path))
        try:
            target_connection = sqlite3.connect(str(plaintext_target))
            try:
                source_connection.backup(target_connection)
            finally:
                target_connection.close()
        finally:
            source_connection.close()

        if self.passphrase:
            self._sync_encrypted_runtime()

        summary = self._database_summary(plaintext_target)
        summary.update(
            {
                "backup_db": str(backup_path),
                "restored_db": str(target_path),
            }
        )
        return summary

    def _infer_entity_type(self, entity_name: str) -> str:
        lowered = entity_name.lower()
        if lowered in {DEFAULT_USER_ENTITY, "yo", "i"}:
            return "person"
        if " " in entity_name and entity_name.split(" ", 1)[0].lower() in {"san", "santa", "new"}:
            return "location"
        if entity_name.isupper():
            return "organization"
        return "concept"

    def _database_summary(self, db_path: Path) -> Dict[str, Any]:
        connection = sqlite3.connect(db_path)
        try:
            memory_count = int(connection.execute("SELECT COUNT(*) FROM memories").fetchone()[0])
            project_count = int(connection.execute("SELECT COUNT(*) FROM projects").fetchone()[0])
            entity_count = int(connection.execute("SELECT COUNT(*) FROM entities").fetchone()[0])
            relation_count = int(connection.execute("SELECT COUNT(*) FROM relations").fetchone()[0])
        finally:
            connection.close()

        return {
            "projects": project_count,
            "memories": memory_count,
            "entities": entity_count,
            "relations": relation_count,
            "size_bytes": db_path.stat().st_size if db_path.exists() else 0,
        }

    def _remove_sqlite_files(self, db_path: Path) -> None:
        for suffix in ("", "-wal", "-shm"):
            candidate = Path(str(db_path) + suffix)
            if candidate.exists():
                candidate.unlink()

    def _ensure_column(
        self,
        connection: sqlite3.Connection,
        table: str,
        column: str,
        definition: str,
    ) -> None:
        rows = connection.execute("PRAGMA table_info({})".format(table)).fetchall()
        existing = {row["name"] for row in rows}
        if column not in existing:
            connection.execute(
                "ALTER TABLE {} ADD COLUMN {} {}".format(table, column, definition)
            )

    def _ensure_entity(
        self,
        connection: sqlite3.Connection,
        project: str,
        entity_name: str,
        entity_type: str,
    ) -> int:
        canonical_name = canonicalize_entity(entity_name)
        now = utc_now()
        connection.execute(
            """
            INSERT INTO entities(project, name, canonical_name, entity_type, created_at, updated_at)
            VALUES (?, ?, ?, ?, ?, ?)
            ON CONFLICT(project, canonical_name)
            DO UPDATE SET
                name = excluded.name,
                entity_type = excluded.entity_type,
                updated_at = excluded.updated_at
            """,
            (project, canonical_name, canonical_name.lower(), entity_type, now, now),
        )
        row = connection.execute(
            "SELECT id FROM entities WHERE project = ? AND canonical_name = ?",
            (project, canonical_name.lower()),
        ).fetchone()
        if row is None:
            raise RuntimeError("Failed to upsert entity {}".format(entity_name))
        return int(row["id"])

    def _record_relation_support(
        self,
        connection: sqlite3.Connection,
        project: str,
        subject_entity_id: int,
        predicate: str,
        object_entity_id: int,
        confidence: float,
        source_memory_id: int,
        now: str,
    ) -> None:
        connection.execute(
            """
            INSERT INTO relation_supports(
                project, subject_entity_id, predicate, object_entity_id,
                confidence, source_memory_id, created_at, updated_at
            ) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
            ON CONFLICT(project, subject_entity_id, predicate, object_entity_id, source_memory_id)
            DO UPDATE SET
                confidence = MAX(confidence, excluded.confidence),
                updated_at = excluded.updated_at
            """,
            (
                project,
                subject_entity_id,
                predicate,
                object_entity_id,
                confidence,
                source_memory_id,
                now,
                now,
            ),
        )

    def _refresh_relation_state(
        self,
        connection: sqlite3.Connection,
        project: str,
        subject_entity_id: int,
        predicate: str,
        now: str,
    ) -> None:
        support_rows = connection.execute(
            """
            SELECT
                rs.object_entity_id,
                MAX(rs.confidence) AS max_confidence,
                MAX(rs.source_memory_id) AS source_memory_id,
                COUNT(*) AS support_count,
                MAX(m.created_at) AS latest_created_at
            FROM relation_supports rs
            JOIN memories m ON m.id = rs.source_memory_id
            WHERE rs.project = ? AND rs.subject_entity_id = ? AND rs.predicate = ?
              AND m.invalid_at IS NULL
            GROUP BY rs.object_entity_id
            ORDER BY max_confidence DESC, latest_created_at DESC, source_memory_id DESC, support_count DESC, rs.object_entity_id ASC
            """,
            (project, subject_entity_id, predicate),
        ).fetchall()
        existing_rows = connection.execute(
            """
            SELECT id, object_entity_id
            FROM relations
            WHERE project = ? AND subject_entity_id = ? AND predicate = ? AND invalid_at IS NULL
            """,
            (project, subject_entity_id, predicate),
        ).fetchone()
        existing_by_object = {}
        if existing_rows is not None:
            existing = connection.execute(
                """
                SELECT id, object_entity_id
                FROM relations
                WHERE project = ? AND subject_entity_id = ? AND predicate = ? AND invalid_at IS NULL
                """,
                (project, subject_entity_id, predicate),
            ).fetchall()
            existing_by_object = {int(row["object_entity_id"]): int(row["id"]) for row in existing}

        if not support_rows:
            connection.execute(
                """
                UPDATE relations
                SET invalid_at = ?, updated_at = ?
                WHERE project = ? AND subject_entity_id = ? AND predicate = ? AND invalid_at IS NULL
                """,
                (now, now, project, subject_entity_id, predicate),
            )
            return

        candidates = []
        for row in support_rows:
            aggregated_confidence = min(1.0, float(row["max_confidence"]) + 0.05 * (int(row["support_count"]) - 1))
            candidates.append(
                {
                    "object_entity_id": int(row["object_entity_id"]),
                    "confidence": aggregated_confidence,
                    "source_memory_id": int(row["source_memory_id"]),
                }
            )

        if predicate in EXCLUSIVE_PREDICATES:
            candidates = candidates[:1]

        active_object_ids = {candidate["object_entity_id"] for candidate in candidates}
        connection.execute(
            """
            UPDATE relations
            SET invalid_at = ?, updated_at = ?
            WHERE project = ? AND subject_entity_id = ? AND predicate = ?
              AND invalid_at IS NULL AND object_entity_id NOT IN ({})
            """.format(", ".join("?" for _ in active_object_ids) or "NULL"),
            [now, now, project, subject_entity_id, predicate, *active_object_ids],
        )

        for candidate in candidates:
            relation_id = existing_by_object.get(candidate["object_entity_id"])
            if relation_id is not None:
                connection.execute(
                    """
                    UPDATE relations
                    SET confidence = ?, source_memory_id = ?, updated_at = ?
                    WHERE id = ?
                    """,
                    (
                        candidate["confidence"],
                        candidate["source_memory_id"],
                        now,
                        relation_id,
                    ),
                )
                continue

            connection.execute(
                """
                INSERT INTO relations(
                    project, subject_entity_id, predicate, object_entity_id,
                    confidence, source_memory_id, valid_at, created_at, updated_at
                ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
                """,
                (
                    project,
                    subject_entity_id,
                    predicate,
                    candidate["object_entity_id"],
                    candidate["confidence"],
                    candidate["source_memory_id"],
                    now,
                    now,
                    now,
                ),
            )

    def _find_duplicate(
        self,
        connection: sqlite3.Connection,
        project: str,
        content: str,
        embedding: Sequence[float],
    ) -> Optional[sqlite3.Row]:
        exact = connection.execute(
            """
            SELECT id, content, embedding
            FROM memories
            WHERE project = ? AND invalid_at IS NULL AND content = ?
            ORDER BY id DESC
            LIMIT 1
            """,
            (project, content),
        ).fetchone()
        if exact is not None:
            return exact

        rows = connection.execute(
            """
            SELECT id, content, embedding
            FROM memories
            WHERE project = ? AND invalid_at IS NULL
            ORDER BY id DESC
            LIMIT 250
            """,
            (project,),
        ).fetchall()
        for row in rows:
            other = json.loads(row["embedding"])
            if cosine_similarity(other, embedding) >= 0.98:
                return row
        return None

    def _get_memory_from_connection(self, connection: sqlite3.Connection, memory_id: int) -> MemoryRecord:
        row = connection.execute("SELECT * FROM memories WHERE id = ?", (memory_id,)).fetchone()
        if row is None:
            raise KeyError("Memory {} not found".format(memory_id))
        return MemoryRecord.from_row(row)

    def _semantic_rank(
        self,
        memories: Sequence[MemoryRecord],
        embeddings: Dict[int, Sequence[float]],
        query_embedding: Sequence[float],
        target_memory_id: int,
    ) -> int:
        scores = sorted(
            (
                (memory.id, cosine_similarity(query_embedding, embeddings.get(memory.id, [])))
                for memory in memories
            ),
            key=lambda item: item[1],
            reverse=True,
        )
        for rank, (memory_id, _) in enumerate(scores, start=1):
            if memory_id == target_memory_id:
                return rank
        return len(scores) + 1

    def _graph_rank(
        self,
        memories: Sequence[MemoryRecord],
        graph_counts: Dict[int, int],
        target_memory_id: int,
    ) -> int:
        scores = sorted(
            ((memory.id, graph_counts.get(memory.id, 0)) for memory in memories),
            key=lambda item: item[1],
            reverse=True,
        )
        for rank, (memory_id, _) in enumerate(scores, start=1):
            if memory_id == target_memory_id:
                return rank
        return len(scores) + 1

    def _final_score(self, memory: MemoryRecord) -> float:
        created_at = datetime.fromisoformat(memory.created_at)
        age_days = max(0.0, (datetime.now(timezone.utc) - created_at).total_seconds() / 86400.0)
        recency = math.exp(-0.08 * age_days)
        frequency = normalize_score(math.log1p(memory.access_count) / 4.0)
        return (
            0.2 * recency
            + 0.18 * normalize_score(memory.importance)
            + 0.03 * frequency
            + 0.24 * normalize_score(memory.semantic_similarity)
            + 0.15 * normalize_score(memory.graph_score)
            + 0.2 * normalize_score(memory.fusion_score)
        )
