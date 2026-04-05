# Copyright (c) 2026 Ulian Bass. All rights reserved.
# This software is proprietary. See LICENSE for terms.

"""Exportador de la base de datos Kerebrom al formato de bóveda de Obsidian."""

from __future__ import annotations

import hashlib
import json
import math
import re
from pathlib import Path
from typing import Any, Dict, List, Optional

from .store import DEFAULT_PROJECT, KerebromStore


def _sanitizar_nombre_archivo(nombre: str) -> str:
    """Elimina caracteres no válidos para nombres de archivo."""
    limpio = re.sub(r'[\\/:*?"<>|]', "_", nombre)
    limpio = limpio.strip(". ")
    return limpio or "_sin_nombre"


def _generar_id_canvas(semilla: str) -> str:
    """Genera un identificador hexadecimal de 16 caracteres determinista para nodos del canvas."""
    return hashlib.md5(semilla.encode("utf-8")).hexdigest()[:16]


def _titulo_memoria(memoria: Dict[str, Any]) -> str:
    """Genera un titulo corto para una memoria a partir de su contenido."""
    contenido = memoria.get("content", "")
    # Tomar la primera linea o los primeros 60 caracteres
    primera_linea = contenido.split("\n")[0].strip()
    if len(primera_linea) > 60:
        primera_linea = primera_linea[:57] + "..."
    return primera_linea


def _obtener_entidades_de_memoria(
    store: KerebromStore,
    memory_id: int,
    project: str = DEFAULT_PROJECT,
) -> List[Dict[str, Any]]:
    """Obtiene las entidades vinculadas a una memoria especifica."""
    with store.connect() as conn:
        filas = conn.execute(
            """
            SELECT e.id, e.name, e.canonical_name, e.entity_type, me.role
            FROM memory_entities me
            JOIN entities e ON e.id = me.entity_id
            WHERE me.memory_id = ?
            """,
            (memory_id,),
        ).fetchall()
        return [dict(f) for f in filas]


def _obtener_relaciones_de_memoria(
    store: KerebromStore,
    memory_id: int,
    project: str = DEFAULT_PROJECT,
) -> List[Dict[str, Any]]:
    """Obtiene las relaciones donde la memoria es fuente."""
    with store.connect() as conn:
        filas = conn.execute(
            """
            SELECT r.id, s.name AS subject, r.predicate, o.name AS object,
                   r.confidence
            FROM relations r
            JOIN entities s ON s.id = r.subject_entity_id
            JOIN entities o ON o.id = r.object_entity_id
            WHERE r.source_memory_id = ? AND r.invalid_at IS NULL
            """,
            (memory_id,),
        ).fetchall()
        return [dict(f) for f in filas]


def _obtener_memorias_de_entidad(
    store: KerebromStore,
    entity_id: int,
    project: str = DEFAULT_PROJECT,
) -> List[Dict[str, Any]]:
    """Obtiene las memorias activas que mencionan una entidad."""
    with store.connect() as conn:
        filas = conn.execute(
            """
            SELECT m.id, m.content, m.kind
            FROM memories m
            JOIN memory_entities me ON me.memory_id = m.id
            WHERE me.entity_id = ? AND m.invalid_at IS NULL AND m.project = ?
            ORDER BY m.created_at DESC
            """,
            (entity_id, project),
        ).fetchall()
        return [dict(f) for f in filas]


def _obtener_relaciones_de_entidad(
    store: KerebromStore,
    entity_id: int,
    project: str = DEFAULT_PROJECT,
) -> List[Dict[str, Any]]:
    """Obtiene todas las relaciones activas donde la entidad es sujeto u objeto."""
    with store.connect() as conn:
        filas = conn.execute(
            """
            SELECT s.name AS subject, r.predicate, o.name AS object
            FROM relations r
            JOIN entities s ON s.id = r.subject_entity_id
            JOIN entities o ON o.id = r.object_entity_id
            WHERE (r.subject_entity_id = ? OR r.object_entity_id = ?)
              AND r.invalid_at IS NULL AND r.project = ?
            """,
            (entity_id, entity_id, project),
        ).fetchall()
        return [dict(f) for f in filas]


def _generar_nota_memoria(
    memoria: Dict[str, Any],
    entidades: List[Dict[str, Any]],
    relaciones: List[Dict[str, Any]],
) -> str:
    """Genera el contenido Markdown de una nota de memoria."""
    tags_yaml = ""
    tags = memoria.get("tags", [])
    if tags:
        items = "\n".join("  - {}".format(t) for t in tags)
        tags_yaml = "tags:\n{}\n".format(items)

    frontmatter = (
        "---\n"
        "id: {id}\n"
        "kind: {kind}\n"
        "importance: {importance}\n"
        "confidence: {confidence}\n"
        "{tags}"
        "source: {source}\n"
        "created_at: {created_at}\n"
        "aliases:\n"
        "  - memory-{id}\n"
        "---\n"
    ).format(tags=tags_yaml, **memoria)

    contenido = memoria.get("content", "")
    secciones = [frontmatter, "", contenido]

    if entidades:
        lineas_entidades = ["", "## Entidades"]
        for ent in entidades:
            nombre = ent["name"]
            lineas_entidades.append("- [[{}]]".format(nombre))
        secciones.extend(lineas_entidades)

    if relaciones:
        lineas_relaciones = ["", "## Relaciones"]
        for rel in relaciones:
            lineas_relaciones.append(
                "- {} \u2192 {} \u2192 [[{}]]".format(rel["subject"], rel["predicate"], rel["object"])
            )
        secciones.extend(lineas_relaciones)

    return "\n".join(secciones) + "\n"


