package setup

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	mcptransport "github.com/ulianbass/kerebrom/internal/transport/mcp"
)

const (
	codexMemoryBlockStart = "<!-- KEREBROM:START -->"
	codexMemoryBlockEnd   = "<!-- KEREBROM:END -->"
)

// kerebromMCPToolPrefix is the namespace Claude Code uses to identify
// MCP tools served by the "Kerebrom" MCP server entry. Permission
// strings are built as <prefix><toolName>.
const kerebromMCPToolPrefix = "mcp__Kerebrom__"

// kerebromLegacyToolPrefix matches the v1 mem_* tool entries that may
// linger in user settings.json after upgrading from v1.x. Setup removes
// these so the surface is clean — only the v2 semantic tools remain.
const kerebromLegacyToolPrefix = kerebromMCPToolPrefix + "mem_"

// kerebromClaudeAutoApproveTools is the Kerebrom v2 MCP surface added to
// Claude Code's permissions.allow so the agent never has to ask for
// confirmation before invoking memory. Derived from the canonical tool
// lists exported by the MCP transport package.
var kerebromClaudeAutoApproveTools = func() []string {
	all := append([]string{}, mcptransport.SemanticAgentTools...)
	all = append(all, mcptransport.SemanticAdminTools...)
	out := make([]string, 0, len(all))
	for _, name := range all {
		out = append(out, kerebromMCPToolPrefix+name)
	}
	return out
}()

type Options struct {
	HomeDir    string
	ProjectDir string
	BinaryPath string
}

type Result struct {
	Agent string
	Files []string
}

func Run(agent string, opts Options) (Result, error) {
	agent = normalizeAgent(agent)
	if agent == "" {
		return Result{}, fmt.Errorf("agent is required")
	}

	opts = fillDefaults(opts)
	if opts.HomeDir == "" {
		return Result{}, fmt.Errorf("home dir is required")
	}
	if opts.BinaryPath == "" {
		return Result{}, fmt.Errorf("binary path is required")
	}

	switch agent {
	case "codex":
		return setupCodex(opts)
	case "claude":
		return setupClaude(opts)
	case "claude-code":
		return setupClaudeCode(opts)
	case "claude-desktop":
		return setupClaudeDesktop(opts)
	case "gemini-cli":
		return setupGeminiCLI(opts)
	case "opencode":
		return setupOpenCode(opts)
	case "cursor":
		return setupCursor(opts)
	case "windsurf":
		return setupWindsurf(opts)
	case "vscode":
		return setupVSCode(opts)
	case "all":
		return setupAll(opts)
	case "auto":
		return setupAuto(opts)
	default:
		return Result{}, fmt.Errorf("unsupported agent %q", agent)
	}
}

func SupportedAgents() []string {
	return []string{"codex", "claude", "claude-code", "claude-desktop", "gemini-cli", "opencode", "cursor", "windsurf", "vscode", "auto", "all"}
}

func fillDefaults(opts Options) Options {
	if opts.HomeDir == "" {
		if homeDir, err := os.UserHomeDir(); err == nil {
			opts.HomeDir = homeDir
		}
	}

	if opts.ProjectDir == "" {
		if cwd, err := os.Getwd(); err == nil {
			opts.ProjectDir = cwd
		}
	}

	return opts
}

func normalizeAgent(agent string) string {
	agent = strings.ToLower(strings.TrimSpace(agent))
	switch agent {
	case "gemini":
		return "gemini-cli"
	case "vs-code", "code":
		return "vscode"
	default:
		return agent
	}
}

func setupAuto(opts Options) (Result, error) {
	agents := detectedAgents(opts)
	if len(agents) == 0 {
		agents = []string{"claude-desktop"}
	}

	files := []string{}
	for _, agent := range agents {
		result, err := Run(agent, opts)
		if err != nil {
			return Result{}, err
		}
		files = append(files, result.Files...)
	}
	return Result{Agent: "auto", Files: files}, nil
}

