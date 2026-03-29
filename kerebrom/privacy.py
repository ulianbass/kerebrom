"""Sensitive data scrubbing and detection."""

from __future__ import annotations

import re
from dataclasses import dataclass
from typing import List


@dataclass(frozen=True)
class SensitiveMatch:
    label: str
    value: str


SENSITIVE_PATTERNS = [
    ("email", re.compile(r"\b[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}\b", re.IGNORECASE)),
    ("api_key", re.compile(r"\b(?:sk|rk|pk)_[A-Za-z0-9]{10,}\b")),
    ("bearer_token", re.compile(r"\bBearer\s+[A-Za-z0-9._-]{12,}\b", re.IGNORECASE)),
    ("assignment_secret", re.compile(r"\b(?:api[_-]?key|token|secret|password)\s*[:=]\s*([^\s,;]+)", re.IGNORECASE)),
    ("jwt", re.compile(r"\beyJ[A-Za-z0-9_-]+\.[A-Za-z0-9._-]+\.[A-Za-z0-9._-]+\b")),
]


def scrub_sensitive(text: str) -> tuple[str, List[SensitiveMatch]]:
    redacted = text
    matches: List[SensitiveMatch] = []

    for label, pattern in SENSITIVE_PATTERNS:
        found = pattern.findall(redacted)
        if not found:
            continue

        normalized = found if isinstance(found, list) else [found]
        for item in normalized:
            if isinstance(item, tuple):
                value = next((part for part in item if part), "")
            else:
                value = item
            matches.append(SensitiveMatch(label=label, value=str(value)))

        def _replace(match: re.Match[str]) -> str:
            return "[REDACTED:{}]".format(label.upper())

        redacted = pattern.sub(_replace, redacted)

    return redacted, matches
