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
    """Construye nodos y links para el grafo interactivo."""
    nodes = []
    links = []
    entity_conn_count: Dict[int, int] = {}

    # --- Entidades ---
    with store.connect() as conn:
        entities = conn.execute(
            "SELECT id, name, entity_type, created_at FROM entities WHERE project = ?",
            (project,),
        ).fetchall()

        relations = conn.execute(
            "SELECT subject_entity_id, predicate, object_entity_id, confidence "
            "FROM relations WHERE project = ? AND invalid_at IS NULL",
            (project,),
        ).fetchall()

        # Contar conexiones por entidad
        for r in relations:
            entity_conn_count[r[0]] = entity_conn_count.get(r[0], 0) + 1
            entity_conn_count[r[2]] = entity_conn_count.get(r[2], 0) + 1

        # Nodos entidad
        for e in entities:
            eid = e[0]
            conns = entity_conn_count.get(eid, 0)
            if conns == 0:
                # Contar menciones en memoria como conexiones
                mem_count = conn.execute(
                    "SELECT COUNT(*) FROM memory_entities WHERE entity_id = ?",
                    (eid,),
                ).fetchone()[0]
                conns = mem_count

            nodes.append({
                "id": f"e_{eid}",
                "name": e[1],
                "group": e[2] or "concept",
                "type": "entity",
                "val": max(3, conns * 2),
                "color": _ENTITY_COLORS.get(e[2] or "concept", _DEFAULT_COLOR),
                "connections": conns,
            })

        # Links de relaciones
        for r in relations:
            links.append({
                "source": f"e_{r[0]}",
                "target": f"e_{r[2]}",
                "label": r[1],
                "value": r[3],
            })

        # --- Memorias importantes ---
        memories = conn.execute(
            "SELECT id, content, kind, importance, tags FROM memories "
            "WHERE project = ? AND invalid_at IS NULL AND importance >= 0.7 "
            "ORDER BY importance DESC LIMIT 100",
            (project,),
        ).fetchall()

        for m in memories:
            mid = m[0]
            snippet = m[1][:80] + ("..." if len(m[1]) > 80 else "")
            kind = m[2]
            nodes.append({
                "id": f"m_{mid}",
                "name": snippet,
                "group": f"memory_{kind}",
                "type": "memory",
                "kind": kind,
                "importance": m[3],
                "val": max(2, int(m[3] * 6)),
                "color": _MEMORY_COLORS.get(kind, _DEFAULT_COLOR),
                "connections": 0,
            })

            # Links memoria -> entidades
            mem_entities = conn.execute(
                "SELECT entity_id FROM memory_entities WHERE memory_id = ?",
                (mid,),
            ).fetchall()
            for me in mem_entities:
                links.append({
                    "source": f"m_{mid}",
                    "target": f"e_{me[0]}",
                    "label": "menciona",
                    "value": 0.3,
                })

    return {
        "nodes": nodes,
        "links": links,
        "stats": {
            "entities": len(entities),
            "relations": len(relations),
            "memories": len(memories),
            "total_nodes": len(nodes),
            "total_links": len(links),
        },
    }


# ---------------------------------------------------------------------------
# HTML del visualizador (inline, sin archivos externos)
# ---------------------------------------------------------------------------

