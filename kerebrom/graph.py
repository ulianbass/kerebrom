# Copyright (c) 2026 Ulian Bass. All rights reserved.
# Proprietary — see LICENSE for details.
"""Visualizador interactivo de grafo de conocimiento para Kerebrom.

Lanza un servidor HTTP local que sirve una interfaz WebGL con force-graph
para explorar entidades, relaciones y memorias de forma visual.
"""
from __future__ import annotations

import json
import threading
import webbrowser
from http.server import HTTPServer, BaseHTTPRequestHandler
from pathlib import Path
from typing import Any, Dict, Optional

from .store import KerebromStore


# ---------------------------------------------------------------------------
# Construccion de datos del grafo
# ---------------------------------------------------------------------------

_ENTITY_COLORS: Dict[str, str] = {
    "person": "#4a9eff",
    "location": "#4ecdc4",
    "organization": "#ff6b6b",
    "concept": "#a855f7",
}
_MEMORY_COLORS: Dict[str, str] = {
    "core": "#fbbf24",
    "semantic": "#818cf8",
    "episodic": "#6b7280",
    "procedural": "#34d399",
}
_DEFAULT_COLOR = "#9ca3af"


def build_graph_data(store: KerebromStore, project: str = "default") -> Dict[str, Any]:
    """Construye nodos y links para el grafo interactivo.

    Enfoque centrado en memorias: las memorias son los nodos principales,
    conectadas entre si cuando comparten entidades significativas.
    Las entidades con relaciones reales tambien aparecen como nodos puente.
    """
    nodes = []
    links = []
    link_set: set = set()  # evitar links duplicados

    from .capture import COMMON_ENTITY_WORDS

    with store.connect() as conn:
        # --- Memorias activas ---
        memories = conn.execute(
            "SELECT id, content, kind, importance, tags FROM memories "
            "WHERE project = ? AND invalid_at IS NULL "
            "ORDER BY importance DESC",
            (project,),
        ).fetchall()

        # Mapa: entity_id -> [memory_ids]
        entity_to_mems: Dict[int, list] = {}
        mem_to_entities: Dict[int, list] = {}
        for m in memories:
            mid = m[0]
            ents = conn.execute(
                "SELECT entity_id FROM memory_entities WHERE memory_id = ?",
                (mid,),
            ).fetchall()
            ent_ids = [e[0] for e in ents]
            mem_to_entities[mid] = ent_ids
            for eid in ent_ids:
                entity_to_mems.setdefault(eid, []).append(mid)

        # Filtrar entidades significativas (nombre > 2 chars, no stopword, >=2 memorias)
        entity_names = {}
        entity_types = {}
        sig_entities = set()
        all_ents = conn.execute(
            "SELECT id, name, entity_type FROM entities WHERE project = ?",
            (project,),
        ).fetchall()
        for e in all_ents:
            entity_names[e[0]] = e[1]
            entity_types[e[0]] = e[2] or "concept"
            mems = entity_to_mems.get(e[0], [])
            # Entidad significativa: nombre > 2 chars, no es stopword, conecta >=2 memorias
            if len(e[1]) > 2 and e[1] not in COMMON_ENTITY_WORDS and len(mems) >= 2:
                sig_entities.add(e[0])

        # Relaciones reales
        relations = conn.execute(
            "SELECT subject_entity_id, predicate, object_entity_id, confidence "
            "FROM relations WHERE project = ? AND invalid_at IS NULL",
            (project,),
        ).fetchall()
        for r in relations:
            sig_entities.add(r[0])
            sig_entities.add(r[2])

        # --- Nodos de memoria (solo las que conectan con entidades significativas) ---
        connected_mems = set()
        for eid in sig_entities:
            for mid in entity_to_mems.get(eid, []):
                connected_mems.add(mid)

        mem_lookup = {m[0]: m for m in memories}
        for mid in connected_mems:
            m = mem_lookup.get(mid)
            if not m:
                continue
            content = m[1]
            kind = m[2]
            importance = m[3]
            snippet = content[:100] + ("..." if len(content) > 100 else "")

            nodes.append({
                "id": f"m_{mid}",
                "name": snippet,
                "group": f"memory_{kind}",
                "type": "memory",
                "kind": kind,
                "importance": importance,
                "val": max(3, int(importance * 8)),
                "color": _MEMORY_COLORS.get(kind, _DEFAULT_COLOR),
                "connections": 0,
            })

        # --- Nodos de entidades significativas ---
        for eid in sig_entities:
            name = entity_names.get(eid, "?")
            etype = entity_types.get(eid, "concept")
            mems = entity_to_mems.get(eid, [])
            nodes.append({
                "id": f"e_{eid}",
                "name": name,
                "group": etype,
                "type": "entity",
                "val": max(4, len(mems) * 2),
                "color": _ENTITY_COLORS.get(etype, _DEFAULT_COLOR),
                "connections": len(mems),
            })

        # --- Links: memoria <-> entidad significativa ---
        for eid in sig_entities:
            for mid in entity_to_mems.get(eid, []):
                key = (f"m_{mid}", f"e_{eid}")
                if key not in link_set:
                    link_set.add(key)
                    links.append({
                        "source": f"m_{mid}",
                        "target": f"e_{eid}",
                        "label": "menciona",
                        "value": 0.3,
                    })

        # --- Links: entidad <-> entidad (relaciones) ---
        for r in relations:
            key = (f"e_{r[0]}", f"e_{r[2]}")
            if key not in link_set:
                link_set.add(key)
                links.append({
                    "source": f"e_{r[0]}",
                    "target": f"e_{r[2]}",
                    "label": r[1],
                    "value": r[3],
                })

        # Actualizar conteo de conexiones en nodos
        conn_count: Dict[str, int] = {}
        for l in links:
            conn_count[l["source"]] = conn_count.get(l["source"], 0) + 1
            conn_count[l["target"]] = conn_count.get(l["target"], 0) + 1
        for n in nodes:
            n["connections"] = conn_count.get(n["id"], 0)

    return {
        "nodes": nodes,
        "links": links,
        "stats": {
            "entities": len(sig_entities),
            "relations": len(relations),
            "memories": len(memories),
            "total_nodes": len(nodes),
            "total_links": len(links),
        },
    }


# ---------------------------------------------------------------------------
# HTML del visualizador (inline, sin archivos externos)
# ---------------------------------------------------------------------------

