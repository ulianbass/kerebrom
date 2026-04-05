# Copyright (c) 2026 Ulian Bass. All rights reserved.
# This software is proprietary. See LICENSE for terms.

"""Kerebrom MCP Server — raw JSON-RPC over stdio.

Implements the Model Context Protocol (spec 2025-03-26) without external
dependencies so it runs on Python 3.9+.  Exposes Kerebrom as tools that
any MCP-compatible AI client can invoke autonomously.

Usage:
    python -m kerebrom serve [--db PATH] [--project NAME]

Clients configure it as:
    {
      "mcpServers": {
        "kerebrom": {
          "command": "python3",
          "args": ["-m", "kerebrom", "serve", "--db", "~/.kerebrom/kerebrom.db"]
        }
      }
    }
"""

from __future__ import annotations

import json
import sys
from pathlib import Path
from typing import Any, Dict, List

from .store import DEFAULT_PROJECT, KerebromStore

PROTOCOL_VERSION = "2025-03-26"
SERVER_NAME = "Kerebrom"
SERVER_VERSION = "0.1.0"

# ---------------------------------------------------------------------------
# Tool definitions exposed via MCP
# ---------------------------------------------------------------------------

TOOLS: List[Dict[str, Any]] = [
    {
        "name": "remember",
        "description": (
            "Store a new memory.  The AI should call this automatically "
            "whenever it learns something worth persisting about the user, "
            "the project, or the conversation — without the user asking."
        ),
        "inputSchema": {
            "type": "object",
            "properties": {
                "content": {
                    "type": "string",
                    "description": "The memory content to store.",
                },
                "kind": {
                    "type": "string",
                    "enum": ["core", "episodic", "semantic", "procedural"],
                    "default": "episodic",
                    "description": "Memory tier.",
                },
                "importance": {
                    "type": "number",
                    "default": 0.5,
                    "description": "Importance score 0-1.",
                },
                "confidence": {
                    "type": "number",
                    "default": 0.8,
                    "description": "Confidence score 0-1.",
                },
                "tags": {
                    "type": "array",
                    "items": {"type": "string"},
                    "default": [],
                    "description": (
                        "Observation tags: decision, bugfix, feature, refactor, "
                        "discovery, change, preference, or custom."
                    ),
                },
                "metadata": {
                    "type": "object",
                    "default": {},
                    "description": (
                        "Metadata adicional en formato JSON. Permite almacenar "
                        "datos estructurados arbitrarios junto a la memoria."
                    ),
                },
            },
            "required": ["content"],
        },
    },
    {
        "name": "recall",
        "description": (
            "Search memories by semantic + keyword + graph query.  "
            "Use this to retrieve relevant context before answering the user."
        ),
        "inputSchema": {
            "type": "object",
            "properties": {
                "query": {
                    "type": "string",
                    "description": "Natural-language search query.",
                },
                "limit": {
                    "type": "integer",
                    "default": 5,
                    "description": "Max memories to return.",
                },
            },
            "required": ["query"],
        },
    },
    {
        "name": "forget",
        "description": "Invalidate a specific memory by ID.",
        "inputSchema": {
            "type": "object",
            "properties": {
                "memory_id": {
                    "type": "integer",
                    "description": "The ID of the memory to invalidate.",
                },
            },
            "required": ["memory_id"],
        },
    },
    {
        "name": "entities",
        "description": "List known entities in the knowledge graph.",
        "inputSchema": {
            "type": "object",
            "properties": {
                "limit": {
                    "type": "integer",
                    "default": 20,
                    "description": "Max entities to return.",
                },
            },
        },
    },
    {
        "name": "facts",
        "description": "List active semantic facts (subject → predicate → object).",
        "inputSchema": {
            "type": "object",
            "properties": {
                "limit": {
                    "type": "integer",
                    "default": 20,
                    "description": "Max facts to return.",
                },
            },
        },
    },
    {
        "name": "sopor",
        "description": (
            "Run Sopor Plenus — consolidate memories from Claude Code "
            "session transcripts.  Reads the JSONL transcript, extracts "
            "decisions, changes, bugs, preferences, and conclusions, "
            "then stores them in Kerebrom.  Call this at the end of long "
            "sessions or when the context window is getting large."
        ),
        "inputSchema": {
            "type": "object",
            "properties": {
                "session": {
                    "type": "string",
                    "default": "latest",
                    "description": (
                        "Which transcript to process: 'latest' (default), "
                        "'all', or a specific session ID / file path."
                    ),
                },
                "project_path": {
                    "type": "string",
                    "description": (
                        "Filesystem path of the project whose transcripts "
                        "to consolidate.  If omitted, searches all projects."
                    ),
                },
            },
        },
    },
    {
        "name": "query",
        "description": (
            "Consulta estructurada de memorias con filtros combinables. "
            "Permite filtrar por tipo, tags, importancia, fecha, fuente y metadata."
        ),
        "inputSchema": {
            "type": "object",
            "properties": {
                "kind": {
                    "type": "string",
                    "enum": ["core", "episodic", "semantic", "procedural"],
                    "description": "Filtrar por tipo de memoria.",
                },
                "tags": {
                    "type": "array",
                    "items": {"type": "string"},
                    "description": "Filtrar memorias que contengan al menos uno de estos tags.",
                },
                "importance_min": {
                    "type": "number",
                    "description": "Importancia mínima (0-1).",
                },
                "importance_max": {
                    "type": "number",
                    "description": "Importancia máxima (0-1).",
                },
                "created_after": {
                    "type": "string",
                    "description": "Solo memorias creadas después de esta fecha ISO.",
                },
                "created_before": {
                    "type": "string",
                    "description": "Solo memorias creadas antes de esta fecha ISO.",
                },
                "source": {
                    "type": "string",
                    "description": "Filtrar por fuente (mcp, manual, sopor, etc.).",
                },
                "metadata_filter": {
                    "type": "object",
                    "description": "Filtro clave/valor sobre el campo metadata JSON.",
                },
                "limit": {
                    "type": "integer",
                    "default": 20,
                    "description": "Máximo de memorias a devolver.",
                },
                "order_by": {
                    "type": "string",
                    "default": "created_at",
                    "description": "Columna de ordenamiento (created_at, updated_at, importance, confidence, access_count).",
                },
                "order_dir": {
                    "type": "string",
                    "default": "DESC",
                    "enum": ["ASC", "DESC"],
                    "description": "Dirección de ordenamiento.",
                },
            },
        },
    },
    {
        "name": "gaps",
        "description": (
            "Listar referencias no resueltas — huecos de conocimiento en el grafo. "
            "Devuelve entidades mencionadas en relaciones que no existían como "
            "entidades canónicas al momento de ser extraídas."
        ),
        "inputSchema": {
            "type": "object",
            "properties": {
                "limit": {
                    "type": "integer",
                    "default": 20,
                    "description": "Máximo de referencias a devolver.",
                },
            },
        },
    },
    {
        "name": "context",
        "description": (
            "Build a rich recall context for a query — returns facts and "
            "relevant memories in a compact, progressive-disclosure format."
        ),
        "inputSchema": {
            "type": "object",
            "properties": {
                "query": {
                    "type": "string",
                    "description": "Natural-language context query.",
                },
                "limit": {
                    "type": "integer",
                    "default": 5,
                    "description": "Max memories in context.",
                },
                "layer": {
                    "type": "integer",
                    "enum": [1, 2, 3],
                    "default": 2,
                    "description": (
                        "Disclosure layer: 1=compact index (~50 tokens), "
                        "2=chronological summary (~200 tokens), "
                        "3=full detail (~500+ tokens)."
                    ),
                },
            },
            "required": ["query"],
        },
    },
]

