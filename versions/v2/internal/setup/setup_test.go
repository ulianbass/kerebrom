package setup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRunCodexSetupIsIdempotent(t *testing.T) {
	t.Parallel()

	homeDir := t.TempDir()
	binaryPath := filepath.Join(homeDir, "bin", "kerebrom")

	configPath := filepath.Join(homeDir, ".codex", "config.toml")
	agentsPath := filepath.Join(homeDir, ".codex", "AGENTS.md")
	hooksPath := filepath.Join(homeDir, ".codex", "hooks.json")
	globalStatePath := filepath.Join(homeDir, ".codex", ".codex-global-state.json")

	mustWriteFile(t, configPath, `model = "gpt-5.4"

[mcp_servers.tradingview]
command = "/path/to/node"
args = ["/path/to/tradingview.js"]
`)
	mustWriteFile(t, agentsPath, "# TradingView\n\nExisting instructions.\n")
	mustWriteFile(t, hooksPath, `{
  "hooks": {
    "UserPromptSubmit": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "/Users/test/.kerebrom/hooks/claude-code/user-prompt-submit.sh",
            "timeout": 2
          }
        ]
      }
    ],
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [
          {
            "type": "command",
            "command": "/tmp/keep.sh",
            "statusMessage": "Existing user hook"
          }
        ]
      }
    ]
  }
}`)
	mustWriteFile(t, globalStatePath, `{"localeOverride":"en-US"}`)

	result, err := Run("codex", Options{
		HomeDir:    homeDir,
		BinaryPath: binaryPath,
	})
	if err != nil {
		t.Fatalf("run codex setup: %v", err)
	}
	if result.Agent != "codex" || len(result.Files) != 5 {
		t.Fatalf("unexpected codex result: %+v", result)
	}

	configContent := mustReadFile(t, configPath)
	if !strings.Contains(configContent, `[mcp_servers.tradingview]`) {
		t.Fatalf("codex setup removed existing server block: %q", configContent)
	}
	if strings.Count(configContent, "[mcp_servers.kerebrom]") != 1 {
		t.Fatalf("expected one kerebrom config block, got %q", configContent)
	}
	if strings.Count(configContent, "approval_mode = \"auto\"") != 6 {
		t.Fatalf("expected auto approval for 6 Kerebrom agent tools, got %q", configContent)
	}
	if !strings.Contains(configContent, `args = ["mcp", "--tools=agent"]`) {
		t.Fatalf("expected agent MCP profile args, got %q", configContent)
	}
	if !strings.Contains(configContent, `command = "`+binaryPath+`"`) {
		t.Fatalf("config missing binary path: %q", configContent)
	}
	if !strings.Contains(configContent, "[features]") || !strings.Contains(configContent, "codex_hooks = true") {
		t.Fatalf("config missing enabled Codex hooks feature: %q", configContent)
	}

	hooksContent := mustReadFile(t, hooksPath)
	for _, hook := range []struct {
		event  string
		script string
	}{
		{event: "SessionStart", script: "session-start.sh"},
		{event: "UserPromptSubmit", script: "user-prompt-submit.sh"},
		{event: "Stop", script: "session-stop.sh"},
	} {
		assertHookCommandBool(t, hooksPath, hook.event, hook.script, "silent", true)
	}
	for _, statusMessage := range []string{
		"Loading Kerebrom memory...",
		"Updating Kerebrom memory...",
		"Closing Kerebrom session...",
	} {
		if !strings.Contains(hooksContent, statusMessage) {
			t.Fatalf("missing friendly Codex hook status message %q in %s", statusMessage, hooksContent)
		}
	}
	if strings.Contains(hooksContent, ".kerebrom/hooks/claude-code") {
		t.Fatalf("Codex hooks should use Codex-specific Kerebrom scripts: %q", hooksContent)
	}
	if strings.Count(hooksContent, "user-prompt-submit.sh") != 1 {
		t.Fatalf("Codex setup duplicated prompt hook: %q", hooksContent)
	}
	if !strings.Contains(hooksContent, "Existing user hook") {
		t.Fatalf("Codex setup removed unrelated user hook: %q", hooksContent)
	}

	agentsContent := mustReadFile(t, agentsPath)
	if !strings.Contains(agentsContent, "# TradingView") {
		t.Fatalf("codex setup removed existing AGENTS content: %q", agentsContent)
	}
	if strings.Contains(agentsContent, codexMemoryBlockStart) {
		t.Fatalf("codex setup should not write Kerebrom protocol into user AGENTS preferences: %q", agentsContent)
	}
	if !strings.Contains(agentsContent, "Existing instructions.") {
		t.Fatalf("codex setup changed existing AGENTS preferences: %q", agentsContent)
	}

	if _, err := Run("codex", Options{
		HomeDir:    homeDir,
		BinaryPath: binaryPath,
	}); err != nil {
		t.Fatalf("rerun codex setup: %v", err)
	}

	configContent = mustReadFile(t, configPath)
	if strings.Count(configContent, "[mcp_servers.kerebrom]") != 1 {
		t.Fatalf("codex config duplicated kerebrom block: %q", configContent)
	}
	if strings.Count(configContent, "approval_mode = \"auto\"") != 6 {
		t.Fatalf("codex config duplicated Kerebrom tool approvals: %q", configContent)
	}
	hooksContent = mustReadFile(t, hooksPath)
	if strings.Count(hooksContent, "user-prompt-submit.sh") != 1 || strings.Count(hooksContent, "session-stop.sh") != 1 {
		t.Fatalf("codex hooks duplicated Kerebrom hook entries: %q", hooksContent)
	}
	for _, hook := range []struct {
		event  string
		script string
	}{
		{event: "SessionStart", script: "session-start.sh"},
		{event: "UserPromptSubmit", script: "user-prompt-submit.sh"},
		{event: "Stop", script: "session-stop.sh"},
	} {
		assertHookCommandBool(t, hooksPath, hook.event, hook.script, "silent", true)
	}

	agentsContent = mustReadFile(t, agentsPath)
	if strings.Contains(agentsContent, codexMemoryBlockStart) {
		t.Fatalf("codex AGENTS should remain free of Kerebrom protocol blocks: %q", agentsContent)
	}
}

