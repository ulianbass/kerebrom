"""Sopor Plenus — transcript consolidation for Kerebrom.

Reads Claude Code JSONL transcripts, extracts meaningful memories
(decisions, changes, bugs, preferences, conclusions), and stores them
in Kerebrom.  Analogous to Anthropic's "Auto Dream" but:

  * Works with any MCP-compatible tool (not just Claude Code).
  * Stores into Kerebrom's semantic/graph engine instead of flat .md files.
  * Can run mid-session (via hook) or post-session (CLI / MCP tool).

Naming:  Latin *sopor* = deep sleep — the phase where the brain
consolidates short-term experience into long-term memory.

Usage:
    # CLI
    kerebrom sopor --session <session-id-or-path>
    kerebrom sopor --latest
    kerebrom sopor --all

    # MCP tool (from Claude Code)
    sopor(session="latest")
"""

from __future__ import annotations

import json
import re
import sys
from dataclasses import dataclass, field
from datetime import datetime, timezone
from pathlib import Path
from typing import Any, Dict, List, Optional

from .store import DEFAULT_PROJECT, KerebromStore

# ── Constants ───────────────────────────────────────────────────────────

# Claude Code stores transcripts here (per-project).
_CLAUDE_PROJECTS_DIR = Path.home() / ".claude" / "projects"

# Minimum transcript size (bytes) worth processing.
_MIN_TRANSCRIPT_BYTES = 5_000

# Maximum characters of text per message to include in extraction.
_MAX_MSG_CHARS = 3000

# Tags for Sopor-generated memories.
_SOPOR_SOURCE = "sopor"


# ── Data structures ─────────────────────────────────────────────────────


@dataclass
class TranscriptMessage:
    """A single turn extracted from a JSONL transcript."""

    role: str  # "user", "assistant", "system"
    text: str  # Combined text content (tool calls stripped)
    tool_names: List[str] = field(default_factory=list)  # Tools invoked
    timestamp: str = ""
    index: int = 0  # Position in transcript


@dataclass
class SoporResult:
    """Result of a Sopor consolidation run."""

    session_id: str
    transcript_path: str
    messages_read: int
    memories_extracted: int
    memories_stored: int
    memories_skipped_duplicate: int
    errors: List[str] = field(default_factory=list)


# ── Transcript parsing ──────────────────────────────────────────────────


def find_transcripts(
    project_path: Optional[str] = None,
) -> List[Path]:
    """Find all JSONL transcript files, sorted newest first."""
    if project_path:
        # Encode path the way Claude Code does: slashes → dashes.
        encoded = project_path.replace("/", "-")
        # Leading dash is expected for absolute paths (e.g. /Users → -Users).
        search_dir = _CLAUDE_PROJECTS_DIR / encoded
        if search_dir.is_dir():
            paths = list(search_dir.glob("*.jsonl"))
        else:
            paths = []
    else:
        # Search all project directories.
        paths = list(_CLAUDE_PROJECTS_DIR.glob("*/*.jsonl"))

    # Sort by modification time, newest first.
    paths.sort(key=lambda p: p.stat().st_mtime, reverse=True)
    return paths


def find_latest_transcript(project_path: Optional[str] = None) -> Optional[Path]:
    """Find the most recently modified transcript."""
    transcripts = find_transcripts(project_path)
    return transcripts[0] if transcripts else None


def parse_transcript(path: Path) -> List[TranscriptMessage]:
    """Parse a Claude Code JSONL transcript into structured messages.

    Only extracts user messages and assistant text responses — tool call
    details (inputs/outputs) are omitted to keep the extraction focused
    on conversational content where decisions and context live.
    """
    messages: List[TranscriptMessage] = []
    index = 0

    with open(path, "r", encoding="utf-8") as fh:
        for line in fh:
            line = line.strip()
            if not line:
                continue

            try:
                obj = json.loads(line)
            except json.JSONDecodeError:
                continue

            msg_type = obj.get("type", "")
            timestamp = obj.get("timestamp", "")

            if msg_type not in ("user", "assistant", "system"):
                continue

            msg = obj.get("message", {})
            role = msg.get("role", msg_type)
            content = msg.get("content", "")

            text_parts: List[str] = []
            tool_names: List[str] = []

            if isinstance(content, str):
                # Strip system-reminder tags — they're noise.
                cleaned = re.sub(
                    r"<system-reminder>.*?</system-reminder>",
                    "",
                    content,
                    flags=re.DOTALL,
                )
                cleaned = cleaned.strip()
                if cleaned:
                    text_parts.append(cleaned[:_MAX_MSG_CHARS])

            elif isinstance(content, list):
                for block in content:
                    if not isinstance(block, dict):
                        continue
                    btype = block.get("type", "")
                    if btype == "text":
                        text = block.get("text", "").strip()
                        if text:
                            text_parts.append(text[:_MAX_MSG_CHARS])
                    elif btype == "tool_use":
                        tool_names.append(block.get("name", "unknown"))

            combined_text = "\n".join(text_parts).strip()
            if not combined_text and not tool_names:
                continue

            messages.append(
                TranscriptMessage(
                    role=role,
                    text=combined_text,
                    tool_names=tool_names,
                    timestamp=timestamp,
                    index=index,
                )
            )
            index += 1

    return messages


