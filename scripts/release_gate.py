#!/usr/bin/env python3
from __future__ import annotations

import argparse
import json
import os
import subprocess
import sys
import tempfile
from pathlib import Path
from shutil import which
from typing import Callable, Dict, Iterable, List


REPO_ROOT = Path(__file__).resolve().parents[1]


def _assert(condition: bool, message: str) -> None:
    if not condition:
        raise AssertionError(message)


def _run(
    args: Iterable[str],
    *,
    cwd: Path = REPO_ROOT,
    env: Dict[str, str] | None = None,
    input_text: str | None = None,
) -> subprocess.CompletedProcess[str]:
    result = subprocess.run(
        list(args),
        cwd=str(cwd),
        env=env,
        input=input_text,
        text=True,
        capture_output=True,
    )
    if result.returncode != 0:
        raise AssertionError(
            "Command failed: {}\nstdout:\n{}\nstderr:\n{}".format(
                " ".join(result.args),
                result.stdout,
                result.stderr,
            )
        )
    return result


def _venv_bin(venv_dir: Path, name: str) -> Path:
    bin_dir = "Scripts" if os.name == "nt" else "bin"
    return venv_dir / bin_dir / name


def _build_env(home: Path, extra_pythonpath: bool = False) -> Dict[str, str]:
    env = os.environ.copy()
    env["HOME"] = str(home)
    if extra_pythonpath:
        pythonpath = str(REPO_ROOT)
        existing = env.get("PYTHONPATH", "")
        env["PYTHONPATH"] = pythonpath if not existing else pythonpath + os.pathsep + existing
    return env


def _find_claude_bin() -> Path | None:
    direct = which("claude")
    if direct:
        return Path(direct)

    bundled_root = Path.home() / "Library" / "Application Support" / "Claude" / "claude-code"
    if not bundled_root.exists():
        return None

    candidates = sorted(bundled_root.glob("*/claude.app/Contents/MacOS/claude"), reverse=True)
    return candidates[0] if candidates else None


def _phase_unit_suite() -> None:
    _run([sys.executable, "-m", "unittest", "discover", "-s", str(REPO_ROOT / "tests"), "-v"])


def _phase_source_smoke() -> None:
    _run([sys.executable, str(REPO_ROOT / "scripts" / "local_release_smoke.py")])


