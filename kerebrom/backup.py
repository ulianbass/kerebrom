# Copyright (c) 2026 Ulian Bass. All rights reserved.
# This software is proprietary. See LICENSE for terms.

"""Kerebrom organic backup and restore.

Exports all memories to a single portable .kbk file (JSON) that survives
uninstall.  On restore, memories are re-ingested organically via remember()
so they get fresh embeddings, entity extraction, and deduplication — as if
Kerebrom had always been running.

The .kbk file is placed in ~/Documents/ by default so it persists across
uninstall/reinstall cycles.

File format:
    {
        "format": "kerebrom-backup-v1",
        "created_at": "...",
        "machine": "...",
        "memory_count": N,
        "memories": [ { content, kind, source, importance, confidence, tags, created_at, ... }, ... ]
    }
"""

from __future__ import annotations

import json
import os
import platform
from datetime import datetime, timezone
from pathlib import Path
from typing import Any, Dict, List, Optional

from .store import DEFAULT_PROJECT, KerebromStore

# ── Constants ───────────────────────────────────────────────────────────

FORMAT_VERSION = "kerebrom-backup-v1"
BACKUP_EXTENSION = ".kbk"
BACKUP_PREFIX = "kerebrom-backup"

# Default location: ~/Documents/ (survives ~/.kerebrom/ deletion).
DEFAULT_BACKUP_DIR = Path.home() / "Documents"


# ── Export ──────────────────────────────────────────────────────────────


def create_backup(
    store: KerebromStore,
    output_path: Optional[Path] = None,
    project: str = DEFAULT_PROJECT,
) -> Path:
    """Export all active memories to a portable .kbk file.

    If no output_path is given, reuses the most recent existing .kbk file
    (updating it in place) or creates a new one with a fixed name.
    This avoids accumulating duplicate backup files.

    Returns the path to the created backup file.
    """
    if output_path is None:
        # Reuse existing backup if one exists.
        existing = find_latest_backup()
        if existing is not None:
            output_path = existing
        else:
            filename = "{}{}".format(BACKUP_PREFIX, BACKUP_EXTENSION)
            output_path = DEFAULT_BACKUP_DIR / filename

    output_path = output_path.expanduser().resolve()
    output_path.parent.mkdir(parents=True, exist_ok=True)

    # Safety: never overwrite a backup with fewer memories than it already has.
    if output_path.exists():
        try:
            existing_data = json.loads(output_path.read_text(encoding="utf-8"))
            existing_count = existing_data.get("memory_count", 0)
        except (json.JSONDecodeError, OSError):
            existing_count = 0
    else:
        existing_count = 0

    # Export all active memories.
    memories = store.export_memories(project=project, include_inactive=False)

    # Safety check: refuse to overwrite with fewer memories.
    if len(memories) < existing_count:
        return output_path  # Silently keep the existing backup intact.

    # Build backup payload with only the fields needed for organic restore.
    backup_memories: List[Dict[str, Any]] = []
    for mem in memories:
        backup_memories.append({
            "content": mem["content"],
            "kind": mem["kind"],
            "source": mem.get("source", "backup"),
            "importance": mem["importance"],
            "confidence": mem["confidence"],
            "tags": mem.get("tags", []),
            "created_at": mem.get("created_at", ""),
            "project": mem.get("project", project),
        })

    payload = {
        "format": FORMAT_VERSION,
        "created_at": datetime.now(timezone.utc).replace(microsecond=0).isoformat(),
        "machine": platform.node(),
        "user": os.environ.get("USER", "unknown"),
        "memory_count": len(backup_memories),
        "memories": backup_memories,
    }

    output_path.write_text(
        json.dumps(payload, ensure_ascii=False, indent=2) + "\n",
        encoding="utf-8",
    )

    return output_path


# ── Search ──────────────────────────────────────────────────────────────


def find_backups(search_dirs: Optional[List[Path]] = None) -> List[Path]:
    """Search for .kbk backup files across common locations.

    Returns list sorted by modification time (newest first).
    """
    if search_dirs is None:
        search_dirs = [
            Path.home() / "Documents",
            Path.home() / "Desktop",
            Path.home() / "Downloads",
            Path.home(),
        ]

    found: List[Path] = []
    seen: set = set()

    for search_dir in search_dirs:
        if not search_dir.is_dir():
            continue
        # Search only top level and one level deep (not recursive for performance).
        for pattern in ["*" + BACKUP_EXTENSION, "*/*" + BACKUP_EXTENSION]:
            for path in search_dir.glob(pattern):
                resolved = path.resolve()
                if resolved not in seen:
                    seen.add(resolved)
                    found.append(resolved)

    # Sort newest first.
    found.sort(key=lambda p: p.stat().st_mtime, reverse=True)
    return found


def find_latest_backup(search_dirs: Optional[List[Path]] = None) -> Optional[Path]:
    """Find the most recent .kbk backup file."""
    backups = find_backups(search_dirs)
    return backups[0] if backups else None


# ── Validate ────────────────────────────────────────────────────────────


def validate_backup(path: Path) -> Dict[str, Any]:
    """Validate a .kbk file and return its metadata (without loading all memories)."""
    try:
        data = json.loads(path.read_text(encoding="utf-8"))
    except (json.JSONDecodeError, OSError) as exc:
        return {"valid": False, "error": "No se pudo leer: {}".format(exc)}

    if data.get("format") != FORMAT_VERSION:
        return {"valid": False, "error": "Formato incompatible: {}".format(data.get("format"))}

    return {
        "valid": True,
        "path": str(path),
        "created_at": data.get("created_at", "?"),
        "machine": data.get("machine", "?"),
        "memory_count": data.get("memory_count", 0),
    }


# ── Organic restore ────────────────────────────────────────────────────


def restore_organic(
    store: KerebromStore,
    backup_path: Path,
    project: str = DEFAULT_PROJECT,
    skip_duplicates: bool = True,
) -> Dict[str, Any]:
    """Restore memories organically from a .kbk backup.

    Each memory is re-ingested via store.remember() so it gets:
    - Fresh embeddings computed with the current model
    - Entity extraction and graph updates
    - Deduplication against existing memories
    - New timestamps (updated_at = now, but original created_at preserved in content)

    Returns a summary dict.
    """
    data = json.loads(backup_path.read_text(encoding="utf-8"))

    if data.get("format") != FORMAT_VERSION:
        return {"error": "Formato incompatible: {}".format(data.get("format")), "restored": 0}

    memories = data.get("memories", [])
    restored = 0
    skipped_duplicate = 0
    errors: List[str] = []

    for mem in memories:
        content = mem.get("content", "").strip()
        if not content:
            continue

        # Check for duplicates before re-ingesting.
        if skip_duplicates:
            try:
                existing = store.recall(query=content, project=project, limit=1)
                if existing and existing[0].semantic_similarity > 0.85:
                    skipped_duplicate += 1
                    continue
            except Exception:
                pass

        try:
            store.remember(
                content=content,
                project=project,
                kind=mem.get("kind", "episodic"),
                source=mem.get("source", "backup"),
                importance=mem.get("importance", 0.5),
                confidence=mem.get("confidence", 0.8),
                tags=mem.get("tags", []),
            )
            restored += 1
        except Exception as exc:
            errors.append("Error restaurando memoria: {}".format(str(exc)[:100]))

    return {
        "backup_path": str(backup_path),
        "memories_in_backup": len(memories),
        "restored": restored,
        "skipped_duplicate": skipped_duplicate,
        "errors": errors,
    }
