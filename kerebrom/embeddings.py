"""Pluggable embedding system for Kerebrom.

Default: enhanced hash model with character n-grams (zero dependencies).
Optional: sentence-transformers for real semantic embeddings.

Install with:  pip install kerebrom[ml]
"""

from __future__ import annotations

import hashlib
import math
import re
from typing import Iterable, List, Protocol, Sequence

TOKEN_RE = re.compile(r"[0-9A-Za-zÁÉÍÓÚÜÑáéíóúüñ_+-]+")


def tokenize(text: str) -> List[str]:
    return [token.lower() for token in TOKEN_RE.findall(text)]


def _char_ngrams(token: str, min_n: int = 3, max_n: int = 5) -> List[str]:
    """Extract character n-grams from a token, FastText-style.

    "guatemala" -> ["<gu", "gua", "uat", "ate", "tem", "ema", "mal", "ala", "la>",
                     "<gua", "guat", "uate", ..., "ala>", ...]

    This gives partial matching: "guatemalteco" shares n-grams with "guatemala".
    """
    padded = "<" + token + ">"
    ngrams: List[str] = []
    for n in range(min_n, min(max_n + 1, len(padded) + 1)):
        for i in range(len(padded) - n + 1):
            ngrams.append(padded[i : i + n])
    return ngrams


class EmbeddingModel(Protocol):
    """Interface that any embedding model must satisfy."""

    dimensions: int

    def embed(self, text: str) -> List[float]: ...


class HashEmbeddingModel:
    """Enhanced feature-hashing embeddings with character n-grams.

    Produces deterministic, stable vectors with zero external dependencies.
    Character n-grams give partial word matching (e.g., "Guatemala" matches
    "guatemalteco") — much better than word-level hashing alone.
    """

    def __init__(self, dimensions: int = 256) -> None:
        self.dimensions = dimensions

    def embed(self, text: str) -> List[float]:
        vector = [0.0] * self.dimensions
        tokens = tokenize(text)
        if not tokens:
            return vector

        for token in tokens:
            # Word-level hash (weight=2 for exact word importance).
            self._hash_into(vector, token, weight=2.0)
            # Character n-gram hashes (weight=1 for fuzzy matching).
            for ngram in _char_ngrams(token):
                self._hash_into(vector, ngram, weight=1.0)

        norm = math.sqrt(sum(v * v for v in vector))
        if norm == 0:
            return vector
        return [v / norm for v in vector]

    def _hash_into(self, vector: List[float], feature: str, weight: float) -> None:
        digest = hashlib.blake2b(feature.encode("utf-8"), digest_size=16).digest()
        for offset in range(0, len(digest), 2):
            bucket = ((digest[offset] << 8) | digest[offset + 1]) % self.dimensions
            sign = 1.0 if digest[offset] & 1 else -1.0
            vector[bucket] += sign * weight


class SentenceTransformerModel:
    """Real semantic embeddings via sentence-transformers.

    Requires:  pip install sentence-transformers
    Loads the model lazily on first embed() call.
    """

    def __init__(self, model_name: str = "all-MiniLM-L6-v2") -> None:
        self.model_name = model_name
        self._model = None
        self.dimensions = 384  # all-MiniLM-L6-v2 output size

    def embed(self, text: str) -> List[float]:
        if self._model is None:
            from sentence_transformers import SentenceTransformer

            self._model = SentenceTransformer(self.model_name)
            self.dimensions = self._model.get_sentence_embedding_dimension()
        return self._model.encode(text, show_progress_bar=False).tolist()


def auto_select_model() -> EmbeddingModel:
    """Pick the best available embedding model automatically.

    If sentence-transformers is installed, use real semantic embeddings.
    Otherwise, fall back to the enhanced hash model (zero dependencies).
    """
    try:
        import sentence_transformers  # noqa: F401

        return SentenceTransformerModel()
    except ImportError:
        return HashEmbeddingModel()


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
    norm = math.sqrt(sum(v * v for v in accumulator))
    if norm == 0:
        return accumulator
    return [v / norm for v in accumulator]
