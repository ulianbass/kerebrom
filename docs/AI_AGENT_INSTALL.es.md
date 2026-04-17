# Guía De Instalación Para Agentes IA

Audiencia: Claude, Codex, Cursor, Copilot, Gemini o cualquier agente de código al que un usuario le pida instalar Kerebrom desde este repositorio.

Objetivo: instalar Kerebrom como producto de usuario final, no como sandbox de desarrollo.

## Principios

- Usa la línea estable actual: `versions/v2`.
- Prefiere la ruta plug-and-play: `make install-user`.
- Mantén Kerebrom local-first. No inicies ni expongas `mcp-http` salvo que el usuario pida explícitamente soporte para conectores remotos.
- No importes backups legacy, no borres memoria, no resetees bases de datos y no modifiques configuración ajena de clientes IA.
- Después de instalar o actualizar, dile al usuario que reinicie por completo cualquier cliente IA abierto.
- Si un comando falla porque falta Go o Make, explica la dependencia faltante y detente con próximos pasos claros.

## Targets Soportados

`kerebrom setup` acepta:

```text
auto
all
claude
claude-code
claude-desktop
codex
cursor
gemini-cli
opencode
windsurf
vscode
```

Usa `auto` por defecto. Usa un target específico solo cuando el usuario lo pida o cuando estés reparando un cliente conocido.

## Instalación Limpia

Ejecuta:

```bash
git clone https://github.com/ulianbass/kerebrom.git
cd kerebrom/versions/v2
make install-user
```

Efectos esperados:

- Compila `bin/kerebrom`.
- Instala el ejecutable en `~/local/bin/kerebrom`.
- Crea o refresca el symlink `~/.local/bin/kerebrom`.
- Ejecuta `kerebrom setup auto` con la ruta del binario instalado.
- Escribe solo bloques marcados de Kerebrom o entradas MCP en archivos de configuración de clientes soportados.

Si el repositorio ya está clonado, no lo reclones encima. Usa el checkout existente:

```bash
cd path/to/kerebrom/versions/v2
git pull --ff-only
make install-user
```

Si el usuario quiere configurar todos los clientes soportados:

```bash
make install-user SETUP_AGENT=all
```

## Actualizar Una Instalación Existente

Si `kerebrom` ya está instalado:

```bash
kerebrom update --check
kerebrom update
```

Usa `kerebrom update --yes` solo si el usuario pidió una actualización no interactiva.

Si `kerebrom update` no está disponible porque el binario instalado es muy viejo, usa la ruta de instalación limpia desde `versions/v2`.

## Verificar

Ejecuta:

```bash
kerebrom version
kerebrom stats
```

Después revisa la salida de setup del comando de instalación. Debe listar archivos configurados para los clientes detectados.

Chequeo final recomendado para el usuario:

1. Cerrar por completo y reabrir Claude Desktop, Codex, Cursor o el cliente IA relevante.
2. Abrir un chat nuevo.
3. Preguntar: `What do you know about my projects from Kerebrom?`
4. El agente debería llamar `context` o `recall` antes de responder, cuando el cliente expone tools MCP.

## Notas Por Cliente

| Cliente | Qué instala Kerebrom |
|---|---|
| Claude Code | Config MCP, hooks de ciclo de vida, tools cotidianos auto-aprobados, bloque de protocolo en instrucciones globales de Claude, scripts de hook. |
| Claude Desktop Chat | Entrada MCP local. La memoria de cuenta de Claude Chat vive en cloud, así que Kerebrom no parchea APIs privadas ni bases internas del navegador. |
| Claude Cowork | MCP local y semilla nativa en `memory/CLAUDE.md` cuando existe storage local de Cowork. |
| Codex | Config MCP, auto-aprobación para tools cotidianos, bloque de protocolo en `AGENTS.md` global. |
| Cursor | Config MCP y regla de memoria Kerebrom. |
| Gemini CLI | Config MCP, prompt de sistema y flag de entorno para instrucciones de sistema. |
| OpenCode | Config MCP y archivo de protocolo de memoria Kerebrom. |
| Windsurf | Config MCP y reglas globales. |
| VS Code | Config MCP e instrucciones de prompt donde esté soportado. |

## Qué Decirle Al Usuario

Tras una instalación exitosa, reporta:

- Versión instalada de Kerebrom.
- Qué target de setup se ejecutó (`auto`, `all` o un cliente específico).
- Qué clientes quedaron configurados, en lenguaje simple.
- Que debe reiniciar por completo las apps IA abiertas.
- Que la memoria de runtime vive localmente en `~/.kerebrom/`.

No pegues contenido privado de archivos de configuración salvo que el usuario pida debugging.

## Troubleshooting

| Síntoma | Causa probable | Acción |
|---|---|---|
| `make: command not found` | Make no está instalado. | Pide al usuario instalar las herramientas de compilación de su OS y reintentar. |
| `go: command not found` | Go no está instalado. | Pide al usuario instalar Go y reintentar. |
| El cliente IA muestra Kerebrom pero no lo usa | El cliente cacheó una sesión MCP vieja. | Cerrar por completo y reabrir el cliente. |
| Claude Chat no retiene la pista nativa de memoria | La memoria de cuenta de Claude Chat vive en cloud. | Añadir la regla de autoridad Kerebrom manualmente por la UI soportada de Claude. |
| El usuario quiere soporte remoto para ChatGPT/Claude web | Los clientes cloud no pueden lanzar MCP stdio local. | Explica el tradeoff de privacidad antes de usar `mcp-http`; exige consentimiento explícito. |

## Límite De Privacidad

Nunca expongas Kerebrom por red por defecto. Este comando es avanzado y solo debe usarse después de que el usuario acepte explícitamente el riesgo:

```bash
KEREBROM_REMOTE_TOKEN="change-me" kerebrom mcp-http --addr 127.0.0.1:7437 --path /mcp
```

Direcciones fuera de loopback requieren token de auth o un override inseguro explícito. No sugieras modo público sin autenticación para usuarios normales.
