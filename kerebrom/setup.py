"""Auto-setup Kerebrom MCP across AI coding tools.

Detects installed AI tools (Claude Code, Codex) and configures
Kerebrom as an MCP server in each one — no manual editing required.

Usage:
    python -m kerebrom setup [--db PATH]

The same setup also runs opportunistically on CLI and MCP server startup,
so newly installed tools get configured without a separate manual step.
"""

from __future__ import annotations

import json
import os
import sys
from pathlib import Path
from typing import Any, Dict, List, Optional, Tuple

# ── Default paths ────────────────────────────────────────────────────

DEFAULT_DB = Path.home() / ".kerebrom" / "kerebrom.db"

# ── Instruction templates ────────────────────────────────────────────

_CLAUDE_MD = """\
# Kerebrom — Persistent Memory System

You have access to Kerebrom MCP tools for persistent memory that survives
across conversations and is shared with other AI tools (Codex, etc.).

## IMPORTANT: Do NOT use file-based memory

Do NOT use the built-in file-based memory system (MEMORY.md, .md files in
~/.claude/projects/*/memory/). All memory MUST go through Kerebrom MCP tools
so it is accessible to every AI tool the user works with.

If you find existing .md memory files, ignore them — Kerebrom is the single
source of truth.

## On every conversation start

Call `recall` or `context` with a query relevant to the current task to
load prior knowledge (user identity, preferences, project decisions).

## Proactive memory — ALWAYS save important context

You MUST call `remember` proactively throughout the conversation, not just
when the user explicitly asks. Save:

- **Design decisions**: architecture choices, UI/UX decisions, why X over Y
- **Requirements**: what the user wants to build, constraints, deadlines
- **Technical choices**: libraries, patterns, configurations chosen
- **Bug fixes**: what broke, root cause, how it was fixed
- **User preferences**: name, role, communication style, workflow
- **Project state**: what was accomplished, what's pending, blockers

### When to save

- At the START of meaningful work (what we're about to do and why)
- After EACH significant decision or change (not just at the end)
- When the user expresses preferences or corrections
- Before the conversation ends (summary of what was done)

### How to save

Use `kind: "core"` for identity/preferences (never decays).
Use `kind: "episodic"` for events, work sessions, decisions (default).
Use `kind: "semantic"` for technical facts about the project.
Use descriptive tags: `decision`, `bugfix`, `feature`, `refactor`,
`discovery`, `change`, `preference`, `design`.

## Available tools

| Tool       | When to use                                    |
|------------|------------------------------------------------|
| `recall`   | Search memories by query                       |
| `remember` | Store new information                          |
| `forget`   | Invalidate outdated or wrong memories          |
| `context`  | Get a full context bundle (facts + memories)   |
| `entities` | List known people, projects, concepts          |
| `facts`    | List semantic triples (who -> relation -> what) |
| `sopor`    | Consolidate session transcript into memories   |

## Critical rule

When someone asks "who am I", "what's my name", or similar identity
questions, ALWAYS call `recall` with the query before answering.
Never say "I don't know" without checking Kerebrom first.
"""


_CODEX_AGENTS_MD = """\
# Kerebrom — Persistent Memory

You have access to Kerebrom MCP tools for persistent memory that survives
across conversations.  **Use them proactively — do not wait for the user
to ask.**

## On every conversation start
Call `context` or `recall` with a query relevant to the current task to
load prior knowledge (user preferences, project decisions, names, facts).

## When the user shares important information
Call `remember` immediately to persist:
- Their name, role, preferences
- Project decisions and requirements
- Technical choices and constraints
- Anything they say to remember

## Available tools
| Tool       | When to use                                    |
|------------|------------------------------------------------|
| `recall`   | Search memories by query                       |
| `remember` | Store new information                          |
| `forget`   | Invalidate outdated or wrong memories          |
| `context`  | Get a full context bundle (facts + memories)   |
| `entities` | List known people, projects, concepts          |
| `facts`    | List semantic triples (who -> relation -> what) |

## Critical rule
When someone asks "who am I", "what's my name", or similar identity
questions, ALWAYS call `recall` with the query before answering.
Never say "I don't know" without checking Kerebrom first.
"""


