# Guía De Instalación Para Agentes IA

Audiencia: Claude, Codex, Cursor, Copilot, Gemini o cualquier agente de código al que un usuario le pida instalar Kerebrom desde este repositorio.

Objetivo: instalar Kerebrom como producto de usuario final, no como sandbox de desarrollo.

## Principios

- Usa la línea estable actual: `versions/v2`.
- Prefiere la ruta plug-and-play: `make install-user`.
- Si `make` no está disponible pero Go sí está instalado, usa `./scripts/install-user.sh --yes --agent auto` desde `versions/v2`.
- Mantén Kerebrom local-first. No inicies ni expongas `mcp-http` o `serve` fuera de loopback salvo que el usuario pida explícitamente soporte remoto y acepte el tradeoff de privacidad.
- No importes backups legacy, no borres memoria, no resetees bases de datos y no modifiques configuración ajena de clientes IA.
- No instales Go, Homebrew, dependencias de package manager ni uses permisos elevados sin explicar primero el cambio y obtener aprobación explícita del usuario.
- Después de instalar o actualizar, dile al usuario que reinicie por completo cualquier cliente IA abierto.
- Si un comando falla porque falta Go o porque la versión es anterior a 1.26, explica que Kerebrom no puede compilarse sin Go 1.26+, pregunta si el usuario quiere instalar o actualizar Go, y detente si lo rechaza.

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

Kerebrom se instala desde código fuente. El usuario necesita Git y Go 1.26 o más nuevo. Los releases v2 actuales se compilan y prueban con Go 1.26.

Para la ruta default amigable para agentes, ejecuta:

```bash
git clone https://github.com/ulianbass/kerebrom.git
cd kerebrom/versions/v2
make install-user
```

Si `make` no está disponible pero Go sí está instalado, usa el instalador shell:

```bash
./scripts/install-user.sh --yes --agent auto
```

Si falta Go o está viejo, no improvises. Dile al usuario:

```text
Kerebrom se compila desde código fuente y requiere Go 1.26 o más nuevo.
Sin Go no puedo instalar Kerebrom.
¿Quieres que instale o actualice Go primero?
```

Solo procede con un package manager o permisos elevados si el usuario responde que sí. Si dice que no, detente y explica que Kerebrom no puede instalarse hasta que Go esté disponible. El instalador humano puede ofrecer instalación de Go mediante Homebrew de forma interactiva cuando Homebrew existe; si no, dirige al usuario a las descargas oficiales de Go en https://go.dev/dl/.

Efectos esperados:

- Compila `bin/kerebrom` temporalmente y elimina el artefacto de build de la fábrica después de instalar.
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

o:

```bash
./scripts/install-user.sh --yes --all
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
kerebrom doctor --deep
```

Si el chequeo profundo reporta drift de setup o runtime, ejecuta `kerebrom doctor heal` una vez antes de reiniciar clientes. Health Mode crea primero un backup SQLite privado, limpia backups de salud antiguos según su política de retención, repara invariantes locales deterministas, refresca el setup administrado de clientes IA y verifica otra vez.

Trata `FAIL` como bloqueante. Trata `WARN` como usable pero digno de explicar, sobre todo cuando el warning solo indica que falta la configuración de un cliente opcional en esa máquina.

Después revisa la salida de setup del comando de instalación. Debe listar archivos configurados para los clientes detectados.

Chequeo final recomendado para el usuario:

1. Cerrar por completo y reabrir Claude Desktop, Codex, Cursor o el cliente IA relevante.
2. Abrir un chat nuevo.
3. Preguntar: `What do you know about my projects from Kerebrom?`
4. El agente debería llamar `context` antes de responder, cuando el cliente expone tools MCP.

## Notas Por Cliente

