# Kerebrom v2

> Memoria persistente local para agentes de IA — un solo cerebro durable compartido entre Claude, Codex, Cursor, Gemini CLI, OpenCode, Windsurf, VS Code y cualquier cliente compatible con MCP.

Kerebrom v2 es una reescritura clean-room que reemplaza la superficie `mem_*` de v1 con siete verbos semánticos que el modelo entiende por intuición: **context**, **recall**, **remember**, **summary**, **forget**, **timeline** y **projects**. Ya no tienes que pedir "usa Kerebrom" — el agente llama a la memoria por iniciativa propia al ver el nombre correcto.

## Instalación

```bash
git clone https://github.com/ulianbass/kerebrom.git
cd kerebrom/versions/v2
make install-user
```

Ese único comando:

1. Compila el binario `kerebrom` con metadatos de versión embebidos.
2. Lo instala en `~/local/bin/kerebrom` y lo enlaza desde `~/.local/bin/kerebrom`.
3. Ejecuta `kerebrom setup auto` para configurar cada cliente IA detectado (Claude Desktop, Claude Code, Codex, Cursor, Gemini CLI, OpenCode, Windsurf, VS Code).

Reinicia cualquier cliente IA abierto para que recoja el nuevo servidor MCP. A partir de ahí, la memoria es automática.

## Actualizar

```bash
kerebrom update
```

Descarga el último release de GitHub, extrae el tarball de fuentes y ejecuta `make install-user` contra `versions/v2`. Usa `--check` para verificar sin instalar o `--yes` para saltar la confirmación.

## Arquitectura en una pantalla

```text
versions/v2/
  cmd/kerebrom/             entrada CLI
  internal/cli/             comandos, hooks, TUI
  internal/setup/           configuración de clientes IA + limpieza de entradas v1
  internal/store/sqlite/    almacén SQLite + FTS5 (mismo esquema que v1)
  internal/transport/mcp/   servidor MCP: 7 tools semánticos, sin alias
  internal/transport/http/  API HTTP local
  internal/sync/            chunks de sincronización comprimidos
  internal/updater/         auto-actualización vía GitHub Releases
  docs/                     ADRs, arquitectura, migración, checklist de release
```

La DB vive en `~/.kerebrom/kerebrom.db`. v2 lee y escribe el mismo esquema que v1, así que actualizar no toca tus datos.

## El ciclo

Los seis tools cotidianos y un tool admin explícito componen el ritmo de memoria:

| Cuándo | Tool |
|---|---|
| Inicio de cualquier conversación no trivial | `context` |
| Necesitas buscar un tema | `recall` |
| Aparece un hecho durable, decisión o aprendizaje | `remember` |
| Cerrando trabajo sustancial | `summary` |
| Inspeccionar historia | `timeline` |
| El usuario dice que algo está mal | `forget` |
| Consolidar variantes de nombres de proyecto cuando el usuario lo pide explícitamente | `projects` (perfil admin) |

Detalles en [docs/architecture-v2.md](docs/architecture-v2.md). Notas de migración en [docs/migration-v1-to-v2.md](docs/migration-v1-to-v2.md).

## Compilar y probar

```bash
cd versions/v2
make test
make build
./bin/kerebrom version
```

## Licencia

Software propietario con código disponible. Ver [LICENSE](../../LICENSE).
