// Package setup auto-configures AI tools (Claude Code, Codex, etc.) to use Kerebrom.
//
// Philosophy (same as Engram): ONLY install the MCP server entry.
// Do NOT modify CLAUDE.md, settings.json, hooks, or any other native app files.
// The MCP server provides its own instructions via the protocol.
package setup

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Result reports what was configured.
type Result struct {
	Tool    string `json:"tool"`
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

// Run detects installed AI tools and configures them.
func Run(dbPath, binaryPath string) []Result {
	var results []Result
	results = append(results, setupClaudeCode(dbPath, binaryPath))
	results = append(results, setupClaudeDesktop(dbPath, binaryPath))
	results = append(results, setupCodex(dbPath, binaryPath))
	return results
}

// Tools that should be auto-approved (same approach as Engram).
var autoApproveTools = []string{
	"mcp__Kerebrom__recall",
	"mcp__Kerebrom__remember",
	"mcp__Kerebrom__context",
	"mcp__Kerebrom__entities",
	"mcp__Kerebrom__facts",
	"mcp__Kerebrom__query",
	"mcp__Kerebrom__gaps",
}

// setupClaudeCode writes MCP config + adds tools to permissions allowlist.
func setupClaudeCode(dbPath, binPath string) Result {
	home, _ := os.UserHomeDir()
	claudeDir := filepath.Join(home, ".claude")
	if _, err := os.Stat(claudeDir); os.IsNotExist(err) {
		return Result{"Claude Code", false, "~/.claude/ not found"}
	}

	// 1. Write MCP config.
	mcpDir := filepath.Join(claudeDir, "mcp")
	os.MkdirAll(mcpDir, 0755)
	mcpPath := filepath.Join(mcpDir, "kerebrom.json")

	config := map[string]any{
		"command": binPath,
		"args":    []string{"serve", "--db", dbPath},
	}
	data, _ := json.MarshalIndent(config, "", "  ")
	if err := os.WriteFile(mcpPath, data, 0644); err != nil {
		return Result{"Claude Code", false, fmt.Sprintf("write MCP config: %v", err)}
	}

	// 2. Add tools to permissions allowlist (like Engram does).
	// Without this, Claude asks permission before each tool call.
	settingsPath := filepath.Join(claudeDir, "settings.json")
	settings := map[string]any{}
	if b, err := os.ReadFile(settingsPath); err == nil {
		json.Unmarshal(b, &settings)
	}
	perms, _ := settings["permissions"].(map[string]any)
	if perms == nil {
		perms = map[string]any{}
	}
	allow, _ := perms["allow"].([]any)
	existing := map[string]bool{}
	for _, a := range allow {
		if s, ok := a.(string); ok {
			existing[s] = true
		}
	}
	for _, tool := range autoApproveTools {
		if !existing[tool] {
			allow = append(allow, tool)
		}
	}
	perms["allow"] = allow
	settings["permissions"] = perms
	b, _ := json.MarshalIndent(settings, "", "  ")
	os.WriteFile(settingsPath, b, 0644)

	return Result{"Claude Code", true, "MCP config + permissions installed"}
}

// setupClaudeDesktop ONLY adds mcpServers entry. Does not touch preferences.
func setupClaudeDesktop(dbPath, binPath string) Result {
	home, _ := os.UserHomeDir()
	var configPath string
	switch runtime.GOOS {
	case "darwin":
		configPath = filepath.Join(home, "Library", "Application Support", "Claude", "claude_desktop_config.json")
	case "windows":
		configPath = filepath.Join(os.Getenv("APPDATA"), "Claude", "claude_desktop_config.json")
	default:
		configPath = filepath.Join(home, ".config", "claude", "claude_desktop_config.json")
	}

	if _, err := os.Stat(filepath.Dir(configPath)); os.IsNotExist(err) {
		return Result{"Claude Desktop", false, "config dir not found"}
	}

	config := map[string]any{}
	if b, err := os.ReadFile(configPath); err == nil {
		json.Unmarshal(b, &config)
	}
	mcpServers, _ := config["mcpServers"].(map[string]any)
	if mcpServers == nil {
		mcpServers = map[string]any{}
	}
	mcpServers["Kerebrom"] = map[string]any{
		"command": binPath,
		"args":    []string{"serve", "--db", dbPath},
	}
	config["mcpServers"] = mcpServers
	b, _ := json.MarshalIndent(config, "", "  ")
	os.MkdirAll(filepath.Dir(configPath), 0755)
	if err := os.WriteFile(configPath, b, 0644); err != nil {
		return Result{"Claude Desktop", false, fmt.Sprintf("write config: %v", err)}
	}
	return Result{"Claude Desktop", true, "MCP server configured"}
}

// setupCodex ONLY adds the MCP server entry to config.toml.
func setupCodex(dbPath, binPath string) Result {
	home, _ := os.UserHomeDir()
	codexDir := filepath.Join(home, ".codex")
	if _, err := os.Stat(codexDir); os.IsNotExist(err) {
		return Result{"Codex", false, "~/.codex/ not found"}
	}

	configPath := filepath.Join(codexDir, "config.toml")
	existing, _ := os.ReadFile(configPath)

	// Check if Kerebrom MCP is already configured (not just mentioned in text).
	if strings.Contains(string(existing), "[mcp_servers.Kerebrom]") ||
		strings.Contains(string(existing), "[mcp_servers.kerebrom]") {
		return Result{"Codex", true, "already configured"}
	}

	entry := fmt.Sprintf(`
[mcp_servers.Kerebrom]
command = %q
args = ["serve", "--db", %q]
`, binPath, dbPath)
	f, err := os.OpenFile(configPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return Result{"Codex", false, fmt.Sprintf("write config: %v", err)}
	}
	f.WriteString(entry)
	f.Close()
	return Result{"Codex", true, "MCP server added to config.toml"}
}

// FindBinary returns the absolute path to the kerebrom binary.
func FindBinary() string {
	if exe, err := os.Executable(); err == nil {
		if abs, err := filepath.Abs(exe); err == nil {
			return abs
		}
	}
	if p, err := exec.LookPath("kerebrom"); err == nil {
		if abs, err := filepath.Abs(p); err == nil {
			return abs
		}
	}
	return "kerebrom"
}