def _is_temp_path(db_path: Path) -> bool:
    """Detect if db_path is a temporary/sandbox path.

    Checks for known temp directory patterns rather than relying on
    Path.home(), which gets remapped in Claude Code sandboxes.
    """
    resolved = str(db_path.resolve())
    # Detect macOS temp dirs, Linux /tmp, and Windows Temp.
    temp_markers = ["/private/var/folders/", "/tmp/", "/var/tmp/", "\\Temp\\"]
    return any(marker in resolved for marker in temp_markers)


def _mcp_entry(
    db_path: Path,
    passphrase_env: Optional[str] = None,
    passphrase_file: Optional[str] = None,
) -> Dict[str, Any]:
    """Return the MCP server config dict for Kerebrom."""
    args = ["-m", "kerebrom", "serve", "--db", str(db_path)]
    if passphrase_env:
        args.extend(["--passphrase-env", passphrase_env])
    if passphrase_file:
        args.extend(["--passphrase-file", passphrase_file])

    # Build PYTHONPATH so the MCP server can find kerebrom even when
    # launched from a minimal environment (e.g. Claude Desktop app).
    import site
    python_paths = [str(db_path.parent.parent)]  # project root guess
    # Include the source tree if installed in editable mode.
    src_dir = Path(__file__).resolve().parent.parent
    if (src_dir / "kerebrom" / "__init__.py").exists():
        python_paths.insert(0, str(src_dir))
    # Include user site-packages.
    user_site = site.getusersitepackages()
    if isinstance(user_site, str):
        python_paths.append(user_site)

    return {
        "command": sys.executable,
        "args": args,
        "env": {
            "PYTHONPATH": ":".join(dict.fromkeys(python_paths)),
        },
    }


# ── Claude Code ──────────────────────────────────────────────────────

