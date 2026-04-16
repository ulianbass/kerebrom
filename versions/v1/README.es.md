# Kerebrom v1

Este directorio contiene la fuente de la línea `v1` dentro del repositorio Kerebrom.

## Build Y Tests

```bash
make build
make test
go run ./cmd/kerebrom version
go run ./cmd/kerebrom mcp
go run ./cmd/kerebrom mcp-http --addr 127.0.0.1:7437 --path /mcp
```

## Instalación De Usuario

```bash
make install-user
```

La instalación de usuario compila Kerebrom desde el checkout actual, instala el binario en `~/local/bin/kerebrom`, crea el enlace en `~/.local/bin/kerebrom` y ejecuta `kerebrom setup auto`. `auto` configura clientes con configuración local existente y usa Claude Desktop como fallback cuando no detecta ningún cliente. Usa `make install-user SETUP_AGENT=all` para configurar todos los clientes soportados, o `make install-user SETUP_AGENT=cursor` / `claude-desktop` / `codex` para forzar uno.

Claude Code también recibe hooks de lifecycle bajo `~/.kerebrom/hooks/claude-code/` para inicio de sesión, ingesta de prompt, recuperación post-compaction, captura pasiva de subagentes y cierre de sesión. Otros clientes de IA reciben la integración local más fuerte que exponen: MCP más un protocolo obligatorio de memoria como prompt/resource cuando no hay API de hooks. Claude Desktop está en esta categoría solo-MCP: Kerebrom puede exponer herramientas, prompts y resources ahí, pero no puede instalar los hooks por turno de Claude Code dentro de Claude Desktop.

Kerebrom guarda dos capas distintas: `mem_save_prompt` conserva historial de intención del usuario, mientras `mem_save` guarda observaciones destiladas por el agente usando `What / Why / Where / Learned`. Las memorias canónicas deben ser interpretaciones, no copias crudas de transcripts.

## Contrato Runtime

- Binario único en Go.
- Memoria local compartida entre agentes soportados.
- Store SQLite + FTS5.
- Superficies HTTP + MCP + CLI.
- 16 herramientas MCP `mem_*` totales más alias natural `recall`, con `--tools=agent` usado por setup para reducir el contexto de agentes.
- Protocolo MCP de memoria como prompt/resource para Claude Desktop y otros clientes solo-MCP.
- Transporte MCP remoto por Streamable HTTP para clientes cloud como Claude Chat/Cowork y ChatGPT.
- Comandos de recuperación como `context`, `search`, `timeline` y el dashboard terminal.
- Hook runner para clientes de IA con soporte de hooks.
- Export/import y sync por chunks comprimidos bajo `.kerebrom/`.
- Setup idempotente para Codex, Claude Code, Claude Desktop, Gemini CLI, OpenCode, Cursor, Windsurf y VS Code.
- Instalador selectivo por defecto con `setup auto`; `setup all` sigue disponible explícitamente.

## Layout

```text
cmd/kerebrom/           Entrada CLI
internal/               Código de producto
docs/                   Documentos de arquitectura y release v1
scripts/                Scripts de instalación local y operación
test/                   Suites de contrato y e2e
```

## Documentos Canónicos v1

- [Product Spec](docs/product-spec-v1.md)
- [Parity Matrix](docs/parity-matrix-engram.md)
- [Release Checklist](docs/release-checklist-v1.md)
