#!/usr/bin/env python3
from __future__ import annotations

import json
import os
import subprocess
import sys
import tempfile
import time
from pathlib import Path
from statistics import median
from typing import Dict, Iterable, List, Tuple


REPO_ROOT = Path(__file__).resolve().parents[1]
if str(REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(REPO_ROOT))

from kerebrom.store import KerebromStore, utc_now


THRESHOLDS = {
    "setup_ms_max": 1000.0,
    "packaged_install_ms_max": 10000.0,
    "source_smoke_ms_max": 3000.0,
    "mcp_roundtrip_ms_max": 600.0,
    "remember_p95_ms_max": 20.0,
    "facts_p95_ms_max": 20.0,
    "recall_p95_ms_max": 50.0,
    "context_p95_ms_max": 75.0,
    "recall_top1_hit_rate_min": 0.95,
    "recall_top3_hit_rate_min": 1.0,
    "context_hit_rate_min": 0.95,
}


def _assert(condition: bool, message: str) -> None:
    if not condition:
        raise AssertionError(message)


def _build_env(home: Path, extra_pythonpath: bool = False) -> Dict[str, str]:
    env = os.environ.copy()
    env["HOME"] = str(home)
    if extra_pythonpath:
        pythonpath = str(REPO_ROOT)
        existing = env.get("PYTHONPATH", "")
        env["PYTHONPATH"] = pythonpath if not existing else pythonpath + os.pathsep + existing
    return env


def _run_timed(
    args: Iterable[str],
    *,
    cwd: Path = REPO_ROOT,
    env: Dict[str, str] | None = None,
    input_text: str | None = None,
) -> Tuple[float, subprocess.CompletedProcess[str]]:
    started = time.perf_counter()
    result = subprocess.run(
        list(args),
        cwd=str(cwd),
        env=env,
        input=input_text,
        text=True,
        capture_output=True,
        check=False,
    )
    elapsed_ms = (time.perf_counter() - started) * 1000.0
    if result.returncode != 0:
        raise AssertionError(
            "Command failed: {}\nstdout:\n{}\nstderr:\n{}".format(
                " ".join(result.args),
                result.stdout,
                result.stderr,
            )
        )
    return elapsed_ms, result


def _percentile(values: List[float], percentile: float) -> float:
    ordered = sorted(values)
    index = int(round((len(ordered) - 1) * percentile))
    return ordered[index]


def _measure_retrieval_metrics() -> Dict[str, float]:
    with tempfile.TemporaryDirectory() as td:
        db_path = Path(td) / "benchmark.db"
        store = KerebromStore(db_path)
        store.initialize(project="bench", description="Benchmark corpus")

        with store.connect() as connection:
            now = utc_now()
            connection.execute(
                """
                INSERT INTO maintenance_log(project, task, last_run_at)
                VALUES (?, ?, ?), (?, ?, ?)
                ON CONFLICT(project, task) DO UPDATE SET last_run_at = excluded.last_run_at
                """,
                ("bench", "decay", now, "bench", "consolidate", now),
            )

        remember_samples: List[float] = []
        for index in range(1, 51):
            for content in (
                "Trabajo en Empresa{}.".format(index),
                "Uso Herramienta{}.".format(index),
                "Vivo en Ciudad{}.".format(index),
            ):
                started = time.perf_counter()
                store.remember(content, project="bench")
                remember_samples.append((time.perf_counter() - started) * 1000.0)

        queries = [
            "Empresa{}".format(index) for index in range(1, 11)
        ] + [
            "Herramienta{}".format(index) for index in range(1, 11)
        ] + [
            "Ciudad{}".format(index) for index in range(1, 11)
        ]

        recall_samples: List[float] = []
        context_samples: List[float] = []
        facts_samples: List[float] = []
        recall_top1_hits = 0
        recall_top3_hits = 0
        context_hits = 0

        for query in queries:
            started = time.perf_counter()
            facts = store.list_facts(project="bench", limit=25)
            facts_samples.append((time.perf_counter() - started) * 1000.0)
            _assert(facts, "Facts benchmark corpus did not produce any facts")

            started = time.perf_counter()
            recall_results = store.recall(
                query,
                project="bench",
                limit=3,
                maintain=False,
                reactivate=False,
            )
            recall_samples.append((time.perf_counter() - started) * 1000.0)

            if recall_results and query in recall_results[0].content:
                recall_top1_hits += 1
            if any(query in result.content for result in recall_results):
                recall_top3_hits += 1

            started = time.perf_counter()
            context = store.build_context(query, project="bench", limit=3, layer=2)
            context_samples.append((time.perf_counter() - started) * 1000.0)

            fact_hit = any(fact.get("object") == query for fact in context.get("facts", []))
            memory_hit = any(query in memory.get("snippet", "") for memory in context.get("memories", []))
            if fact_hit or memory_hit:
                context_hits += 1

        total_queries = float(len(queries))
        return {
            "remember_median_ms": round(median(remember_samples), 3),
            "remember_p95_ms": round(_percentile(remember_samples, 0.95), 3),
            "facts_median_ms": round(median(facts_samples), 3),
            "facts_p95_ms": round(_percentile(facts_samples, 0.95), 3),
            "recall_median_ms": round(median(recall_samples), 3),
            "recall_p95_ms": round(_percentile(recall_samples, 0.95), 3),
            "context_median_ms": round(median(context_samples), 3),
            "context_p95_ms": round(_percentile(context_samples, 0.95), 3),
            "recall_top1_hit_rate": round(recall_top1_hits / total_queries, 4),
            "recall_top3_hit_rate": round(recall_top3_hits / total_queries, 4),
            "context_hit_rate": round(context_hits / total_queries, 4),
            "corpus_memories": 150,
            "queries": int(total_queries),
        }


