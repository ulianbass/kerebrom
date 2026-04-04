# Kerebrom

[Read in English](README.md)

Kerebrom es un motor de memoria persistente local para herramientas de AI. Le da a Claude Code, Codex, Cursor y Claude Desktop una memoria compartida a largo plazo que sobrevive entre conversaciones — sin servicios en la nube, sin bases de datos externas, solo un archivo SQLite en tu maquina.

## Caracteristicas

- **SQLite en un solo archivo** con WAL para acceso concurrente.
- **Busqueda hibrida** combinando palabras clave (FTS5), similitud semantica, recencia, frecuencia de acceso y grafo de entidades.
- **Grafo de entidades** con tripletas de hechos, deteccion de contradicciones e invalidacion automatica.
- **Divulgacion progresiva** — contexto compacto, resumen o detalle completo.
- **Privacidad primero** — limpieza de valores sensibles antes de guardar; modo encriptado opcional.
- **Mantenimiento automatico** — decaimiento de memorias, consolidacion episodica-a-semantica y limpieza programada.
- **Servidor MCP** via stdio — funciona con cualquier herramienta que hable Model Context Protocol.
- **Auto-setup** — un comando detecta y configura Claude Code, Codex, Claude Desktop y Cursor.
- **Backups portables** — archivos `.kbk` para recuperacion y migracion.

## Instalacion

```bash
pip install .
```

## Inicio rapido

```bash
# Auto-configurar todas las herramientas AI detectadas
python3 -m kerebrom setup --db ~/.kerebrom/kerebrom.db

# Guardar memorias
python3 -m kerebrom remember --db ~/.kerebrom/kerebrom.db "La API usa tokens JWT con expiracion de 24h."
python3 -m kerebrom remember --db ~/.kerebrom/kerebrom.db "El usuario prefiere modo oscuro y espanol."

# Buscar memorias
python3 -m kerebrom recall --db ~/.kerebrom/kerebrom.db "autenticacion"

# Ver grafo de entidades
python3 -m kerebrom facts --db ~/.kerebrom/kerebrom.db

# Obtener contexto estructurado para un prompt
python3 -m kerebrom context --db ~/.kerebrom/kerebrom.db "arquitectura del proyecto" --layer 2

# Iniciar servidor MCP
python3 -m kerebrom serve --db ~/.kerebrom/kerebrom.db
```

## Comandos CLI

| Comando | Descripcion |
|---------|-------------|
| `setup` | Auto-detectar herramientas AI y configurar MCP + hooks |
| `remember` | Guardar una nueva memoria |
| `recall` | Buscar memorias por query |
| `forget` | Invalidar una memoria por ID o limpiar datos sensibles |
| `context` | Obtener un paquete de contexto estructurado (hechos + memorias) |
| `entities` | Listar entidades conocidas en el grafo |
| `facts` | Listar tripletas semanticas activas |
| `consolidate` | Fusionar memorias episodicas relacionadas en semanticas |
| `decay` | Aplicar decaimiento de importancia a memorias viejas |
| `backup` | Crear copia de la base de datos SQLite |
| `snapshot` | Crear archivo de backup portable `.kbk` |
| `revive` | Restaurar memorias desde un archivo `.kbk` |
| `export` | Exportar memorias como JSON |
| `serve` | Iniciar el servidor MCP via stdio |
| `sopor` | Consolidar transcripts de sesiones en memorias |

## Como funciona el setup

`kerebrom setup` auto-detecta las herramientas AI instaladas y configura cada una:

- **Claude Code** — registra servidor MCP en `.mcp.json`, agrega instrucciones a `CLAUDE.md`, desactiva la memoria basada en archivos, instala hooks de captura pasiva.
- **Claude Desktop** — configura MCP en `claude_desktop_config.json` (Chat + Cowork).
- **Codex** — registra MCP en `config.toml`, agrega instrucciones a `AGENTS.md`.
- **Cursor** — registra MCP en `mcp.json`.
- **LaunchAgent** (macOS) — instala un agente periodico que auto-repara configs si se eliminan.

## Arquitectura

```
~/.kerebrom/
  kerebrom.db        # Base de datos SQLite (memorias, entidades, hechos, embeddings)
  reports/           # Reportes de mantenimiento versionados
  backups/           # Archivos de backup .kbk versionados
  Kerebrom           # Script wrapper para LaunchAgent
```

La base de datos es la unica fuente de verdad. Todas las herramientas AI se conectan al mismo servidor MCP y comparten el mismo pool de memorias.

## Modo encriptado

```bash
export KEREBROM_PASSPHRASE="tu-contrasena"
python3 -m kerebrom init --db ~/.kerebrom/secure.kdb --passphrase-env KEREBROM_PASSPHRASE
python3 -m kerebrom setup --db ~/.kerebrom/secure.kdb --passphrase-env KEREBROM_PASSPHRASE
```

Envuelve SQLite dentro de un contenedor encriptado usando el binario `openssl` del sistema. Compatible con todos los comandos CLI y el servidor MCP.

## Testing

```bash
# Tests unitarios
python3 -m pytest tests/test_kerebrom.py

# Smoke test en maquina local
python3 scripts/local_release_smoke.py

# Release gate completo (tests + smoke + instalacion venv + benchmarks)
python3 scripts/release_gate.py
```

## Limites conocidos

- La busqueda usa embeddings hash deterministicos, no embeddings neuronales aun.
- El modo encriptado usa el binario `openssl` del sistema en vez de SQLCipher.
- El modo encriptado crea un archivo temporal en texto plano mientras un proceso usa activamente la base de datos.

## Licencia

MIT