func detectedAgents(opts Options) []string {
	candidates := []struct {
		agent string
		paths []string
	}{
		{
			agent: "codex",
			paths: []string{
				filepath.Join(opts.HomeDir, ".codex", "config.toml"),
				filepath.Join(opts.HomeDir, ".codex", "AGENTS.md"),
			},
		},
		{
			agent: "claude-code",
			paths: []string{
				filepath.Join(opts.HomeDir, ".claude", "settings.json"),
				filepath.Join(opts.HomeDir, ".claude", "mcp.json"),
				filepath.Join(opts.HomeDir, ".claude", "CLAUDE.md"),
			},
		},
		{
			agent: "claude-desktop",
			paths: []string{
				claudeDesktopConfigPath(opts.HomeDir),
			},
		},
		{
			agent: "gemini-cli",
			paths: []string{
				filepath.Join(opts.HomeDir, ".gemini", "settings.json"),
				filepath.Join(opts.HomeDir, ".gemini", "system.md"),
			},
		},
		{
			agent: "opencode",
			paths: []string{
				filepath.Join(opts.HomeDir, ".config", "opencode", "opencode.json"),
			},
		},
		{
			agent: "cursor",
			paths: []string{
				filepath.Join(opts.HomeDir, ".cursor", "mcp.json"),
				filepath.Join(opts.HomeDir, ".cursor", "rules"),
			},
		},
		{
			agent: "windsurf",
			paths: []string{
				filepath.Join(opts.HomeDir, ".codeium", "windsurf", "mcp_config.json"),
				filepath.Join(opts.HomeDir, ".windsurfrules"),
			},
		},
		{
			agent: "vscode",
			paths: []string{
				filepath.Join(vscodeUserConfigDir(opts.HomeDir), "mcp.json"),
				filepath.Join(vscodeUserConfigDir(opts.HomeDir), "settings.json"),
				filepath.Join(opts.ProjectDir, ".vscode", "mcp.json"),
			},
		},
	}

	agents := []string{}
	for _, candidate := range candidates {
		for _, path := range candidate.paths {
			if pathExists(path) {
				agents = append(agents, candidate.agent)
				break
			}
		}
	}
	return agents
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func setupAll(opts Options) (Result, error) {
	agents := []string{"codex", "claude", "gemini-cli", "opencode", "cursor", "windsurf", "vscode"}
	files := []string{}
	for _, agent := range agents {
		result, err := Run(agent, opts)
		if err != nil {
			return Result{}, err
		}
		files = append(files, result.Files...)
	}
	return Result{Agent: "all", Files: files}, nil
}

func setupCodex(opts Options) (Result, error) {
	configPath := filepath.Join(opts.HomeDir, ".codex", "config.toml")
	agentsPath := filepath.Join(opts.HomeDir, ".codex", "AGENTS.md")

	configContent, err := readTextFile(configPath)
	if err != nil {
		return Result{}, err
	}

	configBlock := codexConfigBlock(opts.BinaryPath)
	configContent = upsertCodexConfig(configContent, configBlock)
	if err := writeTextFile(configPath, configContent); err != nil {
		return Result{}, err
	}

	agentsContent, err := readTextFile(agentsPath)
	if err != nil {
		return Result{}, err
	}

	agentsContent = upsertMarkedBlock(agentsContent, codexMemoryBlockStart, codexMemoryBlockEnd, codexAGENTSBlock())
	if err := writeTextFile(agentsPath, agentsContent); err != nil {
		return Result{}, err
	}

	return Result{
		Agent: "codex",
		Files: []string{configPath, agentsPath},
	}, nil
}

func setupClaude(opts Options) (Result, error) {
	codeResult, err := setupClaudeCode(opts)
	if err != nil {
		return Result{}, err
	}
	desktopResult, err := setupClaudeDesktop(opts)
	if err != nil {
		return Result{}, err
	}
	return Result{
		Agent: "claude",
		Files: append(codeResult.Files, desktopResult.Files...),
	}, nil
}

func setupClaudeCode(opts Options) (Result, error) {
	settingsPath := filepath.Join(opts.HomeDir, ".claude", "settings.json")
	mcpPath := filepath.Join(opts.HomeDir, ".claude", "mcp.json")
	claudeMDPath := filepath.Join(opts.HomeDir, ".claude", "CLAUDE.md")
	hookDir := filepath.Join(opts.HomeDir, ".kerebrom", "hooks", "claude-code")

	settings, err := readJSONMap(settingsPath)
	if err != nil {
		return Result{}, err
	}
	if err := upsertClaudeHooks(settings, hookDir); err != nil {
		return Result{}, err
	}
	if err := upsertClaudePermissionsAllow(settings, kerebromClaudeAutoApproveTools); err != nil {
		return Result{}, err
	}
	if err := writeJSONFile(settingsPath, settings); err != nil {
		return Result{}, err
	}
	hookFiles, err := writeClaudeHookScripts(hookDir, opts.BinaryPath)
	if err != nil {
		return Result{}, err
	}

	mcpConfig, err := readJSONMap(mcpPath)
	if err != nil {
		return Result{}, err
	}

	rawServers, ok := mcpConfig["mcpServers"]
	if !ok || rawServers == nil {
		rawServers = map[string]any{}
	}

	servers, ok := rawServers.(map[string]any)
	if !ok {
		return Result{}, fmt.Errorf("unexpected mcpServers shape in %s", mcpPath)
	}

	servers["Kerebrom"] = map[string]any{
		"command": opts.BinaryPath,
		"args":    mcpAgentArgs(),
	}
	mcpConfig["mcpServers"] = servers

	if err := writeJSONFile(mcpPath, mcpConfig); err != nil {
		return Result{}, err
	}

	if err := upsertTextBlockFile(claudeMDPath, memoryProtocolBlock()); err != nil {
		return Result{}, err
	}

	return Result{
		Agent: "claude-code",
		Files: append([]string{settingsPath, mcpPath, claudeMDPath}, hookFiles...),
	}, nil
}

func setupClaudeDesktop(opts Options) (Result, error) {
	desktopMCPPath := claudeDesktopConfigPath(opts.HomeDir)
	if err := upsertMCPServer(desktopMCPPath, "mcpServers", "Kerebrom", map[string]any{
		"command": opts.BinaryPath,
		"args":    mcpAgentArgs(),
	}); err != nil {
		return Result{}, err
	}

	return Result{
		Agent: "claude-desktop",
		Files: []string{desktopMCPPath},
	}, nil
}

func claudeDesktopConfigPath(homeDir string) string {
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(homeDir, "Library", "Application Support", "Claude", "claude_desktop_config.json")
	case "windows":
		return filepath.Join(homeDir, "AppData", "Roaming", "Claude", "claude_desktop_config.json")
	default:
		return filepath.Join(homeDir, ".config", "Claude", "claude_desktop_config.json")
	}
}

