# Copyright (c) 2026 Ulian Bass. All rights reserved.
# This software is proprietary. See LICENSE for terms.

"""Hook de cierre de sesion para Claude Code.

Al cerrar una sesion sustancial, ejecuta Sopor para consolidar
el transcript en memorias destiladas. NO captura texto crudo —
la captura automatica genera basura (leccion aprendida, abr/2026).
"""

from __future__ import annotations

import json
import sys
from pathlib import Path
from typing import Optional

from .store import DEFAULT_PROJECT, KerebromStore


# Tamano minimo del transcript para disparar Sopor (bytes).
_SOPOR_AUTO_THRESHOLD = 100_000  # ~100KB = conversacion sustancial


def handle_capture(
    db_path: str,
    project: str = DEFAULT_PROJECT,
    passphrase: Optional[str] = None,
) -> int:
    """Procesa eventos de hooks de Claude Code.

    Solo actua en eventos Stop con sesion sustancial: ejecuta Sopor
    para destilar el transcript en memorias de calidad.
    Retorna 0 siempre (los hooks no deben bloquear al AI).
    """
    try:
        raw = sys.stdin.read().strip()
        if not raw:
            return 0
        event = json.loads(raw)
    except (json.JSONDecodeError, OSError):
        return 0

    # Solo actuar en eventos Stop (cierre de sesion).
    stop_reason = event.get("stop_reason", "")
    session_id = event.get("session_id", "")
    if stop_reason and session_id:
        try:
            _trigger_sopor(db_path, session_id, project, passphrase)
        except Exception:
            pass

    return 0


def _trigger_sopor(
    db_path: str,
    session_id: str,
    project: str,
    passphrase: Optional[str],
) -> None:
    """Ejecuta Sopor en el transcript de la sesion si es sustancial."""
    from .sopor import run_sopor

    claude_projects = Path.home() / ".claude" / "projects"
    candidates = list(claude_projects.rglob("{}.jsonl".format(session_id)))

    if not candidates:
        return

    transcript = candidates[0]

    if transcript.stat().st_size < _SOPOR_AUTO_THRESHOLD:
        return

    store = KerebromStore(Path(db_path), passphrase=passphrase)
    store.initialize(project=project)
    try:
        run_sopor(store, transcript, project=project)
    finally:
        store.close()
