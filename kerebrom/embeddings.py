"""Pluggable embedding system for Kerebrom.

Three tiers, auto-selected by what's installed:

1. ONNX (best)     — pip install kerebrom[onnx]  (~30MB install, ~80MB model on first use)
2. sentence-transformers — pip install kerebrom[ml]  (~2GB, heavy but full torch)
3. Hash (fallback) — zero dependencies, character n-grams

The user never picks — auto_select_model() finds the best available.
"""

from __future__ import annotations

import hashlib
import math
import re
import sys
import urllib.request
from pathlib import Path
from typing import Iterable, List, Protocol, Sequence

TOKEN_RE = re.compile(r"[0-9A-Za-zÁÉÍÓÚÜÑáéíóúüñ_+-]+")

# Where lazy-downloaded model files live.
_MODEL_CACHE_DIR = Path.home() / ".cache" / "kerebrom" / "all-MiniLM-L6-v2"
_HF_REPO = "sentence-transformers/all-MiniLM-L6-v2"
_MODEL_FILES = {
    "model.onnx": "https://huggingface.co/{}/resolve/main/onnx/model.onnx".format(_HF_REPO),
    "tokenizer.json": "https://huggingface.co/{}/resolve/main/tokenizer.json".format(_HF_REPO),
}


def tokenize(text: str) -> List[str]:
    return [token.lower() for token in TOKEN_RE.findall(text)]


def _char_ngrams(token: str, min_n: int = 3, max_n: int = 5) -> List[str]:
    """Character n-grams for fuzzy word matching (FastText-style)."""
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


# ---------------------------------------------------------------------------
# Tier 3: Hash model (zero dependencies, always available)
# ---------------------------------------------------------------------------


class HashEmbeddingModel:
    """Feature-hashing embeddings with character n-grams.

    Zero dependencies. Good for word-overlap matching.
    Cannot do true semantic similarity ("where do I live?" vs "Vivo en Guatemala").
    """

    def __init__(self, dimensions: int = 256) -> None:
        self.dimensions = dimensions

    def embed(self, text: str) -> List[float]:
        vector = [0.0] * self.dimensions
        tokens = tokenize(text)
        if not tokens:
            return vector

        for token in tokens:
            self._hash_into(vector, token, weight=2.0)
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


# ---------------------------------------------------------------------------
# Tier 1: ONNX model (lightweight, real semantics)
# ---------------------------------------------------------------------------


def _ensure_model_files() -> tuple[Path, Path]:
    """Download model files on first use (~80MB, one time only)."""
    _MODEL_CACHE_DIR.mkdir(parents=True, exist_ok=True)
    paths = {}
    for filename, url in _MODEL_FILES.items():
        local_path = _MODEL_CACHE_DIR / filename
        if not local_path.exists():
            print(
                "Kerebrom: descargando modelo de embeddings ({})...".format(filename),
                file=sys.stderr,
            )
            urllib.request.urlretrieve(url, local_path)
        paths[filename] = local_path
    return paths["model.onnx"], paths["tokenizer.json"]


class ONNXEmbeddingModel:
    """Real semantic embeddings via ONNX Runtime + all-MiniLM-L6-v2.

    Requires:  pip install kerebrom[onnx]  (onnxruntime + tokenizers)
    Model downloaded lazily on first embed() call (~80MB, one time).
    Output: 384-dimensional normalized sentence embeddings.
    """

    def __init__(self) -> None:
        self.dimensions = 384
        self._session = None
        self._tokenizer = None

    def _load(self) -> None:
        import numpy as np  # noqa: F811
        import onnxruntime as ort
        from tokenizers import Tokenizer

        model_path, tokenizer_path = _ensure_model_files()
        self._session = ort.InferenceSession(
            str(model_path),
            providers=["CPUExecutionProvider"],
        )
        self._tokenizer = Tokenizer.from_file(str(tokenizer_path))
        self._tokenizer.enable_padding(pad_id=0, pad_token="[PAD]")
        self._tokenizer.enable_truncation(max_length=256)
        self._np = np

    def embed(self, text: str) -> List[float]:
        if self._session is None:
            self._load()

        np = self._np
        encoded = self._tokenizer.encode(text)

        input_ids = np.array([encoded.ids], dtype=np.int64)
        attention_mask = np.array([encoded.attention_mask], dtype=np.int64)
        token_type_ids = np.zeros_like(input_ids, dtype=np.int64)

        # Check which inputs the model expects.
        input_names = {inp.name for inp in self._session.get_inputs()}
        feed = {"input_ids": input_ids, "attention_mask": attention_mask}
        if "token_type_ids" in input_names:
            feed["token_type_ids"] = token_type_ids

        outputs = self._session.run(None, feed)
        token_embeddings = outputs[0]  # (1, seq_len, 384)

        # Mean pooling over non-padding tokens.
        mask = attention_mask[:, :, np.newaxis].astype(np.float32)
        pooled = np.sum(token_embeddings * mask, axis=1) / np.clip(
            mask.sum(axis=1), a_min=1e-9, a_max=None
        )
        # L2 normalize.
        norm = np.linalg.norm(pooled, axis=1, keepdims=True)
        normalized = (pooled / norm)[0]

        return normalized.tolist()


# ---------------------------------------------------------------------------
# Tier 2: sentence-transformers (heavy but fully featured)
# ---------------------------------------------------------------------------


class SentenceTransformerModel:
    """Real semantic embeddings via sentence-transformers (requires torch).

    Requires:  pip install sentence-transformers
    """

    def __init__(self, model_name: str = "all-MiniLM-L6-v2") -> None:
        self.model_name = model_name
        self._model = None
        self.dimensions = 384

    def embed(self, text: str) -> List[float]:
        if self._model is None:
            from sentence_transformers import SentenceTransformer

            self._model = SentenceTransformer(self.model_name)
            self.dimensions = self._model.get_sentence_embedding_dimension()
        return self._model.encode(text, show_progress_bar=False).tolist()


# ---------------------------------------------------------------------------
# Auto-selection
# ---------------------------------------------------------------------------


def auto_select_model() -> EmbeddingModel:
    """Pick the best available embedding model automatically.

    Priority: ONNX > sentence-transformers > hash (always available).
    """
    # Tier 1: ONNX (lightweight, real semantics)
    try:
        import onnxruntime  # noqa: F401
        import tokenizers  # noqa: F401

        return ONNXEmbeddingModel()
    except ImportError:
        pass

    # Tier 2: sentence-transformers (heavy but full)
    try:
        import sentence_transformers  # noqa: F401

        return SentenceTransformerModel()
    except ImportError:
        pass

    # Tier 3: hash (zero deps, always works)
    return HashEmbeddingModel()


# ---------------------------------------------------------------------------
# Utilities
# ---------------------------------------------------------------------------


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