func setupGeminiCLI(opts Options) (Result, error) {
	settingsPath := filepath.Join(opts.HomeDir, ".gemini", "settings.json")
	systemPath := filepath.Join(opts.HomeDir, ".gemini", "system.md")
	envPath := filepath.Join(opts.HomeDir, ".gemini", ".env")

	if err := upsertMCPServer(settingsPath, "mcpServers", "kerebrom", map[string]any{
		"command": opts.BinaryPath,
		"args":    mcpAgentArgs(),
	}); err != nil {
		return Result{}, err
	}
	if err := upsertTextBlockFile(systemPath, memoryProtocolBlock()); err != nil {
		return Result{}, err
	}
	if err := ensureLine(envPath, "GEMINI_SYSTEM_MD=1"); err != nil {
		return Result{}, err
	}

	return Result{
		Agent: "gemini-cli",
		Files: []string{settingsPath, systemPath, envPath},
	}, nil
}

func setupOpenCode(opts Options) (Result, error) {
	configPath := filepath.Join(opts.HomeDir, ".config", "opencode", "opencode.json")
	instructionsPath := filepath.Join(opts.HomeDir, ".config", "opencode", "kerebrom-memory.md")

	if err := upsertMCPServer(configPath, "mcp", "kerebrom", map[string]any{
		"type":    "local",
		"command": mcpAgentCommand(opts.BinaryPath),
		"enabled": true,
	}); err != nil {
		return Result{}, err
	}
	if err := upsertTextBlockFile(instructionsPath, memoryProtocolBlock()); err != nil {
		return Result{}, err
	}
	if err := ensureJSONStringList(configPath, "instructions", instructionsPath); err != nil {
		return Result{}, err
	}

	return Result{
		Agent: "opencode",
		Files: []string{configPath, instructionsPath},
	}, nil
}

func setupCursor(opts Options) (Result, error) {
	mcpPath := filepath.Join(opts.HomeDir, ".cursor", "mcp.json")
	rulePath := filepath.Join(opts.HomeDir, ".cursor", "rules", "kerebrom.mdc")

	if err := upsertMCPServer(mcpPath, "mcpServers", "kerebrom", map[string]any{
		"command": opts.BinaryPath,
		"args":    mcpAgentArgs(),
	}); err != nil {
		return Result{}, err
	}
	if err := writeTextFile(rulePath, cursorRule()); err != nil {
		return Result{}, err
	}

	return Result{
		Agent: "cursor",
		Files: []string{mcpPath, rulePath},
	}, nil
}

