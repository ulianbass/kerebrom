"""Complete uninstall of Kerebrom — removes all traces.

Removes:
- ~/.kerebrom/ (database, lock files, WAL files, everything)
- Kerebrom entries from ~/.claude/.mcp.json
- Kerebrom section from ~/.claude/CLAUDE.md
- Kerebrom entries from ~/.codex/config.toml
- Kerebrom section from ~/.codex/AGENTS.md
- The kerebrom Python package itself (pip uninstall)

Does NOT remove:
- Python itself (even if kerebrom was the only package)
- ~/.claude/ or ~/.codex/ directories (other tools use them)
- pip, setuptools, or any system packages
"""

from __future__ import annotations

import json
import re
import shutil
import subprocess
import sys
from pathlib import Path
from typing import Any, Dict, List


def _remove_kerebrom_db() -> str:
    """Remove ~/.kerebrom/ entirely."""
    kerebrom_dir = Path.home() / ".kerebrom"
    if not kerebrom_dir.exists():
        return "~/.kerebrom/: no existe, nada que borrar"

    shutil.rmtree(kerebrom_dir)
    return "~/.kerebrom/: eliminado (base de datos y toda la memoria)"


def _clean_claude_mcp() -> str:
    """Remove kerebrom entry from ~/.claude/.mcp.json."""
    mcp_file = Path.home() / ".claude" / ".mcp.json"
    if not mcp_file.exists():
        return ".mcp.json: no existe"

    try:
        data = json.loads(mcp_file.read_text(encoding="utf-8"))
    except (json.JSONDecodeError, OSError):
        return ".mcp.json: no se pudo leer"

    if "kerebrom" not in data:
        return ".mcp.json: kerebrom no estaba configurado"

    del data["kerebrom"]
    if not data:
        mcp_file.unlink()
        return ".mcp.json: eliminado (solo contenía kerebrom)"
    else:
        mcp_file.write_text(
            json.dumps(data, indent=2, ensure_ascii=False) + "\n",
            encoding="utf-8",
        )
        return ".mcp.json: entrada kerebrom removida"


def _clean_claude_md() -> str:
    """Remove Kerebrom section from ~/.claude/CLAUDE.md."""
    claude_md = Path.home() / ".claude" / "CLAUDE.md"
    if not claude_md.exists():
        return "CLAUDE.md: no existe"

    content = claude_md.read_text(encoding="utf-8")
    marker = "# Kerebrom — Persistent Memory System"
    if marker not in content:
        return "CLAUDE.md: kerebrom no estaba configurado"

    # The Kerebrom block is either the entire file or prepended with a "---" separator.
    # Pattern: everything from marker to "---" separator or EOF.
    cleaned = re.sub(
        r"# Kerebrom — Persistent Memory System.*?(?=\n---\n|$)",
        "",
        content,
        flags=re.DOTALL,
    )
    # Remove the "---" separator that was added between Kerebrom and original content.
    cleaned = re.sub(r"^\s*---\s*\n", "", cleaned)
    cleaned = cleaned.strip()

    if not cleaned:
        claude_md.unlink()
        return "CLAUDE.md: eliminado (solo contenía instrucciones de kerebrom)"
    else:
        claude_md.write_text(cleaned + "\n", encoding="utf-8")
        return "CLAUDE.md: sección kerebrom removida"


def _clean_codex_config() -> str:
    """Remove [mcp_servers.kerebrom] from ~/.codex/config.toml."""
    config_file = Path.home() / ".codex" / "config.toml"
    if not config_file.exists():
        return "config.toml: no existe"

    content = config_file.read_text(encoding="utf-8")
    if "[mcp_servers.kerebrom]" not in content:
        return "config.toml: kerebrom no estaba configurado"

    # Remove the [mcp_servers.kerebrom] section and all its sub-sections.
    # This handles [mcp_servers.kerebrom], [mcp_servers.kerebrom.tools.*], etc.
    cleaned = re.sub(
        r"\n?\[mcp_servers\.kerebrom[^\]]*\][^\[]*",
        "",
        content,
    )
    cleaned = cleaned.rstrip() + "\n"

    # If the file is now essentially empty (only whitespace or blank config), delete it.
    if not cleaned.strip() or cleaned.strip() == "":
        config_file.unlink()
        return "config.toml: eliminado (solo contenía config de kerebrom)"
    else:
        config_file.write_text(cleaned, encoding="utf-8")
        return "config.toml: sección kerebrom removida"