# ── Memory extraction ───────────────────────────────────────────────────

# Patterns that signal high-value content worth extracting.
_DECISION_SIGNALS = [
    r"\bdecid[ióe]",
    r"\belegimos?\b",
    r"\bvamos (a|con)\b",
    r"\bse rechaz[óa]",
    r"\bno (?:vamos|quiero|queremos)\b",
    r"\bmejor (?:usar|optar|ir)\b",
    r"\bdecision\b",
    r"\barchitecture\b",
    r"\bdiseño\b",
    r"\brediseñ",
    r"\bcambi(?:amos|é|ó|ar)\b",
    r"\bfix(?:ed)?\b",
    r"\bbug\b",
    r"\berror\b",
    r"\bproblem(?:a)?\b",
    r"\bsolución\b",
    r"\bresult(?:ado|s)?\b",
    r"\bconfirm(?:ado|ed)?\b",
    r"\brecuerda\b",
    r"\bimportante\b",
    r"\bconclu(?:sión|imos)\b",
    r"\bpreferencia\b",
    r"\bsiempre\b.*\b(?:usar|hacer)\b",
    r"\bnunca\b.*\b(?:usar|hacer)\b",
    r"\bfunciona\b",
    r"\blisto\b",
    r"\bversión\b",
    r"\bdeployment\b",
    r"\bdespliegue\b",
    r"\bproducción\b",
]
_DECISION_RE = re.compile("|".join(_DECISION_SIGNALS), re.IGNORECASE)


def _score_message(msg: TranscriptMessage) -> float:
    """Score a message by its consolidation value (0.0 – 1.0)."""
    score = 0.0

    # User messages that express decisions/preferences are high value.
    if msg.role == "user":
        score += 0.3
        if len(msg.text) > 100:
            score += 0.1
    elif msg.role == "assistant":
        score += 0.1

    # Decision-signal keywords.
    matches = _DECISION_RE.findall(msg.text)
    score += min(len(matches) * 0.1, 0.4)

    # Longer messages tend to carry more substance.
    if len(msg.text) > 200:
        score += 0.1

    return min(score, 1.0)


def _is_summary_message(text: str) -> bool:
    """Detect compaction summaries injected by Claude Code."""
    return text.startswith(
        "This session is being continued from a previous conversation"
    )


def extract_consolidation_blocks(
    messages: List[TranscriptMessage],
    score_threshold: float = 0.35,
) -> List[Dict[str, Any]]:
    """Extract high-value conversational blocks from parsed messages.

    Returns a list of blocks, each containing:
      - messages: list of sequential high-scoring messages (context window)
      - score: aggregate score
      - tags: inferred tags
    """
    blocks: List[Dict[str, Any]] = []
    window: List[TranscriptMessage] = []
    window_score = 0.0

    for msg in messages:
        # Skip compaction summaries — they're already lossy.
        if _is_summary_message(msg.text):
            continue

        # Skip very short system messages.
        if msg.role == "system" and len(msg.text) < 30:
            continue

        score = _score_message(msg)

        if score >= score_threshold:
            window.append(msg)
            window_score = max(window_score, score)
        else:
            if window:
                blocks.append(_finalize_block(window, window_score))
                window = []
                window_score = 0.0

    # Flush remaining window.
    if window:
        blocks.append(_finalize_block(window, window_score))

    return blocks


def _finalize_block(
    window: List[TranscriptMessage],
    score: float,
) -> Dict[str, Any]:
    """Build a consolidation block from a message window."""
    combined = "\n".join(
        "[{}] {}".format(m.role, m.text[:1500]) for m in window
    )

    # Infer tags from content.
    tags = _infer_tags(combined)

    return {
        "text": combined,
        "score": score,
        "tags": tags,
        "msg_count": len(window),
        "first_index": window[0].index,
        "last_index": window[-1].index,
    }


def _infer_tags(text: str) -> List[str]:
    """Infer observation tags from block content."""
    tags: List[str] = []
    lower = text.lower()

    if any(w in lower for w in ("decidí", "decidió", "elegimos", "vamos con", "mejor usar", "decision", "diseño")):
        tags.append("decision")
    if any(w in lower for w in ("bug", "error", "fix", "broke", "rompió", "solución")):
        tags.append("bugfix")
    if any(w in lower for w in ("feature", "nueva funcionalidad", "agregar", "implementar", "nuevo")):
        tags.append("feature")
    if any(w in lower for w in ("refactor", "limpiar", "reorganizar", "simplificar")):
        tags.append("refactor")
    if any(w in lower for w in ("descubrí", "discovery", "hallazgo", "resulta que", "encontré")):
        tags.append("discovery")
    if any(w in lower for w in ("cambié", "cambió", "cambiamos", "rename", "migrar", "change")):
        tags.append("change")
    if any(w in lower for w in ("prefiero", "preferencia", "siempre", "nunca", "no quiero", "preference")):
        tags.append("preference")
    if any(w in lower for w in ("diseño", "design", "layout", "visual", "UI", "UX", "frontend", "estilo")):
        tags.append("design")

    return tags or ["discovery"]