def _generar_nota_entidad(
    entidad: Dict[str, Any],
    memorias: List[Dict[str, Any]],
    relaciones: List[Dict[str, Any]],
) -> str:
    """Genera el contenido Markdown de una nota de entidad."""
    nombre = entidad["name"]
    tipo = entidad.get("entity_type", "concept")
    canonical = entidad.get("canonical_name", nombre.lower())
    created = entidad.get("created_at", "")

    frontmatter = (
        "---\n"
        "entity_type: {}\n"
        "canonical_name: {}\n"
        "created_at: {}\n"
        "---\n"
    ).format(tipo, canonical, created)

    secciones = [frontmatter, "", "# {}".format(nombre)]

    if memorias:
        secciones.append("")
        secciones.append("## Memorias que mencionan esta entidad")
        for mem in memorias:
            alias = "memory-{}".format(mem["id"])
            titulo = _titulo_memoria(mem)
            secciones.append("- [[{}|{}]]".format(alias, titulo))

    if relaciones:
        secciones.append("")
        secciones.append("## Relaciones")
        for rel in relaciones:
            if rel["subject"].lower() == canonical:
                secciones.append("- {} \u2192 [[{}]]".format(rel["predicate"], rel["object"]))
            else:
                secciones.append("- [[{}]] \u2192 {} \u2192 este".format(rel["subject"], rel["predicate"]))

    return "\n".join(secciones) + "\n"


def _generar_indice(
    total_memorias: int,
    total_entidades: int,
    total_relaciones: int,
    entidades: List[Dict[str, Any]],
    memorias: List[Dict[str, Any]],
) -> str:
    """Genera la nota indice del vault."""
    secciones = [
        "---",
        "aliases:",
        "  - Kerebrom",
        "---",
        "",
        "# Indice Kerebrom",
        "",
        "## Estadisticas",
        "",
        "| Concepto | Cantidad |",
        "|----------|----------|",
        "| Memorias | {} |".format(total_memorias),
        "| Entidades | {} |".format(total_entidades),
        "| Relaciones | {} |".format(total_relaciones),
    ]

    if entidades:
        secciones.append("")
        secciones.append("## Entidades")
        secciones.append("")
        for ent in entidades:
            nombre = ent["name"]
            tipo = ent.get("entity_type", "concept")
            conteo = ent.get("memory_count", 0)
            secciones.append("- [[{}]] ({}, {} memorias)".format(nombre, tipo, conteo))

    # Indice de etiquetas
    conteo_tags: Dict[str, int] = {}
    for mem in memorias:
        for tag in mem.get("tags", []):
            conteo_tags[tag] = conteo_tags.get(tag, 0) + 1

    if conteo_tags:
        secciones.append("")
        secciones.append("## Etiquetas")
        secciones.append("")
        for tag, conteo in sorted(conteo_tags.items(), key=lambda x: (-x[1], x[0])):
            secciones.append("- **{}**: {} memorias".format(tag, conteo))

    return "\n".join(secciones) + "\n"


def _generar_canvas(
    entidades: List[Dict[str, Any]],
    relaciones: List[Dict[str, Any]],
) -> str:
    """Genera un archivo JSON Canvas con el grafo de conocimiento."""
    nodos: List[Dict[str, Any]] = []
    aristas: List[Dict[str, Any]] = []
    mapa_nodos: Dict[str, str] = {}  # canonical_name -> node_id

    total = len(entidades)
    if total == 0:
        canvas = {"nodes": [], "edges": []}
        return json.dumps(canvas, ensure_ascii=False, indent=2)

    # Distribuir las entidades en circulo
    radio = max(300, total * 40)
    centro_x, centro_y = 0, 0
    ancho_nodo, alto_nodo = 250, 60

    for i, ent in enumerate(entidades):
        nombre = ent["name"]
        canonical = ent.get("canonical_name", nombre.lower())
        angulo = (2 * math.pi * i) / total

        x = centro_x + int(radio * math.cos(angulo)) - ancho_nodo // 2
        y = centro_y + int(radio * math.sin(angulo)) - alto_nodo // 2

        node_id = _generar_id_canvas("entidad:{}".format(canonical))
        mapa_nodos[canonical] = node_id

        archivo_entidad = "entidades/{}.md".format(_sanitizar_nombre_archivo(nombre))
        nodos.append({
            "id": node_id,
            "type": "file",
            "file": archivo_entidad,
            "x": x,
            "y": y,
            "width": ancho_nodo,
            "height": alto_nodo,
        })

    for rel in relaciones:
        sujeto_canonical = rel.get("subject", "").lower().strip()
        objeto_canonical = rel.get("object", "").lower().strip()
        from_id = mapa_nodos.get(sujeto_canonical)
        to_id = mapa_nodos.get(objeto_canonical)

        if not from_id or not to_id:
            continue

        edge_seed = "arista:{}:{}:{}".format(sujeto_canonical, rel["predicate"], objeto_canonical)
        edge_id = _generar_id_canvas(edge_seed)

        aristas.append({
            "id": edge_id,
            "fromNode": from_id,
            "toNode": to_id,
            "label": rel["predicate"],
        })

    canvas = {"nodes": nodos, "edges": aristas}
    return json.dumps(canvas, ensure_ascii=False, indent=2)


