"""Passive capture hooks for Claude Code and other AI tools.

Claude Code hooks send JSON on stdin describing tool calls and their results.
This module parses that JSON and stores relevant information as memories
in Kerebrom — no explicit "remember" call needed.

Hook events (PostToolUse):
    {
        "session_id": "...",
        "tool_name": "Write" | "Edit" | "Bash" | ...,
        "tool_input": { ... },
        "tool_output": "...",
        ...
    }

Hook events (Stop):
    {
        "session_id": "...",
        "stop_reason": "end_turn",
        "message": "...",
        ...
    }
"""

from __future__ import annotations

import json
import sys
from pathlib import Path
from typing import Any, Dict, Optional

from .store import DEFAULT_PROJECT, KerebromStore

# Tools worth capturing memories from.
_INTERESTING_TOOLS = {"Write", "Edit", "Bash", "NotebookEdit"}

# Maximum content length for a single capture (avoid storing huge outputs).
_MAX_CONTENT_LEN = 2000


def _summarize_tool_event(event: Dict[str, Any]) -> Optional[str]:
    """Extract a meaningful memory from a hook event, or None to skip."""
    tool_name = event.get("tool_name", "")

    # PostToolUse events
    if tool_name in _INTERESTING_TOOLS:
        tool_input = event.get("tool_input", {})

        if tool_name in ("Write", "Edit"):
            file_path = tool_input.get("file_path", "unknown")
            description = tool_input.get("description", "")
            if description:
                return "[auto-capture] {} editado: {}".format(file_path, description)
            old = tool_input.get("old_string", "")
            new = tool_input.get("new_string", "")
            if old and new:
                return "[auto-capture] {} modificado: reemplazó código".format(file_path)
            content = tool_input.get("content", "")
            if content:
                return "[auto-capture] {} creado/escrito".format(file_path)
            return "[auto-capture] {} editado".format(file_path)

        if tool_name == "Bash":
            command = tool_input.get("command", "")
            if not command:
                return None
            # Skip trivial commands
            if command.strip() in ("ls", "pwd", "cd"):
                return None
            output = str(event.get("tool_output", ""))[:500]
            return "[auto-capture] Ejecutado: {} → {}".format(
                command[:200], output[:200] if output else "(sin salida)"
            )

        if tool_name == "NotebookEdit":
            return "[auto-capture] Celda de notebook editada"

    # Stop events — capture session summary if present
    stop_reason = event.get("stop_reason", "")
    if stop_reason:
        message = str(event.get("message", ""))[:_MAX_CONTENT_LEN]
        if message and len(message) > 50:
            return "[auto-capture] Sesión finalizada: {}".format(message[:500])

    return None


def handle_capture(
    db_path: str,
    project: str = DEFAULT_PROJECT,
    passphrase: Optional[str] = None,
) -> int:
    """Read hook event JSON from stdin, store as memory if interesting.

    Called by: kerebrom capture
    Returns 0 on success (even if event was skipped).
    """
    try:
        raw = sys.stdin.read().strip()
        if not raw:
            return 0

        event = json.loads(raw)
    except (json.JSONDecodeError, OSError):
        # Malformed input — silently ignore (hooks must not block the AI tool).
        return 0

    summary = _summarize_tool_event(event)
    if not summary:
        return 0

    # Truncate to max length.
    if len(summary) > _MAX_CONTENT_LEN:
        summary = summary[:_MAX_CONTENT_LEN]

    try:
        store = KerebromStore(Path(db_path), passphrase=passphrase)
        store.initialize(project=project)
        store.remember(
            content=summary,
            project=project,
            kind="episodic",
            source="hook",
            importance=0.3,
            confidence=0.7,
            tags=["auto-capture"],
        )
        store.close()
    except Exception:
        # Never block the AI tool if capture fails.
        pass

    return 0