# ---------------------------------------------------------------------------
# JSON-RPC helpers
# ---------------------------------------------------------------------------


def _ok(id: Any, result: Any) -> Dict[str, Any]:
    return {"jsonrpc": "2.0", "id": id, "result": result}


def _error(id: Any, code: int, message: str) -> Dict[str, Any]:
    return {"jsonrpc": "2.0", "id": id, "error": {"code": code, "message": message}}


def _write(msg: Dict[str, Any]) -> None:
    payload = json.dumps(msg, ensure_ascii=False)
    sys.stdout.write(payload + "\n")
    sys.stdout.flush()


def _log(text: str) -> None:
    sys.stderr.write("[kerebrom] {}\n".format(text))
    sys.stderr.flush()


# ---------------------------------------------------------------------------
# Request handlers
# ---------------------------------------------------------------------------


def handle_initialize(id: Any, _params: Dict[str, Any]) -> Dict[str, Any]:
    # Negotiate protocol version: use the client's version if provided,
    # so we stay compatible with older clients (e.g. Claude Desktop).
    client_version = _params.get("protocolVersion", PROTOCOL_VERSION)
    return _ok(id, {
        "protocolVersion": client_version,
        "capabilities": {
            "tools": {"listChanged": False},
        },
        "serverInfo": {
            "name": SERVER_NAME,
            "version": SERVER_VERSION,
        },
    })


def handle_tools_list(id: Any, _params: Dict[str, Any]) -> Dict[str, Any]:
    return _ok(id, {"tools": TOOLS})


def handle_tools_call(
    id: Any,
    params: Dict[str, Any],
    store: KerebromStore,
    project: str,
) -> Dict[str, Any]:
    tool_name = params.get("name", "")
    arguments = params.get("arguments", {})

    try:
        result = _dispatch_tool(tool_name, arguments, store, project)
        return _ok(id, {
            "content": [{"type": "text", "text": json.dumps(result, ensure_ascii=False, indent=2)}],
        })
    except Exception as exc:
        return _ok(id, {
            "content": [{"type": "text", "text": "Error: {}".format(exc)}],
            "isError": True,
        })