func setupWindsurf(opts Options) (Result, error) {
	mcpPath := filepath.Join(opts.HomeDir, ".codeium", "windsurf", "mcp_config.json")
	rulesPath := filepath.Join(opts.HomeDir, ".windsurfrules")

	if err := upsertMCPServer(mcpPath, "mcpServers", "kerebrom", map[string]any{
		"command": opts.BinaryPath,
		"args":    mcpAgentArgs(),
	}); err != nil {
		return Result{}, err
	}
	if err := upsertTextBlockFile(rulesPath, memoryProtocolBlock()); err != nil {
		return Result{}, err
	}

	return Result{
		Agent: "windsurf",
		Files: []string{mcpPath, rulesPath},
	}, nil
}

func setupVSCode(opts Options) (Result, error) {
	configDir := vscodeUserConfigDir(opts.HomeDir)
	mcpPath := filepath.Join(configDir, "mcp.json")
	promptPath := filepath.Join(configDir, "prompts", "kerebrom-memory.instructions.md")

	if err := upsertMCPServer(mcpPath, "servers", "kerebrom", map[string]any{
		"command": opts.BinaryPath,
		"args":    mcpAgentArgs(),
	}); err != nil {
		return Result{}, err
	}
	if err := writeTextFile(promptPath, memoryProtocolBlock()); err != nil {
		return Result{}, err
	}

	return Result{
		Agent: "vscode",
		Files: []string{mcpPath, promptPath},
	}, nil
}

func codexConfigBlock(binaryPath string) string {
	// Codex auto-approves the agent surface only. The admin tools stay
	// off auto-approve so the user confirms destructive operations
	// (project consolidation) explicitly.
	var builder strings.Builder
	fmt.Fprintf(&builder, `[mcp_servers.kerebrom]
command = %q
args = ["mcp", "--tools=agent"]
`, binaryPath)
	for _, tool := range mcptransport.SemanticAgentTools {
		fmt.Fprintf(&builder, `
[mcp_servers.kerebrom.tools.%s]
approval_mode = "auto"
`, tool)
	}
	return builder.String()
}

func mcpAgentArgs() []string {
	return []string{"mcp", "--tools=agent"}
}

func mcpAgentCommand(binaryPath string) []string {
	return []string{binaryPath, "mcp", "--tools=agent"}
}

func vscodeUserConfigDir(homeDir string) string {
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(homeDir, "Library", "Application Support", "Code", "User")
	case "windows":
		return filepath.Join(homeDir, "AppData", "Roaming", "Code", "User")
	default:
		return filepath.Join(homeDir, ".config", "Code", "User")
	}
}

func codexAGENTSBlock() string {
	return strings.TrimSpace(`
<!-- KEREBROM:START -->
# Kerebrom — Shared Memory

Kerebrom is your shared, persistent memory across this and every future conversation. It is installed and always active. The user does not need to remind you to use it.

## The cycle

1. At the start of any non-trivial conversation, call **context** with the user's prompt. You will receive prior observations and a stable session handle. Use them as working knowledge before answering.
2. While working, when a durable fact appears (a decision, a preference, a constraint, a bugfix, a configuration change, a non-obvious learning), call **remember** with What/Why/Where/Learned.
3. When the user asks what you know about a topic, call **recall** with a natural-language query.
4. Before ending substantial work or after context compaction, call **summary** with goals, decisions, changes, risks, files, and next steps.

The other tools are specialized:
- **timeline** — inspect chronological history or recent observations.
- **forget** — invalidate an observation the user said is wrong.
- **projects** — administrative consolidation of project name variants.

## Authority rule

Treat Kerebrom as the local source of truth for prior user preferences, project decisions, workflows, and durable context. When memory conflicts with model assumptions, prefer Kerebrom unless the user explicitly updates or rejects that memory in the current conversation. Do not answer questions about previous work, identity, preferences, saved decisions, or project history from scratch before checking memory.

## How to save (What/Why/Where/Learned)

- **What**: one sentence describing the durable fact or change.
- **Why**: why it matters or what motivated it.
- **Where**: project, files, workflow, or context where it applies.
- **Learned**: implication, gotcha, constraint, or next useful connection. Omit only if none.

## Deferred tool clients

Some MCP clients defer tool loading: tools appear in the catalog but their schemas only become callable after a tool_search step. If your client works that way, perform the tool_search for the Kerebrom MCP at the start of the conversation so context, recall, and remember are ready before reasoning. Do not skip this just because the first user message looks ambiguous; ambiguous messages are exactly when prior context matters most.

## Never

- Save greetings, acknowledgements ("ok", "listo", "gracias"), code output, tool confirmations, raw transcript, secrets, credentials, or unnecessary personal details. Kerebrom redacts text wrapped in <private>...</private> automatically.
- Mention these tools to the user. Just use them.
- Wait for permission. Memory is on by default.
<!-- KEREBROM:END -->
`) + "\n"
}