def _clean_codex_agents_md() -> str:
    """Remove Kerebrom section from ~/.codex/AGENTS.md."""
    agents_file = Path.home() / ".codex" / "AGENTS.md"
    if not agents_file.exists():
        return "AGENTS.md: no existe"

    content = agents_file.read_text(encoding="utf-8")
    marker = "# Kerebrom — Persistent Memory"
    if marker not in content:
        return "AGENTS.md: kerebrom no estaba configurado"

    # Remove from marker to EOF (it's always appended at the end).
    cleaned = content[: content.index(marker)].rstrip()

    if not cleaned:
        agents_file.unlink()
        return "AGENTS.md: eliminado (solo contenía instrucciones de kerebrom)"
    else:
        agents_file.write_text(cleaned + "\n", encoding="utf-8")
        return "AGENTS.md: sección kerebrom removida"


def _clean_temp_runtime() -> str:
    """Remove any leftover kerebrom-runtime-* temp directories."""
    import tempfile

    temp_base = Path(tempfile.gettempdir())
    removed = 0
    for candidate in temp_base.iterdir():
        if candidate.is_dir() and candidate.name.startswith("kerebrom-runtime-"):
            shutil.rmtree(candidate, ignore_errors=True)
            removed += 1
    if removed:
        return "Temp: {} directorios runtime eliminados".format(removed)
    return "Temp: sin residuos runtime"


def _clean_model_cache() -> str:
    """Remove downloaded ONNX model files from ~/.cache/kerebrom/."""
    cache_dir = Path.home() / ".cache" / "kerebrom"
    if not cache_dir.exists():
        return "Cache: sin modelo descargado"
    shutil.rmtree(cache_dir)
    return "Cache: modelo ONNX eliminado (~80MB liberados)"


def _pip_uninstall() -> str:
    """Uninstall the kerebrom Python package."""
    result = subprocess.run(
        [sys.executable, "-m", "pip", "uninstall", "kerebrom", "-y"],
        capture_output=True,
        text=True,
    )
    if result.returncode == 0:
        return "pip: kerebrom desinstalado"
    if "not installed" in result.stdout.lower() or "not installed" in result.stderr.lower():
        return "pip: kerebrom no estaba instalado via pip"
    return "pip: no se pudo desinstalar ({})".format(result.stderr.strip()[:100])


def run_uninstall(skip_pip: bool = False, quiet: bool = False) -> Dict[str, Any]:
    """Remove all traces of Kerebrom from this machine.

    Returns a summary dict with results per step.
    """
    steps = [
        ("Base de datos", _remove_kerebrom_db),
        ("Claude Code MCP", _clean_claude_mcp),
        ("Claude Code CLAUDE.md", _clean_claude_md),
        ("Codex config.toml", _clean_codex_config),
        ("Codex AGENTS.md", _clean_codex_agents_md),
        ("Archivos temporales", _clean_temp_runtime),
        ("Modelo ONNX", _clean_model_cache),
    ]
    if not skip_pip:
        steps.append(("Paquete Python", _pip_uninstall))

    results: Dict[str, Any] = {"steps": {}}

    for name, fn in steps:
        msg = fn()
        results["steps"][name] = msg
        if not quiet:
            print("  {} {}".format("x" if "eliminad" in msg or "removid" in msg or "desinstalad" in msg else "-", msg))

    if not quiet:
        print()
        print("Kerebrom desinstalado. No queda rastro.")

    return results
