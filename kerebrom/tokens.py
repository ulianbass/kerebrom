# Copyright (c) 2026 Ulian Bass. All rights reserved.
# This software is proprietary. See LICENSE for terms.

"""Token tracking y estimacion de ahorro para Kerebrom.

Cada operacion de memoria (recall, context, query, remember) se registra
para poder estimar cuantos tokens evita el uso de Kerebrom comparado con
un AI sin memoria persistente.

La estimacion es una heuristica conservadora basada en multiplicadores
por operacion. No es una medicion exacta — se marca como 'estimate' en
todas las metricas expuestas al usuario.
"""
from __future__ import annotations

import json
import sqlite3
from datetime import datetime, timezone, timedelta
from typing import Any, Dict, List, Optional


# ── Multiplicadores de ahorro (heuristica conservadora) ──
# Basados en la premisa de que sin Kerebrom, el modelo tendria que
# reconstruir el contexto desde archivos/transcripts.
SAVINGS_MULTIPLIER = {
    "recall": 3.0,      # evita que el modelo relea archivos buscando la info
    "context": 5.0,     # evita reconstruccion completa desde transcripts
    "query": 2.0,       # consulta filtrada, ahorro menor
    "remember": 0.0,    # no ahorra, solo costea el almacenamiento
    "forget": 0.0,
    "entities": 1.5,
    "facts": 1.5,
}