func TestRunCodexSetupUsesSpanishHookStatusMessages(t *testing.T) {
	t.Parallel()

	homeDir := t.TempDir()
	binaryPath := filepath.Join(homeDir, "bin", "kerebrom")
	configPath := filepath.Join(homeDir, ".codex", "config.toml")
	globalStatePath := filepath.Join(homeDir, ".codex", ".codex-global-state.json")

	mustWriteFile(t, configPath, `model = "gpt-5.4"`)
	mustWriteFile(t, globalStatePath, `{"localeOverride":"es-GT"}`)

	if _, err := Run("codex", Options{
		HomeDir:    homeDir,
		BinaryPath: binaryPath,
	}); err != nil {
		t.Fatalf("run codex setup: %v", err)
	}

	hooksContent := mustReadFile(t, filepath.Join(homeDir, ".codex", "hooks.json"))
	for _, statusMessage := range []string{
		"Cargando memoria de Kerebrom...",
		"Guardando prompt en Kerebrom...",
		"Cerrando sesión de Kerebrom...",
	} {
		if !strings.Contains(hooksContent, statusMessage) {
			t.Fatalf("missing Spanish Codex hook status message %q in %s", statusMessage, hooksContent)
		}
	}
}

func TestRunCodexSetupFallsBackToMacOSAppleLanguages(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS defaults fallback only applies on darwin")
	}

	homeDir := t.TempDir()
	binaryPath := filepath.Join(homeDir, "bin", "kerebrom")
	configPath := filepath.Join(homeDir, ".codex", "config.toml")
	binDir := filepath.Join(homeDir, "fake-bin")
	defaultsPath := filepath.Join(binDir, "defaults")

	mustWriteFile(t, configPath, `model = "gpt-5.4"`)
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("create fake bin dir: %v", err)
	}
	if err := os.WriteFile(defaultsPath, []byte("#!/bin/sh\nprintf '%s\\n' '(\"es-GT\")'\n"), 0o755); err != nil {
		t.Fatalf("write fake defaults: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	if _, err := Run("codex", Options{
		HomeDir:    homeDir,
		BinaryPath: binaryPath,
	}); err != nil {
		t.Fatalf("run codex setup: %v", err)
	}

	hooksContent := mustReadFile(t, filepath.Join(homeDir, ".codex", "hooks.json"))
	if !strings.Contains(hooksContent, "Cargando memoria de Kerebrom...") {
		t.Fatalf("missing Spanish Codex hook status from macOS fallback in %s", hooksContent)
	}
}

