#!/usr/bin/env python3
from __future__ import annotations

import json
import os
import subprocess
import sys
import tempfile
from pathlib import Path
from typing import Any, Dict, List


REPO_ROOT = Path(__file__).resolve().parents[1]


def _build_env(home: Path) -> Dict[str, str]:
    env = os.environ.copy()
    env["HOME"] = str(home)
    pythonpath = str(REPO_ROOT)
    existing = env.get("PYTHONPATH", "")
    env["PYTHONPATH"] = pythonpath if not existing else pythonpath + os.pathsep + existing
    return env


def _run(
    args: List[str],
    env: Dict[str, str],
    input_text: str | None = None,
) -> subprocess.CompletedProcess[str]:
    result = subprocess.run(
        args,
        cwd=str(REPO_ROOT),
        env=env,
        input=input_text,
        text=True,
        capture_output=True,
    )
    if result.returncode != 0:
        raise AssertionError(
            "Command failed: {}\nstdout:\n{}\nstderr:\n{}".format(
                " ".join(args),
                result.stdout,
                result.stderr,
            )
        )
    return result


def _assert(condition: bool, message: str) -> None:
    if not condition:
        raise AssertionError(message)


def _phase_fresh_cli_and_setup() -> None:
    with tempfile.TemporaryDirectory() as td:
        home = Path(td)
        (home / ".claude").mkdir(parents=True, exist_ok=True)
        (home / ".codex").mkdir(parents=True, exist_ok=True)
        env = _build_env(home)
        db_path = home / ".kerebrom" / "smoke.db"

        _run(
            [
                sys.executable,
                "-m",
                "kerebrom",
                "init",
                "--db",
                str(db_path),
                "--project",
                "smoke",
                "--description",
                "Single-machine release smoke",
            ],
            env,
        )

        _assert((home / ".claude" / ".mcp.json").exists(), "Claude MCP config was not created")
        _assert((home / ".claude" / "CLAUDE.md").exists(), "CLAUDE.md was not created")
        _assert((home / ".codex" / "config.toml").exists(), "Codex config.toml was not created")
        _assert((home / ".codex" / "AGENTS.md").exists(), "Codex AGENTS.md was not created")

        remember = _run(
            [
                sys.executable,
                "-m",
                "kerebrom",
                "remember",
                "--db",
                str(db_path),
                "--project",
                "smoke",
                "Me llamo Ulian. Vivo en Guatemala. Trabajo en Kerebrom.",
            ],
            env,
        )
        remember_data = json.loads(remember.stdout)
        _assert(remember_data["inserted"], "remember did not insert the initial memory")

        recall = _run(
            [
                sys.executable,
                "-m",
                "kerebrom",
                "recall",
                "--db",
                str(db_path),
                "--project",
                "smoke",
                "--json",
                "Guatemala",
            ],
            env,
        )
        recall_data = json.loads(recall.stdout)
        _assert(recall_data, "recall returned no results")
        _assert("Guatemala" in recall_data[0]["content"], "recall did not return the expected memory")

        context = _run(
            [
                sys.executable,
                "-m",
                "kerebrom",
                "context",
                "--db",
                str(db_path),
                "--project",
                "smoke",
                "--layer",
                "2",
                "--json",
                "perfil del usuario",
            ],
            env,
        )
        context_data = json.loads(context.stdout)
        _assert(context_data["layer"] == 2, "context layer 2 failed")
        _assert(context_data["memories"], "context returned no memories")


def _phase_delayed_tool_install() -> None:
    with tempfile.TemporaryDirectory() as td:
        home = Path(td)
        env = _build_env(home)
        db_path = home / ".kerebrom" / "delayed.db"

        _run(
            [sys.executable, "-m", "kerebrom", "init", "--db", str(db_path), "--project", "delayed"],
            env,
        )
        _assert(not (home / ".codex" / "config.toml").exists(), "Codex config exists before Codex is installed")

        (home / ".codex").mkdir(parents=True, exist_ok=True)
        _run(
            [sys.executable, "-m", "kerebrom", "facts", "--db", str(db_path), "--project", "delayed", "--json"],
            env,
        )

        _assert((home / ".codex" / "config.toml").exists(), "Codex config was not created after delayed install")
        _assert((home / ".codex" / "AGENTS.md").exists(), "Codex AGENTS.md was not created after delayed install")


def _phase_mcp_stdio() -> None:
    with tempfile.TemporaryDirectory() as td:
        home = Path(td)
        env = _build_env(home)
        db_path = home / ".kerebrom" / "mcp.db"

        messages = [
            {"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": {}},
            {"jsonrpc": "2.0", "method": "notifications/initialized", "params": {}},
            {"jsonrpc": "2.0", "id": 2, "method": "tools/list", "params": {}},
            {
                "jsonrpc": "2.0",
                "id": 3,
                "method": "tools/call",
                "params": {"name": "remember", "arguments": {"content": "Me llamo Ulian. Vivo en Guatemala."}},
            },
            {
                "jsonrpc": "2.0",
                "id": 4,
                "method": "tools/call",
                "params": {"name": "recall", "arguments": {"query": "Guatemala", "limit": 3}},
            },
            {
                "jsonrpc": "2.0",
                "id": 5,
                "method": "tools/call",
                "params": {"name": "context", "arguments": {"query": "perfil", "layer": 2}},
            },
        ]
        payload = "\n".join(json.dumps(message, ensure_ascii=False) for message in messages) + "\n"
        result = _run(
            [sys.executable, "-m", "kerebrom", "serve", "--db", str(db_path), "--project", "mcp-smoke"],
            env,
            input_text=payload,
        )

        responses = [json.loads(line) for line in result.stdout.splitlines() if line.strip()]
        by_id = {response["id"]: response for response in responses}

        _assert(by_id[1]["result"]["protocolVersion"] == "2025-03-26", "MCP initialize returned the wrong protocol version")
        tool_names = {tool["name"] for tool in by_id[2]["result"]["tools"]}
        _assert({"remember", "recall", "context"}.issubset(tool_names), "MCP tools/list is missing expected tools")

        remember_data = json.loads(by_id[3]["result"]["content"][0]["text"])
        _assert(remember_data["inserted"], "MCP remember did not insert a memory")

        recall_data = json.loads(by_id[4]["result"]["content"][0]["text"])
        _assert(recall_data, "MCP recall returned no results")
        _assert("Guatemala" in recall_data[0]["content"], "MCP recall did not return the expected memory")

        context_data = json.loads(by_id[5]["result"]["content"][0]["text"])
        _assert(context_data["layer"] == 2, "MCP context layer 2 failed")


def main() -> int:
    phases = [
        ("fresh-cli-and-setup", _phase_fresh_cli_and_setup),
        ("delayed-tool-install", _phase_delayed_tool_install),
        ("mcp-stdio", _phase_mcp_stdio),
    ]

    for name, phase in phases:
        phase()
        print("[ok] {}".format(name))

    print("\nLocal release smoke passed on this machine.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