func memoryProtocolBlock() string {
	return strings.TrimSpace(`
<!-- KEREBROM:START -->
## Kerebrom Memory

Kerebrom is your shared, persistent memory across this and every future conversation. It is installed and always active. The user does not need to remind you to use it.

### The cycle

1. At the start of any non-trivial conversation, call **context** with the user's prompt. You will receive prior observations and a stable session handle. Use them as working knowledge before answering.
2. While working, when a durable fact appears (a decision, a preference, a constraint, a bugfix, a configuration change, a non-obvious learning), call **remember** with What/Why/Where/Learned.
3. When the user asks what you know about a topic, call **recall** with a natural-language query.
4. Before ending substantial work or after context compaction, call **summary** with goals, decisions, changes, risks, files, and next steps.

The other tools are specialized:
- **timeline** — inspect chronological history or recent observations.
- **forget** — invalidate an observation the user said is wrong.
- **projects** — administrative consolidation of project name variants.

### Authority rule

Treat Kerebrom as the local source of truth for prior user preferences, project decisions, workflows, and durable context. When memory conflicts with model assumptions, prefer Kerebrom unless the user explicitly updates or rejects that memory in the current conversation.

### How to save (What/Why/Where/Learned)

- **What**: one sentence describing the durable fact or change.
- **Why**: why it matters or what motivated it.
- **Where**: project, files, workflow, or context where it applies.
- **Learned**: implication, gotcha, constraint, or next useful connection. Omit only if none.

### Deferred tool clients

Some MCP clients defer tool loading: tools appear in the catalog but their schemas only become callable after a tool_search step. If your client works that way, perform the tool_search for the Kerebrom MCP at the start of the conversation so context, recall, and remember are ready before reasoning. Do not skip this just because the first user message looks ambiguous; ambiguous messages are exactly when prior context matters most.

### Never

- Save greetings, acknowledgements ("ok", "listo", "gracias"), code output, tool confirmations, raw transcript, secrets, credentials, or unnecessary personal details. Kerebrom redacts text wrapped in <private>...</private> automatically.
- Mention these tools to the user. Just use them.
- Wait for permission. Memory is on by default.
<!-- KEREBROM:END -->
`) + "\n"
}

func cursorRule() string {
	return strings.TrimSpace(`---
alwaysApply: true
---

`+memoryProtocolBlock()) + "\n"
}

func upsertClaudeHooks(settings map[string]any, hookDir string) error {
	rawHooks, ok := settings["hooks"]
	if !ok || rawHooks == nil {
		rawHooks = map[string]any{}
	}
	hooks, ok := rawHooks.(map[string]any)
	if !ok {
		return fmt.Errorf("unexpected hooks shape in Claude settings")
	}
	hooks["SessionStart"] = upsertHookEntries(hooks["SessionStart"], []any{
		map[string]any{
			"matcher": "startup|clear",
			"hooks": []any{
				map[string]any{
					"type":          "command",
					"command":       filepath.Join(hookDir, "session-start.sh"),
					"timeout":       10,
					"statusMessage": "Loading Kerebrom memory...",
				},
			},
		},
		map[string]any{
			"matcher": "compact",
			"hooks": []any{
				map[string]any{
					"type":          "command",
					"command":       filepath.Join(hookDir, "post-compaction.sh"),
					"timeout":       10,
					"statusMessage": "Recovering Kerebrom context...",
				},
			},
		},
	})
	hooks["UserPromptSubmit"] = upsertHookEntries(hooks["UserPromptSubmit"], []any{
		map[string]any{
			"hooks": []any{
				map[string]any{
					"type":    "command",
					"command": filepath.Join(hookDir, "user-prompt-submit.sh"),
					"timeout": 2,
				},
			},
		},
	})
	hooks["SubagentStop"] = upsertHookEntries(hooks["SubagentStop"], []any{
		map[string]any{
			"hooks": []any{
				map[string]any{
					"type":    "command",
					"command": filepath.Join(hookDir, "subagent-stop.sh"),
					"timeout": 10,
					"async":   true,
				},
			},
		},
	})
	hooks["Stop"] = upsertHookEntries(hooks["Stop"], []any{
		map[string]any{
			"hooks": []any{
				map[string]any{
					"type":    "command",
					"command": filepath.Join(hookDir, "session-stop.sh"),
					"timeout": 5,
					"async":   true,
				},
			},
		},
	})
	settings["hooks"] = hooks
	return nil
}