func TestRunCodexSetupRemovesLegacyUserPreferenceBlock(t *testing.T) {
	t.Parallel()

	homeDir := t.TempDir()
	binaryPath := filepath.Join(homeDir, "bin", "kerebrom")
	configPath := filepath.Join(homeDir, ".codex", "config.toml")
	agentsPath := filepath.Join(homeDir, ".codex", "AGENTS.md")

	mustWriteFile(t, configPath, `model = "gpt-5.4"`)
	mustWriteFile(t, agentsPath, `# My Preferences

Keep this.

<!-- KEREBROM:START -->
# Kerebrom old protocol
context remember summary
<!-- KEREBROM:END -->

Also keep this.
`)

	result, err := Run("codex", Options{
		HomeDir:    homeDir,
		BinaryPath: binaryPath,
	})
	if err != nil {
		t.Fatalf("run codex setup: %v", err)
	}
	if result.Agent != "codex" || len(result.Files) != 6 {
		t.Fatalf("unexpected codex result: %+v", result)
	}

	agentsContent := mustReadFile(t, agentsPath)
	if strings.Contains(agentsContent, codexMemoryBlockStart) || strings.Contains(agentsContent, "Kerebrom old protocol") {
		t.Fatalf("codex setup should remove legacy Kerebrom preference block: %q", agentsContent)
	}
	if !strings.Contains(agentsContent, "Keep this.") || !strings.Contains(agentsContent, "Also keep this.") {
		t.Fatalf("codex setup removed user preferences: %q", agentsContent)
	}
}