def export_vault(
    store: KerebromStore,
    output_dir: str | Path,
    project: str = DEFAULT_PROJECT,
    include_canvas: bool = True,
) -> Dict[str, Any]:
    """Exporta la base de datos Kerebrom como una boveda de Obsidian.

    Genera notas Markdown para cada memoria y entidad, un archivo canvas
    con el grafo de conocimiento, y una nota indice con estadisticas.

    Args:
        store: Instancia de KerebromStore con la base de datos abierta.
        output_dir: Directorio de salida para la boveda.
        project: Namespace del proyecto a exportar.
        include_canvas: Si es True, genera el archivo canvas con el grafo.

    Returns:
        Diccionario con conteos de elementos exportados.
    """
    ruta_salida = Path(output_dir).expanduser().resolve()
    ruta_memorias = ruta_salida / "memorias"
    ruta_entidades = ruta_salida / "entidades"

    ruta_memorias.mkdir(parents=True, exist_ok=True)
    ruta_entidades.mkdir(parents=True, exist_ok=True)

    # --- Exportar memorias ---
    memorias = store.export_memories(project=project, include_inactive=False)
    memorias_exportadas = 0

    for mem in memorias:
        mid = mem["id"]
        entidades_mem = _obtener_entidades_de_memoria(store, mid, project)
        relaciones_mem = _obtener_relaciones_de_memoria(store, mid, project)
        contenido_nota = _generar_nota_memoria(mem, entidades_mem, relaciones_mem)
        nombre_archivo = "memory-{}.md".format(mid)
        (ruta_memorias / nombre_archivo).write_text(contenido_nota, encoding="utf-8")
        memorias_exportadas += 1

    # --- Exportar entidades ---
    entidades = store.list_entities(project=project, limit=10000, active_only=True)
    entidades_exportadas = 0

    # Para las notas de entidad necesitamos datos adicionales del DB
    entidades_completas: List[Dict[str, Any]] = []
    with store.connect() as conn:
        for ent in entidades:
            fila = conn.execute(
                "SELECT * FROM entities WHERE id = ?", (ent["id"],)
            ).fetchone()
            if fila:
                ent_completa = dict(fila)
                ent_completa["memory_count"] = ent["memory_count"]
                entidades_completas.append(ent_completa)

    for ent in entidades_completas:
        memorias_ent = _obtener_memorias_de_entidad(store, ent["id"], project)
        relaciones_ent = _obtener_relaciones_de_entidad(store, ent["id"], project)
        contenido_nota = _generar_nota_entidad(ent, memorias_ent, relaciones_ent)
        nombre_archivo = "{}.md".format(_sanitizar_nombre_archivo(ent["name"]))
        (ruta_entidades / nombre_archivo).write_text(contenido_nota, encoding="utf-8")
        entidades_exportadas += 1

    # --- Relaciones para el canvas e indice ---
    relaciones = store.list_facts(project=project, active_only=True, limit=10000)
    total_relaciones = len(relaciones)

    # --- Canvas ---
    canvas_generado = False
    if include_canvas and entidades_completas:
        contenido_canvas = _generar_canvas(entidades_completas, relaciones)
        (ruta_salida / "kerebrom-graph.canvas").write_text(contenido_canvas, encoding="utf-8")
        canvas_generado = True

    # --- Indice ---
    contenido_indice = _generar_indice(
        total_memorias=memorias_exportadas,
        total_entidades=entidades_exportadas,
        total_relaciones=total_relaciones,
        entidades=entidades_completas,
        memorias=memorias,
    )
    (ruta_salida / "_Kerebrom Index.md").write_text(contenido_indice, encoding="utf-8")

    return {
        "exported_memories": memorias_exportadas,
        "exported_entities": entidades_exportadas,
        "exported_relations": total_relaciones,
        "canvas_generated": canvas_generado,
        "output_dir": str(ruta_salida),
    }