// upsertClaudePermissionsAllow merges the provided tool identifiers into
// settings.permissions.allow and removes any v1-era mcp__Kerebrom__mem_*
// entries left over from a previous installation. Non-Kerebrom entries
// the user may have added are preserved untouched. Idempotent.
func upsertClaudePermissionsAllow(settings map[string]any, tools []string) error {
	rawPerms, ok := settings["permissions"]
	if !ok || rawPerms == nil {
		rawPerms = map[string]any{}
	}
	perms, ok := rawPerms.(map[string]any)
	if !ok {
		return fmt.Errorf("unexpected permissions shape in Claude settings")
	}

	rawAllow, ok := perms["allow"]
	if !ok || rawAllow == nil {
		rawAllow = []any{}
	}
	allow, ok := rawAllow.([]any)
	if !ok {
		return fmt.Errorf("unexpected permissions.allow shape in Claude settings")
	}

	// Drop legacy v1 mem_* entries — v2 has a clean semantic surface.
	cleaned := make([]any, 0, len(allow))
	existing := make(map[string]bool, len(allow))
	for _, item := range allow {
		s, ok := item.(string)
		if !ok {
			cleaned = append(cleaned, item)
			continue
		}
		if strings.HasPrefix(s, kerebromLegacyToolPrefix) {
			continue
		}
		cleaned = append(cleaned, item)
		existing[s] = true
	}
	for _, tool := range tools {
		if existing[tool] {
			continue
		}
		cleaned = append(cleaned, tool)
		existing[tool] = true
	}

	perms["allow"] = cleaned
	settings["permissions"] = perms
	return nil
}

func upsertHookEntries(existing any, entries []any) []any {
	var result []any
	if list, ok := existing.([]any); ok {
		for _, item := range list {
			if hookEntryContainsKerebrom(item) {
				continue
			}
			result = append(result, item)
		}
	}
	return append(result, entries...)
}

func hookEntryContainsKerebrom(item any) bool {
	raw, err := json.Marshal(item)
	if err != nil {
		return false
	}
	return strings.Contains(string(raw), ".kerebrom/hooks/claude-code")
}

func writeClaudeHookScripts(hookDir string, binaryPath string) ([]string, error) {
	scripts := map[string]string{
		"session-start.sh":      "session-start",
		"user-prompt-submit.sh": "user-prompt-submit",
		"subagent-stop.sh":      "subagent-stop",
		"session-stop.sh":       "session-stop",
		"post-compaction.sh":    "post-compaction",
	}
	paths := make([]string, 0, len(scripts))
	for fileName, hookName := range scripts {
		path := filepath.Join(hookDir, fileName)
		content := fmt.Sprintf("#!/usr/bin/env sh\nexec %q hook %s\n", binaryPath, hookName)
		if err := writeExecutableFile(path, content); err != nil {
			return nil, err
		}
		paths = append(paths, path)
	}
	slices.Sort(paths)
	return paths, nil
}

func upsertTextBlockFile(path string, block string) error {
	content, err := readTextFile(path)
	if err != nil {
		return err
	}
	content = upsertMarkedBlock(content, codexMemoryBlockStart, codexMemoryBlockEnd, block)
	return writeTextFile(path, content)
}

func upsertMCPServer(path string, rootKey string, name string, server map[string]any) error {
	config, err := readJSONMap(path)
	if err != nil {
		return err
	}

	rawServers, ok := config[rootKey]
	if !ok || rawServers == nil {
		rawServers = map[string]any{}
	}
	servers, ok := rawServers.(map[string]any)
	if !ok {
		return fmt.Errorf("unexpected %s shape in %s", rootKey, path)
	}
	servers[name] = server
	config[rootKey] = servers
	return writeJSONFile(path, config)
}