_GRAPH_HTML = """<!DOCTYPE html>
<html lang="es">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Kerebrom</title>
<script src="https://cdn.jsdelivr.net/npm/d3@7.9.0/dist/d3.min.js"></script>
<link href="https://fonts.googleapis.com/css2?family=Inter:wght@400;500;600;700&display=swap" rel="stylesheet" crossorigin="anonymous">
<style>
:root {
  /* Color tokens */
  --bg-canvas: #0a0a0f;
  --bg-panel: #13131a;
  --bg-elevated: #1a1a24;
  --bg-hover: #1f1f2c;
  --border: rgba(255,255,255,0.06);
  --border-strong: rgba(255,255,255,0.12);
  --text: #e4e4e7;
  --text-muted: #8b8b95;
  --text-dim: #5b5b65;
  --brand: #a855f7;
  --brand-dim: #7e3fb8;

  /* Type semantic colors */
  --type-person: #3b82f6;
  --type-location: #10b981;
  --type-organization: #f59e0b;
  --type-concept: #a855f7;
  --type-mem-core: #fbbf24;
  --type-mem-semantic: #818cf8;
  --type-mem-episodic: #6b7280;
  --type-mem-procedural: #34d399;

  /* Spacing */
  --sp-1: 4px;  --sp-2: 8px;  --sp-3: 12px;
  --sp-4: 16px; --sp-5: 24px; --sp-6: 32px;
  --sp-7: 48px;

  /* Radius */
  --r-sm: 6px; --r-md: 8px; --r-lg: 12px;

  /* Shadows */
  --shadow-sm: 0 1px 3px rgba(0,0,0,0.4);
  --shadow-lg: 0 8px 32px rgba(0,0,0,0.5);

  /* Type — SF Pro cae primero en Mac (ya está nativo), Inter como override web */
  --font: 'Inter', -apple-system, BlinkMacSystemFont, 'SF Pro Text', 'Segoe UI', system-ui, sans-serif;
}

* { margin: 0; padding: 0; box-sizing: border-box; }
html, body { height: 100%; overflow: hidden; }
body {
  background: var(--bg-canvas);
  color: var(--text);
  font-family: var(--font);
  font-size: 13px;
  font-feature-settings: 'cv11', 'ss01';
  -webkit-font-smoothing: antialiased;
}

/* ── HEADER ────────────────────────────────────────────── */
.header {
  position: fixed; top: 0; left: 0; right: 0; height: 56px;
  background: rgba(13,13,20,0.85);
  backdrop-filter: blur(20px) saturate(180%);
  border-bottom: 1px solid var(--border);
  display: flex; align-items: center; padding: 0 var(--sp-5);
  z-index: 100;
}
.header-brand {
  display: flex; align-items: center; gap: var(--sp-3);
  margin-right: var(--sp-6);
}
.brand-mark {
  width: 28px; height: 28px; border-radius: var(--r-md);
  background: linear-gradient(135deg, var(--brand) 0%, var(--brand-dim) 100%);
  display: flex; align-items: center; justify-content: center;
  font-weight: 700; color: white; font-size: 14px;
  box-shadow: 0 0 20px rgba(168,85,247,0.3);
}
.brand-name { font-size: 15px; font-weight: 600; letter-spacing: -0.01em; }
.header-stats {
  display: flex; gap: var(--sp-4); margin-right: auto; margin-left: var(--sp-5);
  font-size: 12px; color: var(--text-muted);
}

/* ── TABS ─────────────────────────── */
.tabs {
  display: flex; gap: 2px; align-items: center;
  background: var(--bg-elevated); padding: 3px;
  border-radius: var(--r-md); margin-right: var(--sp-4);
}
.tab {
  display: flex; align-items: center; gap: 6px;
  padding: 6px 12px; border: none; background: transparent;
  color: var(--text-muted); cursor: pointer; border-radius: var(--r-sm);
  font-family: inherit; font-size: 12px; font-weight: 500;
  transition: background 0.15s, color 0.15s;
}
.tab:hover { color: var(--text); }
.tab.active {
  background: var(--bg-panel); color: var(--text);
  box-shadow: 0 1px 2px rgba(0,0,0,0.3);
}
.stat { display: flex; align-items: center; gap: var(--sp-2); }
.stat-num { font-weight: 600; color: var(--text); font-variant-numeric: tabular-nums; }
.stat-dot { width: 6px; height: 6px; border-radius: 50%; }

/* ── SEARCH ──────────────────────────────────────────── */
.search {
  position: relative; width: 320px;
}
.search input {
  width: 100%; height: 36px;
  background: var(--bg-elevated); border: 1px solid var(--border);
  border-radius: var(--r-md);
  padding: 0 var(--sp-3) 0 36px;
  color: var(--text); font-family: inherit; font-size: 13px;
  outline: none; transition: border-color 0.15s, background 0.15s;
}
.search input:focus { border-color: var(--brand); background: var(--bg-hover); }
.search input::placeholder { color: var(--text-dim); }
.search-icon {
  position: absolute; left: 12px; top: 50%; transform: translateY(-50%);
  width: 14px; height: 14px; color: var(--text-dim); pointer-events: none;
}
.search-kbd {
  position: absolute; right: 10px; top: 50%; transform: translateY(-50%);
  font-size: 10px; padding: 2px 6px; border-radius: 4px;
  background: var(--bg-canvas); border: 1px solid var(--border);
  color: var(--text-muted); font-family: ui-monospace, monospace;
  pointer-events: none;
}

/* ── SIDEBAR (FILTROS) ──────────────────────────── */
.sidebar {
  position: fixed; top: 56px; left: 0; bottom: 0; width: 240px;
  background: var(--bg-panel); border-right: 1px solid var(--border);
  padding: var(--sp-5) var(--sp-4); overflow-y: auto;
  z-index: 50;
}
.sidebar-section { margin-bottom: var(--sp-6); }
.sidebar-title {
  font-size: 11px; font-weight: 600; text-transform: uppercase;
  letter-spacing: 0.06em; color: var(--text-dim);
  margin-bottom: var(--sp-3);
}
.filter-list { display: flex; flex-direction: column; gap: 2px; }
.filter-item {
  display: flex; align-items: center; gap: var(--sp-3);
  padding: var(--sp-2) var(--sp-3);
  border-radius: var(--r-sm);
  cursor: pointer; user-select: none;
  font-size: 13px; color: var(--text-muted);
  transition: background 0.12s, color 0.12s;
}
.filter-item:hover { background: var(--bg-hover); color: var(--text); }
.filter-item.active { background: var(--bg-elevated); color: var(--text); }
.filter-item .dot {
  width: 10px; height: 10px; border-radius: 50%; flex-shrink: 0;
}
.filter-item .name { flex: 1; }
.filter-item .count {
  font-size: 11px; font-variant-numeric: tabular-nums;
  color: var(--text-dim); padding: 0 6px;
  background: var(--bg-canvas); border-radius: 4px; min-width: 24px; text-align: center;
}
.filter-item.active .count { color: var(--text-muted); background: var(--bg-canvas); }
.filter-item.disabled .dot { opacity: 0.25; }
.filter-item.disabled .name, .filter-item.disabled .count { opacity: 0.4; }

/* ── CANVAS ──────────────────────────────────── */
#canvas-wrap {
  position: fixed; top: 56px; left: 240px; right: 0; bottom: 0;
  background: var(--bg-canvas);
}
#graph-svg { width: 100%; height: 100%; cursor: grab; }
#graph-svg:active { cursor: grabbing; }
.node circle {
  stroke: rgba(255,255,255,0.08); stroke-width: 1px;
  cursor: pointer; transition: stroke 0.15s, stroke-width 0.15s;
}
.node:hover circle { stroke: rgba(255,255,255,0.5); stroke-width: 1.5px; }
.node.selected circle {
  stroke: white; stroke-width: 2px;
  filter: drop-shadow(0 0 8px currentColor);
}
.node.dim { opacity: 0.12; pointer-events: none; }
.node text {
  font-family: var(--font); font-size: 10px; font-weight: 500;
  fill: var(--text-muted); pointer-events: none;
  paint-order: stroke; stroke: var(--bg-canvas); stroke-width: 3px; stroke-linejoin: round;
  text-anchor: middle;
}
.node:hover text, .node.selected text { fill: var(--text); }
.link { stroke: rgba(255,255,255,0.06); transition: stroke 0.15s, stroke-opacity 0.15s; }
.link.highlight { stroke: var(--brand); stroke-opacity: 0.8; }
.link.dim { stroke-opacity: 0.02; }

/* ── ZOOM CONTROLS ──────────────────────────── */
.zoom-controls {
  position: fixed; bottom: var(--sp-5); left: 50%; transform: translateX(-50%);
  display: flex; align-items: center; gap: 1px;
  background: var(--bg-panel); border: 1px solid var(--border);
  border-radius: var(--r-md); padding: 4px;
  box-shadow: var(--shadow-lg); z-index: 60;
}
.zoom-btn {
  width: 28px; height: 28px; display: flex; align-items: center; justify-content: center;
  background: none; border: none; color: var(--text-muted);
  border-radius: var(--r-sm); cursor: pointer; font-size: 16px;
  transition: background 0.12s, color 0.12s;
}
.zoom-btn:hover { background: var(--bg-hover); color: var(--text); }
.zoom-level {
  padding: 0 var(--sp-3); font-size: 11px; color: var(--text-muted);
  font-variant-numeric: tabular-nums; min-width: 48px; text-align: center;
}
.zoom-sep {
  width: 1px; height: 20px; background: var(--border);
  margin: 0 4px;
}

/* ── MINIMAP ──────────────────────────── */
.minimap {
  position: fixed; bottom: var(--sp-5); right: var(--sp-5);
  width: 180px; height: 120px;
  background: var(--bg-panel); border: 1px solid var(--border);
  border-radius: var(--r-md);
  box-shadow: var(--shadow-lg); z-index: 60;
  overflow: hidden;
}
.minimap svg { display: block; }
.minimap circle { fill: var(--text-muted); opacity: 0.7; }
.minimap-viewport {
  position: absolute; border: 1.5px solid var(--brand);
  background: rgba(168,85,247,0.08);
  pointer-events: none; border-radius: 2px;
}

/* ── HELP PANEL ──────────────────────── */
.help-panel {
  position: fixed; bottom: calc(var(--sp-5) + 48px); left: 50%;
  transform: translateX(-50%) translateY(10px);
  background: var(--bg-panel); border: 1px solid var(--border-strong);
  border-radius: var(--r-lg);
  box-shadow: var(--shadow-lg);
  z-index: 90; width: 300px;
  opacity: 0; pointer-events: none;
  transition: opacity 0.2s, transform 0.2s;
}
.help-panel.open {
  opacity: 1; pointer-events: auto;
  transform: translateX(-50%) translateY(0);
}
.help-header {
  display: flex; align-items: center; justify-content: space-between;
  padding: var(--sp-4); border-bottom: 1px solid var(--border);
}
.help-title { font-size: 13px; font-weight: 600; }
.help-close {
  width: 22px; height: 22px; background: none; border: none;
  color: var(--text-muted); cursor: pointer; font-size: 16px;
  border-radius: var(--r-sm); display: flex; align-items: center; justify-content: center;
}
.help-close:hover { background: var(--bg-hover); color: var(--text); }
.help-body { padding: var(--sp-3) var(--sp-4); }
.help-row {
  display: flex; justify-content: space-between; align-items: center;
  padding: var(--sp-2) 0; font-size: 12px;
}
.help-row kbd {
  background: var(--bg-canvas); border: 1px solid var(--border);
  border-radius: 4px; padding: 2px 8px; font-size: 11px;
  color: var(--text); font-family: ui-monospace, monospace;
  min-width: 24px; text-align: center;
}
.help-row span { color: var(--text-muted); }

/* ── DETAIL PANEL ──────────────────────────── */
.detail-panel {
  position: fixed; top: 56px; right: 0; bottom: 0; width: 380px;
  background: var(--bg-panel); border-left: 1px solid var(--border);
  transform: translateX(100%); transition: transform 0.25s cubic-bezier(0.4,0,0.2,1);
  z-index: 80; display: flex; flex-direction: column;
}
.detail-panel.open { transform: translateX(0); }
.detail-header {
  padding: var(--sp-5); border-bottom: 1px solid var(--border);
}
.detail-type-badge {
  display: inline-flex; align-items: center; gap: var(--sp-2);
  padding: 4px 10px; font-size: 11px; font-weight: 500;
  border-radius: 100px; text-transform: lowercase;
  margin-bottom: var(--sp-3);
}
.detail-type-badge .dot { width: 6px; height: 6px; border-radius: 50%; }
.detail-title {
  font-size: 18px; font-weight: 600; line-height: 1.3;
  letter-spacing: -0.01em; word-break: break-word;
}
.detail-meta {
  display: flex; gap: var(--sp-4); margin-top: var(--sp-3);
  font-size: 11px; color: var(--text-muted);
}
.detail-meta-item { display: flex; align-items: center; gap: 4px; }
.detail-close {
  position: absolute; top: var(--sp-4); right: var(--sp-4);
  width: 28px; height: 28px; border: none; background: none;
  color: var(--text-muted); cursor: pointer; border-radius: var(--r-sm);
  display: flex; align-items: center; justify-content: center;
  font-size: 18px; line-height: 1;
}
.detail-close:hover { background: var(--bg-hover); color: var(--text); }
.detail-body {
  flex: 1; overflow-y: auto; padding: var(--sp-5);
}
.detail-content {
  font-size: 13px; line-height: 1.6; color: var(--text);
  white-space: pre-wrap; word-wrap: break-word;
}
.detail-section { margin-top: var(--sp-5); }
.detail-section-title {
  font-size: 11px; font-weight: 600; text-transform: uppercase;
  letter-spacing: 0.06em; color: var(--text-dim);
  margin-bottom: var(--sp-3);
}
.relation-list { display: flex; flex-direction: column; gap: var(--sp-2); }
.relation {
  display: flex; align-items: center; gap: var(--sp-3);
  padding: var(--sp-3); border-radius: var(--r-sm);
  background: var(--bg-elevated); cursor: pointer;
  transition: background 0.12s; font-size: 12px;
}
.relation:hover { background: var(--bg-hover); }
.relation .arrow { color: var(--text-dim); font-size: 10px; }
.relation .label { color: var(--brand); font-weight: 500; flex-shrink: 0; }
.relation .target { color: var(--text); flex: 1;
  white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }

/* ── TOOLTIP ──────────────────────────────── */
.tooltip {
  position: fixed; pointer-events: none; z-index: 200;
  background: var(--bg-elevated); border: 1px solid var(--border-strong);
  border-radius: var(--r-md); padding: var(--sp-3) var(--sp-3);
  box-shadow: var(--shadow-lg);
  font-size: 12px; color: var(--text);
  max-width: 280px; opacity: 0;
  transition: opacity 0.12s; backdrop-filter: blur(12px);
}
.tooltip.show { opacity: 1; }
.tooltip-title { font-weight: 600; margin-bottom: 4px; }
.tooltip-meta { font-size: 11px; color: var(--text-muted); }
.tooltip-meta span + span::before { content: " · "; color: var(--text-dim); }

/* ── EMPTY STATE ────────────────────────── */
.empty {
  position: fixed; top: 50%; left: calc(50% + 120px); transform: translate(-50%, -50%);
  text-align: center; color: var(--text-dim); pointer-events: none;
}
.empty-icon { font-size: 32px; margin-bottom: var(--sp-3); opacity: 0.5; }
.empty-text { font-size: 13px; }
.empty.hidden { display: none; }

/* ── STATS VIEW ───────────────────────── */
.stats-view {
  position: fixed; top: 56px; left: 240px; right: 0; bottom: 0;
  background: var(--bg-canvas); overflow-y: auto;
  display: none;
}
.stats-view.active { display: block; }
#canvas-wrap.hidden { display: none; }

.stats-container {
  max-width: 1100px; margin: 0 auto;
  padding: var(--sp-7) var(--sp-5);
}
.stats-header { margin-bottom: var(--sp-6); }
.stats-title-row {
  display: flex; align-items: center; gap: var(--sp-4);
  margin-bottom: var(--sp-3); flex-wrap: wrap;
}
.stats-title {
  font-size: 28px; font-weight: 700; letter-spacing: -0.02em;
}
.live-badge {
  display: inline-flex; align-items: center; gap: 8px;
  padding: 5px 12px 5px 10px;
  background: rgba(16, 185, 129, 0.08);
  border: 1px solid rgba(16, 185, 129, 0.22);
  border-radius: 100px;
  font-size: 11px; font-weight: 600; color: #10b981;
  letter-spacing: 0.04em;
}
.live-dot {
  width: 7px; height: 7px; border-radius: 50%;
  background: #10b981;
  box-shadow: 0 0 0 0 rgba(16, 185, 129, 0.6);
  animation: live-pulse 2s infinite;
}
@keyframes live-pulse {
  0% { box-shadow: 0 0 0 0 rgba(16, 185, 129, 0.6); }
  70% { box-shadow: 0 0 0 6px rgba(16, 185, 129, 0); }
  100% { box-shadow: 0 0 0 0 rgba(16, 185, 129, 0); }
}
.live-updated {
  color: var(--text-dim); font-weight: 500;
  font-variant-numeric: tabular-nums; margin-left: 4px;
}
.stats-subtitle {
  font-size: 14px; color: var(--text-muted); line-height: 1.6;
  max-width: 640px;
}

/* Layout hero + rolling cards */
.stat-hero {
  background: linear-gradient(135deg, rgba(168,85,247,0.1), rgba(168,85,247,0.02));
  border: 1px solid rgba(168,85,247,0.3);
  border-radius: var(--r-lg); padding: var(--sp-6);
  margin-bottom: var(--sp-4);
  display: grid; grid-template-columns: 1fr auto;
  align-items: center; gap: var(--sp-5);
}
.stat-hero-label {
  font-size: 11px; font-weight: 600; text-transform: uppercase;
  letter-spacing: 0.06em; color: var(--brand); margin-bottom: var(--sp-3);
}
.stat-hero-value {
  font-size: 56px; font-weight: 700; line-height: 1;
  letter-spacing: -0.03em; color: var(--text);
  font-variant-numeric: tabular-nums;
}
.stat-hero-unit {
  font-size: 18px; color: var(--text-muted); font-weight: 400; margin-left: var(--sp-2);
}
.stat-hero-meta {
  text-align: right; display: flex; flex-direction: column; gap: var(--sp-2);
  color: var(--text-muted); font-size: 12px;
}
.stat-hero-meta strong { color: var(--text); font-size: 14px; font-weight: 600; }

.stat-cards {
  display: grid; grid-template-columns: repeat(4, 1fr);
  gap: var(--sp-3); margin-bottom: var(--sp-6);
}
@media (max-width: 980px) {
  .stat-cards { grid-template-columns: repeat(2, 1fr); }
  .stat-hero { grid-template-columns: 1fr; text-align: left; }
  .stat-hero-meta { text-align: left; flex-direction: row; gap: var(--sp-4); }
}
.stat-card {
  background: var(--bg-panel); border: 1px solid var(--border);
  border-radius: var(--r-lg); padding: var(--sp-4) var(--sp-5);
  transition: border-color 0.2s;
}
.stat-card:hover { border-color: var(--border-strong); }
.stat-card.empty .stat-card-value { color: var(--text-dim); }
.stat-card-label {
  font-size: 11px; font-weight: 600; text-transform: uppercase;
  letter-spacing: 0.06em; color: var(--text-dim); margin-bottom: var(--sp-3);
}
.stat-card-value {
  font-size: 26px; font-weight: 700; line-height: 1;
  letter-spacing: -0.02em; color: var(--text);
  font-variant-numeric: tabular-nums;
}
.stat-card-unit {
  font-size: 12px; color: var(--text-muted); font-weight: 400;
}
.stat-card-delta {
  display: inline-flex; align-items: center; gap: 4px;
  font-size: 11px; color: var(--text-muted); margin-top: var(--sp-2);
}

/* Empty state */
.stats-empty {
  background: var(--bg-panel); border: 1px dashed var(--border-strong);
  border-radius: var(--r-lg); padding: var(--sp-7) var(--sp-5);
  text-align: center; margin-bottom: var(--sp-6);
}
.stats-empty-icon {
  font-size: 32px; opacity: 0.4; margin-bottom: var(--sp-4);
}
.stats-empty-title {
  font-size: 16px; font-weight: 600; margin-bottom: var(--sp-3);
  color: var(--text);
}
.stats-empty-text {
  font-size: 13px; color: var(--text-muted); line-height: 1.6;
  max-width: 460px; margin: 0 auto;
}
.stats-empty-text code {
  background: var(--bg-elevated); padding: 2px 8px; border-radius: 4px;
  font-family: ui-monospace, monospace; font-size: 12px; color: var(--brand);
}

.stats-row {
  display: grid; grid-template-columns: 1fr 1fr;
  gap: var(--sp-4); margin-bottom: var(--sp-5);
}
.stats-row:first-of-type { grid-template-columns: 1fr; }
.stats-panel {
  background: var(--bg-panel); border: 1px solid var(--border);
  border-radius: var(--r-lg); padding: var(--sp-5);
}
.stats-panel-title {
  font-size: 11px; font-weight: 600; text-transform: uppercase;
  letter-spacing: 0.06em; color: var(--text-dim);
  margin-bottom: var(--sp-4);
}

/* Timeline chart (SVG) */
.timeline-chart { height: 180px; position: relative; }
.timeline-chart svg { width: 100%; height: 100%; }
.timeline-bar { fill: var(--brand); transition: fill 0.2s; }
.timeline-bar:hover { fill: #c084fc; }
.timeline-axis { stroke: var(--border); stroke-width: 1; }
.timeline-label {
  font-size: 10px; fill: var(--text-dim); font-family: var(--font);
}

/* Ops table */
.ops-table {
  display: flex; flex-direction: column; gap: var(--sp-2);
}
.ops-row {
  display: grid; grid-template-columns: 80px 1fr auto;
  gap: var(--sp-3); align-items: center;
  padding: var(--sp-2) 0;
  border-bottom: 1px solid var(--border);
}
.ops-row:last-child { border-bottom: none; }
.ops-name { font-size: 12px; font-weight: 500; color: var(--text); }
.ops-bar-wrap {
  height: 6px; background: var(--bg-elevated); border-radius: 3px;
  overflow: hidden;
}
.ops-bar { height: 100%; background: var(--brand); transition: width 0.4s; }
.ops-value {
  font-size: 11px; color: var(--text-muted); font-variant-numeric: tabular-nums;
  min-width: 60px; text-align: right;
}

/* Formula box */
.formula-box { display: flex; flex-direction: column; gap: var(--sp-2); }
.formula-row {
  display: grid; grid-template-columns: 80px 60px 1fr;
  gap: var(--sp-3); font-size: 12px; align-items: center;
  padding: var(--sp-2) 0;
}
.formula-row .op-name {
  color: var(--text); font-weight: 500; font-family: ui-monospace, monospace;
}
.formula-row .op-mult {
  color: var(--brand); font-weight: 600; font-variant-numeric: tabular-nums;
}
.formula-row .op-desc { color: var(--text-muted); }
.formula-disclaimer {
  margin-top: var(--sp-3); padding-top: var(--sp-3);
  border-top: 1px solid var(--border);
  font-size: 11px; color: var(--text-dim); line-height: 1.5;
}

/* ── LOADING ──────────────────────────── */
.loading {
  position: fixed; inset: 0; display: flex; align-items: center; justify-content: center;
  background: var(--bg-canvas); z-index: 300;
  transition: opacity 0.3s; flex-direction: column; gap: var(--sp-4);
}
.loading.hidden { opacity: 0; pointer-events: none; }
.loading-spinner {
  width: 32px; height: 32px; border-radius: 50%;
  border: 2px solid var(--bg-elevated); border-top-color: var(--brand);
  animation: spin 0.8s linear infinite;
}
.loading-text { font-size: 12px; color: var(--text-muted); }
@keyframes spin { to { transform: rotate(360deg); } }

/* ── SCROLLBAR ──────────────────────────── */
::-webkit-scrollbar { width: 8px; height: 8px; }
::-webkit-scrollbar-track { background: transparent; }
::-webkit-scrollbar-thumb { background: var(--border); border-radius: 4px; }
::-webkit-scrollbar-thumb:hover { background: var(--border-strong); }
</style>
</head>
<body>

<!-- LOADING -->
<div class="loading" id="loading">
  <div class="loading-spinner"></div>
  <div class="loading-text">Cargando grafo de conocimiento...</div>
</div>

<!-- HEADER -->
<header class="header">
  <div class="header-brand">
    <div class="brand-mark">K</div>
    <div class="brand-name">Kerebrom</div>
  </div>
  <nav class="tabs">
    <button class="tab active" data-view="graph">
      <svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2">
        <circle cx="12" cy="12" r="3"/><circle cx="5" cy="5" r="2"/><circle cx="19" cy="5" r="2"/>
        <circle cx="5" cy="19" r="2"/><circle cx="19" cy="19" r="2"/>
        <line x1="12" y1="12" x2="5" y2="5"/><line x1="12" y1="12" x2="19" y2="5"/>
        <line x1="12" y1="12" x2="5" y2="19"/><line x1="12" y1="12" x2="19" y2="19"/>
      </svg>
      <span>Grafo</span>
    </button>
    <button class="tab" data-view="stats">
      <svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2">
        <line x1="18" y1="20" x2="18" y2="10"/><line x1="12" y1="20" x2="12" y2="4"/>
        <line x1="6" y1="20" x2="6" y2="14"/>
      </svg>
      <span>Estadísticas</span>
    </button>
  </nav>
  <div class="header-stats" id="header-stats"></div>
  <div class="search">
    <svg class="search-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
      <circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/>
    </svg>
    <input id="search-input" type="text" placeholder="Buscar memoria o entidad…" />
    <kbd class="search-kbd">⌘K</kbd>
  </div>
</header>

<!-- SIDEBAR -->
<aside class="sidebar">
  <div class="sidebar-section">
    <div class="sidebar-title">Entidades</div>
    <div class="filter-list" id="filter-entities"></div>
  </div>
  <div class="sidebar-section">
    <div class="sidebar-title">Memorias</div>
    <div class="filter-list" id="filter-memories"></div>
  </div>
</aside>

<!-- CANVAS -->
<div id="canvas-wrap">
  <svg id="graph-svg"></svg>
  <div class="empty hidden" id="empty">
    <div class="empty-icon">○</div>
    <div class="empty-text">No hay nodos visibles<br>Activa al menos un filtro</div>
  </div>
</div>

<!-- STATS VIEW -->
<div id="stats-view" class="stats-view">
  <div class="stats-container">
    <div class="stats-header">
      <div class="stats-title-row">
        <h1 class="stats-title">Tokens de contexto servidos</h1>
        <div class="live-badge">
          <span class="live-dot"></span>
          <span>EN VIVO</span>
          <span class="live-updated" id="live-updated"></span>
        </div>
      </div>
      <p class="stats-subtitle">
        Cantidad real de información que Kerebrom ha inyectado en tus
        conversaciones de AI. Sin multiplicadores inventados — solo el
        contenido medido de cada <code>recall</code>, <code>context</code>
        y <code>query</code>. Es el mínimo de tokens que no tuviste que
        reintroducir manualmente en tus prompts.
      </p>
    </div>

    <div id="stat-cards-container"></div>

    <div class="stats-row">
      <div class="stats-panel">
        <div class="stats-panel-title">Últimos 30 días</div>
        <div class="timeline-chart" id="timeline-chart"></div>
      </div>
    </div>

    <div class="stats-row">
      <div class="stats-panel">
        <div class="stats-panel-title">Por operación</div>
        <div class="ops-table" id="ops-table"></div>
      </div>
      <div class="stats-panel">
        <div class="stats-panel-title">Cómo se mide</div>
        <div class="formula-box">
          <div class="formula-row">
            <span class="op-name">recall</span>
            <span class="op-mult">=</span>
            <span class="op-desc">tokens del contenido devuelto al modelo</span>
          </div>
          <div class="formula-row">
            <span class="op-name">context</span>
            <span class="op-mult">=</span>
            <span class="op-desc">tokens del paquete de contexto servido</span>
          </div>
          <div class="formula-row">
            <span class="op-name">query</span>
            <span class="op-mult">=</span>
            <span class="op-desc">tokens de los resultados filtrados</span>
          </div>
          <div class="formula-row">
            <span class="op-name">remember</span>
            <span class="op-mult">—</span>
            <span class="op-desc">no sirve contexto, solo persiste</span>
          </div>
          <div class="formula-disclaimer">
            Medición directa sin multiplicadores. Es el mínimo real que
            Kerebrom inyectó en tus conversaciones. Sin Kerebrom habrías
            tenido que introducir estos tokens manualmente o dejar que
            el modelo reconstruya el contexto (gastando aún más).
          </div>
        </div>
      </div>
    </div>
  </div>
</div>

<!-- ZOOM CONTROLS -->
<div class="zoom-controls">
  <button class="zoom-btn" id="zoom-out" title="Zoom out (−)">−</button>
  <div class="zoom-level" id="zoom-level">100%</div>
  <button class="zoom-btn" id="zoom-in" title="Zoom in (+)">+</button>
  <button class="zoom-btn" id="zoom-fit" title="Ajustar vista (F)">⊡</button>
  <div class="zoom-sep"></div>
  <button class="zoom-btn" id="help-toggle" title="Atajos de teclado (?)">?</button>
</div>

<!-- MINIMAP -->
<div class="minimap" id="minimap">
  <svg id="minimap-svg" width="180" height="120"></svg>
  <div class="minimap-viewport" id="minimap-viewport"></div>
</div>

<!-- HELP PANEL -->
<div class="help-panel" id="help-panel">
  <div class="help-header">
    <span class="help-title">Atajos de teclado</span>
    <button class="help-close" id="help-close">×</button>
  </div>
  <div class="help-body">
    <div class="help-row"><kbd>⌘K</kbd><span>Buscar</span></div>
    <div class="help-row"><kbd>Esc</kbd><span>Deseleccionar / cerrar</span></div>
    <div class="help-row"><kbd>F</kbd><span>Ajustar a la vista</span></div>
    <div class="help-row"><kbd>+</kbd><span>Zoom in</span></div>
    <div class="help-row"><kbd>−</kbd><span>Zoom out</span></div>
    <div class="help-row"><kbd>Click</kbd><span>Seleccionar nodo</span></div>
    <div class="help-row"><kbd>Drag</kbd><span>Mover nodo o lienzo</span></div>
    <div class="help-row"><kbd>Scroll</kbd><span>Zoom</span></div>
  </div>
</div>

<!-- DETAIL PANEL -->
<aside class="detail-panel" id="detail-panel">
  <button class="detail-close" id="detail-close">×</button>
  <div class="detail-header" id="detail-header"></div>
  <div class="detail-body" id="detail-body"></div>
</aside>

<!-- TOOLTIP -->
<div class="tooltip" id="tooltip"></div>

<script>
// ═══════════════════════════════════════════════════════
//  CONFIG
// ═══════════════════════════════════════════════════════
const TYPE_COLORS = {
  'person': '#3b82f6',
  'location': '#10b981',
  'organization': '#f59e0b',
  'concept': '#a855f7',
  'memory_core': '#fbbf24',
  'memory_semantic': '#818cf8',
  'memory_episodic': '#6b7280',
  'memory_procedural': '#34d399',
};
const TYPE_LABELS = {
  'person': 'Personas',
  'location': 'Lugares',
  'organization': 'Organizaciones',
  'concept': 'Conceptos',
  'memory_core': 'Identidad',
  'memory_semantic': 'Semánticas',
  'memory_episodic': 'Episódicas',
  'memory_procedural': 'Procedurales',
};

let allData = null;
let activeFilters = new Set();
let selectedNode = null;
let zoomLevel = 1;
let currentTransform = d3.zoomIdentity;

const W = () => window.innerWidth - 240;
const H = () => window.innerHeight - 56;

// ═══════════════════════════════════════════════════════
//  D3 SETUP
// ═══════════════════════════════════════════════════════
const svg = d3.select('#graph-svg');
const g = svg.append('g');
const gLinks = g.append('g').attr('class', 'links');
const gNodes = g.append('g').attr('class', 'nodes');

const zoom = d3.zoom()
  .scaleExtent([0.1, 8])
  .on('zoom', (event) => {
    currentTransform = event.transform;
    g.attr('transform', event.transform);
    zoomLevel = event.transform.k;
    document.getElementById('zoom-level').textContent = Math.round(zoomLevel * 100) + '%';
    // Show labels only when zoomed in enough
    gNodes.selectAll('text')
      .style('opacity', zoomLevel > 0.7 ? 1 : 0);
    // Update minimap viewport indicator
    updateMinimapViewport();
  });
svg.call(zoom);

let simulation = null;

// ═══════════════════════════════════════════════════════
//  LOAD DATA
// ═══════════════════════════════════════════════════════
async function load() {
  try {
    const res = await fetch('/api/graph');
    allData = await res.json();
    renderHeader();
    renderFilters();
    renderGraph();
    document.getElementById('loading').classList.add('hidden');
    // fitView se llamará automáticamente cuando la simulación converja
  } catch (e) {
    document.getElementById('loading').innerHTML = '<div style="color:#ef4444">Error: ' + e.message + '</div>';
  }
}

function renderHeader() {
  const s = allData.stats;
  document.getElementById('header-stats').innerHTML =
    '<div class="stat"><span class="stat-num">' + s.memories + '</span> memorias</div>' +
    '<div class="stat"><span class="stat-num">' + s.entities + '</span> entidades</div>' +
    '<div class="stat"><span class="stat-num">' + s.total_links + '</span> conexiones</div>';
}

function renderFilters() {
  // Count by type
  const counts = {};
  allData.nodes.forEach(n => {
    const k = n.type === 'memory' ? 'memory_' + (n.kind || 'episodic') : (n.group || 'concept');
    counts[k] = (counts[k] || 0) + 1;
  });

  const entityTypes = ['person', 'location', 'organization', 'concept'];
  const memoryTypes = ['memory_core', 'memory_semantic', 'memory_episodic', 'memory_procedural'];

  const buildItem = (k) => {
    const color = TYPE_COLORS[k];
    const label = TYPE_LABELS[k];
    const count = counts[k] || 0;
    const disabled = count === 0;
    if (!disabled) activeFilters.add(k);
    return '<div class="filter-item ' + (disabled ? 'disabled' : 'active') + '" data-type="' + k + '">' +
           '<div class="dot" style="background:' + color + '"></div>' +
           '<div class="name">' + label + '</div>' +
           '<div class="count">' + count + '</div>' +
           '</div>';
  };

  document.getElementById('filter-entities').innerHTML =
    entityTypes.map(buildItem).join('');
  document.getElementById('filter-memories').innerHTML =
    memoryTypes.map(buildItem).join('');

  document.querySelectorAll('.filter-item').forEach(el => {
    el.addEventListener('click', () => {
      if (el.classList.contains('disabled')) return;
      const t = el.dataset.type;
      if (activeFilters.has(t)) {
        activeFilters.delete(t);
        el.classList.remove('active');
      } else {
        activeFilters.add(t);
        el.classList.add('active');
      }
      applyFilters();
    });
  });
}

// ═══════════════════════════════════════════════════════
//  RENDER GRAPH
// ═══════════════════════════════════════════════════════
function renderGraph() {
  // Color nodes
  allData.nodes.forEach(n => {
    const k = n.type === 'memory' ? 'memory_' + (n.kind || 'episodic') : (n.group || 'concept');
    n._color = TYPE_COLORS[k];
    n._typeKey = k;
    n._radius = Math.max(4, Math.sqrt(n.val || 4) * 2.5);
  });

  // Label bounding box estimation para evitar solape
  allData.nodes.forEach(n => {
    const label = n.name || '';
    n._labelW = Math.min(label.length * 5.5, 180); // estimación ~5.5px por char
    n._labelH = 14;
  });

  simulation = d3.forceSimulation(allData.nodes)
    .force('link', d3.forceLink(allData.links).id(d => d.id).distance(95).strength(0.25))
    .force('charge', d3.forceManyBody().strength(-240).distanceMax(500))
    .force('center', d3.forceCenter(W() / 2, H() / 2))
    // Collision más fuerte que incluye label height
    .force('collision', d3.forceCollide()
      .radius(d => d._radius + Math.max(d._labelW / 3, 18))
      .strength(0.85))
    // Force custom para evitar solape de etiquetas verticalmente
    .force('labels', labelAvoidanceForce())
    .alphaDecay(0.02);

  const link = gLinks.selectAll('line')
    .data(allData.links).join('line')
    .attr('class', 'link')
    .attr('stroke-width', d => Math.max(0.4, (d.value || 0.3) * 1.2));

  const node = gNodes.selectAll('g.node')
    .data(allData.nodes).join('g')
    .attr('class', 'node')
    .call(d3.drag()
      .on('start', (e, d) => { if (!e.active) simulation.alphaTarget(0.3).restart(); d.fx = d.x; d.fy = d.y; })
      .on('drag', (e, d) => { d.fx = e.x; d.fy = e.y; })
      .on('end', (e, d) => { if (!e.active) simulation.alphaTarget(0); d.fx = null; d.fy = null; })
    );

  node.append('circle')
    .attr('r', d => d._radius)
    .attr('fill', d => d._color);

  node.append('text')
    .attr('dy', d => d._radius + 11)
    .text(d => {
      const name = d.name || '';
      return name.length > 30 ? name.slice(0, 28) + '…' : name;
    })
    .style('opacity', zoomLevel > 0.7 ? 1 : 0);

  node.on('mouseenter', (e, d) => {
    showTooltip(e, d);
    highlightNeighbors(d);
  });
  node.on('mousemove', (e) => moveTooltip(e));
  node.on('mouseleave', () => {
    hideTooltip();
    if (!selectedNode) clearHighlight();
  });
  node.on('click', (e, d) => {
    e.stopPropagation();
    selectNode(d);
  });

  svg.on('click', () => deselectNode());

  let minimapTickCount = 0;
  let autoFitDone = false;
  simulation.on('tick', () => {
    link
      .attr('x1', d => d.source.x).attr('y1', d => d.source.y)
      .attr('x2', d => d.target.x).attr('y2', d => d.target.y);
    node.attr('transform', d => `translate(${d.x},${d.y})`);
    // Refresh minimap periodically during simulation
    if (++minimapTickCount % 6 === 0) renderMinimap();
    // Auto-fit cuando la simulación empieza a estabilizarse
    if (!autoFitDone && simulation.alpha() < 0.1) {
      autoFitDone = true;
      fitView();
    }
  });
  simulation.on('end', () => {
    renderMinimap();
    if (!autoFitDone) {
      autoFitDone = true;
      fitView();
    }
  });
}

// ═══════════════════════════════════════════════════════
//  INTERACTIONS
// ═══════════════════════════════════════════════════════
function highlightNeighbors(node) {
  const neighborIds = new Set([node.id]);
  allData.links.forEach(l => {
    const sid = typeof l.source === 'object' ? l.source.id : l.source;
    const tid = typeof l.target === 'object' ? l.target.id : l.target;
    if (sid === node.id) neighborIds.add(tid);
    if (tid === node.id) neighborIds.add(sid);
  });
  gNodes.selectAll('g.node').classed('dim', d => !neighborIds.has(d.id));
  gLinks.selectAll('line')
    .classed('highlight', d => {
      const sid = typeof d.source === 'object' ? d.source.id : d.source;
      const tid = typeof d.target === 'object' ? d.target.id : d.target;
      return sid === node.id || tid === node.id;
    })
    .classed('dim', d => {
      const sid = typeof d.source === 'object' ? d.source.id : d.source;
      const tid = typeof d.target === 'object' ? d.target.id : d.target;
      return !(sid === node.id || tid === node.id);
    });
}

function clearHighlight() {
  gNodes.selectAll('g.node').classed('dim', false);
  gLinks.selectAll('line').classed('highlight', false).classed('dim', false);
}

function selectNode(node) {
  selectedNode = node;
  gNodes.selectAll('g.node').classed('selected', d => d.id === node.id);
  highlightNeighbors(node);
  showDetail(node);

  // Pan to node smoothly
  const scale = Math.max(currentTransform.k, 1.2);
  const x = W() / 2 - node.x * scale;
  const y = H() / 2 - node.y * scale;
  svg.transition().duration(400)
    .call(zoom.transform, d3.zoomIdentity.translate(x, y).scale(scale));
}

function deselectNode() {
  selectedNode = null;
  gNodes.selectAll('g.node').classed('selected', false);
  clearHighlight();
  hideDetail();
}

// ═══════════════════════════════════════════════════════
//  DETAIL PANEL
// ═══════════════════════════════════════════════════════
function showDetail(node) {
  const panel = document.getElementById('detail-panel');
  const header = document.getElementById('detail-header');
  const body = document.getElementById('detail-body');

  const typeLabel = TYPE_LABELS[node._typeKey] || node._typeKey;
  const neighbors = allData.links.filter(l => {
    const sid = typeof l.source === 'object' ? l.source.id : l.source;
    const tid = typeof l.target === 'object' ? l.target.id : l.target;
    return sid === node.id || tid === node.id;
  });

  header.innerHTML =
    '<div class="detail-type-badge" style="background:' + node._color + '22;color:' + node._color + '">' +
      '<div class="dot" style="background:' + node._color + '"></div>' + typeLabel +
    '</div>' +
    '<h2 class="detail-title">' + escapeHtml(node.name || 'Sin nombre') + '</h2>' +
    '<div class="detail-meta">' +
      '<div class="detail-meta-item">' + neighbors.length + ' conexiones</div>' +
      (node.importance ? '<div class="detail-meta-item">Importancia ' + node.importance.toFixed(2) + '</div>' : '') +
    '</div>';

  // Body content
  let bodyHtml = '';
  if (node.type === 'memory') {
    fetch('/api/memory/' + node.id.replace('m_', ''))
      .then(r => r.json())
      .then(mem => {
        bodyHtml = '<div class="detail-content">' + escapeHtml(mem.content || node.name) + '</div>';
        bodyHtml += renderRelations(neighbors, node);
        body.innerHTML = bodyHtml;
      });
  } else {
    bodyHtml = renderRelations(neighbors, node);
    body.innerHTML = bodyHtml || '<div class="detail-content" style="color:var(--text-muted)">Sin información adicional</div>';
  }

  panel.classList.add('open');
}

function renderRelations(neighbors, node) {
  if (!neighbors.length) return '';
  let html = '<div class="detail-section"><div class="detail-section-title">Conexiones</div><div class="relation-list">';
  neighbors.forEach(l => {
    const sid = typeof l.source === 'object' ? l.source.id : l.source;
    const other = (sid === node.id) ?
      (typeof l.target === 'object' ? l.target : allData.nodes.find(n => n.id === l.target)) :
      (typeof l.source === 'object' ? l.source : allData.nodes.find(n => n.id === l.source));
    if (!other) return;
    const arrow = sid === node.id ? '→' : '←';
    html += '<div class="relation" data-id="' + other.id + '">' +
      '<span class="arrow">' + arrow + '</span>' +
      '<span class="label">' + escapeHtml(l.label || '') + '</span>' +
      '<span class="target">' + escapeHtml(other.name || '') + '</span>' +
    '</div>';
  });
  html += '</div></div>';
  return html;
}

function hideDetail() {
  document.getElementById('detail-panel').classList.remove('open');
}

document.getElementById('detail-close').addEventListener('click', deselectNode);

// Click on relation → navigate
document.getElementById('detail-body').addEventListener('click', (e) => {
  const rel = e.target.closest('.relation');
  if (rel) {
    const id = rel.dataset.id;
    const target = allData.nodes.find(n => n.id === id);
    if (target) selectNode(target);
  }
});

// ═══════════════════════════════════════════════════════
//  TOOLTIP
// ═══════════════════════════════════════════════════════
const tooltip = document.getElementById('tooltip');
function showTooltip(e, node) {
  const typeLabel = TYPE_LABELS[node._typeKey] || node._typeKey;
  tooltip.innerHTML =
    '<div class="tooltip-title">' + escapeHtml(node.name || '') + '</div>' +
    '<div class="tooltip-meta">' +
      '<span>' + typeLabel + '</span>' +
      '<span>' + (node.connections || 0) + ' conexiones</span>' +
    '</div>';
  tooltip.classList.add('show');
  moveTooltip(e);
}
function moveTooltip(e) {
  const x = Math.min(e.clientX + 14, window.innerWidth - 300);
  const y = Math.min(e.clientY + 14, window.innerHeight - 80);
  tooltip.style.left = x + 'px';
  tooltip.style.top = y + 'px';
}
function hideTooltip() { tooltip.classList.remove('show'); }

// ═══════════════════════════════════════════════════════
//  FILTERS
// ═══════════════════════════════════════════════════════
function applyFilters() {
  let visibleCount = 0;
  gNodes.selectAll('g.node').style('display', d => {
    const visible = activeFilters.has(d._typeKey);
    if (visible) visibleCount++;
    return visible ? null : 'none';
  });
  // Update node visibility flag
  allData.nodes.forEach(n => n._visible = activeFilters.has(n._typeKey));
  gLinks.selectAll('line').style('display', d => {
    const sid = typeof d.source === 'object' ? d.source.id : d.source;
    const tid = typeof d.target === 'object' ? d.target.id : d.target;
    const sn = allData.nodes.find(n => n.id === sid);
    const tn = allData.nodes.find(n => n.id === tid);
    return (sn && sn._visible && tn && tn._visible) ? null : 'none';
  });
  document.getElementById('empty').classList.toggle('hidden', visibleCount > 0);
}

// ═══════════════════════════════════════════════════════
//  SEARCH
// ═══════════════════════════════════════════════════════
const searchInput = document.getElementById('search-input');
searchInput.addEventListener('input', (e) => {
  const q = e.target.value.toLowerCase().trim();
  if (!q) { clearHighlight(); return; }
  gNodes.selectAll('g.node').classed('dim', d => {
    return !(d.name || '').toLowerCase().includes(q);
  });
  gLinks.selectAll('line').classed('dim', true);
});

document.addEventListener('keydown', (e) => {
  if ((e.metaKey || e.ctrlKey) && e.key === 'k') {
    e.preventDefault();
    searchInput.focus();
    searchInput.select();
  }
  if (e.key === 'Escape') {
    if (document.activeElement === searchInput) {
      searchInput.value = '';
      searchInput.blur();
      clearHighlight();
    } else {
      deselectNode();
    }
  }
  if (e.key === 'f' && document.activeElement !== searchInput) {
    fitView();
  }
});

// ═══════════════════════════════════════════════════════
//  ZOOM CONTROLS
// ═══════════════════════════════════════════════════════
document.getElementById('zoom-in').addEventListener('click', () => {
  svg.transition().duration(200).call(zoom.scaleBy, 1.4);
});
document.getElementById('zoom-out').addEventListener('click', () => {
  svg.transition().duration(200).call(zoom.scaleBy, 0.7);
});
document.getElementById('zoom-fit').addEventListener('click', fitView);

function fitView() {
  if (!allData) return;
  const bounds = gNodes.node().getBBox();
  if (!bounds.width || !bounds.height) return;
  const fullW = W(), fullH = H();
  const padding = 60;
  const availW = Math.max(fullW - padding * 2, 200);
  const availH = Math.max(fullH - padding * 2, 200);
  let scale = Math.min(availW / bounds.width, availH / bounds.height);
  // Clamp al rango permitido [0.1, 8]
  scale = Math.max(0.1, Math.min(2, scale));
  const tx = fullW / 2 - scale * (bounds.x + bounds.width / 2);
  const ty = fullH / 2 - scale * (bounds.y + bounds.height / 2);
  svg.transition().duration(600)
    .call(zoom.transform, d3.zoomIdentity.translate(tx, ty).scale(scale));
}

// ═══════════════════════════════════════════════════════
//  HELPERS
// ═══════════════════════════════════════════════════════
function escapeHtml(s) {
  if (s == null) return '';
  return String(s).replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
}

// ═══════════════════════════════════════════════════════
//  LABEL AVOIDANCE FORCE
// ═══════════════════════════════════════════════════════
function labelAvoidanceForce() {
  let nodes;
  function force(alpha) {
    const k = alpha * 0.4;
    for (let i = 0; i < nodes.length; i++) {
      const a = nodes[i];
      for (let j = i + 1; j < nodes.length; j++) {
        const b = nodes[j];
        const dx = b.x - a.x;
        const dy = b.y - a.y;
        // Bounding box de la etiqueta (debajo del nodo)
        const aLabelY = a.y + a._radius + 12;
        const bLabelY = b.y + b._radius + 12;
        const dyLabels = bLabelY - aLabelY;
        const absDx = Math.abs(dx);
        const absDy = Math.abs(dyLabels);
        // Si las cajas de labels se solapan, empujar verticalmente
        const xOverlap = (a._labelW + b._labelW) / 2;
        const yOverlap = 16;
        if (absDx < xOverlap && absDy < yOverlap) {
          const push = (yOverlap - absDy) * k;
          if (dy >= 0) {
            b.vy = (b.vy || 0) + push;
            a.vy = (a.vy || 0) - push;
          } else {
            b.vy = (b.vy || 0) - push;
            a.vy = (a.vy || 0) + push;
          }
        }
      }
    }
  }
  force.initialize = (n) => { nodes = n; };
  return force;
}

// ═══════════════════════════════════════════════════════
//  MINIMAP
// ═══════════════════════════════════════════════════════
let minimapSvg = null;
let minimapScale = 1;
let minimapOffset = { x: 0, y: 0 };

function renderMinimap() {
  if (!allData) return;
  minimapSvg = d3.select('#minimap-svg');
  const mw = 180, mh = 120, pad = 10;
  const bounds = gNodes.node().getBBox();
  if (!bounds.width) return;
  const sx = (mw - pad * 2) / bounds.width;
  const sy = (mh - pad * 2) / bounds.height;
  minimapScale = Math.min(sx, sy);
  minimapOffset.x = pad - bounds.x * minimapScale + (mw - pad * 2 - bounds.width * minimapScale) / 2;
  minimapOffset.y = pad - bounds.y * minimapScale + (mh - pad * 2 - bounds.height * minimapScale) / 2;

  minimapSvg.selectAll('*').remove();
  const g2 = minimapSvg.append('g')
    .attr('transform', `translate(${minimapOffset.x},${minimapOffset.y}) scale(${minimapScale})`);

  g2.selectAll('circle')
    .data(allData.nodes.filter(n => n._visible !== false))
    .join('circle')
    .attr('cx', d => d.x)
    .attr('cy', d => d.y)
    .attr('r', d => Math.max(1.5, d._radius / 3))
    .attr('fill', d => d._color)
    .attr('opacity', 0.7);

  updateMinimapViewport();
}

function updateMinimapViewport() {
  const mw = 180, mh = 120;
  const vp = document.getElementById('minimap-viewport');
  // La vista visible en coordenadas del grafo
  const k = currentTransform.k || 1;
  const tx = currentTransform.x || 0;
  const ty = currentTransform.y || 0;
  const viewW = W() / k;
  const viewH = H() / k;
  const viewX = -tx / k;
  const viewY = -ty / k;
  // Proyectar a coordenadas del minimap
  vp.style.left = (viewX * minimapScale + minimapOffset.x) + 'px';
  vp.style.top = (viewY * minimapScale + minimapOffset.y) + 'px';
  vp.style.width = (viewW * minimapScale) + 'px';
  vp.style.height = (viewH * minimapScale) + 'px';
}

// ═══════════════════════════════════════════════════════
//  HELP PANEL
// ═══════════════════════════════════════════════════════
const helpPanel = document.getElementById('help-panel');
document.getElementById('help-toggle').addEventListener('click', (e) => {
  e.stopPropagation();
  helpPanel.classList.toggle('open');
});
document.getElementById('help-close').addEventListener('click', () => {
  helpPanel.classList.remove('open');
});
document.addEventListener('click', (e) => {
  if (!helpPanel.contains(e.target) && e.target.id !== 'help-toggle') {
    helpPanel.classList.remove('open');
  }
});
document.addEventListener('keydown', (e) => {
  if (e.key === '?' && document.activeElement !== searchInput) {
    helpPanel.classList.toggle('open');
  }
});

window.addEventListener('resize', () => {
  if (simulation) {
    simulation.force('center', d3.forceCenter(W() / 2, H() / 2));
    simulation.alpha(0.3).restart();
  }
  updateMinimapViewport();
});

// ═══════════════════════════════════════════════════════
//  TABS + STATS VIEW
// ═══════════════════════════════════════════════════════
let currentView = 'graph';
let statsLoaded = false;
let statsRefreshTimer = null;
const STATS_REFRESH_INTERVAL_MS = 5000; // refresh cada 5 segundos en vivo

function switchView(view) {
  currentView = view;
  document.querySelectorAll('.tab').forEach(t => {
    t.classList.toggle('active', t.dataset.view === view);
  });
  const canvasWrap = document.getElementById('canvas-wrap');
  const statsView = document.getElementById('stats-view');
  const sidebar = document.querySelector('.sidebar');
  const minimap = document.getElementById('minimap');
  const zoomControls = document.querySelector('.zoom-controls');

  if (view === 'graph') {
    canvasWrap.classList.remove('hidden');
    statsView.classList.remove('active');
    if (sidebar) sidebar.style.display = '';
    if (minimap) minimap.style.display = '';
    if (zoomControls) zoomControls.style.display = '';
    // Detener refresh cuando no estamos en stats
    if (statsRefreshTimer) {
      clearInterval(statsRefreshTimer);
      statsRefreshTimer = null;
    }
  } else {
    canvasWrap.classList.add('hidden');
    statsView.classList.add('active');
    if (sidebar) sidebar.style.display = 'none';
    if (minimap) minimap.style.display = 'none';
    if (zoomControls) zoomControls.style.display = 'none';
    // Ajustar stats view para ocupar todo el ancho sin sidebar
    statsView.style.left = '0';
    loadStats();
    // Iniciar auto-refresh en vivo mientras el tab este abierto
    if (statsRefreshTimer) clearInterval(statsRefreshTimer);
    statsRefreshTimer = setInterval(() => {
      if (currentView === 'stats') {
        loadStats();
      }
    }, STATS_REFRESH_INTERVAL_MS);
  }
}

// Al volver a la pestana activa, refrescar inmediatamente
document.addEventListener('visibilitychange', () => {
  if (document.visibilityState === 'visible' && currentView === 'stats') {
    loadStats();
  }
});

document.querySelectorAll('.tab').forEach(tab => {
  tab.addEventListener('click', () => {
    switchView(tab.dataset.view);
    history.replaceState(null, '', '#' + tab.dataset.view);
  });
});
// Soporte para deep-link via hash: #stats abre directo en Estadisticas
if (location.hash === '#stats') {
  // Retrasar hasta que allData este cargado
  const waitForData = setInterval(() => {
    if (allData) {
      clearInterval(waitForData);
      switchView('stats');
    }
  }, 100);
}

function fmtNum(n) {
  if (n >= 1000000) return (n / 1000000).toFixed(2) + 'M';
  if (n >= 1000) return (n / 1000).toFixed(1) + 'k';
  return n.toString();
}

async function loadStats() {
  try {
    const [summaryRes, timelineRes, opsRes] = await Promise.all([
      fetch('/api/stats/summary').then(r => r.json()),
      fetch('/api/stats/timeline').then(r => r.json()),
      fetch('/api/stats/operations').then(r => r.json()),
    ]);
    renderStatCards(summaryRes);
    renderTimeline(timelineRes);
    renderOpsTable(opsRes);
    statsLoaded = true;
    // Indicador de ultima actualizacion
    const updated = document.getElementById('live-updated');
    if (updated) {
      const now = new Date();
      updated.textContent = '· ' + now.toLocaleTimeString('es', {hour: '2-digit', minute: '2-digit', second: '2-digit'});
    }
  } catch (e) {
    console.error('Error cargando stats:', e);
  }
}

function renderStatCards(summary) {
  const total = summary.total || {};
  const lastHour = summary.last_hour || {};
  const last24h = summary.last_24h || {};
  const last7d = summary.last_7d || {};
  const last30d = summary.last_30d || {};
  const container = document.getElementById('stat-cards-container');
  const totalOps = total.operations || 0;

  // Empty state dedicado cuando no hay datos
  if (totalOps === 0) {
    container.innerHTML =
      '<div class="stats-empty">' +
        '<div class="stats-empty-icon">◌</div>' +
        '<div class="stats-empty-title">Aún no hay datos de uso</div>' +
        '<div class="stats-empty-text">' +
          'Kerebrom empieza a rastrear tokens en cuanto uses una herramienta de AI ' +
          'conectada (Claude, Codex) y hagas un <code>recall</code>, <code>remember</code>, ' +
          '<code>context</code> o <code>query</code>. Los números aparecerán aquí ' +
          'en tiempo real en los próximos segundos.' +
        '</div>' +
      '</div>';
    return;
  }

  // Hero card con el total destacado + metadata
  const totalServed = total.tokens_served || total.tokens_saved_estimate || 0;
  const heroHtml =
    '<div class="stat-hero">' +
      '<div>' +
        '<div class="stat-hero-label">Contexto servido al modelo</div>' +
        '<div class="stat-hero-value">' + fmtNum(totalServed) +
          '<span class="stat-hero-unit">tokens</span></div>' +
      '</div>' +
      '<div class="stat-hero-meta">' +
        '<div><strong>' + totalOps.toLocaleString() + '</strong> operaciones</div>' +
        '<div>medición real · sin multiplicadores</div>' +
      '</div>' +
    '</div>';

  // Tarjetas rolling (4 en fila)
  const rollingCard = (label, data) => {
    const served = data.served || data.saved || 0;
    const ops = data.ops || 0;
    const emptyClass = served === 0 ? ' empty' : '';
    const deltaText = ops === 0 ? 'sin actividad' : ops + ' operación' + (ops === 1 ? '' : 'es');
    return '<div class="stat-card' + emptyClass + '">' +
      '<div class="stat-card-label">' + label + '</div>' +
      '<div class="stat-card-value">' + fmtNum(served) + '</div>' +
      '<div class="stat-card-delta">' + deltaText + '</div>' +
    '</div>';
  };

  const cardsHtml =
    '<div class="stat-cards">' +
      rollingCard('Últimos 30 días', last30d) +
      rollingCard('Últimos 7 días', last7d) +
      rollingCard('Últimas 24 horas', last24h) +
      rollingCard('Última hora', lastHour) +
    '</div>';

  container.innerHTML = heroHtml + cardsHtml;
}

function renderTimeline(data) {
  const container = document.getElementById('timeline-chart');
  if (!data || data.length === 0) {
    container.innerHTML = '<div style="text-align:center;padding:var(--sp-5);color:var(--text-dim);font-size:12px">Sin datos todavía. Usa Kerebrom un poco y vuelve.</div>';
    return;
  }
  const w = container.offsetWidth || 600;
  const h = 180;
  const padL = 40, padR = 16, padT = 16, padB = 32;
  const chartW = w - padL - padR;
  const chartH = h - padT - padB;
  const maxVal = Math.max(...data.map(d => d.saved), 1);
  const barW = chartW / data.length - 2;

  let svg = '<svg viewBox="0 0 ' + w + ' ' + h + '" xmlns="http://www.w3.org/2000/svg">';
  // Eje X
  svg += '<line x1="' + padL + '" y1="' + (h - padB) + '" x2="' + (w - padR) + '" y2="' + (h - padB) + '" class="timeline-axis"/>';
  // Gridlines horizontales (4)
  for (let i = 1; i <= 4; i++) {
    const y = padT + (chartH / 4) * i;
    svg += '<line x1="' + padL + '" y1="' + y + '" x2="' + (w - padR) + '" y2="' + y + '" stroke="rgba(255,255,255,0.04)"/>';
  }
  // Labels Y
  for (let i = 0; i <= 4; i++) {
    const v = Math.round((maxVal / 4) * (4 - i));
    const y = padT + (chartH / 4) * i + 4;
    svg += '<text x="' + (padL - 6) + '" y="' + y + '" class="timeline-label" text-anchor="end">' + fmtNum(v) + '</text>';
  }
  // Bars
  data.forEach((d, i) => {
    const barH = (d.saved / maxVal) * chartH;
    const x = padL + (chartW / data.length) * i + 1;
    const y = h - padB - barH;
    svg += '<rect class="timeline-bar" x="' + x + '" y="' + y + '" width="' + barW + '" height="' + barH + '" rx="2">';
    svg += '<title>' + d.day + ': ' + fmtNum(d.saved) + ' tokens (' + d.ops + ' ops)</title>';
    svg += '</rect>';
  });
  // Labels X (primer y último)
  if (data.length > 0) {
    svg += '<text x="' + padL + '" y="' + (h - 8) + '" class="timeline-label">' + data[0].day.slice(5) + '</text>';
    svg += '<text x="' + (w - padR) + '" y="' + (h - 8) + '" class="timeline-label" text-anchor="end">' + data[data.length - 1].day.slice(5) + '</text>';
  }
  svg += '</svg>';
  container.innerHTML = svg;
}

function renderOpsTable(ops) {
  const container = document.getElementById('ops-table');
  if (!ops || ops.length === 0) {
    container.innerHTML = '<div style="color:var(--text-dim);font-size:12px">Sin datos todavía.</div>';
    return;
  }
  const maxSaved = Math.max(...ops.map(o => o.tokens_saved), 1);
  let html = '';
  ops.forEach(o => {
    const pct = (o.tokens_saved / maxSaved) * 100;
    html += '<div class="ops-row">' +
      '<div class="ops-name">' + o.operation + '</div>' +
      '<div class="ops-bar-wrap"><div class="ops-bar" style="width:' + pct + '%"></div></div>' +
      '<div class="ops-value">' + fmtNum(o.tokens_saved) + ' · ' + o.operations + ' ops</div>' +
    '</div>';
  });
  container.innerHTML = html;
}

load();
</script>
</body>
</html>
"""


