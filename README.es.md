# Kerebrom

[English](README.md) · [Release v1.1.0](https://github.com/ulianbass/kerebrom/releases/tag/v1.1.0) · [Historial del repositorio](docs/BRANCHES.md)

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
| `v1` | rama actual | Línea estable de release v1 |

Kerebrom v1 vive en `versions/v1/` para que las versiones futuras puedan evolucionar sin reescribir historial.

## Qué Incluye v1

- Binario único en Go, sin stack de servicios que mantener.
- Memoria local compartida entre agentes de IA soportados.
- SQLite + FTS5 para recuperación local rápida.
- Servidor MCP con 16 herramientas `mem_*`, alias natural `recall`, anotaciones seguras de lectura/escritura y un perfil de agente que expone por defecto solo las herramientas que los agentes necesitan.
- Protocolo MCP como prompt/resource para Claude Desktop y otros clientes solo-MCP.
- Transporte MCP por Streamable HTTP para conectores remotos como Claude Chat/Cowork y ChatGPT cuando el cliente no puede ejecutar MCP local por `stdio`.
- CLI, HTTP API, dashboard terminal, export/import y sync por chunks comprimidos.
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
kerebrom setup auto
```

`setup auto` configura solo clientes con configuración local existente, y usa Claude Desktop como fallback si no detecta ningún cliente. Para forzar un cliente:

```bash
make install-user SETUP_AGENT=cursor
make install-user SETUP_AGENT=claude-desktop
make install-user SETUP_AGENT=all
```

Las instalaciones desde fuente compilan el checkout que el usuario clonó. Un clone fresco de la rama default `v1` instala la línea de release actual; un clone viejo debe actualizarse antes de correr el instalador.

## Comandos Diarios

```bash
kerebrom version
kerebrom setup auto
kerebrom setup all
kerebrom stats
kerebrom context --project my-project "what matters here?"
kerebrom search "release decision"
KEREBROM_REMOTE_TOKEN="change-me" kerebrom mcp-http --addr 127.0.0.1:7437 --path /mcp
kerebrom tui
kerebrom export --output memory-export.json
kerebrom sync --status
```

En integraciones con agentes, la mayoría de usuarios no debería llamar herramientas de memoria manualmente. Kerebrom registra herramientas MCP, un protocolo de memoria como prompt/resource e instrucciones de cliente para que el agente pueda guardar prompts, recuperar contexto y persistir aprendizajes destilados durante el trabajo normal. Los clientes con hooks como Claude Code pueden ejecutarlo por turno; clientes solo-MCP como Claude Desktop reciben el comportamiento más fuerte que su superficie MCP expone. Clientes cloud como Claude Chat/Cowork y ChatGPT no pueden ejecutar el binario local directamente: deben conectarse a un endpoint remoto de Kerebrom por Streamable HTTP expuesto por el usuario o por una capa cloud.

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
