# Kerebrom

[English](README.md) · [Release v1.0.0](https://github.com/ulianbass/kerebrom/releases/tag/v1.0.0) · [Historial del repositorio](docs/BRANCHES.md)

> Memoria persistente local para agentes de IA.
> Una capa de memoria durable para Claude, Codex, Cursor, Gemini CLI, OpenCode, Windsurf, VS Code y otros clientes compatibles con MCP.

Kerebrom permite que los agentes de código compartan memoria local sin enviar el contexto del proyecto a un servicio en la nube. Corre como un binario único en Go, guarda datos en SQLite con FTS5, expone un servidor MCP e instala el flujo de memoria más fuerte disponible para cada cliente de IA soportado.

![Flujo de memoria de Kerebrom](docs/assets/kerebrom-memory-flow.svg)

## Línea Del Producto

La rama pública activa es `v1`. Las líneas históricas se conservan como tags, ordenadas de más antigua a más nueva:

| Línea | Estado | Propósito |
|---|---:|---|
| `history/legacy-main-2026-04-10` | tag | Historial legacy de la rama default antes del reset v1 |
| `history/go-rewrite-2026-04-10` | tag | Experimento previo de reescritura en Go |
| `v1` | rama actual | Release estable v1.0.0 |

Kerebrom v1 vive en `versions/v1/` para que las versiones futuras puedan evolucionar sin reescribir historial.

## Qué Incluye v1

- Binario único en Go, sin stack de servicios que mantener.
- Memoria local compartida entre agentes de IA soportados.
- SQLite + FTS5 para recuperación local rápida.
- Servidor MCP con 15 herramientas `mem_*`.
- CLI, HTTP API, TUI, export/import y sync por chunks comprimidos.
- Hooks de lifecycle para Claude Code: inicio de sesión, ingesta de prompt, captura pasiva, recuperación post-compaction y cierre de sesión.
- Setup por MCP e instrucciones para Codex, Claude Desktop, Cursor, Gemini CLI, OpenCode, Windsurf y VS Code.
- Framework de memoria destilada: los prompts son historial de intención; las observaciones canónicas son memorias interpretadas con `What / Why / Where / Learned`.

## Instalación Desde Fuente

```bash
git clone https://github.com/ulianbass/kerebrom.git
cd kerebrom/versions/v1
make install-user
```

Esto compila `kerebrom`, lo instala en `~/local/bin/kerebrom`, crea el enlace en `~/.local/bin/kerebrom` y ejecuta:

```bash
kerebrom setup all
```

## Comandos Diarios

```bash
kerebrom version
kerebrom setup all
kerebrom stats
kerebrom context --project my-project "what matters here?"
kerebrom search "release decision"
kerebrom tui
kerebrom export --output memory-export.json
```

En integraciones con agentes, la mayoría de usuarios no debería llamar herramientas de memoria manualmente. Kerebrom registra MCP e instrucciones para que el agente pueda guardar prompts, recuperar contexto y persistir aprendizajes destilados durante el trabajo normal.

## Modelo De Memoria

Kerebrom separa cuatro conceptos:

| Objeto | Propósito |
|---|---|
| Proyecto | Límite normalizado del workspace o dominio |
| Sesión | Lifecycle de una conversación o ejecución de agente |
| Prompt | Historial de intención del usuario |
| Observación | Memoria durable canónica, destilada por un agente de IA |

Las observaciones no deben ser transcripts crudos. Deben ser concisas, interpretadas y útiles para trabajo futuro.

## Arquitectura

```text
versions/v1/
  cmd/kerebrom/             Entrada CLI
  internal/cli/             Comandos, hooks, TUI
  internal/setup/           Setup local de clientes de IA
  internal/store/sqlite/    Store SQLite + FTS5
  internal/transport/mcp/   Servidor MCP
  internal/transport/http/  HTTP API local
  internal/sync/            Sync por chunks comprimidos
  docs/                     ADRs, spec, checklist de release
```

Los datos runtime se guardan en el directorio local de datos de Kerebrom del usuario. El repositorio no contiene memorias de usuario, backups, configuración específica de máquina ni material de staging de migración.

## Build Y Tests

```bash
cd versions/v1
make test
make build
./bin/kerebrom version
```

## Procedencia

Kerebrom v1 es la línea limpia del producto. Conserva la dirección del producto y la identidad del repositorio público mientras mantiene experimentos anteriores como tags de historial. Ver [docs/BRANCHES.md](docs/BRANCHES.md).

## Licencia

Software propietario con código disponible para revisión. Ver [LICENSE](LICENSE).
