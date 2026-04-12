# Kerebrom v1

Este directorio contiene la fuente de la línea `v1` dentro del repositorio Kerebrom.

## Build Y Tests

```bash
make build
make test
go run ./cmd/kerebrom version
go run ./cmd/kerebrom mcp
```

## Instalación De Usuario

```bash
make install-user
```

La instalación de usuario compila Kerebrom, instala el binario en `~/local/bin/kerebrom`, crea el enlace en `~/.local/bin/kerebrom` y ejecuta `kerebrom setup all`. En macOS configura Claude Code bajo `~/.claude/` y Claude Desktop bajo `~/Library/Application Support/Claude/claude_desktop_config.json`, preservando servidores MCP existentes.

Claude Code también recibe hooks de lifecycle bajo `~/.kerebrom/hooks/claude-code/` para inicio de sesión, ingesta de prompt, recuperación post-compaction, captura pasiva de subagentes y cierre de sesión. Otros clientes de IA reciben la integración local más fuerte que exponen: MCP más un protocolo obligatorio de memoria como prompt/resource cuando no hay API de hooks. Claude Desktop está en esta categoría solo-MCP: Kerebrom puede exponer herramientas, prompts y resources ahí, pero no puede instalar los hooks por turno de Claude Code dentro de Claude Desktop.

Kerebrom guarda dos capas distintas: `mem_save_prompt` conserva historial de intención del usuario, mientras `mem_save` guarda observaciones destiladas por el agente usando `What / Why / Where / Learned`. Las memorias canónicas deben ser interpretaciones, no copias crudas de transcripts.

## Contrato Runtime

- Binario único en Go.
- Memoria local compartida entre agentes soportados.
- Store SQLite + FTS5.
- Superficies HTTP + MCP + CLI.
- 15 herramientas MCP `mem_*`.
- Protocolo MCP de memoria como prompt/resource para Claude Desktop y otros clientes solo-MCP.
- Comandos de recuperación como `context`, `search`, `timeline` y `tui`.
- Hook runner para clientes de IA con soporte de hooks.
- Export/import y sync por chunks comprimidos bajo `.kerebrom/`.
- Setup idempotente para Codex, Claude Code, Claude Desktop, Gemini CLI, OpenCode, Cursor, Windsurf y VS Code.

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