def _measure_setup_latency() -> float:
    with tempfile.TemporaryDirectory() as td:
        home = Path(td)
        (home / ".claude").mkdir(parents=True, exist_ok=True)
        (home / ".codex").mkdir(parents=True, exist_ok=True)
        env = _build_env(home, extra_pythonpath=True)
        db_path = home / ".kerebrom" / "kerebrom.db"
        elapsed_ms, _ = _run_timed(
            [sys.executable, "-m", "kerebrom", "setup", "--db", str(db_path)],
            env=env,
        )
        return round(elapsed_ms, 3)


def _measure_source_smoke_latency() -> float:
    elapsed_ms, _ = _run_timed([sys.executable, str(REPO_ROOT / "scripts" / "local_release_smoke.py")])
    return round(elapsed_ms, 3)


def _measure_packaged_install_latency() -> float:
    with tempfile.TemporaryDirectory() as td:
        root = Path(td)
        home = root / "home"
        home.mkdir(parents=True, exist_ok=True)
        env = _build_env(home)
        venv_dir = root / "venv"

        _run_timed([sys.executable, "-m", "venv", str(venv_dir)], env=env)
        python_bin = venv_dir / ("Scripts" if os.name == "nt" else "bin") / "python"
        elapsed_ms, _ = _run_timed(
            [
                str(python_bin),
                "-m",
                "pip",
                "install",
                "--no-deps",
                str(REPO_ROOT),
            ],
            env=env,
        )
        return round(elapsed_ms, 3)


def _measure_mcp_roundtrip_latency() -> float:
    with tempfile.TemporaryDirectory() as td:
        home = Path(td)
        env = _build_env(home, extra_pythonpath=True)
        db_path = home / ".kerebrom" / "mcp.db"
        payload = "\n".join(
            [
                '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}',
                '{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}',
                '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}',
                '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"remember","arguments":{"content":"Vivo en Guatemala."}}}',
                '{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"recall","arguments":{"query":"Guatemala","limit":3}}}',
                '{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"context","arguments":{"query":"perfil","layer":2}}}',
            ]
        ) + "\n"
        elapsed_ms, result = _run_timed(
            [sys.executable, "-m", "kerebrom", "serve", "--db", str(db_path), "--project", "bench-mcp"],
            env=env,
            input_text=payload,
        )
        responses = [json.loads(line) for line in result.stdout.splitlines() if line.strip()]
        by_id = {response["id"]: response for response in responses}
        recall_data = json.loads(by_id[4]["result"]["content"][0]["text"])
        _assert(recall_data and "Guatemala" in recall_data[0]["content"], "MCP round-trip recall did not return Guatemala")
        return round(elapsed_ms, 3)


def collect_metrics() -> Dict[str, float]:
    metrics = {
        "setup_ms": _measure_setup_latency(),
        "packaged_install_ms": _measure_packaged_install_latency(),
        "source_smoke_ms": _measure_source_smoke_latency(),
        "mcp_roundtrip_ms": _measure_mcp_roundtrip_latency(),
    }
    metrics.update(_measure_retrieval_metrics())
    return metrics


def assert_thresholds(metrics: Dict[str, float]) -> None:
    _assert(metrics["setup_ms"] <= THRESHOLDS["setup_ms_max"], "setup latency exceeded threshold")
    _assert(
        metrics["packaged_install_ms"] <= THRESHOLDS["packaged_install_ms_max"],
        "packaged install latency exceeded threshold",
    )
    _assert(metrics["source_smoke_ms"] <= THRESHOLDS["source_smoke_ms_max"], "source smoke exceeded threshold")
    _assert(metrics["mcp_roundtrip_ms"] <= THRESHOLDS["mcp_roundtrip_ms_max"], "MCP round-trip exceeded threshold")
    _assert(metrics["remember_p95_ms"] <= THRESHOLDS["remember_p95_ms_max"], "remember p95 exceeded threshold")
    _assert(metrics["facts_p95_ms"] <= THRESHOLDS["facts_p95_ms_max"], "facts p95 exceeded threshold")
    _assert(metrics["recall_p95_ms"] <= THRESHOLDS["recall_p95_ms_max"], "recall p95 exceeded threshold")
    _assert(metrics["context_p95_ms"] <= THRESHOLDS["context_p95_ms_max"], "context p95 exceeded threshold")
    _assert(
        metrics["recall_top1_hit_rate"] >= THRESHOLDS["recall_top1_hit_rate_min"],
        "recall top1 hit rate dropped below threshold",
    )
    _assert(
        metrics["recall_top3_hit_rate"] >= THRESHOLDS["recall_top3_hit_rate_min"],
        "recall top3 hit rate dropped below threshold",
    )
    _assert(metrics["context_hit_rate"] >= THRESHOLDS["context_hit_rate_min"], "context hit rate dropped below threshold")


def main() -> int:
    metrics = collect_metrics()
    assert_thresholds(metrics)

    print(json.dumps({"thresholds": THRESHOLDS, "metrics": metrics}, ensure_ascii=False, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
