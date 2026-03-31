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


def _auto_backup() -> str:
    """Create an automatic backup before uninstall."""
    kerebrom_dir = Path.home() / ".kerebrom"
    db_path = kerebrom_dir / "kerebrom.db"

    if not db_path.exists():
        return "Backup: no hay base de datos, nada que respaldar"

    try:
        from .backup import create_backup
        from .store import KerebromStore

        store = KerebromStore(db_path)
        store.initialize()
        memories = store.export_memories(include_inactive=False)
        if not memories:
            store.close()
            return "Backup: base de datos vacía, nada que respaldar"

        backup_path = create_backup(store)
        store.close()
        return "Backup: {} memorias respaldadas en {}".format(len(memories), backup_path)
    except Exception as exc:
        return "Backup: error al respaldar ({})".format(str(exc)[:100])


def _remove_kerebrom_db() -> str:
    """Remove ~/.kerebrom/ entirely."""
    kerebrom_dir = Path.home() / ".kerebrom"
    if not kerebrom_dir.exists():
        return "~/.kerebrom/: no existe, nada que borrar"

    # Remove immutable flags (macOS chflags uchg) before rmtree so that
    # protected files like the Kerebrom wrapper script can be deleted.
    if sys.platform == "darwin":
        subprocess.run(
            ["chflags", "-R", "nouchg", str(kerebrom_dir)],
            capture_output=True,
        )

    shutil.rmtree(kerebrom_dir)
    return "~/.kerebrom/: eliminado (base de datos y toda la memoria)"


def _clean_claude_mcp() -> str:
    """Remove kerebrom entry from ~/.claude.json mcpServers and legacy ~/.claude/.mcp.json."""
    messages = []

    # ── New location: ~/.claude.json ──
    claude_json = Path.home() / ".claude.json"
    if claude_json.exists():
        try:
            data = json.loads(claude_json.read_text(encoding="utf-8"))
            mcp_servers = data.get("mcpServers", {})
            for key in ["Kerebrom", "kerebrom"]:
                mcp_servers.pop(key, None)
            if not mcp_servers:
                data.pop("mcpServers", None)
            else:
                data["mcpServers"] = mcp_servers
            claude_json.write_text(
                json.dumps(data, indent=2, ensure_ascii=False) + "\n",
                encoding="utf-8",
            )
            messages.append("claude.json: MCP removido")
        except (json.JSONDecodeError, OSError):
            pass

    # ── Primary location: ~/.claude/.mcp.json ──
    mcp_file = Path.home() / ".claude" / ".mcp.json"
    if mcp_file.exists():
        try:
            data = json.loads(mcp_file.read_text(encoding="utf-8"))
            mcp_servers = data.get("mcpServers", {})
            for key in ["Kerebrom", "kerebrom"]:
                mcp_servers.pop(key, None)
            if not mcp_servers:
                mcp_file.unlink()
            else:
                data["mcpServers"] = mcp_servers
                mcp_file.write_text(
                    json.dumps(data, indent=2, ensure_ascii=False) + "\n",
                    encoding="utf-8",
                )
            messages.append(".mcp.json: Kerebrom removido")
        except (json.JSONDecodeError, OSError):
            pass

    return " + ".join(messages) if messages else "MCP: kerebrom no estaba configurado"


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