# ── Summarization ───────────────────────────────────────────────────────

def summarize_block(block: Dict[str, Any]) -> str:
    """Produce a concise memory from a consolidation block.

    This is a heuristic summarizer that extracts the core content.
    A future version could use an LLM for better summaries, but for
    v1 we keep it dependency-free and fast.
    """
    text = block["text"]
    lines = text.split("\n")

    # Extract just the most substantive lines.
    key_lines: List[str] = []
    for line in lines:
        stripped = line.strip()
        if not stripped:
            continue
        # Prefer user messages (they express intent/decisions).
        if stripped.startswith("[user]"):
            content = stripped[6:].strip()
            if len(content) > 20:
                key_lines.append(content)
        # Assistant conclusions (shorter, usually contain the result).
        elif stripped.startswith("[assistant]"):
            content = stripped[11:].strip()
            # Only include if it looks like a conclusion, not code.
            if len(content) > 30 and not content.startswith("{") and not content.startswith("```"):
                key_lines.append(content)

    if not key_lines:
        # Fallback: take the first substantial chunk.
        return text[:500]

    # Combine, but cap total length.
    combined = "\n".join(key_lines)
    if len(combined) > 1500:
        combined = combined[:1500] + "..."

    return combined


# ── Core consolidation engine ───────────────────────────────────────────


def _is_duplicate(store: KerebromStore, content: str, project: str) -> bool:
    """Check if a very similar memory already exists."""
    try:
        existing = store.recall(query=content, project=project, limit=1)
        if existing and existing[0].semantic_similarity > 0.85:
            return True
    except Exception:
        pass
    return False


def run_sopor(
    store: KerebromStore,
    transcript_path: Path,
    project: str = DEFAULT_PROJECT,
    score_threshold: float = 0.35,
    skip_duplicates: bool = True,
) -> SoporResult:
    """Run Sopor Plenus on a single transcript.

    1. Parse the JSONL transcript into messages.
    2. Score and extract high-value blocks.
    3. Summarize each block.
    4. Store as memories in Kerebrom (deduplicating against existing).
    """
    session_id = transcript_path.stem

    result = SoporResult(
        session_id=session_id,
        transcript_path=str(transcript_path),
        messages_read=0,
        memories_extracted=0,
        memories_stored=0,
        memories_skipped_duplicate=0,
    )

    # 1. Parse
    try:
        messages = parse_transcript(transcript_path)
    except Exception as exc:
        result.errors.append("Parse error: {}".format(exc))
        return result

    result.messages_read = len(messages)

    if not messages:
        return result

    # 2. Extract blocks
    blocks = extract_consolidation_blocks(messages, score_threshold)
    result.memories_extracted = len(blocks)

    # 3. Summarize and store
    for block in blocks:
        summary = summarize_block(block)
        if len(summary.strip()) < 20:
            continue

        # Deduplicate against existing memories.
        if skip_duplicates and _is_duplicate(store, summary, project):
            result.memories_skipped_duplicate += 1
            continue

        tags = block.get("tags", [])
        if "sopor" not in tags:
            tags.append("sopor")

        importance = min(0.4 + block["score"] * 0.4, 0.8)

        try:
            store.remember(
                content=summary,
                project=project,
                kind="episodic",
                source=_SOPOR_SOURCE,
                importance=importance,
                confidence=0.7,
                tags=tags,
            )
            result.memories_stored += 1
        except Exception as exc:
            result.errors.append("Store error: {}".format(exc))

    return result


def run_sopor_latest(
    store: KerebromStore,
    project_path: Optional[str] = None,
    project: str = DEFAULT_PROJECT,
    score_threshold: float = 0.35,
) -> Optional[SoporResult]:
    """Run Sopor on the most recent transcript."""
    path = find_latest_transcript(project_path)
    if not path:
        return None
    if path.stat().st_size < _MIN_TRANSCRIPT_BYTES:
        return None
    return run_sopor(store, path, project, score_threshold)


def run_sopor_all(
    store: KerebromStore,
    project_path: Optional[str] = None,
    project: str = DEFAULT_PROJECT,
    score_threshold: float = 0.35,
) -> List[SoporResult]:
    """Run Sopor on all transcripts for a project (or all projects)."""
    paths = find_transcripts(project_path)
    results: List[SoporResult] = []

    for path in paths:
        if path.stat().st_size < _MIN_TRANSCRIPT_BYTES:
            continue
        result = run_sopor(store, path, project, score_threshold)
        results.append(result)

    return results