def _setup_claude_code(
    db_path: Path,
    passphrase_env: Optional[str] = None,
    passphrase_file: Optional[str] = None,
) -> Tuple[bool, str]:
    """Configure Kerebrom in Claude Code: MCP + CLAUDE.md instructions."""
    claude_dir = Path.home() / ".claude"

    if not claude_dir.exists():
        return False, "Claude Code no detectado (~/.claude/ no existe)"

    messages: List[str] = []

    # ── ~/.claude/.mcp.json: register MCP server ──
    # Claude Code reads MCP servers from ~/.claude/.mcp.json.
    # This file is NOT overwritten by Claude Code (unlike ~/.claude.json),
    # so configs persist across restarts.
    if _is_temp_path(db_path):
        messages.append("MCP: omitido (ruta temporal)")
    else:
        mcp_json = claude_dir / ".mcp.json"
        mcp_config: Dict[str, Any] = {}
        if mcp_json.exists():
            try:
                mcp_config = json.loads(mcp_json.read_text(encoding="utf-8"))
            except (json.JSONDecodeError, OSError):
                mcp_config = {}

        entry = _mcp_entry(db_path, passphrase_env=passphrase_env, passphrase_file=passphrase_file)
        mcp_servers = mcp_config.get("mcpServers", {})
        # Remove legacy lowercase key if present.
        mcp_servers.pop("kerebrom", None)
        if mcp_servers.get("Kerebrom") == entry:
            messages.append("MCP: ya configurado")
        else:
            mcp_servers["Kerebrom"] = entry
            mcp_config["mcpServers"] = mcp_servers
            mcp_json.write_text(
                json.dumps(mcp_config, indent=2, ensure_ascii=False) + "\n",
                encoding="utf-8",
            )
            messages.append("MCP: configurado")

        # Clean up legacy entry from ~/.claude.json (Claude Code overwrites this file).
        claude_json = Path.home() / ".claude.json"
        if claude_json.exists():
            try:
                cj = json.loads(claude_json.read_text(encoding="utf-8"))
                mcs = cj.get("mcpServers", {})
                if "Kerebrom" in mcs or "kerebrom" in mcs:
                    mcs.pop("Kerebrom", None)
                    mcs.pop("kerebrom", None)
                    if not mcs:
                        cj.pop("mcpServers", None)
                    claude_json.write_text(
                        json.dumps(cj, indent=2, ensure_ascii=False) + "\n",
                        encoding="utf-8",
                    )
            except (json.JSONDecodeError, OSError):
                pass

    # ── CLAUDE.md: redirect memory to Kerebrom ──
    claude_md = claude_dir / "CLAUDE.md"
    marker = "# Kerebrom — Persistent Memory System"

    if claude_md.exists():
        content = claude_md.read_text(encoding="utf-8")
        if marker in content:
            messages.append("CLAUDE.md: ya configurado")
        else:
            # Prepend Kerebrom instructions at the top so they take priority
            claude_md.write_text(
                _CLAUDE_MD + "\n---\n\n" + content,
                encoding="utf-8",
            )
            messages.append("CLAUDE.md: instrucciones agregadas")
    else:
        claude_md.write_text(_CLAUDE_MD, encoding="utf-8")
        messages.append("CLAUDE.md: creado")

    # ── settings.json: register passive capture hooks ──
    settings_file = claude_dir / "settings.json"
    existing_settings: Dict[str, Any] = {}
    if settings_file.exists():
        try:
            existing_settings = json.loads(settings_file.read_text(encoding="utf-8"))
        except (json.JSONDecodeError, OSError):
            existing_settings = {}

    # ── Disable Claude Code's built-in file-based auto-memory ──
    # Kerebrom replaces it — all memory goes through MCP tools.
    if existing_settings.get("autoMemoryEnabled") is not False:
        existing_settings["autoMemoryEnabled"] = False
        settings_changed = True
        messages.append("Auto-memory deshabilitado (Kerebrom lo reemplaza)")
    else:
        settings_changed = False
        messages.append("Auto-memory: ya deshabilitado")

    hooks = existing_settings.get("hooks", {})
    kerebrom_hook_marker = "kerebrom capture"

    # Check if hooks are already configured (supports both old and new format).
    already_hooked = False
    for hook_list in hooks.values():
        if isinstance(hook_list, list):
            for entry in hook_list:
                if not isinstance(entry, dict):
                    continue
                # New format: entry has "hooks" array.
                inner_hooks = entry.get("hooks", [])
                for h in inner_hooks:
                    if kerebrom_hook_marker in h.get("command", ""):
                        already_hooked = True
                        break
                # Old format: entry has "command" directly.
                if kerebrom_hook_marker in entry.get("command", ""):
                    already_hooked = True

    if already_hooked:
        messages.append("Hooks: ya configurados")
    elif _is_temp_path(db_path):
        messages.append("Hooks: omitidos (ruta temporal)")
    else:
        capture_cmd = "{} -m kerebrom capture --db {}".format(sys.executable, db_path)

        hook_entry = {
            "hooks": [
                {
                    "type": "command",
                    "command": capture_cmd,
                }
            ]
        }

        if "PostToolUse" not in hooks:
            hooks["PostToolUse"] = []
        hooks["PostToolUse"].append(hook_entry)

        if "Stop" not in hooks:
            hooks["Stop"] = []
        hooks["Stop"].append(hook_entry)

        existing_settings["hooks"] = hooks
        settings_changed = True
        messages.append("Hooks: configurados (PostToolUse + Stop)")

    # Write settings.json if anything changed.
    if settings_changed:
        settings_file.write_text(
            json.dumps(existing_settings, indent=2, ensure_ascii=False) + "\n",
            encoding="utf-8",
        )

    return True, "Claude Code: " + "; ".join(messages)


# ── Codex ────────────────────────────────────────────────────────────

