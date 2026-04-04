# Copyright (c) 2026 Ulian Bass. All rights reserved.
# This software is proprietary. See LICENSE for terms.

"""Heuristic capture for entities and relations."""

from __future__ import annotations

import re
from dataclasses import dataclass
from typing import List, Set

ENTITY_RE = re.compile(
    r"\b[A-ZÁÉÍÓÚÜÑ][a-záéíóúüñ]+(?:\s+[A-ZÁÉÍÓÚÜÑ][a-záéíóúüñ]+)*\b"
)

COMMON_ENTITY_WORDS = {
    "A",
    "Al",
    "And",
    "Con",
    "De",
    "El",
    "Ella",
    "En",
    "I",
    "La",
    "Las",
    "Le",
    "Lo",
    "Los",
    "Me",
    "Mi",
    "Por",
    "The",
    "Yo",
}

LEADING_NOISE_WORDS = {
    "Aprendo",
    "Building",
    "Conozco",
    "Domino",
    "Estoy",
    "Learning",
    "Me",
    "Necesito",
    "Prefiero",
    "Quiero",
    "Resido",
    "Soy",
    "Tengo",
    "Trabajo",
    "Uso",
    "Utilizo",
    "Vengo",
    "Vivo",
    "Want",
    "Work",
    "Working",
}


@dataclass(frozen=True)
class RelationCandidate:
    predicate: str
    object_value: str
    confidence: float
    entity_type: str


RELATION_PATTERNS = [
    # --- Identity ---
    (
        "name",
        "person",
        0.95,
        re.compile(
            r"\b(?:me llamo|mi nombre es|i am|my name is)\s+(?P<object>[A-ZÁÉÍÓÚÜÑ][\wÁÉÍÓÚÜÑáéíóúüñ-]+(?:\s+[A-ZÁÉÍÓÚÜÑ][\wÁÉÍÓÚÜÑáéíóúüñ-]+)*)",
            re.IGNORECASE,
        ),
    ),
    # --- Location ---
    (
        "lives_in",
        "location",
        0.92,
        re.compile(
            r"\b(?:vivo en|vivo por|i live in|live in|resido en)\s+(?P<object>[A-ZÁÉÍÓÚÜÑ][^,.!;]+)",
            re.IGNORECASE,
        ),
    ),
    (
        "from",
        "location",
        0.85,
        re.compile(
            r"\b(?:soy de|i(?:'m| am) from|vengo de|nací en|i was born in)\s+(?P<object>[A-ZÁÉÍÓÚÜÑ][^,.!;]+)",
            re.IGNORECASE,
        ),
    ),
    # --- Work & study ---
    (
        "works_at",
        "organization",
        0.9,
        re.compile(
            r"\b(?:trabajo en|trabajo para|i work at|i work for|work at|work for)\s+(?P<object>[A-ZÁÉÍÓÚÜÑ][^,.!;]+)",
            re.IGNORECASE,
        ),
    ),
    (
        "role",
        "concept",
        0.88,
        re.compile(
            r"\b(?:soy\s+(?!de\b)|i(?:'m| am) a|i(?:'m| am) an|mi rol es|my role is|trabajo como|trabajo de)\s+(?P<object>[^,.!;]+)",
            re.IGNORECASE,
        ),
    ),
    (
        "studied",
        "concept",
        0.82,
        re.compile(
            r"\b(?:estudié|estudie|i studied|i majored in|estudié en|i went to)\s+(?P<object>[^,.!;]+)",
            re.IGNORECASE,
        ),
    ),
    # --- Preferences ---
    (
        "prefers",
        "concept",
        0.75,
        re.compile(r"\b(?:prefiero|i prefer|me inclino por)\s+(?P<object>[^,.!;]+)", re.IGNORECASE),
    ),
    (
        "likes",
        "concept",
        0.72,
        re.compile(r"\b(?:me gusta|me gustan|me encanta|me encantan|i like|i love|i enjoy)\s+(?P<object>[^,.!;]+)", re.IGNORECASE),
    ),
    (
        "dislikes",
        "concept",
        0.72,
        re.compile(r"\b(?:no me gusta|odio|detesto|i hate|i dislike|i don't like)\s+(?P<object>[^,.!;]+)", re.IGNORECASE),
    ),
    # --- Skills & learning ---
    (
        "learning",
        "concept",
        0.78,
        re.compile(
            r"\b(?:estoy aprendiendo|aprendo|i(?:'m| am) learning|learning)\s+(?P<object>[^,.!;]+)",
            re.IGNORECASE,
        ),
    ),
    (
        "uses",
        "concept",
        0.76,
        re.compile(
            r"\b(?:uso|utilizo|i use|i(?:'m| am) using|we use|usamos)\s+(?P<object>[^,.!;]+)",
            re.IGNORECASE,
        ),
    ),
    (
        "knows",
        "concept",
        0.74,
        re.compile(
            r"\b(?:sé|conozco|domino|i know|i(?:'m| am) fluent in|i(?:'m| am) experienced with)\s+(?P<object>[^,.!;]+)",
            re.IGNORECASE,
        ),
    ),
    # --- Possessions & relations ---
    (
        "has",
        "concept",
        0.70,
        re.compile(
            r"\b(?:tengo|i have|i(?:'ve| have) got)\s+(?P<object>[^,.!;]+)",
            re.IGNORECASE,
        ),
    ),
    (
        "needs",
        "concept",
        0.68,
        re.compile(
            r"\b(?:necesito|i need|requiero)\s+(?P<object>[^,.!;]+)",
            re.IGNORECASE,
        ),
    ),
    (
        "wants",
        "concept",
        0.65,
        re.compile(
            r"\b(?:quiero|quisiera|i want|i(?:'d| would) like)\s+(?P<object>[^,.!;]+)",
            re.IGNORECASE,
        ),
    ),
    # --- Projects & building ---
    (
        "building",
        "concept",
        0.80,
        re.compile(
            r"\b(?:estoy construyendo|estoy creando|estoy haciendo|i(?:'m| am) building|i(?:'m| am) creating|i(?:'m| am) making|i(?:'m| am) working on)\s+(?P<object>[^,.!;]+)",
            re.IGNORECASE,
        ),
    ),
]


def canonicalize_entity(name: str) -> str:
    return re.sub(r"\s+", " ", name).strip()


def extract_entities(text: str) -> List[str]:
    entities: Set[str] = set()
    for match in ENTITY_RE.findall(text):
        candidate = canonicalize_entity(match)
        if candidate in COMMON_ENTITY_WORDS:
            continue
        if len(candidate) <= 1:
            continue
        parts = candidate.split(" ")
        if len(parts) > 1 and parts[0] in LEADING_NOISE_WORDS:
            continue
        entities.add(candidate)

    for relation in extract_relation_candidates(text):
        entities.add(canonicalize_entity(relation.object_value))

    return sorted(entities)


def extract_relation_candidates(text: str) -> List[RelationCandidate]:
    relations: List[RelationCandidate] = []
    for predicate, entity_type, confidence, pattern in RELATION_PATTERNS:
        for match in pattern.finditer(text):
            object_value = canonicalize_entity(match.group("object"))
            if not object_value:
                continue
            relations.append(
                RelationCandidate(
                    predicate=predicate,
                    object_value=object_value,
                    confidence=confidence,
                    entity_type=entity_type,
                )
            )
    return relations