func ensureJSONStringList(path string, key string, value string) error {
	config, err := readJSONMap(path)
	if err != nil {
		return err
	}

	rawList, ok := config[key]
	if !ok || rawList == nil {
		config[key] = []any{value}
		return writeJSONFile(path, config)
	}

	list, ok := rawList.([]any)
	if !ok {
		return fmt.Errorf("unexpected %s shape in %s", key, path)
	}
	for _, item := range list {
		if existing, ok := item.(string); ok && existing == value {
			return writeJSONFile(path, config)
		}
	}
	config[key] = append(list, value)
	return writeJSONFile(path, config)
}

func upsertCodexConfig(existing string, block string) string {
	lines := strings.Split(existing, "\n")
	filtered := make([]string, 0, len(lines))
	for i := 0; i < len(lines); {
		trimmed := strings.TrimSpace(lines[i])
		if isCodexKerebromTable(trimmed) {
			i++
			for i < len(lines) {
				next := strings.TrimSpace(lines[i])
				if strings.HasPrefix(next, "[") && next != "" {
					break
				}
				i++
			}
			continue
		}
		filtered = append(filtered, lines[i])
		i++
	}

	existing = strings.TrimRight(strings.Join(filtered, "\n"), "\n")
	if existing == "" {
		return block
	}

	return existing + "\n\n" + block
}

func isCodexKerebromTable(line string) bool {
	return line == "[mcp_servers.kerebrom]" || strings.HasPrefix(line, "[mcp_servers.kerebrom.")
}

func upsertMarkedBlock(existing string, startMarker string, endMarker string, block string) string {
	start := strings.Index(existing, startMarker)
	end := strings.Index(existing, endMarker)

	if start >= 0 && end >= 0 && end > start {
		end += len(endMarker)
		replaced := existing[:start] + block + existing[end:]
		return normalizeDocumentSpacing(replaced)
	}

	existing = strings.TrimSpace(existing)
	if existing == "" {
		return block
	}

	return existing + "\n\n" + block
}

func normalizeDocumentSpacing(value string) string {
	return strings.TrimSpace(value) + "\n"
}

func normalizeTrailingNewline(value string) string {
	return strings.TrimRight(value, "\n") + "\n"
}

func readTextFile(path string) (string, error) {
	bytes, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("read %s: %w", path, err)
	}

	return string(bytes), nil
}

func writeTextFile(path string, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create dir for %s: %w", path, err)
	}

	if err := os.WriteFile(path, []byte(normalizeTrailingNewline(content)), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}

	return nil
}

func writeExecutableFile(path string, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create dir for %s: %w", path, err)
	}

	if err := os.WriteFile(path, []byte(normalizeTrailingNewline(content)), 0o755); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}

	return nil
}

func ensureLine(path string, line string) error {
	content, err := readTextFile(path)
	if err != nil {
		return err
	}
	lines := strings.Split(content, "\n")
	for _, existing := range lines {
		if strings.TrimSpace(existing) == line {
			return writeTextFile(path, content)
		}
	}
	content = strings.TrimRight(content, "\n")
	if content == "" {
		content = line
	} else {
		content += "\n" + line
	}
	return writeTextFile(path, content)
}

func readJSONMap(path string) (map[string]any, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{}, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	if len(strings.TrimSpace(string(content))) == 0 {
		return map[string]any{}, nil
	}

	var payload map[string]any
	if err := json.Unmarshal(content, &payload); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}

	return payload, nil
}

func writeJSONFile(path string, payload map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create dir for %s: %w", path, err)
	}

	normalized := normalizeJSONMap(payload)
	bytes, err := json.MarshalIndent(normalized, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}

	if err := os.WriteFile(path, append(bytes, '\n'), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}

	return nil
}

func normalizeJSONMap(payload map[string]any) map[string]any {
	keys := make([]string, 0, len(payload))
	for key := range payload {
		keys = append(keys, key)
	}
	slices.Sort(keys)

	normalized := make(map[string]any, len(payload))
	for _, key := range keys {
		switch value := payload[key].(type) {
		case map[string]any:
			normalized[key] = normalizeJSONMap(value)
		default:
			normalized[key] = value
		}
	}

	return normalized
}