func TestRunClaudeSetupIsIdempotent(t *testing.T) {
	t.Parallel()

	homeDir := t.TempDir()
	binaryPath := filepath.Join(homeDir, "bin", "kerebrom")

	settingsPath := filepath.Join(homeDir, ".claude", "settings.json")
	mcpPath := filepath.Join(homeDir, ".claude", "mcp.json")
	desktopMCPPath := claudeDesktopConfigPath(homeDir)
	coworkMemoryPath := filepath.Join(claudeLocalAgentModeSessionsDir(homeDir), "account-1", "org-1", "memory", "CLAUDE.md")

	mustWriteFile(t, settingsPath, `{"extraKnownMarketplaces":{"test":{"source":{"repo":"example/repo","source":"github"}}},"permissions":{"allow":["mcp__Kerebrom__mem_context","mcp__Kerebrom__projects","mcp__TradingView__chart_get_state"]}}`)
	mustWriteFile(t, mcpPath, `{"mcpServers":{"TradingView MCP":{"command":"/path/to/node","args":["/path/to/tradingview.js"]}}}`)
	mustWriteFile(t, desktopMCPPath, `{"mcpServers":{"TradingView MCP":{"command":"/path/to/node","args":["/path/to/tradingview.js"]}},"preferences":{"menuBarEnabled":false}}`)
	mustWriteFile(t, filepath.Join(claudeLocalAgentModeSessionsDir(homeDir), "account-1", "org-1", "cowork-gb-cache.json"), `{}`)
	mustWriteFile(t, coworkMemoryPath, "Existing Cowork instructions.\n")

	result, err := Run("claude", Options{
		HomeDir:    homeDir,
		BinaryPath: binaryPath,
	})
	if err != nil {
		t.Fatalf("run claude setup: %v", err)
	}
	if result.Agent != "claude" || len(result.Files) != 9 {
		t.Fatalf("unexpected claude result: %+v", result)
	}

	settings := mustReadJSONMap(t, settingsPath)
	if _, ok := settings["extraKnownMarketplaces"]; !ok {
		t.Fatalf("claude setup removed existing settings content: %#v", settings)
	}
	permissions := settings["permissions"].(map[string]any)
	allow := permissions["allow"].([]any)
	if len(allow) != 7 {
		t.Fatalf("expected 6 Kerebrom agent tools plus existing TradingView permission, got %#v", allow)
	}
	if containsString(allow, "mcp__Kerebrom__mem_context") {
		t.Fatalf("legacy mem_* permission was not removed: %#v", allow)
	}
	if containsString(allow, "mcp__Kerebrom__projects") {
		t.Fatalf("admin projects permission should not be auto-approved in the agent profile: %#v", allow)
	}
	for _, tool := range []string{"context", "recall", "remember", "summary", "forget", "timeline"} {
		if !containsString(allow, "mcp__Kerebrom__"+tool) {
			t.Fatalf("missing auto-approved Kerebrom agent tool %s in %#v", tool, allow)
		}
	}
	if !containsString(allow, "mcp__TradingView__chart_get_state") {
		t.Fatalf("non-Kerebrom permission was not preserved: %#v", allow)
	}
	hooks, ok := settings["hooks"].(map[string]any)
	if !ok {
		t.Fatalf("missing claude hooks: %#v", settings)
	}
	for _, hookName := range []string{"SessionStart", "UserPromptSubmit", "SubagentStop", "Stop"} {
		if _, ok := hooks[hookName]; !ok {
			t.Fatalf("missing %s hook in %#v", hookName, hooks)
		}
	}
	settingsContent := mustReadFile(t, settingsPath)
	for _, statusMessage := range []string{
		"Loading Kerebrom memory...",
		"Recovering Kerebrom context...",
		"Updating Kerebrom memory...",
		"Saving Kerebrom learnings...",
		"Closing Kerebrom session...",
	} {
		if !strings.Contains(settingsContent, statusMessage) {
			t.Fatalf("missing friendly hook status message %q in %s", statusMessage, settingsContent)
		}
	}
	hookPath := filepath.Join(homeDir, ".kerebrom", "hooks", "claude-code", "session-start.sh")
	hookScript := mustReadFile(t, hookPath)
	if !strings.Contains(hookScript, binaryPath) || !strings.Contains(hookScript, "hook session-start") {
		t.Fatalf("unexpected hook script: %q", hookScript)
	}

	mcpConfig := mustReadJSONMap(t, mcpPath)
	servers := mcpConfig["mcpServers"].(map[string]any)
	if _, ok := servers["TradingView MCP"]; !ok {
		t.Fatalf("claude setup removed existing MCP server: %#v", servers)
	}
	kerebrom, ok := servers["Kerebrom"].(map[string]any)
	if !ok {
		t.Fatalf("missing Kerebrom server: %#v", servers)
	}
	if kerebrom["command"] != binaryPath {
		t.Fatalf("unexpected Kerebrom command: %#v", kerebrom)
	}
	assertArgs(t, kerebrom, []string{"mcp", "--tools=agent"})

	desktopConfig := mustReadJSONMap(t, desktopMCPPath)
	desktopServers := desktopConfig["mcpServers"].(map[string]any)
	if _, ok := desktopServers["TradingView MCP"]; !ok {
		t.Fatalf("claude desktop setup removed existing MCP server: %#v", desktopServers)
	}
	desktopKerebrom, ok := desktopServers["Kerebrom"].(map[string]any)
	if !ok {
		t.Fatalf("missing Claude Desktop Kerebrom server: %#v", desktopServers)
	}
	if desktopKerebrom["command"] != binaryPath {
		t.Fatalf("unexpected Claude Desktop Kerebrom command: %#v", desktopKerebrom)
	}
	assertArgs(t, desktopKerebrom, []string{"mcp", "--tools=agent"})
	if _, ok := desktopConfig["preferences"]; !ok {
		t.Fatalf("claude desktop setup removed preferences: %#v", desktopConfig)
	}
	coworkMemory := mustReadFile(t, coworkMemoryPath)
	if !strings.Contains(coworkMemory, "Existing Cowork instructions.") {
		t.Fatalf("claude desktop setup removed existing Cowork memory: %q", coworkMemory)
	}
	if strings.Count(coworkMemory, codexMemoryBlockStart) != 1 {
		t.Fatalf("expected one Kerebrom seed in Cowork global memory: %q", coworkMemory)
	}
	if !strings.Contains(coworkMemory, "only authoritative durable memory source") {
		t.Fatalf("Cowork seed should establish Kerebrom authority: %q", coworkMemory)
	}
	if !strings.Contains(coworkMemory, "Before answering any user message") {
		t.Fatalf("Cowork seed should require every-turn context activation: %q", coworkMemory)
	}

	if _, err := Run("claude", Options{
		HomeDir:    homeDir,
		BinaryPath: binaryPath,
	}); err != nil {
		t.Fatalf("rerun claude setup: %v", err)
	}

	mcpConfig = mustReadJSONMap(t, mcpPath)
	servers = mcpConfig["mcpServers"].(map[string]any)
	if len(servers) != 2 {
		t.Fatalf("unexpected server count after rerun: %#v", servers)
	}
	desktopConfig = mustReadJSONMap(t, desktopMCPPath)
	desktopServers = desktopConfig["mcpServers"].(map[string]any)
	if len(desktopServers) != 2 {
		t.Fatalf("unexpected Claude Desktop server count after rerun: %#v", desktopServers)
	}
	settings = mustReadJSONMap(t, settingsPath)
	hooks = settings["hooks"].(map[string]any)
	sessionStartHooks := hooks["SessionStart"].([]any)
	if len(sessionStartHooks) != 2 {
		t.Fatalf("unexpected SessionStart hook count after rerun: %#v", sessionStartHooks)
	}

	if _, err := os.Stat(filepath.Join(homeDir, ".claude", "CLAUDE.md")); !os.IsNotExist(err) {
		t.Fatalf("claude setup should not create global CLAUDE.md preferences, err=%v", err)
	}
	coworkMemory = mustReadFile(t, coworkMemoryPath)
	if strings.Count(coworkMemory, codexMemoryBlockStart) != 1 {
		t.Fatalf("claude Cowork global memory duplicated Kerebrom block: %q", coworkMemory)
	}
}

