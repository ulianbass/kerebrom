# Copyright (c) 2026 Ulian Bass. All rights reserved.
# This software is proprietary. See LICENSE for terms.

"""Encrypted container helpers for Kerebrom.

This module provides whole-file envelope encryption for SQLite databases
using AES-256-CTR + PBKDF2 + HMAC-SHA256 authentication.

Two backends are supported:
- ``cryptography`` library (preferred, portable, pip install kerebrom[crypto])
- System OpenSSL binary via subprocess (fallback, macOS/Linux only)

It is intentionally conservative:
- The encrypted container protects the database file at rest.
- A plaintext runtime SQLite file still exists while a process is using
  the database, so encrypted mode trades away cross-process concurrency.
- The passphrase itself is never written into the container.
"""

from __future__ import annotations

import hashlib
import hmac
import os
import struct
import tempfile
from pathlib import Path

MAGIC = b"KEREBROM-ENC-1"
SALT_SIZE = 16
IV_SIZE = 16
MAC_SIZE = 32
ITERATIONS = 200_000
HEADER_STRUCT = struct.Struct(">I")

# ---------------------------------------------------------------------------
# AES-256-CTR backend selection
# ---------------------------------------------------------------------------

_USE_CRYPTOGRAPHY_LIB = False
try:
    from cryptography.hazmat.primitives.ciphers import Cipher, algorithms, modes

    _USE_CRYPTOGRAPHY_LIB = True
except ImportError:
    pass


def _aes_ctr(data: bytes, key: bytes, iv: bytes) -> bytes:
    """Encrypt or decrypt data with AES-256-CTR.

    CTR mode is symmetric — encrypt and decrypt are the same operation.
    """
    if _USE_CRYPTOGRAPHY_LIB:
        cipher = Cipher(algorithms.AES(key), modes.CTR(iv))
        encryptor = cipher.encryptor()
        return encryptor.update(data) + encryptor.finalize()
    else:
        return _aes_ctr_openssl(data, key, iv)


def _aes_ctr_openssl(data: bytes, key: bytes, iv: bytes) -> bytes:
    """AES-256-CTR via system OpenSSL binary (fallback for no-deps install)."""
    import subprocess

    with tempfile.TemporaryDirectory() as td:
        input_path = Path(td) / "input.bin"
        output_path = Path(td) / "output.bin"
        input_path.write_bytes(data)

        args = [
            "openssl", "enc", "-aes-256-ctr", "-nosalt",
            "-K", key.hex(),
            "-iv", iv.hex(),
            "-in", str(input_path),
            "-out", str(output_path),
        ]
        result = subprocess.run(args, text=True, capture_output=True, check=False)
        if result.returncode != 0:
            raise RuntimeError(
                "OpenSSL AES-CTR failed: {}".format(result.stderr.strip() or "unknown error")
            )
        return output_path.read_bytes()


# ---------------------------------------------------------------------------
# Public API
# ---------------------------------------------------------------------------


def is_encrypted_container(path: str | Path) -> bool:
    candidate = Path(path)
    try:
        with candidate.open("rb") as handle:
            return handle.read(len(MAGIC)) == MAGIC
    except FileNotFoundError:
        return False


def encrypt_database_file(
    plaintext_path: str | Path,
    encrypted_path: str | Path,
    passphrase: str,
) -> None:
    plain = Path(plaintext_path).expanduser().resolve()
    target = Path(encrypted_path).expanduser().resolve()
    if not plain.exists():
        raise FileNotFoundError("Plaintext database does not exist: {}".format(plain))

    salt = os.urandom(SALT_SIZE)
    iv = os.urandom(IV_SIZE)
    enc_key, mac_key = _derive_keys(passphrase, salt)

    plaintext_data = plain.read_bytes()
    ciphertext = _aes_ctr(plaintext_data, enc_key, iv)

    mac = hmac.new(
        mac_key,
        MAGIC + HEADER_STRUCT.pack(ITERATIONS) + salt + iv + ciphertext,
        hashlib.sha256,
    ).digest()
    payload = MAGIC + HEADER_STRUCT.pack(ITERATIONS) + salt + iv + mac + ciphertext

    target.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(dir=str(target.parent)) as td:
        temp_target = Path(td) / "database.enc"
        temp_target.write_bytes(payload)
        os.replace(temp_target, target)


def decrypt_database_file(
    encrypted_path: str | Path,
    plaintext_path: str | Path,
    passphrase: str,
) -> None:
    source = Path(encrypted_path).expanduser().resolve()
    target = Path(plaintext_path).expanduser().resolve()
    raw = source.read_bytes()
    minimum_size = len(MAGIC) + HEADER_STRUCT.size + SALT_SIZE + IV_SIZE + MAC_SIZE
    if len(raw) < minimum_size:
        raise ValueError("Encrypted container is truncated: {}".format(source))
    if raw[: len(MAGIC)] != MAGIC:
        raise ValueError("Not a Kerebrom encrypted container: {}".format(source))

    cursor = len(MAGIC)
    try:
        iterations = HEADER_STRUCT.unpack(raw[cursor : cursor + HEADER_STRUCT.size])[0]
    except struct.error:
        raise ValueError("Corrupted encrypted container header: {}".format(source))
    if iterations < 100_000 or iterations > 10_000_000:
        raise ValueError("Invalid iteration count ({}) in container: {}".format(iterations, source))
    cursor += HEADER_STRUCT.size
    salt = raw[cursor : cursor + SALT_SIZE]
    cursor += SALT_SIZE
    iv = raw[cursor : cursor + IV_SIZE]
    cursor += IV_SIZE
    expected_mac = raw[cursor : cursor + MAC_SIZE]
    cursor += MAC_SIZE
    ciphertext = raw[cursor:]

    enc_key, mac_key = _derive_keys(passphrase, salt, iterations=iterations)
    actual_mac = hmac.new(
        mac_key,
        MAGIC + HEADER_STRUCT.pack(iterations) + salt + iv + ciphertext,
        hashlib.sha256,
    ).digest()
    if not hmac.compare_digest(expected_mac, actual_mac):
        raise ValueError("Invalid passphrase or tampered encrypted container: {}".format(source))

    plaintext_data = _aes_ctr(ciphertext, enc_key, iv)

    target.parent.mkdir(parents=True, exist_ok=True)
    target.write_bytes(plaintext_data)


def _derive_keys(passphrase: str, salt: bytes, iterations: int = ITERATIONS) -> tuple[bytes, bytes]:
    material = hashlib.pbkdf2_hmac(
        "sha256",
        passphrase.encode("utf-8"),
        salt,
        iterations,
        dklen=64,
    )
    return material[:32], material[32:]
