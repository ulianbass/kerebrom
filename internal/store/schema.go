package store

// Schema contains all CREATE TABLE / INDEX / TRIGGER statements.
// Called once by Migrate().
const schema = `
-- Pragmas
PRAGMA journal_mode = WAL;
PRAGMA foreign_keys = ON;
PRAGMA busy_timeout = 5000;

-- === MEMORIES ===
CREATE TABLE IF NOT EXISTS memories (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    project         TEXT    NOT NULL,
    content         TEXT    NOT NULL,
    raw_content     TEXT    NOT NULL,
    kind            TEXT    NOT NULL CHECK(kind IN ('core','episodic','semantic','procedural')),
    source          TEXT    NOT NULL,
    importance      REAL    NOT NULL DEFAULT 0.5,
    confidence      REAL    NOT NULL DEFAULT 0.8,
    access_count    INTEGER NOT NULL DEFAULT 0,
    duplicate_count INTEGER NOT NULL DEFAULT 0,
    is_redacted     INTEGER NOT NULL DEFAULT 0,
    pii_hits        TEXT    NOT NULL DEFAULT '[]',
    embedding       BLOB    NOT NULL,
    created_at      TEXT    NOT NULL,
    updated_at      TEXT    NOT NULL,
    last_accessed_at TEXT,
    last_decay_at   TEXT,
    valid_at        TEXT    NOT NULL,
    invalid_at      TEXT,
    consolidated_at TEXT,
    tags            TEXT    NOT NULL DEFAULT '[]',
    metadata        TEXT    NOT NULL DEFAULT '{}',
    content_hash    TEXT    NOT NULL DEFAULT ''
);

-- FTS5 full-text search
CREATE VIRTUAL TABLE IF NOT EXISTS memories_fts USING fts5(
    content,
    content='memories',
    content_rowid='id',
    tokenize='unicode61 remove_diacritics 2'
);

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

-- === KNOWLEDGE GRAPH ===
CREATE TABLE IF NOT EXISTS entities (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    project        TEXT NOT NULL,
    name           TEXT NOT NULL,
    canonical_name TEXT NOT NULL,
    entity_type    TEXT NOT NULL DEFAULT 'concept',
    created_at     TEXT NOT NULL,
    updated_at     TEXT NOT NULL,
    UNIQUE(project, canonical_name)
);

CREATE TABLE IF NOT EXISTS memory_entities (
    memory_id INTEGER NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
    entity_id INTEGER NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
    role      TEXT    NOT NULL DEFAULT 'mention',
    PRIMARY KEY(memory_id, entity_id)
);

CREATE TABLE IF NOT EXISTS relations (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    project           TEXT    NOT NULL,
    subject_entity_id INTEGER NOT NULL REFERENCES entities(id),
    predicate         TEXT    NOT NULL,
    object_entity_id  INTEGER NOT NULL REFERENCES entities(id),
    confidence        REAL    NOT NULL DEFAULT 0.7,
    source_memory_id  INTEGER NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
    valid_at          TEXT    NOT NULL,
    invalid_at        TEXT,
    created_at        TEXT    NOT NULL,
    updated_at        TEXT    NOT NULL
);

CREATE TABLE IF NOT EXISTS relation_supports (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    project           TEXT    NOT NULL,
    subject_entity_id INTEGER NOT NULL REFERENCES entities(id),
    predicate         TEXT    NOT NULL,
    object_entity_id  INTEGER NOT NULL REFERENCES entities(id),
    confidence        REAL    NOT NULL DEFAULT 0.7,
    source_memory_id  INTEGER NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
    created_at        TEXT    NOT NULL,
    updated_at        TEXT    NOT NULL,
    UNIQUE(project, subject_entity_id, predicate, object_entity_id, source_memory_id)
);

CREATE TABLE IF NOT EXISTS entity_references (
    entity_id  INTEGER NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
    memory_id  INTEGER NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
    direction  TEXT    NOT NULL DEFAULT 'mention',
    created_at TEXT    NOT NULL,
    PRIMARY KEY(entity_id, memory_id, direction)
);

CREATE TABLE IF NOT EXISTS unresolved_references (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    project          TEXT    NOT NULL,
    reference_text   TEXT    NOT NULL,
    source_memory_id INTEGER NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
    created_at       TEXT    NOT NULL,
    resolved_at      TEXT,
    resolved_entity_id INTEGER REFERENCES entities(id),
    UNIQUE(project, reference_text, source_memory_id)
);

-- === TOPICS (from Engram) ===
CREATE TABLE IF NOT EXISTS topics (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    project    TEXT    NOT NULL,
    key        TEXT    NOT NULL,
    revision   INTEGER NOT NULL DEFAULT 1,
    memory_id  INTEGER NOT NULL REFERENCES memories(id),
    created_at TEXT    NOT NULL,
    updated_at TEXT    NOT NULL,
    UNIQUE(project, key)
);

-- === TOKEN TRACKING ===
CREATE TABLE IF NOT EXISTS token_stats (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    timestamp      TEXT    NOT NULL,
    operation      TEXT    NOT NULL,
    project        TEXT    NOT NULL DEFAULT 'default',
    tokens_input   INTEGER NOT NULL DEFAULT 0,
    tokens_output  INTEGER NOT NULL DEFAULT 0,
    tokens_saved   INTEGER NOT NULL DEFAULT 0,
    memories_count INTEGER NOT NULL DEFAULT 0,
    metadata       TEXT    NOT NULL DEFAULT '{}'
);

-- === MAINTENANCE ===
CREATE TABLE IF NOT EXISTS maintenance_log (
    project     TEXT NOT NULL,
    task        TEXT NOT NULL,
    last_run_at TEXT NOT NULL,
    PRIMARY KEY(project, task)
);

-- === SYNC ===
CREATE TABLE IF NOT EXISTS sync_chunks (
    chunk_hash  TEXT PRIMARY KEY,
    exported_at TEXT NOT NULL,
    memory_ids  TEXT NOT NULL DEFAULT '[]'
);

-- === INDICES ===
CREATE INDEX IF NOT EXISTS idx_memories_project_valid ON memories(project, invalid_at, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_memories_content_hash ON memories(project, content_hash);
CREATE INDEX IF NOT EXISTS idx_memories_kind ON memories(project, kind);
CREATE INDEX IF NOT EXISTS idx_memories_importance ON memories(project, importance DESC);
CREATE INDEX IF NOT EXISTS idx_entities_project_name ON entities(project, canonical_name);
CREATE INDEX IF NOT EXISTS idx_relations_project_predicate ON relations(project, predicate, invalid_at);
CREATE INDEX IF NOT EXISTS idx_memory_entities_entity ON memory_entities(entity_id, memory_id);
CREATE INDEX IF NOT EXISTS idx_relation_supports_subject ON relation_supports(project, subject_entity_id, predicate);
CREATE INDEX IF NOT EXISTS idx_relation_supports_memory ON relation_supports(source_memory_id);
CREATE INDEX IF NOT EXISTS idx_entity_refs_memory ON entity_references(memory_id);
CREATE INDEX IF NOT EXISTS idx_token_stats_ts ON token_stats(timestamp);
`