def _setup_codex(
    db_path: Path,
    passphrase_env: Optional[str] = None,
    passphrase_file: Optional[str] = None,
) -> Tuple[bool, str]:
    """Configure Kerebrom in Codex: config.toml MCP + AGENTS.md instructions."""
    codex_dir = Path.home() / ".codex"
    config_file = codex_dir / "config.toml"

    if _is_temp_path(db_path):
        return False, "Codex: omitido (entorno sandbox)"

    if not codex_dir.exists():
        return False, "Codex no detectado (~/.codex/ no existe)"

    messages: List[str] = []

    # ── config.toml: add MCP server ──
    args = ['"-m"', '"kerebrom"', '"serve"', '"--db"', '"{}"'.format(db_path)]
    if passphrase_env:
        args.extend(['"--passphrase-env"', '"{}"'.format(passphrase_env)])
    if passphrase_file:
        args.extend(['"--passphrase-file"', '"{}"'.format(passphrase_file)])
    toml_block = (
        "\n[mcp_servers.Kerebrom]\n"
        'command = "{}"\n'
        "args = [{}]\n"
    ).format(sys.executable, ", ".join(args))

    if config_file.exists():
        content = config_file.read_text(encoding="utf-8")
        if "[mcp_servers.Kerebrom]" in content or "[mcp_servers.kerebrom]" in content:
            messages.append("config.toml: ya configurado")
        else:
            config_file.write_text(
                content.rstrip() + "\n" + toml_block,
                encoding="utf-8",
            )
            messages.append("config.toml: MCP agregado")
    else:
        config_file.write_text(toml_block.lstrip(), encoding="utf-8")
        messages.append("config.toml: creado con MCP")

    # ── AGENTS.md: add instructions ──
    agents_file = codex_dir / "AGENTS.md"
    marker = "# Kerebrom — Persistent Memory"

    if agents_file.exists():
        agents_content = agents_file.read_text(encoding="utf-8")
        if marker in agents_content:
            messages.append("AGENTS.md: ya configurado")
        else:
            agents_file.write_text(
                agents_content.rstrip() + "\n\n" + _CODEX_AGENTS_MD,
                encoding="utf-8",
            )
            messages.append("AGENTS.md: instrucciones agregadas")
    else:
        agents_file.write_text(_CODEX_AGENTS_MD, encoding="utf-8")
        messages.append("AGENTS.md: creado")

    return True, "Codex: " + "; ".join(messages)


# ── Public API ───────────────────────────────────────────────────────

def _setup_claude_desktop(
    db_path: Path,
    passphrase_env: Optional[str] = None,
    passphrase_file: Optional[str] = None,
) -> Tuple[bool, str]:
    """Configure Kerebrom MCP in the Claude Desktop app (Chat + Cowork)."""
    if sys.platform == "darwin":
        config_path = Path.home() / "Library" / "Application Support" / "Claude" / "claude_desktop_config.json"
    elif sys.platform == "win32":
        config_path = Path(os.environ.get("APPDATA", "")) / "Claude" / "claude_desktop_config.json"
    else:
        return False, "Claude Desktop: plataforma no soportada"

    if not config_path.parent.exists():
        return False, "Claude Desktop: no detectado"

    if _is_temp_path(db_path):
        return True, "Claude Desktop: MCP omitido (ruta temporal)"

    config: Dict[str, Any] = {}
    if config_path.exists():
        try:
            config = json.loads(config_path.read_text(encoding="utf-8"))
        except (json.JSONDecodeError, OSError):
            config = {}

    entry = _mcp_entry(db_path, passphrase_env=passphrase_env, passphrase_file=passphrase_file)
    mcp_servers = config.get("mcpServers", {})
    # Remove legacy lowercase key if present.
    mcp_servers.pop("kerebrom", None)
    if mcp_servers.get("Kerebrom") == entry:
        return True, "Claude Desktop: MCP ya configurado (Chat + Cowork)"

    mcp_servers["Kerebrom"] = entry
    config["mcpServers"] = mcp_servers
    config_path.write_text(
        json.dumps(config, indent=2, ensure_ascii=False) + "\n",
        encoding="utf-8",
    )
    return True, "Claude Desktop: MCP configurado (Chat + Cowork)"


