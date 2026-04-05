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
<title>Kerebrom — Grafo de Conocimiento</title>
<script src="https://cdn.jsdelivr.net/npm/d3@7.9.0/dist/d3.min.js"></script>
<style>
* { margin: 0; padding: 0; box-sizing: border-box; }
body { background: #0f0f1a; color: #e2e8f0; font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', system-ui, sans-serif; overflow: hidden; }

#header {
  position: fixed; top: 0; left: 0; right: 0; z-index: 10;
  background: rgba(15,15,26,0.92); backdrop-filter: blur(12px);
  border-bottom: 1px solid #2d2d44;
  padding: 10px 20px; display: flex; align-items: center; gap: 16px;
}
#header h1 { font-size: 16px; font-weight: 600; color: #a855f7; white-space: nowrap; }
#stats { font-size: 12px; color: #6b7280; white-space: nowrap; }
#search {
  background: #1e1e32; border: 1px solid #3d3d5c; border-radius: 6px;
  color: #e2e8f0; padding: 6px 12px; font-size: 13px; width: 220px; outline: none;
}
#search:focus { border-color: #a855f7; }
#search::placeholder { color: #4b5563; }

.filter-btn {
  background: #1e1e32; border: 1px solid #3d3d5c; border-radius: 4px;
  color: #9ca3af; padding: 4px 10px; font-size: 11px; cursor: pointer;
  transition: all 0.15s;
}
.filter-btn.active { border-color: currentColor; color: #e2e8f0; }
.filter-btn:hover { border-color: #6b7280; }

#panel {
  position: fixed; right: 0; top: 48px; bottom: 0; width: 320px;
  background: rgba(15,15,26,0.95); backdrop-filter: blur(12px);
  border-left: 1px solid #2d2d44; padding: 20px;
  transform: translateX(100%); transition: transform 0.2s ease;
  overflow-y: auto; z-index: 9;
}
#panel.open { transform: translateX(0); }
#panel h2 { font-size: 15px; margin-bottom: 8px; color: #a855f7; }
#panel .type-badge {
  display: inline-block; padding: 2px 8px; border-radius: 3px;
  font-size: 11px; margin-bottom: 12px;
}
#panel .content { font-size: 13px; line-height: 1.6; color: #cbd5e1; }
#panel .relations { margin-top: 16px; }
#panel .relation {
  font-size: 12px; color: #9ca3af; padding: 4px 0;
  border-bottom: 1px solid #1e1e32;
}
#panel .close-btn {
  position: absolute; top: 12px; right: 12px;
  background: none; border: none; color: #6b7280; cursor: pointer; font-size: 18px;
}