def _clean_claude_hooks() -> str:
    """Remove kerebrom hooks from ~/.claude/settings.json."""
    settings_file = Path.home() / ".claude" / "settings.json"
    if not settings_file.exists():
        return "settings.json: no existe"

    try:
        data = json.loads(settings_file.read_text(encoding="utf-8"))
    except (json.JSONDecodeError, OSError):
        return "settings.json: no se pudo leer"

    changed = False

    # Restore auto-memory if we disabled it.
    if data.get("autoMemoryEnabled") is False:
        del data["autoMemoryEnabled"]
        changed = True

    hooks = data.get("hooks", {})
    for event_type in list(hooks.keys()):
        if isinstance(hooks[event_type], list):
            original_len = len(hooks[event_type])

            def _has_kerebrom(entry: dict) -> bool:
                """Check both old and new hook formats for kerebrom."""
                # Old format: {"command": "...kerebrom..."}
                if "kerebrom" in entry.get("command", ""):
                    return True
                # New format: {"hooks": [{"type": "command", "command": "...kerebrom..."}]}
                for h in entry.get("hooks", []):
                    if isinstance(h, dict) and "kerebrom" in h.get("command", ""):
                        return True
                return False

            hooks[event_type] = [
                h for h in hooks[event_type]
                if not (isinstance(h, dict) and _has_kerebrom(h))
            ]
            if len(hooks[event_type]) != original_len:
                changed = True
            if not hooks[event_type]:
                del hooks[event_type]

    if not changed:
        return "settings.json: hooks de kerebrom no estaban configurados"

    if hooks:
        data["hooks"] = hooks
    else:
        data.pop("hooks", None)

    settings_file.write_text(
        json.dumps(data, indent=2, ensure_ascii=False) + "\n",
        encoding="utf-8",
    )
    return "settings.json: hooks de kerebrom removidos"


def _clean_claude_desktop_mcp() -> str:
    """Remove kerebrom entry from Claude Desktop app config."""
    if sys.platform == "darwin":
        config_path = Path.home() / "Library" / "Application Support" / "Claude" / "claude_desktop_config.json"
    elif sys.platform == "win32":
        import os as _os
        config_path = Path(_os.environ.get("APPDATA", "")) / "Claude" / "claude_desktop_config.json"
    else:
        return "Claude Desktop: plataforma no soportada"

    if not config_path.exists():
        return "Claude Desktop: no detectado"

    try:
        data = json.loads(config_path.read_text(encoding="utf-8"))
        mcp_servers = data.get("mcpServers", {})
        found = False
        for key in ["Kerebrom", "kerebrom"]:
            if key in mcp_servers:
                del mcp_servers[key]
                found = True
        if not found:
            return "Claude Desktop: Kerebrom no estaba configurado"
        if not mcp_servers:
            data.pop("mcpServers", None)
        else:
            data["mcpServers"] = mcp_servers
        config_path.write_text(
            json.dumps(data, indent=2, ensure_ascii=False) + "\n",
            encoding="utf-8",
        )
        return "Claude Desktop: MCP removido"
    except (json.JSONDecodeError, OSError):
        return "Claude Desktop: error al limpiar config"


def _clean_codex_config() -> str:
    """Remove [mcp_servers.kerebrom] from ~/.codex/config.toml."""
    config_file = Path.home() / ".codex" / "config.toml"
    if not config_file.exists():
        return "config.toml: no existe"

    content = config_file.read_text(encoding="utf-8")
    if "[mcp_servers.kerebrom]" not in content and "[mcp_servers.Kerebrom]" not in content:
        return "config.toml: kerebrom no estaba configurado"

    # Remove the [mcp_servers.kerebrom] / [mcp_servers.Kerebrom] section (case-insensitive).
    cleaned = re.sub(
        r"\n?\[mcp_servers\.[kK]erebrom[^\]]*\][^\[]*",
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


def _clean_launchagent() -> str:
    """Remove the kerebrom LaunchAgent."""
    if sys.platform != "darwin":
        return "LaunchAgent: no aplica (no macOS)"

    plist_path = Path.home() / "Library" / "LaunchAgents" / "com.kerebrom.setup.plist"
    if not plist_path.exists():
        return "LaunchAgent: no estaba instalado"

    subprocess.run(
        ["launchctl", "unload", str(plist_path)],
        capture_output=True,
    )
    plist_path.unlink()
    return "LaunchAgent: eliminado"


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
        ("Backup automático", _auto_backup),
        ("Base de datos", _remove_kerebrom_db),
        ("Claude Code MCP", _clean_claude_mcp),
        ("Claude Code CLAUDE.md", _clean_claude_md),
        ("Claude Code Hooks", _clean_claude_hooks),
        ("Claude Desktop MCP", _clean_claude_desktop_mcp),
        ("Codex config.toml", _clean_codex_config),
        ("Codex AGENTS.md", _clean_codex_agents_md),
        ("Archivos temporales", _clean_temp_runtime),
        ("LaunchAgent", _clean_launchagent),
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