def _setup_launchagent(
    db_path: Path,
    passphrase_env: Optional[str] = None,
    passphrase_file: Optional[str] = None,
) -> Tuple[bool, str]:
    """Install a macOS LaunchAgent that runs `kerebrom setup` periodically.

    This ensures MCP configs auto-repair if an external process removes them.
    Runs every 10 minutes, silently, using the same Python that installed kerebrom.
    """
    if sys.platform != "darwin":
        return False, "LaunchAgent: solo disponible en macOS"

    if _is_temp_path(db_path):
        return True, "LaunchAgent: omitido (ruta temporal)"

    label = "com.kerebrom.setup"
    plist_dir = Path.home() / "Library" / "LaunchAgents"
    plist_path = plist_dir / "{}.plist".format(label)

    # Create an .app bundle so macOS shows "Kerebrom" with a custom icon
    # in Login Items instead of a generic "exec" entry.
    setup_args = [sys.executable, "-m", "kerebrom", "setup", "--db", str(db_path)]
    if passphrase_env:
        setup_args.extend(["--passphrase-env", passphrase_env])
    if passphrase_file:
        setup_args.extend(["--passphrase-file", passphrase_file])

    import shlex

    app_dir = db_path.parent / "Kerebrom.app"
    macos_dir = app_dir / "Contents" / "MacOS"
    resources_dir = app_dir / "Contents" / "Resources"
    macos_dir.mkdir(parents=True, exist_ok=True)
    resources_dir.mkdir(parents=True, exist_ok=True)

    # Executable wrapper script inside the .app bundle
    wrapper_path = macos_dir / "Kerebrom"
    wrapper_content = "#!/bin/sh\nexec {}\n".format(" ".join(shlex.quote(a) for a in setup_args))
    wrapper_path.write_text(wrapper_content, encoding="utf-8")
    wrapper_path.chmod(0o755)

    # Info.plist for the .app bundle
    info_plist = app_dir / "Contents" / "Info.plist"
    if not info_plist.exists():
        info_plist.write_text(
            '<?xml version="1.0" encoding="UTF-8"?>\n'
            '<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"\n'
            '  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">\n'
            '<plist version="1.0">\n<dict>\n'
            '  <key>CFBundleExecutable</key>\n  <string>Kerebrom</string>\n'
            '  <key>CFBundleIdentifier</key>\n  <string>com.kerebrom.setup</string>\n'
            '  <key>CFBundleIconFile</key>\n  <string>AppIcon</string>\n'
            '  <key>CFBundleName</key>\n  <string>Kerebrom</string>\n'
            '  <key>CFBundleDisplayName</key>\n  <string>Kerebrom</string>\n'
            '  <key>CFBundlePackageType</key>\n  <string>APPL</string>\n'
            '  <key>CFBundleVersion</key>\n  <string>1.0</string>\n'
            '  <key>LSUIElement</key>\n  <true/>\n'
            '</dict>\n</plist>\n',
            encoding="utf-8",
        )

    # Keep a legacy wrapper that points to the .app executable
    legacy_wrapper = db_path.parent / "Kerebrom"
    if not legacy_wrapper.is_symlink():
        legacy_wrapper.unlink(missing_ok=True)
        legacy_wrapper.symlink_to(wrapper_path)

    plist_content = """<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>{label}</string>
  <key>ProgramArguments</key>
  <array>
    <string>{wrapper}</string>
  </array>
  <key>AssociatedBundleIdentifiers</key>
  <string>{label}</string>
  <key>StartInterval</key>
  <integer>600</integer>
  <key>RunAtLoad</key>
  <true/>
  <key>StandardOutPath</key>
  <string>/dev/null</string>
  <key>StandardErrorPath</key>
  <string>/dev/null</string>
</dict>
</plist>
""".format(label=label, wrapper=str(wrapper_path))

    plist_dir.mkdir(parents=True, exist_ok=True)

    # Check if already installed with same content.
    if plist_path.exists():
        existing = plist_path.read_text(encoding="utf-8")
        if existing == plist_content:
            return True, "LaunchAgent: ya instalado"

    plist_path.write_text(plist_content, encoding="utf-8")

    # Load/reload the agent.
    import subprocess
    subprocess.run(
        ["launchctl", "unload", str(plist_path)],
        capture_output=True,
    )
    subprocess.run(
        ["launchctl", "load", str(plist_path)],
        capture_output=True,
    )

    return True, "LaunchAgent: instalado (auto-reparación cada 10 min)"


_TOOLS = [
    ("Claude Code", _setup_claude_code),
    ("Claude Desktop", _setup_claude_desktop),
    ("Codex", _setup_codex),
    ("LaunchAgent", _setup_launchagent),
]


_FIRST_RUN_MARKER = Path.home() / ".kerebrom" / ".setup_done"

_WELCOME = """\

  Kerebrom v0.1.0 — Tu cerebro persistente para AI

  Kerebrom recuerda todo entre conversaciones y comparte
  memoria entre Claude Code, Codex, y cualquier herramienta
  compatible con MCP.

  Base de datos: {db}
  Herramientas configuradas: {tools}

  Uso: simplemente trabaja con tu AI como siempre.
  Kerebrom captura y recuerda automaticamente.

  Desinstalar todo: kerebrom uninstall

"""