# ---------------------------------------------------------------------------
# Servidor HTTP
# ---------------------------------------------------------------------------

class _GraphHandler(BaseHTTPRequestHandler):
    """Handler HTTP para servir el grafo."""

    store: KerebromStore
    graph_data: Dict[str, Any]

    def log_message(self, fmt, *args):  # noqa: ANN001
        pass  # Silenciar logs del servidor

    def do_GET(self):  # noqa: N802
        if self.path == "/" or self.path == "/index.html":
            self._send(_GRAPH_HTML, "text/html")
        elif self.path == "/api/graph":
            self._send(json.dumps(self.graph_data), "application/json")
        elif self.path == "/api/stats/summary":
            summary = self.store.tokens.summary()
            self._send(json.dumps(summary), "application/json")
        elif self.path == "/api/stats/timeline":
            timeline = self.store.tokens.timeline(days=30)
            self._send(json.dumps(timeline), "application/json")
        elif self.path == "/api/stats/operations":
            ops = self.store.tokens.by_operation()
            self._send(json.dumps(ops), "application/json")
        elif self.path.startswith("/api/memory/"):
            try:
                mid = int(self.path.split("/")[-1])
                mem = self.store.get_memory(mid)
                self._send(json.dumps(mem.to_dict()), "application/json")
            except Exception:
                self._send('{"error": "memoria no encontrada"}', "application/json", 404)
        else:
            self._send("404", "text/plain", 404)

    def _send(self, body: str, content_type: str, code: int = 200):
        self.send_response(code)
        self.send_header("Content-Type", f"{content_type}; charset=utf-8")
        self.send_header("Access-Control-Allow-Origin", "*")
        self.end_headers()
        self.wfile.write(body.encode("utf-8"))


def launch_graph(
    db_path: Path,
    port: int = 8420,
    passphrase: Optional[str] = None,
) -> None:
    """Inicia el servidor de visualizacion y abre el navegador."""
    store = KerebromStore(db_path, passphrase=passphrase)
    store.initialize()

    graph_data = build_graph_data(store)

    # Configurar handler con datos del store
    _GraphHandler.store = store
    _GraphHandler.graph_data = graph_data

    server = HTTPServer(("127.0.0.1", port), _GraphHandler)

    print(f"Kerebrom Graph — http://127.0.0.1:{port}")
    print(f"  {graph_data['stats']['total_nodes']} nodos, {graph_data['stats']['total_links']} conexiones")
    print("  Ctrl+C para detener\n")

    # Abrir navegador en un thread separado
    threading.Timer(0.5, lambda: webbrowser.open(f"http://127.0.0.1:{port}")).start()

    try:
        server.serve_forever()
    except KeyboardInterrupt:
        print("\nServidor detenido.")
    finally:
        store.close()
        server.server_close()