def count_tokens(text: str) -> int:
    """Cuenta tokens aproximados en un texto.

    Usa tiktoken si esta disponible (exacto para modelos OpenAI/Anthropic).
    Sino, fallback a estimacion char/4 que es razonable para la mayoria
    de idiomas occidentales.
    """
    if not text:
        return 0
    try:
        import tiktoken  # type: ignore
        enc = tiktoken.get_encoding("cl100k_base")
        return len(enc.encode(text))
    except ImportError:
        # Fallback: ~4 chars por token (aprox GPT-3.5/4)
        return max(1, len(text) // 4)


class TokenTracker:
    """Registra y agrega estadisticas de uso de tokens.

    Opera sobre una tabla `token_stats` en la DB de Kerebrom.
    Todos los metodos son idempotentes y no lanzan excepciones —
    el tracking nunca debe bloquear la operacion principal.
    """

    def __init__(self, connection_factory):
        """connection_factory: callable que devuelve una conexion sqlite3 abierta."""
        self._connect = connection_factory
        self._enabled = True

    # ── Habilitar/deshabilitar ───────────────────────────

    def disable(self) -> None:
        """Desactiva temporalmente el tracking (util para benchmarks)."""
        self._enabled = False

    def enable(self) -> None:
        """Reactiva el tracking."""
        self._enabled = True

    # ── Schema ───────────────────────────────────────────

    @staticmethod
    def ensure_schema(conn: sqlite3.Connection) -> None:
        """Crea la tabla token_stats si no existe."""
        conn.execute(
            """
            CREATE TABLE IF NOT EXISTS token_stats (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                timestamp TEXT NOT NULL,
                operation TEXT NOT NULL,
                project TEXT NOT NULL DEFAULT 'default',
                tokens_input INTEGER NOT NULL DEFAULT 0,
                tokens_output INTEGER NOT NULL DEFAULT 0,
                tokens_saved INTEGER NOT NULL DEFAULT 0,
                memories_count INTEGER NOT NULL DEFAULT 0,
                metadata TEXT NOT NULL DEFAULT '{}'
            )
            """
        )
        conn.execute(
            "CREATE INDEX IF NOT EXISTS idx_token_stats_ts ON token_stats(timestamp)"
        )
        conn.execute(
            "CREATE INDEX IF NOT EXISTS idx_token_stats_op ON token_stats(operation)"
        )

    # ── Registro ─────────────────────────────────────────

    def track(
        self,
        operation: str,
        input_text: str = "",
        output_text: str = "",
        project: str = "default",
        memories_count: int = 0,
        metadata: Optional[Dict[str, Any]] = None,
    ) -> None:
        """Registra una operacion. Silenciosa ante errores.

        No hace nada si el tracker esta deshabilitado.
        """
        if not self._enabled:
            return
        try:
            tokens_in = count_tokens(input_text)
            tokens_out = count_tokens(output_text)
            multiplier = SAVINGS_MULTIPLIER.get(operation, 1.0)
            tokens_saved = int(tokens_out * multiplier)
            now = datetime.now(timezone.utc).isoformat()

            with self._connect() as conn:
                self.ensure_schema(conn)
                conn.execute(
                    """
                    INSERT INTO token_stats
                    (timestamp, operation, project, tokens_input, tokens_output,
                     tokens_saved, memories_count, metadata)
                    VALUES (?, ?, ?, ?, ?, ?, ?, ?)
                    """,
                    (
                        now,
                        operation,
                        project,
                        tokens_in,
                        tokens_out,
                        tokens_saved,
                        memories_count,
                        json.dumps(metadata or {}),
                    ),
                )
        except Exception:
            # El tracking no debe romper operaciones de memoria
            pass

    # ── Consulta agregada ────────────────────────────────

    def summary(self, project: Optional[str] = None) -> Dict[str, Any]:
        """Retorna un resumen agregado con ventanas rolling.

        A diferencia de cortes por dia/semana/mes calendario, estas
        ventanas cuentan hacia atras desde el momento actual — asi
        los numeros se diferencian inmediatamente y reflejan uso real.
        """
        with self._connect() as conn:
            self.ensure_schema(conn)
            where = ""
            params: List[Any] = []
            if project:
                where = "WHERE project = ?"
                params = [project]

            totals = conn.execute(
                f"""
                SELECT
                    COUNT(*) as ops,
                    COALESCE(SUM(tokens_input), 0) as tok_in,
                    COALESCE(SUM(tokens_output), 0) as tok_out,
                    COALESCE(SUM(tokens_saved), 0) as tok_saved
                FROM token_stats {where}
                """,
                params,
            ).fetchone()

            now = datetime.now(timezone.utc)
            hour_cut = (now - timedelta(hours=1)).isoformat()
            day_cut = (now - timedelta(hours=24)).isoformat()
            week_cut = (now - timedelta(days=7)).isoformat()
            month_cut = (now - timedelta(days=30)).isoformat()

            def _period(cut: str) -> Dict[str, int]:
                where_period = "WHERE timestamp >= ?"
                local_params: List[Any] = [cut]
                if project:
                    where_period += " AND project = ?"
                    local_params.append(project)
                row = conn.execute(
                    f"""
                    SELECT
                        COUNT(*) as ops,
                        COALESCE(SUM(tokens_saved), 0) as saved
                    FROM token_stats {where_period}
                    """,
                    local_params,
                ).fetchone()
                return {"ops": int(row[0] or 0), "saved": int(row[1] or 0)}

            return {
                "total": {
                    "operations": int(totals[0] or 0),
                    "tokens_input": int(totals[1] or 0),
                    "tokens_output": int(totals[2] or 0),
                    "tokens_saved_estimate": int(totals[3] or 0),
                },
                "last_hour": _period(hour_cut),
                "last_24h": _period(day_cut),
                "last_7d": _period(week_cut),
                "last_30d": _period(month_cut),
                # Retrocompat — alias para clientes viejos
                "today": _period(day_cut),
                "week": _period(week_cut),
                "month": _period(month_cut),
                "disclaimer": (
                    "Tokens saved es una estimacion basada en multiplicadores "
                    "conservadores. No representa un ahorro medido exacto. "
                    "Las ventanas son rolling: cuentan hacia atras desde ahora."
                ),
            }

    def timeline(self, days: int = 30, project: Optional[str] = None) -> List[Dict[str, Any]]:
        """Retorna ahorro agregado por dia, para los ultimos `days` dias."""
        with self._connect() as conn:
            self.ensure_schema(conn)
            cutoff = (datetime.now(timezone.utc) - timedelta(days=days)).isoformat()
            where = "WHERE timestamp >= ?"
            params: List[Any] = [cutoff]
            if project:
                where += " AND project = ?"
                params.append(project)
            rows = conn.execute(
                f"""
                SELECT
                    substr(timestamp, 1, 10) as day,
                    COALESCE(SUM(tokens_saved), 0) as saved,
                    COUNT(*) as ops
                FROM token_stats {where}
                GROUP BY day
                ORDER BY day ASC
                """,
                params,
            ).fetchall()
            return [
                {"day": r[0], "saved": int(r[1] or 0), "ops": int(r[2] or 0)}
                for r in rows
            ]

    def by_operation(self, project: Optional[str] = None) -> List[Dict[str, Any]]:
        """Breakdown de operaciones: cuantas de cada tipo y tokens ahorrados."""
        with self._connect() as conn:
            self.ensure_schema(conn)
            where = ""
            params: List[Any] = []
            if project:
                where = "WHERE project = ?"
                params = [project]
            rows = conn.execute(
                f"""
                SELECT
                    operation,
                    COUNT(*) as ops,
                    COALESCE(SUM(tokens_saved), 0) as saved,
                    COALESCE(SUM(tokens_output), 0) as tokens_out
                FROM token_stats {where}
                GROUP BY operation
                ORDER BY saved DESC
                """,
                params,
            ).fetchall()
            return [
                {
                    "operation": r[0],
                    "operations": int(r[1] or 0),
                    "tokens_saved": int(r[2] or 0),
                    "tokens_output": int(r[3] or 0),
                }
                for r in rows
            ]

    def reset(self) -> int:
        """Borra todas las estadisticas. Retorna el numero de filas eliminadas."""
        with self._connect() as conn:
            self.ensure_schema(conn)
            cur = conn.execute("DELETE FROM token_stats")
            return cur.rowcount