func TestRunClaudeCodeSetupRemovesLegacyGlobalInstructionBlock(t *testing.T) {
	t.Parallel()

	homeDir := t.TempDir()
	binaryPath := filepath.Join(homeDir, "bin", "kerebrom")
	settingsPath := filepath.Join(homeDir, ".claude", "settings.json")
	mcpPath := filepath.Join(homeDir, ".claude", "mcp.json")
	claudeMDPath := filepath.Join(homeDir, ".claude", "CLAUDE.md")

	mustWriteFile(t, settingsPath, `{}`)
	mustWriteFile(t, mcpPath, `{}`)
	mustWriteFile(t, claudeMDPath, `# Personal Claude Instructions

Keep my style preference.

<!-- KEREBROM:START -->
## Kerebrom old protocol
context remember summary
<!-- KEREBROM:END -->
`)

	result, err := Run("claude-code", Options{
		HomeDir:    homeDir,
		BinaryPath: binaryPath,
	})
	if err != nil {
		t.Fatalf("run claude-code setup: %v", err)
	}
	if result.Agent != "claude-code" || len(result.Files) != 8 {
		t.Fatalf("unexpected claude-code result: %+v", result)
	}

	claudeMD := mustReadFile(t, claudeMDPath)
	if strings.Contains(claudeMD, codexMemoryBlockStart) || strings.Contains(claudeMD, "Kerebrom old protocol") {
		t.Fatalf("claude setup should remove legacy Kerebrom instruction block: %q", claudeMD)
	}
	if !strings.Contains(claudeMD, "Keep my style preference.") {
		t.Fatalf("claude setup removed user instructions: %q", claudeMD)
	}
}