_GRAPH_HTML = r"""<!DOCTYPE html>
<html lang="es">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Kerebrom — Grafo de Conocimiento</title>
<script src="https://unpkg.com/force-graph@1.47.5/dist/force-graph.min.js"></script>
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

#graph { width: 100vw; height: 100vh; }

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

<div id="graph"></div>

<div class="legend">
  <div class="legend-item"><div class="legend-dot" style="background:#4a9eff"></div> Persona</div>
  <div class="legend-item"><div class="legend-dot" style="background:#a855f7"></div> Concepto</div>
  <div class="legend-item"><div class="legend-dot" style="background:#4ecdc4"></div> Lugar</div>
  <div class="legend-item"><div class="legend-dot" style="background:#ff6b6b"></div> Organizacion</div>
  <div class="legend-item"><div class="legend-dot" style="background:#fbbf24"></div> Memoria core</div>
  <div class="legend-item"><div class="legend-dot" style="background:#818cf8"></div> Memoria semantica</div>
</div>

<script>
let graphData = null;
let graph = null;
let selectedNode = null;
let highlightNodes = new Set();
let highlightLinks = new Set();
let activeFilters = new Set(['person','concept','location','organization','memory']);
let searchTerm = '';

async function init() {
  const res = await fetch('/api/graph');
  graphData = await res.json();

  document.getElementById('stats').textContent =
    `${graphData.stats.entities} entidades · ${graphData.stats.relations} relaciones · ${graphData.stats.memories} memorias`;

  graph = ForceGraph()(document.getElementById('graph'))
    .graphData(graphData)
    .backgroundColor('#0f0f1a')
    .nodeLabel(n => `${n.name}\n${n.type === 'entity' ? n.group : n.kind} · ${n.connections || 0} conexiones`)
    .nodeColor(n => getNodeColor(n))
    .nodeVal(n => getNodeSize(n))
    .nodeCanvasObjectMode(() => 'after')
    .nodeCanvasObject((node, ctx, globalScale) => {
      // Dibujar etiqueta solo si el zoom es suficiente o el nodo es grande
      if (globalScale < 1.5 && node.val < 8) return;
      const label = node.name.length > 25 ? node.name.slice(0, 22) + '...' : node.name;
      const fontSize = Math.max(10 / globalScale, 1.5);
      ctx.font = `${fontSize}px -apple-system, sans-serif`;
      ctx.textAlign = 'center';
      ctx.textBaseline = 'top';
      ctx.fillStyle = highlightNodes.has(node) ? '#ffffff' : 'rgba(226,232,240,0.7)';
      ctx.fillText(label, node.x, node.y + node.val / 1.5 + 2);
    })
    .linkColor(l => highlightLinks.has(l) ? '#a855f7' : 'rgba(100,100,140,0.25)')
    .linkWidth(l => highlightLinks.has(l) ? 2 : Math.max(0.5, l.value * 2))
    .linkDirectionalArrowLength(4)
    .linkDirectionalArrowRelPos(1)
    .linkLabel(l => l.label)
    .onNodeClick(handleNodeClick)
    .onNodeHover(handleNodeHover)
    .onBackgroundClick(() => { clearHighlight(); closePanel(); })
    .warmupTicks(80)
    .cooldownTicks(200)
    .d3Force('charge').strength(-120);

  graph.d3Force('link').distance(60);
}

function getNodeColor(n) {
  if (highlightNodes.size > 0 && !highlightNodes.has(n)) return 'rgba(60,60,80,0.3)';
  if (searchTerm && !n.name.toLowerCase().includes(searchTerm)) return 'rgba(60,60,80,0.3)';
  return n.color;
}

function getNodeSize(n) {
  if (highlightNodes.size > 0 && !highlightNodes.has(n)) return Math.max(1, n.val * 0.5);
  return n.val;
}

function handleNodeHover(node) {
  highlightNodes.clear();
  highlightLinks.clear();

  if (node) {
    highlightNodes.add(node);
    const neighbors = graphData.links.filter(l =>
      l.source === node || l.target === node ||
      l.source.id === node.id || l.target.id === node.id
    );
    neighbors.forEach(l => {
      highlightLinks.add(l);
      if (typeof l.source === 'object') highlightNodes.add(l.source);
      if (typeof l.target === 'object') highlightNodes.add(l.target);
    });
  }
  graph.nodeColor(graph.nodeColor());
  graph.linkColor(graph.linkColor());
  graph.linkWidth(graph.linkWidth());
}

function handleNodeClick(node) {
  if (!node) return;
  selectedNode = node;

  highlightNodes.clear();
  highlightLinks.clear();
  highlightNodes.add(node);

  const neighbors = graphData.links.filter(l =>
    l.source === node || l.target === node
  );
  neighbors.forEach(l => {
    highlightLinks.add(l);
    highlightNodes.add(l.source);
    highlightNodes.add(l.target);
  });

  graph.nodeColor(graph.nodeColor());
  graph.linkColor(graph.linkColor());

  // Panel lateral
  const panel = document.getElementById('panel');
  const content = document.getElementById('panel-content');

  let relHtml = '';
  neighbors.forEach(l => {
    const other = l.source === node ? l.target : l.source;
    const dir = l.source === node ? '→' : '←';
    relHtml += `<div class="relation">${dir} <strong>${l.label}</strong> ${other.name}</div>`;
  });

  if (node.type === 'memory') {
    fetch(`/api/memory/${node.id.replace('m_', '')}`)
      .then(r => r.json())
      .then(mem => {
        content.innerHTML = `
          <h2>${node.kind}</h2>
          <div class="type-badge" style="background:${node.color}33;color:${node.color}">
            importance: ${(mem.importance || node.importance).toFixed(2)}
          </div>
          <div class="content">${mem.content || node.name}</div>
          ${relHtml ? `<div class="relations"><h3 style="font-size:13px;margin-bottom:8px;">Conexiones</h3>${relHtml}</div>` : ''}
        `;
      });
  } else {
    content.innerHTML = `
      <h2>${node.name}</h2>
      <div class="type-badge" style="background:${node.color}33;color:${node.color}">
        ${node.group} · ${node.connections} conexiones
      </div>
      ${relHtml ? `<div class="relations"><h3 style="font-size:13px;margin-bottom:8px;">Relaciones</h3>${relHtml}</div>` : ''}
    `;
  }
  panel.classList.add('open');

  // Centrar camara en el nodo
  graph.centerAt(node.x, node.y, 500);
  graph.zoom(3, 500);
}

function clearHighlight() {
  highlightNodes.clear();
  highlightLinks.clear();
  selectedNode = null;
  graph.nodeColor(graph.nodeColor());
  graph.linkColor(graph.linkColor());
  graph.linkWidth(graph.linkWidth());
}

function closePanel() {
  document.getElementById('panel').classList.remove('open');
}

// Busqueda
document.getElementById('search').addEventListener('input', (e) => {
  searchTerm = e.target.value.toLowerCase();
  graph.nodeColor(graph.nodeColor());
});

// Filtros
document.querySelectorAll('.filter-btn').forEach(btn => {
  btn.addEventListener('click', () => {
    const group = btn.dataset.group;
    if (group === 'all') {
      const allActive = document.querySelectorAll('.filter-btn.active').length ===
                         document.querySelectorAll('.filter-btn').length;
      document.querySelectorAll('.filter-btn').forEach(b => {
        if (allActive) b.classList.remove('active');
        else b.classList.add('active');
      });
      if (allActive) activeFilters.clear();
      else activeFilters = new Set(['person','concept','location','organization','memory']);
    } else {
      btn.classList.toggle('active');
      if (btn.classList.contains('active')) activeFilters.add(group);
      else activeFilters.delete(group);
    }
    applyFilters();
  });
});

function applyFilters() {
  const filtered = {
    nodes: graphData.nodes.filter(n => {
      if (n.type === 'memory') return activeFilters.has('memory');
      return activeFilters.has(n.group);
    }),
    links: graphData.links,
  };
  // Solo links cuyos nodos estan visibles
  const visibleIds = new Set(filtered.nodes.map(n => n.id));
  filtered.links = graphData.links.filter(l => {
    const sid = typeof l.source === 'object' ? l.source.id : l.source;
    const tid = typeof l.target === 'object' ? l.target.id : l.target;
    return visibleIds.has(sid) && visibleIds.has(tid);
  });
  graph.graphData(filtered);
}

init();
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
