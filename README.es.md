# Kerebrom

[English](README.md) · [Release v2.0.1](https://github.com/ulianbass/kerebrom/releases/tag/v2.0.1) · [Historial del repositorio](docs/BRANCHES.md)

> Memoria persistente local para agentes de IA.
> Una sola capa de memoria durable para Claude, Codex, Cursor, Gemini CLI, OpenCode, Windsurf, VS Code y cualquier otro cliente compatible con MCP.

Kerebrom da a los agentes de codificación una memoria local compartida sin enviar el contexto de tu proyecto a servicios en la nube. Funciona como un único binario Go, almacena datos en SQLite con FTS5, expone un servidor MCP con superficie semántica limpia, e instala el flujo de memoria más fuerte disponible para cada cliente IA soportado.

![Flujo de memoria de Kerebrom](docs/assets/kerebrom-memory-flow.svg)

## Líneas del producto

| Línea | Estado | Propósito |
|---|---:|---|
| `v2` | **rama actual** | Superficie semántica limpia (7 tools con nombres de verbo), comando de auto-actualización, plug-and-play en Claude Desktop |
| `v1` | mantenida | Superficie `mem_*` previa; aún recibe correcciones críticas |
| `history/legacy-main-2026-04-10` | tag | Historia de la rama por defecto antes del reset v1 |
| `history/go-rewrite-2026-04-10` | tag | Experimento de reescritura Go anterior |

Cada línea vive en su propio directorio `versions/vN/` para que las versiones futuras evolucionen sin reescribir historia. Ver [docs/BRANCHES.md](docs/BRANCHES.md) para el mapa completo del repositorio.

## Lo que provee v2

- **Único binario Go** sin pila de servicios que mantener.
- **Siete tools MCP semánticos** (`context`, `recall`, `remember`, `summary`, `forget`, `timeline`, `projects`) — nombres de verbo que el modelo entiende por intuición, sin prefijos `mem_*` que aprender.
- **Auto-actualización** con `kerebrom update` — descarga el último release de GitHub, baja el código fuente y reinstala en el lugar.
- **Auto-aprobación en Claude Code** para los seis tools del día a día, así el agente nunca pide permiso para usar memoria.
- **Limpieza de restos de v1** durante setup: cualquier entrada vieja `mcp__Kerebrom__mem_*` en `permissions.allow` se elimina.
- **Mismo almacén SQLite + FTS5 que v1** — actualizar no toca tus datos.
- **Transporte MCP Streamable HTTP** para conectores remotos como Claude Chat/Cowork y ChatGPT cuando el cliente no puede lanzar un servidor MCP stdio local.
- **CLI, API HTTP, dashboard de terminal, export/import, chunks de sync comprimidos** — todo lo de v1, con el vocabulario v2.
- **Hooks de ciclo de vida de Claude Code** para inicio de sesión, ingesta de prompt, captura pasiva, recuperación post-compactación y cierre de sesión.
- **Setup para Codex, Claude Desktop, Cursor, Gemini CLI, OpenCode, Windsurf y VS Code** con el nuevo texto de protocolo.
- **Marco de memoria distilada**: los prompts son historia de intención; las observaciones son memorias interpretadas con el formato `What / Why / Where / Learned`.

## Instalación

```bash
git clone https://github.com/ulianbass/kerebrom.git
cd kerebrom/versions/v2
make install-user
```

Esto compila `kerebrom`, lo instala en `~/local/bin/kerebrom`, lo enlaza desde `~/.local/bin/kerebrom`, y ejecuta:

```bash
kerebrom setup auto
```

`setup auto` configura solo los clientes con configuración local existente y cae en Claude Desktop cuando no detecta ninguna. Para forzar un cliente específico:

```bash
make install-user SETUP_AGENT=cursor
make install-user SETUP_AGENT=claude-desktop
make install-user SETUP_AGENT=all
```

Reinicia cualquier cliente IA abierto para que recoja el nuevo servidor MCP.

## Actualizar

```bash
kerebrom update
```

Descarga el último release de GitHub, extrae el tarball de fuentes para ese tag y ejecuta `make install-user` contra `versions/v2`. Usa `--check` para ver si hay actualización sin instalar, `--yes` para saltar la confirmación, o `--pre-release` para considerar tags pre-release.

## El ciclo

Los seis tools cotidianos y un tool admin explícito componen el ritmo de memoria:

| Cuándo | Tool |
|---|---|
| Inicio de cualquier conversación no trivial | `context` |
| Necesitas buscar un tema específico | `recall` |
| Aparece un hecho durable, decisión, preferencia, bugfix o aprendizaje | `remember` |
| Cerrando trabajo sustancial o tras compactación | `summary` |
| Inspeccionar historia cronológica | `timeline` |
| El usuario dice que algo está mal o es obsoleto | `forget` |
| Consolidar variantes de nombres de proyecto cuando el usuario lo pide explícitamente | `projects` (perfil admin) |

El usuario nunca tiene que decir "usa Kerebrom" o "guarda esto en memoria". El agente usa los tools por iniciativa propia cuando los nombres coinciden con la intención.

## Comandos cotidianos

```bash
kerebrom version
kerebrom update
kerebrom setup auto
kerebrom setup all
kerebrom stats
kerebrom context --project my-project "qué importa aquí?"
kerebrom search "decisión release"
KEREBROM_REMOTE_TOKEN="change-me" kerebrom mcp-http --addr 127.0.0.1:7437 --path /mcp
kerebrom tui
kerebrom export --output memory-export.json
kerebrom sync --status
```

## Modelo de memoria

Kerebrom separa cuatro conceptos:

| Objeto | Propósito |
|---|---|
| Project | Frontera normalizada de workspace o dominio |
| Session | Ciclo de vida de una conversación o ejecución de agente |
| Prompt | Historia de intención del usuario |
| Observation | Memoria durable canónica, interpretada por un agente IA |

Las observaciones no deben ser transcripciones crudas. Deben ser concisas, interpretadas y útiles para trabajo futuro — destiladas con el marco **What / Why / Where / Learned**.

## Arquitectura

```text
versions/v2/
  cmd/kerebrom/             entrada CLI
  internal/cli/             comandos, hooks, TUI
  internal/setup/           configuración de clientes IA + limpieza v1
  internal/store/sqlite/    almacén SQLite + FTS5 (esquema sin cambios)
  internal/transport/mcp/   servidor MCP con los 7 tools semánticos
  internal/transport/http/  API HTTP local
  internal/sync/            chunks de sync comprimidos
  internal/updater/         auto-actualización vía GitHub Releases
  docs/                     ADRs, arquitectura, migración, checklist de release
```

Los datos de runtime viven en `~/.kerebrom/`. El repositorio no contiene memoria del usuario, respaldos, configuración específica de máquina ni material de migración.

## Compilar y probar

```bash
cd versions/v2
make test
make build
./bin/kerebrom version
```

## Procedencia

Kerebrom v2 es la evolución limpia de la línea v1. Preserva la dirección del producto, la identidad pública del repositorio y el esquema SQLite, mientras reemplaza la superficie técnica `mem_*` por nombres de verbo semánticos que el modelo invoca automáticamente. v1 permanece en `versions/v1/` para usuarios que no quieran migrar aún. Ver [docs/BRANCHES.md](docs/BRANCHES.md) para el mapa del repositorio y [versions/v2/docs/migration-v1-to-v2.md](versions/v2/docs/migration-v1-to-v2.md) para la guía de actualización.

## Licencia

Software propietario con código disponible. Ver [LICENSE](LICENSE).