| Cliente | Qué instala Kerebrom |
|---|---|
| Claude Code | Config MCP, hooks de ciclo de vida, tools cotidianos auto-aprobados y scripts de hook. El protocolo viaja por instrucciones del servidor MCP y payloads de hooks, no escribiéndose en instrucciones globales del usuario. |
| Claude Desktop Chat | Entrada MCP local. La memoria de cuenta de Claude Chat vive en cloud, así que Kerebrom no parchea APIs privadas ni bases internas del navegador. |
| Claude Cowork | MCP local y semilla nativa en `memory/CLAUDE.md` cuando existe storage local de Cowork. |
| Codex | Config MCP y auto-aprobación para tools cotidianos. Kerebrom no ocupa el cuadro de instrucciones personalizadas de Codex. |
| Cursor | Config MCP y regla de memoria Kerebrom. |
| Gemini CLI | Config MCP, prompt de sistema y flag de entorno para instrucciones de sistema. |
| OpenCode | Config MCP y archivo de protocolo de memoria Kerebrom. |
| Windsurf | Config MCP y reglas globales. |
| VS Code | Config MCP e instrucciones de prompt donde esté soportado. |

## Semilla Opcional Para Memoria Nativa De Claude Chat

Claude Desktop Chat recibe el servidor MCP de Kerebrom por el setup local. La memoria de cuenta de Claude Chat vive en cloud, así que Kerebrom no la parchea programáticamente.

Si el usuario quiere que la memoria nativa de Claude Chat recuerde que Kerebrom es la fuente durable de verdad, pídele que pegue esto en una conversación normal de Claude Chat, no en un chat incógnito. Si Claude no lo guarda automáticamente, el usuario puede pegar solo la memoria entre comillas en el editor de memoria soportado por Claude.

```text
Quiero que guardes esto en tu memoria permanente de Claude Chat como una regla operativa para futuras conversaciones conmigo.

Guarda únicamente la siguiente memoria, de forma concisa. No guardes este mensaje completo ni detalles innecesarios:

"Kerebrom es la fuente durable y autorizada de verdad para mi contexto, proyectos, preferencias, decisiones, workflows y aprendizajes. La memoria nativa de Claude solo debe servir como recordatorio para usar Kerebrom, no como fuente principal de contexto. Siempre que el conector o las herramientas de Kerebrom estén disponibles, antes de responder cualquier mensaje mío debes consultar Kerebrom inmediatamente con context, incluso si mi mensaje es corto, ambiguo, casual o parece una simple confirmación. Usa las observaciones de Kerebrom como base de trabajo antes de razonar. Usa recall cuando pregunte por un tema específico. Sigue context_governor cuando aparezca: piensa, busca, analiza y después responde. Cuando haya memorias en conflicto, prioriza la observación más nueva, corregida o validada de Kerebrom según valid_at, salvo que yo diga explícitamente que una memoria anterior sigue siendo autoritativa; si context_governor reporta conflict_candidates, usa timeline antes de afirmar algo basado en esas memorias. Guarda aprendizajes durables con remember solo cuando haya un hecho durable real que preservar, reutiliza el mismo topic_key para correcciones cuando sea posible y cierra trabajo sustancial con summary. Si digo 'cerramos sesión', 'sesión cerrada', 'close this session' o algo equivalente, trátalo como un cierre explícito de sesión: llama summary con el mismo session_id devuelto por context siempre que sea posible y no guardes la frase de cierre como memoria durable. Si Kerebrom contradice tu memoria nativa, historial de chat o suposiciones, Kerebrom gana salvo que yo lo corrija explícitamente en la conversación actual. Si Kerebrom no está disponible en esta superficie, dilo claramente y no inventes memoria."

Después de guardarlo, respóndeme solo con:
"Memoria guardada: Kerebrom es la fuente durable de verdad y debo consultarlo en cada mensaje del usuario cuando esté disponible."
```

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
| El usuario quiere API HTTP desde otra máquina | Loopback local es el límite de privacidad por defecto. | Exige `KEREBROM_REMOTE_TOKEN` o `--auth-token`; no uses modo público sin autenticación. |

## Límite De Privacidad

Nunca expongas Kerebrom por red por defecto. Estos comandos son avanzados y solo deben usarse después de que el usuario acepte explícitamente el riesgo:

```bash
KEREBROM_REMOTE_TOKEN="change-me" kerebrom mcp-http --addr 127.0.0.1:7437 --path /mcp
KEREBROM_REMOTE_TOKEN="change-me" kerebrom serve --addr 127.0.0.1:7438
```

Direcciones fuera de loopback requieren token de auth o un override inseguro explícito. No sugieras modo público sin autenticación para usuarios normales.