#graph { width: 100vw; height: 100vh; position: fixed; top: 0; left: 0; z-index: 1; }
.link { stroke-opacity: 0.3; }
.link.highlighted { stroke: #a855f7 !important; stroke-opacity: 1; stroke-width: 2px !important; }
.node circle { cursor: pointer; stroke: rgba(255,255,255,0.15); stroke-width: 1px; }
.node circle:hover { stroke: #fff; stroke-width: 2px; }
.node text { font-size: 9px; fill: rgba(226,232,240,0.6); pointer-events: none; }
.node.dimmed circle { opacity: 0.08; }
.node.dimmed text { opacity: 0.05; }
.tooltip { position: fixed; background: rgba(30,30,50,0.95); border: 1px solid #3d3d5c; border-radius: 6px; padding: 8px 12px; font-size: 12px; color: #e2e8f0; pointer-events: none; z-index: 20; max-width: 300px; display: none; }

.legend {
  position: fixed; bottom: 16px; left: 16px; z-index: 10;
  background: rgba(15,15,26,0.9); border: 1px solid #2d2d44;
  border-radius: 8px; padding: 12px 16px; font-size: 11px;
}
.legend-item { display: flex; align-items: center; gap: 8px; margin: 4px 0; }
.legend-dot { width: 10px; height: 10px; border-radius: 50%; }
</style>
</head>
<body>

<div id="header">
  <h1>Kerebrom</h1>
  <span id="stats"></span>
  <input id="search" type="text" placeholder="Buscar entidad o memoria...">
  <button class="filter-btn active" data-group="all">Todo</button>
  <button class="filter-btn active" data-group="person">Personas</button>
  <button class="filter-btn active" data-group="concept">Conceptos</button>
  <button class="filter-btn active" data-group="location">Lugares</button>
  <button class="filter-btn active" data-group="organization">Orgs</button>
  <button class="filter-btn active" data-group="memory">Memorias</button>
</div>

<div id="panel">
  <button class="close-btn" onclick="closePanel()">&times;</button>
  <div id="panel-content"></div>
</div>

<svg id="graph"></svg>

<div class="legend">
  <div class="legend-item"><div class="legend-dot" style="background:#4a9eff"></div> Persona</div>
  <div class="legend-item"><div class="legend-dot" style="background:#a855f7"></div> Concepto</div>
  <div class="legend-item"><div class="legend-dot" style="background:#4ecdc4"></div> Lugar</div>
  <div class="legend-item"><div class="legend-dot" style="background:#ff6b6b"></div> Organizacion</div>
  <div class="legend-item"><div class="legend-dot" style="background:#fbbf24"></div> Memoria core</div>
  <div class="legend-item"><div class="legend-dot" style="background:#818cf8"></div> Memoria semantica</div>
</div>

<div class="tooltip" id="tooltip"></div>
<script>
const W = window.innerWidth, H = window.innerHeight;
const svg = d3.select('#graph').attr('width', W).attr('height', H);
const g = svg.append('g');
const tooltip = document.getElementById('tooltip');

// Zoom y pan con D3
svg.call(d3.zoom()
  .scaleExtent([0.1, 8])
  .on('zoom', e => g.attr('transform', e.transform))
);

fetch('/api/graph').then(r => r.json()).then(data => {
  document.getElementById('stats').textContent =
    data.stats.entities + ' entidades · ' + data.stats.relations + ' relaciones · ' + data.stats.memories + ' memorias';

  const simulation = d3.forceSimulation(data.nodes)
    .force('link', d3.forceLink(data.links).id(d => d.id).distance(60).strength(0.3))
    .force('charge', d3.forceManyBody().strength(-80).distanceMax(250))
    .force('center', d3.forceCenter(W / 2, H / 2))
    .force('collision', d3.forceCollide().radius(d => Math.sqrt(d.val) * 3 + 2));

  // Links
  const link = g.append('g').selectAll('line')
    .data(data.links).join('line')
    .attr('class', 'link')
    .attr('stroke', '#475569')
    .attr('stroke-width', d => Math.max(0.5, d.value * 2));

  // Nodos
  const node = g.append('g').selectAll('g')
    .data(data.nodes).join('g')
    .attr('class', 'node')
    .call(d3.drag()
      .on('start', (e, d) => { if (!e.active) simulation.alphaTarget(0.3).restart(); d.fx = d.x; d.fy = d.y; })
      .on('drag', (e, d) => { d.fx = e.x; d.fy = e.y; })
      .on('end', (e, d) => { if (!e.active) simulation.alphaTarget(0); d.fx = null; d.fy = null; })
    );

  node.append('circle')
    .attr('r', d => Math.max(3, Math.sqrt(d.val) * 2.5))
    .attr('fill', d => d.color);

  node.append('text')
    .attr('dy', d => Math.sqrt(d.val) * 2.5 + 12)
    .attr('text-anchor', 'middle')
    .text(d => d.name.length > 25 ? d.name.slice(0, 22) + '...' : d.name);

  // Hover: resaltar vecinos
  node.on('mouseover', function(e, d) {
    const neighbors = new Set();
    neighbors.add(d.id);
    data.links.forEach(l => {
      const sid = typeof l.source === 'object' ? l.source.id : l.source;
      const tid = typeof l.target === 'object' ? l.target.id : l.target;
      if (sid === d.id) neighbors.add(tid);
      if (tid === d.id) neighbors.add(sid);
    });

    node.classed('dimmed', n => !neighbors.has(n.id));
    link.classed('highlighted', l => {
      const sid = typeof l.source === 'object' ? l.source.id : l.source;
      const tid = typeof l.target === 'object' ? l.target.id : l.target;
      return sid === d.id || tid === d.id;
    });

    tooltip.style.display = 'block';
    tooltip.style.left = (e.pageX + 12) + 'px';
    tooltip.style.top = (e.pageY - 10) + 'px';
    const typeLabel = d.type === 'entity' ? d.group : d.kind;
    tooltip.innerHTML = '<strong>' + d.name.slice(0, 80) + '</strong><br>' + typeLabel + ' · ' + (d.connections || 0) + ' conexiones';
  })
  .on('mouseout', function() {
    node.classed('dimmed', false);
    link.classed('highlighted', false);
    tooltip.style.display = 'none';
  })
  .on('click', function(e, d) {
    const panel = document.getElementById('panel');
    const content = document.getElementById('panel-content');
    const neighbors = data.links.filter(l => {
      const sid = typeof l.source === 'object' ? l.source.id : l.source;
      const tid = typeof l.target === 'object' ? l.target.id : l.target;
      return sid === d.id || tid === d.id;
    });
    let relHtml = '';
    neighbors.forEach(l => {
      const s = typeof l.source === 'object' ? l.source : data.nodes.find(n => n.id === l.source);
      const t = typeof l.target === 'object' ? l.target : data.nodes.find(n => n.id === l.target);
      const other = (s && s.id === d.id) ? t : s;
      if (other) relHtml += '<div class="relation">' + l.label + ' \\u2192 ' + other.name.slice(0, 60) + '</div>';
    });

    if (d.type === 'memory') {
      fetch('/api/memory/' + d.id.replace('m_', ''))
        .then(r => r.json())
        .then(mem => {
          content.innerHTML = '<h2>' + (d.kind || 'memoria') + '</h2>' +
            '<div class="type-badge" style="background:' + d.color + '33;color:' + d.color + '">importancia: ' + (mem.importance || 0).toFixed(2) + '</div>' +
            '<div class="content">' + (mem.content || d.name) + '</div>' +
            (relHtml ? '<div class="relations"><h3 style="font-size:13px;margin:12px 0 8px">Conexiones</h3>' + relHtml + '</div>' : '');
        });
    } else {
      content.innerHTML = '<h2>' + d.name + '</h2>' +
        '<div class="type-badge" style="background:' + d.color + '33;color:' + d.color + '">' + d.group + ' · ' + (d.connections || 0) + ' conexiones</div>' +
        (relHtml ? '<div class="relations"><h3 style="font-size:13px;margin:12px 0 8px">Relaciones</h3>' + relHtml + '</div>' : '');
    }
    panel.classList.add('open');
  });

  // Tick de simulacion
  simulation.on('tick', () => {
    link
      .attr('x1', d => d.source.x).attr('y1', d => d.source.y)
      .attr('x2', d => d.target.x).attr('y2', d => d.target.y);
    node.attr('transform', d => 'translate(' + d.x + ',' + d.y + ')');
  });

  // Busqueda
  document.getElementById('search').addEventListener('input', e => {
    const term = e.target.value.toLowerCase();
    node.classed('dimmed', d => term && !d.name.toLowerCase().includes(term));
  });

  // Filtros
  document.querySelectorAll('.filter-btn').forEach(btn => {
    btn.addEventListener('click', () => {
      btn.classList.toggle('active');
      const activeGroups = new Set();
      document.querySelectorAll('.filter-btn.active').forEach(b => activeGroups.add(b.dataset.group));
      const showAll = activeGroups.has('all');
      node.style('display', d => {
        if (showAll) return null;
        if (d.type === 'memory') return activeGroups.has('memory') ? null : 'none';
        return activeGroups.has(d.group) ? null : 'none';
      });
      link.style('display', l => {
        const sVis = l.source && d3.select('[data-id="' + l.source.id + '"]').style('display') !== 'none';
        return null; // mostrar todos los links por simplicidad
      });
    });
  });

  // Click fondo cierra panel
  svg.on('click', function(e) {
    if (e.target === svg.node()) {
      document.getElementById('panel').classList.remove('open');
    }
  });
});

function closePanel() { document.getElementById('panel').classList.remove('open'); }
</script>
</body>
</html>"""


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