func TestRunClaudeDesktopSetupOnlyIsMinimal(t *testing.T) {
	t.Parallel()

	homeDir := t.TempDir()
	binaryPath := filepath.Join(homeDir, "bin", "kerebrom")
	desktopMCPPath := claudeDesktopConfigPath(homeDir)
	coworkMemoryPath := filepath.Join(claudeLocalAgentModeSessionsDir(homeDir), "account-1", "org-1", "memory", "CLAUDE.md")

	mustWriteFile(t, desktopMCPPath, `{"mcpServers":{"TradingView MCP":{"command":"/path/to/node","args":["/path/to/tradingview.js"]}},"preferences":{"menuBarEnabled":false}}`)
	mustWriteFile(t, filepath.Join(claudeLocalAgentModeSessionsDir(homeDir), "account-1", "org-1", "cowork-gb-cache.json"), `{}`)

	result, err := Run("claude-desktop", Options{
		HomeDir:    homeDir,
		BinaryPath: binaryPath,
	})
	if err != nil {
		t.Fatalf("run claude-desktop setup: %v", err)
	}
	if result.Agent != "claude-desktop" || len(result.Files) != 2 {
		t.Fatalf("unexpected claude-desktop result: %+v", result)
	}

	desktopConfig := mustReadJSONMap(t, desktopMCPPath)
	desktopServers := desktopConfig["mcpServers"].(map[string]any)
	if _, ok := desktopServers["TradingView MCP"]; !ok {
		t.Fatalf("claude desktop setup removed existing MCP server: %#v", desktopServers)
	}
	desktopKerebrom, ok := desktopServers["Kerebrom"].(map[string]any)
	if !ok {
		t.Fatalf("missing Claude Desktop Kerebrom server: %#v", desktopServers)
	}
	if desktopKerebrom["command"] != binaryPath {
		t.Fatalf("unexpected Claude Desktop Kerebrom command: %#v", desktopKerebrom)
	}
	if _, ok := desktopConfig["preferences"]; !ok {
		t.Fatalf("claude desktop setup removed preferences: %#v", desktopConfig)
	}
	if _, err := os.Stat(filepath.Join(homeDir, ".claude", "settings.json")); !os.IsNotExist(err) {
		t.Fatalf("claude-desktop setup should not create Claude Code settings, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(homeDir, ".kerebrom", "hooks", "claude-code")); !os.IsNotExist(err) {
		t.Fatalf("claude-desktop setup should not create Claude Code hooks, err=%v", err)
	}
	coworkMemory := mustReadFile(t, coworkMemoryPath)
	if !strings.Contains(coworkMemory, "Kerebrom Native Memory Seed") {
		t.Fatalf("claude-desktop setup should seed Cowork native memory: %q", coworkMemory)
	}
}

func TestRunAutoSetupFallsBackToClaudeDesktop(t *testing.T) {
	t.Parallel()

	homeDir := t.TempDir()
	binaryPath := filepath.Join(homeDir, "bin", "kerebrom")

	result, err := Run("auto", Options{
		HomeDir:    homeDir,
		BinaryPath: binaryPath,
	})
	if err != nil {
		t.Fatalf("run auto setup: %v", err)
	}
	if result.Agent != "auto" || len(result.Files) != 1 {
		t.Fatalf("unexpected auto result: %+v", result)
	}

	desktopConfig := mustReadJSONMap(t, claudeDesktopConfigPath(homeDir))
	desktopServers := desktopConfig["mcpServers"].(map[string]any)
	if _, ok := desktopServers["Kerebrom"]; !ok {
		t.Fatalf("auto fallback should configure Claude Desktop: %#v", desktopServers)
	}
	if _, err := os.Stat(filepath.Join(homeDir, ".codex", "config.toml")); !os.IsNotExist(err) {
		t.Fatalf("auto fallback should not create Codex config, err=%v", err)
	}
}

func TestRunAutoSetupUsesExistingAgentConfigs(t *testing.T) {
	t.Parallel()

	homeDir := t.TempDir()
	binaryPath := filepath.Join(homeDir, "bin", "kerebrom")

	codexConfigPath := filepath.Join(homeDir, ".codex", "config.toml")
	mustWriteFile(t, codexConfigPath, `model = "gpt-5.4"`)

	result, err := Run("auto", Options{
		HomeDir:    homeDir,
		BinaryPath: binaryPath,
	})
	if err != nil {
		t.Fatalf("run auto setup: %v", err)
	}
	if result.Agent != "auto" || len(result.Files) != 5 {
		t.Fatalf("unexpected auto result: %+v", result)
	}

	configContent := mustReadFile(t, codexConfigPath)
	if !strings.Contains(configContent, "[mcp_servers.kerebrom]") {
		t.Fatalf("auto setup did not configure detected Codex: %q", configContent)
	}
	if !strings.Contains(configContent, "codex_hooks = true") {
		t.Fatalf("auto setup did not enable Codex hooks: %q", configContent)
	}
	if _, err := os.Stat(claudeDesktopConfigPath(homeDir)); !os.IsNotExist(err) {
		t.Fatalf("auto setup should not fall back to Claude Desktop when another client is detected, err=%v", err)
	}
}

func TestRunAdditionalAgentSetups(t *testing.T) {
	t.Parallel()

	homeDir := t.TempDir()
	binaryPath := filepath.Join(homeDir, "bin", "kerebrom")

	cases := []struct {
		agent string
		files []string
	}{
		{
			agent: "gemini-cli",
			files: []string{
				filepath.Join(homeDir, ".gemini", "settings.json"),
				filepath.Join(homeDir, ".gemini", "system.md"),
				filepath.Join(homeDir, ".gemini", ".env"),
			},
		},
		{
			agent: "opencode",
			files: []string{
				filepath.Join(homeDir, ".config", "opencode", "opencode.json"),
				filepath.Join(homeDir, ".config", "opencode", "kerebrom-memory.md"),
			},
		},
		{
			agent: "cursor",
			files: []string{
				filepath.Join(homeDir, ".cursor", "mcp.json"),
				filepath.Join(homeDir, ".cursor", "rules", "kerebrom.mdc"),
			},
		},
		{
			agent: "windsurf",
			files: []string{
				filepath.Join(homeDir, ".codeium", "windsurf", "mcp_config.json"),
				filepath.Join(homeDir, ".windsurfrules"),
			},
		},
		{
			agent: "vscode",
			files: []string{
				filepath.Join(vscodeUserConfigDir(homeDir), "mcp.json"),
				filepath.Join(vscodeUserConfigDir(homeDir), "prompts", "kerebrom-memory.instructions.md"),
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.agent, func(t *testing.T) {
			t.Parallel()

			result, err := Run(tc.agent, Options{
				HomeDir:    homeDir,
				BinaryPath: binaryPath,
			})
			if err != nil {
				t.Fatalf("run %s setup: %v", tc.agent, err)
			}
			if result.Agent != tc.agent || len(result.Files) != len(tc.files) {
				t.Fatalf("unexpected setup result: %+v", result)
			}
			for _, path := range tc.files {
				if _, err := os.Stat(path); err != nil {
					t.Fatalf("expected %s to exist: %v", path, err)
				}
			}
		})
	}
}

func TestRunAllSetup(t *testing.T) {
	t.Parallel()

	homeDir := t.TempDir()
	binaryPath := filepath.Join(homeDir, "bin", "kerebrom")

	result, err := Run("all", Options{
		HomeDir:    homeDir,
		BinaryPath: binaryPath,
	})
	if err != nil {
		t.Fatalf("run all setup: %v", err)
	}
	if result.Agent != "all" || len(result.Files) != 24 {
		t.Fatalf("unexpected all setup result: %+v", result)
	}
}

func mustWriteFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func mustReadFile(t *testing.T, path string) string {
	t.Helper()
	bytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(bytes)
}

func mustReadJSONMap(t *testing.T, path string) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal([]byte(mustReadFile(t, path)), &payload); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return payload
}

func assertArgs(t *testing.T, server map[string]any, want []string) {
	t.Helper()

	raw, ok := server["args"].([]any)
	if !ok {
		t.Fatalf("missing args in server config: %#v", server)
	}
	if len(raw) != len(want) {
		t.Fatalf("args length = %d, want %d: %#v", len(raw), len(want), raw)
	}
	for i, wantArg := range want {
		if raw[i] != wantArg {
			t.Fatalf("args[%d] = %#v, want %q in %#v", i, raw[i], wantArg, raw)
		}
	}
}

func assertHookCommandBool(t *testing.T, path string, event string, script string, field string, want bool) {
	t.Helper()

	config := mustReadJSONMap(t, path)
	rawHooks, ok := config["hooks"].(map[string]any)
	if !ok {
		t.Fatalf("missing hooks map in %s: %#v", path, config)
	}
	rawEntries, ok := rawHooks[event].([]any)
	if !ok {
		t.Fatalf("missing %s hook entries in %s: %#v", event, path, rawHooks)
	}
	for _, rawEntry := range rawEntries {
		entry, ok := rawEntry.(map[string]any)
		if !ok {
			continue
		}
		rawCommands, ok := entry["hooks"].([]any)
		if !ok {
			continue
		}
		for _, rawCommand := range rawCommands {
			command, ok := rawCommand.(map[string]any)
			if !ok {
				continue
			}
			commandPath, _ := command["command"].(string)
			if strings.Contains(commandPath, script) {
				if command[field] != want {
					t.Fatalf("expected %s %s hook field %s=%v, got %#v", event, script, field, want, command)
				}
				return
			}
		}
	}
	t.Fatalf("missing %s command for %s in %s: %#v", script, event, path, rawHooks[event])
}

func containsString(items []any, want string) bool {
	for _, item := range items {
		if got, ok := item.(string); ok && got == want {
			return true
		}
	}
	return false
}
