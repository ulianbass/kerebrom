"""Deterministic local embeddings with no external dependencies."""

from __future__ import annotations

import hashlib
import math
import re
from typing import Iterable, List, Sequence

TOKEN_RE = re.compile(r"[0-9A-Za-zÁÉÍÓÚÜÑáéíóúüñ_+-]+")


def tokenize(text: str) -> List[str]:
    return [token.lower() for token in TOKEN_RE.findall(text)]


class HashEmbeddingModel:
    """Feature hashing embeddings stable across runs."""

    def __init__(self, dimensions: int = 256) -> None:
        self.dimensions = dimensions

    def embed(self, text: str) -> List[float]:
        vector = [0.0] * self.dimensions
        tokens = tokenize(text)
        if not tokens:
            return vector

        for token in tokens:
            digest = hashlib.blake2b(token.encode("utf-8"), digest_size=16).digest()
            for offset in range(0, len(digest), 2):
                bucket = ((digest[offset] << 8) | digest[offset + 1]) % self.dimensions
                sign = 1.0 if digest[offset] & 1 else -1.0
                vector[bucket] += sign

        norm = math.sqrt(sum(value * value for value in vector))
        if norm == 0:
            return vector
        return [value / norm for value in vector]


def cosine_similarity(left: Sequence[float], right: Sequence[float]) -> float:
    if not left or not right or len(left) != len(right):
        return 0.0
    return float(sum(a * b for a, b in zip(left, right)))


def average_embedding(vectors: Iterable[Sequence[float]]) -> List[float]:
    vectors = list(vectors)
    if not vectors:
        return []
    dimensions = len(vectors[0])
    accumulator = [0.0] * dimensions
    for vector in vectors:
        for index, value in enumerate(vector):
            accumulator[index] += value
    norm = math.sqrt(sum(value * value for value in accumulator))
    if norm == 0:
        return accumulator
    return [value / norm for value in accumulator]
