# Kerebrom v2

[README raíz](../../README.es.md) · [Guía para agentes IA](../../docs/AI_AGENT_INSTALL.es.md) · [Spec de producto](docs/product-spec-v2.md) · [Arquitectura](docs/architecture-v2.md)

> Línea estable actual de Kerebrom: siete tools semánticos de memoria, almacenamiento local SQLite + FTS5, gobierno de contexto, trust ledger auditable, setup plug-and-play para clientes IA y auto-actualización desde GitHub Releases.

## Instalación

```bash
git clone https://github.com/ulianbass/kerebrom.git
cd kerebrom/versions/v2
make install-user
```

El target de instalación compila el binario, lo instala en `~/local/bin/kerebrom`, lo enlaza desde `~/.local/bin/kerebrom` y ejecuta `kerebrom setup auto`.

Forzar un target cuando haga falta:

```bash
make install-user SETUP_AGENT=claude-desktop
make install-user SETUP_AGENT=codex
make install-user SETUP_AGENT=all
```

Targets soportados:

```text
auto, all, claude, claude-code, claude-desktop, codex, cursor,
gemini-cli, opencode, windsurf, vscode
```

Reinicia los clientes IA abiertos después de instalar.

## Instalar A Través De Un Agente IA

Si un usuario pide a Claude, Codex u otro agente de código instalar este repo, el agente debe leer:

- [../../AGENTS.md](../../AGENTS.md)
- [../../CLAUDE.md](../../CLAUDE.md)
- [../../docs/AI_AGENT_INSTALL.es.md](../../docs/AI_AGENT_INSTALL.es.md)

La instrucción esperada para el agente es:

```text
Usa versions/v2, ejecuta make install-user, verifica kerebrom version y stats,
reporta clientes configurados y no habilites memoria HTTP remota salvo que se pida.
```

## Actualizar

```bash
kerebrom update --check
kerebrom update
```

`kerebrom update` descarga el tarball de fuentes del último release de GitHub, ejecuta `make install-user` desde `versions/v2` y conserva intacta la data SQLite existente.

## Ciclo De Memoria

| Momento | Tool |
|---|---|
| Cada mensaje del usuario | `context` |
| Buscar un tema | `recall` |
| Guardar aprendizaje durable | `remember` |
| Cerrar trabajo sustancial o cierre explícito | `summary` |
| Inspeccionar cronología | `timeline` |
| Invalidar memoria incorrecta | `forget` |
| Consolidación admin de proyectos y alias | `projects` |

La activación ocurre en cada mensaje del usuario cuando las tools de Kerebrom están disponibles. Las observaciones son memorias interpretadas, no transcripciones crudas. Usa `What / Why / Where / Learned` solo cuando haya un hecho durable que preservar. La consolidación de proyectos guarda alias persistentes para que los nombres viejos sigan resolviendo al proyecto canónico. La recuperación usa `valid_at` como cronología semántica, por lo que las correcciones nuevas y los hechos revalidados tienen prioridad sobre memorias obsoletas sin borrar el historial.

Cada payload de `context`/`recall` incluye `context_governor`, que le indica al agente cómo priorizar coincidencias, recencia, conflictos y cronología antes de responder. Cada observación también tiene eventos de trust ledger para auditar cambios de ciclo de vida sin guardar transcripciones crudas.

## Arquitectura

```text
cmd/kerebrom/             entrada CLI
internal/cli/             comandos, hooks, TUI
internal/contextgov/      contrato de gobierno de contexto
internal/setup/           setup local de clientes IA y limpieza v1
internal/store/sqlite/    almacén SQLite + FTS5
internal/transport/mcp/   servidor MCP semántico
internal/transport/http/  API HTTP local
internal/sync/            chunks de sync comprimidos
internal/updater/         auto-actualización
docs/                     specs, ADRs, migración, checklist de release
```

Los datos de runtime viven en `~/.kerebrom/`.

## Compilar Y Probar

```bash
make test
make build
./bin/kerebrom version
./bin/kerebrom doctor --deep
```

## Seguridad

Kerebrom es local-first por defecto. No expongas `mcp-http` ni `serve` fuera de loopback, y no parchees memoria cloud de Claude Chat, salvo que el usuario elija explícitamente una ruta soportada y documentada. La memoria nativa de Claude Cowork se siembra solo por storage local estable de la app de escritorio cuando existe. Para la memoria cloud nativa de Claude Chat, usa el prompt manual en [../../docs/AI_AGENT_INSTALL.es.md](../../docs/AI_AGENT_INSTALL.es.md#semilla-opcional-para-memoria-nativa-de-claude-chat).