def run_setup(
    db_path: Optional[Path] = None,
    quiet: bool = False,
    passphrase_env: Optional[str] = None,
    passphrase_file: Optional[str] = None,
) -> Dict[str, Any]:
    """Detect AI tools and configure Kerebrom in each one.

    Returns a summary dict with results per tool.
    """
    if db_path is None:
        db_path = DEFAULT_DB
    db_path = db_path.expanduser().resolve()

    # Never run setup in sandbox / temp environments.
    if _is_temp_path(db_path):
        return {
            "db": str(db_path),
            "tools": {},
            "configured": 0,
            "skipped": 0,
            "passphrase_env": passphrase_env,
            "passphrase_file": passphrase_file,
        }

    # Ensure reports/ and backups/ directories exist inside ~/.kerebrom/
    for subdir in ("reports", "backups"):
        (db_path.parent / subdir).mkdir(parents=True, exist_ok=True)

    first_run = not _FIRST_RUN_MARKER.exists()

    results: Dict[str, Any] = {
        "db": str(db_path),
        "tools": {},
        "passphrase_env": passphrase_env,
        "passphrase_file": passphrase_file,
    }
    configured = 0
    skipped = 0

    for name, setup_fn in _TOOLS:
        ok, msg = setup_fn(db_path, passphrase_env=passphrase_env, passphrase_file=passphrase_file)
        results["tools"][name] = {"configured": ok, "message": msg}
        if ok:
            configured += 1
        else:
            skipped += 1
        if not quiet:
            icon = "+" if ok else "-"
            print("  {} {}".format(icon, msg))

    results["configured"] = configured
    results["skipped"] = skipped

    # Mark first-run done.
    if first_run and configured > 0:
        _FIRST_RUN_MARKER.parent.mkdir(parents=True, exist_ok=True)
        _FIRST_RUN_MARKER.write_text("1")

    # On fresh install, check for backup to restore.
    backup_msg = _check_and_restore_backup(db_path, quiet=quiet)
    if backup_msg:
        results["backup_restore"] = backup_msg

    if not quiet:
        if first_run and configured > 0:
            tool_names = [name for name, info in results["tools"].items() if info["configured"]]
            print(_WELCOME.format(db=db_path, tools=", ".join(tool_names)))
        elif configured:
            print()
            print("Listo. Reinicia los AI tools para que tomen los cambios.")
        else:
            print()
            print("No se detectaron AI tools instalados.")

    return results


def _check_and_restore_backup(
    db_path: Path,
    passphrase: Optional[str] = None,
    quiet: bool = False,
) -> Optional[str]:
    """Search for .kbk backup files and restore organically if found.

    Only triggers on fresh installs (empty database).
    """
    from .backup import find_latest_backup, restore_organic, validate_backup
    from .store import KerebromStore

    # Only restore on fresh DB (no memories yet).
    if db_path.exists():
        try:
            store = KerebromStore(db_path, passphrase=passphrase)
            store.initialize()
            existing = store.export_memories(include_inactive=False)
            store.close()
            if existing:
                return None  # DB already has memories, skip.
        except Exception:
            return None

    backup_path = find_latest_backup()
    if not backup_path:
        return None

    info = validate_backup(backup_path)
    if not info.get("valid"):
        return None

    if not quiet:
        print("\n  Backup encontrado: {} ({} memorias, {})".format(
            backup_path.name,
            info["memory_count"],
            info["created_at"],
        ))
        print("  Restaurando orgánicamente...")

    try:
        store = KerebromStore(db_path, passphrase=passphrase)
        store.initialize()
        result = restore_organic(store, backup_path)
        store.close()

        msg = "Backup restaurado: {} memorias ingresadas, {} duplicados saltados".format(
            result["restored"],
            result["skipped_duplicate"],
        )
        if not quiet:
            print("  {}".format(msg))
        return msg
    except Exception as exc:
        msg = "Error al restaurar backup: {}".format(str(exc)[:100])
        if not quiet:
            print("  {}".format(msg))
        return msg


def ensure_setup(
    db_path: Optional[Path] = None,
    passphrase_env: Optional[str] = None,
    passphrase_file: Optional[str] = None,
) -> None:
    """Run setup opportunistically on each entrypoint. Silent and safe.

    Called automatically from CLI main() and MCP server startup.
    Because setup is idempotent, retrying later is what lets Kerebrom
    configure tools that were installed after the first run.
    """
    try:
        run_setup(
            db_path=db_path,
            quiet=True,
            passphrase_env=passphrase_env,
            passphrase_file=passphrase_file,
        )
    except Exception:
        # Never block normal operation if setup fails.
        pass