def _phase_packaged_install() -> None:
    with tempfile.TemporaryDirectory() as td:
        root = Path(td)
        home = root / "home"
        home.mkdir(parents=True, exist_ok=True)
        (home / ".claude").mkdir(parents=True, exist_ok=True)
        (home / ".codex").mkdir(parents=True, exist_ok=True)

        venv_dir = root / "venv"
        _run([sys.executable, "-m", "venv", str(venv_dir)])

        python_bin = _venv_bin(venv_dir, "python")
        kerebrom_bin = _venv_bin(venv_dir, "kerebrom")
        env = _build_env(home)

        _run(
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

        db_path = home / ".kerebrom" / "packaged.db"
        _run(
            [
                str(kerebrom_bin),
                "init",
                "--db",
                str(db_path),
                "--project",
                "packaged",
                "--description",
                "Release gate packaged install",
            ],
            env=env,
        )

        _assert((home / ".claude" / ".mcp.json").exists(), "Packaged install did not configure Claude")
        _assert((home / ".claude" / "CLAUDE.md").exists(), "Packaged install did not create CLAUDE.md")
        _assert((home / ".codex" / "config.toml").exists(), "Packaged install did not configure Codex")
        _assert((home / ".codex" / "AGENTS.md").exists(), "Packaged install did not create Codex AGENTS.md")

        remember = _run(
            [
                str(kerebrom_bin),
                "remember",
                "--db",
                str(db_path),
                "--project",
                "packaged",
                "Me llamo Ulian. Vivo en Guatemala.",
            ],
            env=env,
        )
        remember_data = json.loads(remember.stdout)
        _assert(remember_data["inserted"], "Packaged CLI remember failed")

        recall = _run(
            [
                str(kerebrom_bin),
                "recall",
                "--db",
                str(db_path),
                "--project",
                "packaged",
                "--json",
                "Guatemala",
            ],
            env=env,
        )
        recall_data = json.loads(recall.stdout)
        _assert(recall_data and "Guatemala" in recall_data[0]["content"], "Packaged CLI recall failed")

        messages = [
            {"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": {}},
            {"jsonrpc": "2.0", "method": "notifications/initialized", "params": {}},
            {"jsonrpc": "2.0", "id": 2, "method": "tools/list", "params": {}},
            {
                "jsonrpc": "2.0",
                "id": 3,
                "method": "tools/call",
                "params": {"name": "remember", "arguments": {"content": "Trabajo en Kerebrom."}},
            },
            {
                "jsonrpc": "2.0",
                "id": 4,
                "method": "tools/call",
                "params": {"name": "recall", "arguments": {"query": "Kerebrom", "limit": 3}},
            },
        ]
        payload = "\n".join(json.dumps(message, ensure_ascii=False) for message in messages) + "\n"
        server = _run(
            [
                str(python_bin),
                "-m",
                "kerebrom",
                "serve",
                "--db",
                str(db_path),
                "--project",
                "packaged",
            ],
            env=env,
            input_text=payload,
        )
        responses = [json.loads(line) for line in server.stdout.splitlines() if line.strip()]
        by_id = {response["id"]: response for response in responses}

        _assert(by_id[1]["result"]["protocolVersion"] == "2025-03-26", "Packaged MCP initialize failed")
        tool_names = {tool["name"] for tool in by_id[2]["result"]["tools"]}
        _assert({"remember", "recall", "context"}.issubset(tool_names), "Packaged MCP tools/list failed")

        recall_mcp = json.loads(by_id[4]["result"]["content"][0]["text"])
        _assert(recall_mcp and any("Kerebrom" in row["content"] for row in recall_mcp), "Packaged MCP recall failed")


def _phase_backup_restore() -> None:
    with tempfile.TemporaryDirectory() as td:
        root = Path(td)
        home = root / "home"
        home.mkdir(parents=True, exist_ok=True)
        env = _build_env(home, extra_pythonpath=True)
        source_db = home / ".kerebrom" / "source.db"
        backup_db = home / ".kerebrom" / "backup.db"
        restored_db = home / ".kerebrom" / "restored.db"

        _run(
            [
                sys.executable,
                "-m",
                "kerebrom",
                "init",
                "--db",
                str(source_db),
                "--project",
                "backup",
                "--description",
                "Release gate backup source",
            ],
            env=env,
        )
        _run(
            [
                sys.executable,
                "-m",
                "kerebrom",
                "remember",
                "--db",
                str(source_db),
                "--project",
                "backup",
                "Mi lenguaje favorito es Rust.",
            ],
            env=env,
        )
        backup = _run(
            [
                sys.executable,
                "-m",
                "kerebrom",
                "backup",
                "--db",
                str(source_db),
                "--project",
                "backup",
                "--output",
                str(backup_db),
            ],
            env=env,
        )
        backup_data = json.loads(backup.stdout)
        _assert(backup_data["memories"] == 1, "Backup did not capture the expected memory count")
        _assert(backup_db.exists(), "Backup file was not created")

        restore = _run(
            [
                sys.executable,
                "-m",
                "kerebrom",
                "restore",
                "--db",
                str(restored_db),
                "--project",
                "backup",
                "--input",
                str(backup_db),
            ],
            env=env,
        )
        restore_data = json.loads(restore.stdout)
        _assert(restore_data["memories"] == 1, "Restore did not recover the expected memory count")

        recall = _run(
            [
                sys.executable,
                "-m",
                "kerebrom",
                "recall",
                "--db",
                str(restored_db),
                "--project",
                "backup",
                "--json",
                "Rust",
            ],
            env=env,
        )
        recall_data = json.loads(recall.stdout)
        _assert(recall_data and "Rust" in recall_data[0]["content"], "Restored database did not preserve retrievable memory")


def _phase_encrypted_container() -> None:
    with tempfile.TemporaryDirectory() as td:
        root = Path(td)
        home = root / "home"
        home.mkdir(parents=True, exist_ok=True)
        env = _build_env(home, extra_pythonpath=True)
        env["KEREBROM_RELEASE_PASSPHRASE"] = "release-gate-secret"
        encrypted_db = home / ".kerebrom" / "secure.kdb"

        _run(
            [
                sys.executable,
                "-m",
                "kerebrom",
                "init",
                "--db",
                str(encrypted_db),
                "--project",
                "secure",
                "--passphrase-env",
                "KEREBROM_RELEASE_PASSPHRASE",
            ],
            env=env,
        )
        _run(
            [
                sys.executable,
                "-m",
                "kerebrom",
                "remember",
                "--db",
                str(encrypted_db),
                "--project",
                "secure",
                "--passphrase-env",
                "KEREBROM_RELEASE_PASSPHRASE",
                "Vivo en Guatemala.",
            ],
            env=env,
        )
        recall = _run(
            [
                sys.executable,
                "-m",
                "kerebrom",
                "recall",
                "--db",
                str(encrypted_db),
                "--project",
                "secure",
                "--passphrase-env",
                "KEREBROM_RELEASE_PASSPHRASE",
                "--json",
                "Guatemala",
            ],
            env=env,
        )
        recall_data = json.loads(recall.stdout)
        _assert(recall_data and "Guatemala" in recall_data[0]["content"], "Encrypted recall failed")
        _assert(
            encrypted_db.read_bytes().startswith(b"KEREBROM-ENC-1"),
            "Encrypted database was written as plaintext instead of a protected container",
        )


def _phase_benchmark_gate() -> None:
    _run([sys.executable, str(REPO_ROOT / "scripts" / "benchmark_gate.py")])


def _phase_real_codex_client() -> None:
    codex_bin = which("codex")
    _assert(codex_bin is not None, "Codex CLI is not installed on PATH")

    with tempfile.TemporaryDirectory() as td:
        root = Path(td)
        db_path = root / "real-codex.db"
        last_message = root / "last.txt"
        prompt = (
            "Use the Kerebrom remember tool to store 'Vivo en Guatemala.' "
            "Then use the Kerebrom recall tool and answer with only the location."
        )
        result = _run(
            [
                codex_bin,
                "exec",
                "--skip-git-repo-check",
                "-C",
                str(root),
                "--ephemeral",
                "--dangerously-bypass-approvals-and-sandbox",
                "--json",
                "-c",
                'mcp_servers.kerebrom.command="python3"',
                "-c",
                'mcp_servers.kerebrom.args=["-m","kerebrom","serve","--db","{}"]'.format(db_path),
                "-c",
                'mcp_servers.kerebrom.tools.remember.approval_mode="auto"',
                "-c",
                'mcp_servers.kerebrom.tools.recall.approval_mode="auto"',
                "-o",
                str(last_message),
                prompt,
            ],
        )

        json_events = [
            json.loads(line)
            for line in result.stdout.splitlines()
            if line.strip().startswith("{")
        ]
        completed_items = [
            event["item"]
            for event in json_events
            if event.get("type") == "item.completed" and "item" in event
        ]
        completed_tools = {
            item.get("tool")
            for item in completed_items
            if item.get("type") == "mcp_tool_call" and item.get("status") == "completed"
        }
        final_messages = [
            item.get("text", "").strip()
            for item in completed_items
            if item.get("type") == "agent_message"
        ]

        _assert(last_message.exists(), "Codex CLI did not write the final response")
        _assert(last_message.read_text(encoding="utf-8").strip() == "Guatemala", "Codex CLI did not return the expected location")
        _assert("remember" in completed_tools, "Codex CLI did not execute Kerebrom remember")
        _assert("recall" in completed_tools, "Codex CLI did not execute Kerebrom recall")
        _assert(final_messages and final_messages[-1] == "Guatemala", "Codex CLI final message was not the expected location")


def _phase_real_claude_client() -> None:
    claude_bin = _find_claude_bin()
    _assert(claude_bin is not None, "Claude Code binary was not found on this machine")

    auth = subprocess.run(
        [str(claude_bin), "auth", "status"],
        cwd=str(REPO_ROOT),
        text=True,
        capture_output=True,
        check=False,
    )
    _assert(auth.stdout.strip(), "Claude Code did not return auth status output")
    auth_data = json.loads(auth.stdout)
    _assert(
        auth_data.get("loggedIn") is True,
        "Claude Code binary exists but is not authenticated (loggedIn=false)",
    )

    with tempfile.TemporaryDirectory() as td:
        root = Path(td)
        db_path = root / "real-claude.db"
        config = {
            "mcpServers": {
                "kerebrom": {
                    "command": "python3",
                    "args": ["-m", "kerebrom", "serve", "--db", str(db_path)],
                }
            }
        }
        prompt = (
            "Use the Kerebrom remember tool to store 'Vivo en Guatemala.' "
            "Then use recall and answer with only the location."
        )
        result = _run(
            [
                str(claude_bin),
                "-p",
                "--verbose",
                "--output-format",
                "stream-json",
                "--permission-mode",
                "bypassPermissions",
                "--strict-mcp-config",
                "--mcp-config",
                json.dumps(config),
                "--",
                prompt,
            ],
            cwd=root,
        )
        events = [json.loads(line) for line in result.stdout.splitlines() if line.strip()]
        init_event = next((event for event in events if event.get("type") == "system"), None)
        result_event = next((event for event in reversed(events) if event.get("type") == "result"), None)
        _assert(init_event is not None, "Claude client did not emit an init event")
        mcp_servers = init_event.get("mcp_servers", [])
        _assert(any(server.get("name") == "kerebrom" and server.get("status") == "connected" for server in mcp_servers), "Claude client did not connect to Kerebrom MCP")
        _assert(result_event is not None, "Claude client did not emit a final result event")
        _assert(result_event.get("result", "").strip() == "Guatemala", "Claude client did not return the expected location")


def main(argv: List[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description="Run Kerebrom release validation gates on this machine.")
    parser.add_argument(
        "--with-real-codex",
        action="store_true",
        help="Run an additional non-interactive validation against the installed Codex CLI.",
    )
    parser.add_argument(
        "--with-real-claude",
        action="store_true",
        help="Run an additional non-interactive validation against the bundled or installed Claude Code binary.",
    )
    args = parser.parse_args(argv)

    phases: List[tuple[str, Callable[[], None]]] = [
        ("unit-suite", _phase_unit_suite),
        ("source-smoke", _phase_source_smoke),
        ("packaged-install", _phase_packaged_install),
        ("backup-restore", _phase_backup_restore),
        ("encrypted-container", _phase_encrypted_container),
        ("benchmark-gate", _phase_benchmark_gate),
    ]
    if args.with_real_codex:
        phases.append(("real-codex-client", _phase_real_codex_client))
    if args.with_real_claude:
        phases.append(("real-claude-client", _phase_real_claude_client))

    for name, phase in phases:
        phase()
        print("[ok] {}".format(name))

    print("\nRelease gate passed on this machine.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