def _dispatch_tool(
    name: str,
    args: Dict[str, Any],
    store: KerebromStore,
    project: str,
) -> Any:
    if name == "remember":
        return store.remember(
            content=args["content"],
            project=project,
            kind=args.get("kind", "episodic"),
            source="mcp",
            importance=args.get("importance", 0.5),
            confidence=args.get("confidence", 0.8),
            tags=args.get("tags", []),
            metadata=args.get("metadata"),
        )

    if name == "recall":
        results = store.recall(
            query=args["query"],
            project=project,
            limit=args.get("limit", 5),
        )
        return [r.to_dict() for r in results]

    if name == "forget":
        count = store.forget(project=project, memory_id=args["memory_id"])
        return {"invalidated": count}

    if name == "entities":
        return store.list_entities(project=project, limit=args.get("limit", 20))

    if name == "facts":
        return store.list_facts(project=project, limit=args.get("limit", 20))

    if name == "sopor":
        from .sopor import run_sopor, run_sopor_all, run_sopor_latest
        session = args.get("session", "latest")
        project_path = args.get("project_path")

        if session == "all":
            results = run_sopor_all(store, project_path=project_path, project=project)
            return {
                "transcripts_processed": len(results),
                "total_messages": sum(r.messages_read for r in results),
                "total_extracted": sum(r.memories_extracted for r in results),
                "total_stored": sum(r.memories_stored for r in results),
                "total_skipped": sum(r.memories_skipped_duplicate for r in results),
                "errors": [e for r in results for e in r.errors],
            }
        elif session == "latest":
            result = run_sopor_latest(store, project_path=project_path, project=project)
            if result is None:
                return {"error": "No transcripts found."}
            return {
                "session_id": result.session_id,
                "messages_read": result.messages_read,
                "memories_extracted": result.memories_extracted,
                "memories_stored": result.memories_stored,
                "memories_skipped_duplicate": result.memories_skipped_duplicate,
                "errors": result.errors,
            }
        else:
            from pathlib import Path as _Path
            session_path = _Path(session)
            if not session_path.exists():
                candidates = list(_Path.home().joinpath(".claude", "projects").rglob("{}.jsonl".format(session)))
                if not candidates:
                    return {"error": "Transcript not found: {}".format(session)}
                session_path = candidates[0]
            result = run_sopor(store, session_path, project=project)
            return {
                "session_id": result.session_id,
                "messages_read": result.messages_read,
                "memories_extracted": result.memories_extracted,
                "memories_stored": result.memories_stored,
                "memories_skipped_duplicate": result.memories_skipped_duplicate,
                "errors": result.errors,
            }

    if name == "context":
        layer = args.get("layer", 2)
        return store.build_context(
            query=args["query"],
            project=project,
            limit=args.get("limit", 5),
            layer=layer,
        )

    if name == "query":
        results = store.query(
            project=project,
            kind=args.get("kind"),
            tags=args.get("tags"),
            importance_min=args.get("importance_min"),
            importance_max=args.get("importance_max"),
            created_after=args.get("created_after"),
            created_before=args.get("created_before"),
            source=args.get("source"),
            metadata_filter=args.get("metadata_filter"),
            limit=args.get("limit", 20),
            order_by=args.get("order_by", "created_at"),
            order_dir=args.get("order_dir", "DESC"),
        )
        return [r.to_dict() for r in results]

    if name == "gaps":
        return store.list_unresolved_references(
            project=project,
            limit=args.get("limit", 20),
        )

    raise ValueError("Unknown tool: {}".format(name))


# ---------------------------------------------------------------------------
# Main server loop
# ---------------------------------------------------------------------------


def run_server(db_path: str, project: str = DEFAULT_PROJECT, passphrase: str | None = None) -> None:
    """Blocking stdio loop — reads JSON-RPC from stdin, writes to stdout."""
    # Opportunistic auto-setup — configures detected AI tools silently.
    from .setup import ensure_setup
    ensure_setup(db_path=Path(db_path))

    store = KerebromStore(Path(db_path), passphrase=passphrase)
    store.initialize(project=project)
    _log("Server ready — db={} project={}".format(db_path, project))

    try:
        for line in sys.stdin:
            line = line.strip()
            if not line:
                continue

            try:
                msg = json.loads(line)
            except json.JSONDecodeError:
                _write(_error(None, -32700, "Parse error"))
                continue

            method = msg.get("method", "")
            msg_id = msg.get("id")
            params = msg.get("params", {})

            # Notifications (no id) — just acknowledge silently
            if msg_id is None:
                if method == "notifications/initialized":
                    _log("Client initialized.")
                continue

            if method == "initialize":
                _write(handle_initialize(msg_id, params))
            elif method == "tools/list":
                _write(handle_tools_list(msg_id, params))
            elif method == "tools/call":
                _write(handle_tools_call(msg_id, params, store, project))
            elif method == "ping":
                _write(_ok(msg_id, {}))
            else:
                _write(_error(msg_id, -32601, "Method not found: {}".format(method)))
    finally:
        store.close()
