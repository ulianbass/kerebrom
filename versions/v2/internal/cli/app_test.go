package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ulianbass/kerebrom/internal/config"
	"github.com/ulianbass/kerebrom/internal/version"
)

func TestRunHelp(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := Run(nil, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}

	if !strings.Contains(stdout.String(), "Kerebrom v2") {
		t.Fatalf("help output missing banner: %q", stdout.String())
	}

	if !strings.Contains(stdout.String(), "mcp") {
		t.Fatalf("help output missing mcp command: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "mcp-http") {
		t.Fatalf("help output missing mcp-http command: %q", stdout.String())
	}
}

func TestRunVersion(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := Run([]string{"version"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}

	if !strings.Contains(stdout.String(), version.Version) {
		t.Fatalf("version output missing version: %q", stdout.String())
	}
}

func TestRunSessionEndPersistsSessionSummaryObservation(t *testing.T) {
	t.Setenv(config.DataDirEnv, t.TempDir())

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{
		"session-start",
		"--id", "cli-session-summary",
		"--project", "Proyecto Kerebrom",
		"--directory", ".",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("session-start failed: code=%d stderr=%q", code, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{
		"session-end",
		"--id", "cli-session-summary",
		"--summary", "CLI closed a maintenance session with a durable summary.",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("session-end failed: code=%d stderr=%q", code, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{
		"search",
		"--project", "Proyecto Kerebrom",
		"--query", "durable summary",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("search failed: code=%d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Session summary cli-session-summary") {
		t.Fatalf("session summary observation not found: %q", stdout.String())
	}
}

func TestRunSetupCodex(t *testing.T) {
	t.Parallel()

	homeDir := t.TempDir()
	binaryPath := filepath.Join(homeDir, "bin", "kerebrom")

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := Run([]string{
		"setup",
		"codex",
		"--home", homeDir,
		"--binary-path", binaryPath,
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("setup failed: code=%d stderr=%q", code, stderr.String())
	}

	if !strings.Contains(stdout.String(), "configured codex") {
		t.Fatalf("unexpected setup output: %q", stdout.String())
	}

	configPath := filepath.Join(homeDir, ".codex", "config.toml")
	content, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config after setup: %v", err)
	}
	if !strings.Contains(string(content), "[mcp_servers.kerebrom]") {
		t.Fatalf("missing kerebrom codex block: %q", string(content))
	}
}

func TestRunSaveSearchAndStats(t *testing.T) {
	t.Setenv(config.DataDirEnv, t.TempDir())

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := Run([]string{
		"save",
		"--title", "Shared local memory",
		"--content", "Codex and Claude should retrieve from the same local store.",
		"--project", "Proyecto Kerebrom",
		"--type", "decision",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("save failed: code=%d stderr=%q", code, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()

	code = Run([]string{
		"search",
		"--query", "same local store",
		"--project", "Proyecto Kerebrom",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("search failed: code=%d stderr=%q", code, stderr.String())
	}

	if !strings.Contains(stdout.String(), "Shared local memory") {
		t.Fatalf("search output missing saved observation: %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()

	code = Run([]string{
		"stats",
		"--project", "Proyecto Kerebrom",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("stats failed: code=%d stderr=%q", code, stderr.String())
	}

	if !strings.Contains(stdout.String(), "observations=1") {
		t.Fatalf("stats output missing observation count: %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()

	code = Run([]string{
		"context",
		"--project", "Proyecto Kerebrom",
		"--query", "same local store",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("context failed: code=%d stderr=%q", code, stderr.String())
	}

	if !strings.Contains(stdout.String(), "recent:") || !strings.Contains(stdout.String(), "matches:") {
		t.Fatalf("context output missing expected sections: %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()

	code = Run([]string{
		"timeline",
		"--project", "Proyecto Kerebrom",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("timeline failed: code=%d stderr=%q", code, stderr.String())
	}

	if !strings.Contains(stdout.String(), "Shared local memory") {
		t.Fatalf("timeline output missing saved observation: %q", stdout.String())
	}
}

func TestRunExportWritesPrivateFile(t *testing.T) {
	t.Setenv(config.DataDirEnv, t.TempDir())

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := Run([]string{
		"save",
		"--title", "Private export fixture",
		"--content", "Exports contain private local memory and must not be world-readable.",
		"--project", "Proyecto Kerebrom",
		"--type", "decision",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("save failed: code=%d stderr=%q", code, stderr.String())
	}

	outputDir := filepath.Join(t.TempDir(), "exports")
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		t.Fatalf("precreate export dir: %v", err)
	}
	outputPath := filepath.Join(outputDir, "kerebrom.json")
	if err := os.WriteFile(outputPath, []byte("old"), 0o644); err != nil {
		t.Fatalf("precreate export file: %v", err)
	}
	stdout.Reset()
	stderr.Reset()
	code = Run([]string{
		"export",
		"--project", "Proyecto Kerebrom",
		"--output", outputPath,
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("export failed: code=%d stderr=%q", code, stderr.String())
	}

	info, err := os.Stat(outputPath)
	if err != nil {
		t.Fatalf("stat export: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("export mode=%#o, want 0600", got)
	}
	dirInfo, err := os.Stat(outputDir)
	if err != nil {
		t.Fatalf("stat export dir: %v", err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Fatalf("export dir mode=%#o, want 0700", got)
	}
}

func TestRunMCPHTTPRequiresTokenForPublicAddress(t *testing.T) {
	t.Setenv(config.DataDirEnv, t.TempDir())

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := Run([]string{
		"mcp-http",
		"--addr", "0.0.0.0:7437",
	}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("expected config error exit code 2, got %d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), config.RemoteTokenEnv) {
		t.Fatalf("expected token guidance in stderr, got %q", stderr.String())
	}
}

func TestRunServeRequiresTokenForPublicAddress(t *testing.T) {
	t.Setenv(config.DataDirEnv, t.TempDir())

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := Run([]string{
		"serve",
		"--addr", "0.0.0.0:7437",
	}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("expected config error exit code 2, got %d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), config.RemoteTokenEnv) {
		t.Fatalf("expected token guidance in stderr, got %q", stderr.String())
	}
}

func TestRunDoctor(t *testing.T) {
	t.Setenv(config.DataDirEnv, t.TempDir())

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := Run([]string{"doctor"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("doctor failed: code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "Kerebrom doctor:") || !strings.Contains(stdout.String(), "trust ledger coverage") {
		t.Fatalf("doctor output missing expected checks: %q", stdout.String())
	}
}

func TestRunDoctorStatusUsesCapitalizedDoctor(t *testing.T) {
	t.Setenv(config.DataDirEnv, t.TempDir())
	homeDir := t.TempDir()
	v2Root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("resolve v2 root: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := Run([]string{"doctor", "status", "--home", homeDir, "--project-dir", v2Root, "--setup-agent", "codex"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("doctor status failed: code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "Kerebrom Doctor:") || !strings.Contains(stdout.String(), "codex hook silent mode") {
		t.Fatalf("doctor status output missing expected checks: %q", stdout.String())
	}
}

func TestRunDoctorReportDefaultsToJSON(t *testing.T) {
	t.Setenv(config.DataDirEnv, t.TempDir())
	homeDir := t.TempDir()
	v2Root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("resolve v2 root: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := Run([]string{"doctor", "report", "--home", homeDir, "--project-dir", v2Root}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("doctor report failed: code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	var report doctorReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("doctor report is not JSON: %v output=%q", err, stdout.String())
	}
	if report.Status == "" || report.DBPath == "" {
		t.Fatalf("doctor report missing status/db path: %+v", report)
	}
}

func TestRunDoctorHealCreatesBackupAndRepairsCodexSetup(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv(config.DataDirEnv, dataDir)
	homeDir := t.TempDir()
	v2Root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("resolve v2 root: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{
		"save",
		"--title", "doctor heal seed",
		"--content", "seed observation for doctor backup",
		"--project", "doctor-test",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("seed save failed: code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{
		"doctor", "heal",
		"--home", homeDir,
		"--project-dir", v2Root,
		"--setup-agent", "codex",
		"--binary-path", "/tmp/kerebrom",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("doctor heal failed: code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "Kerebrom Doctor Health Mode:") || !strings.Contains(stdout.String(), "sqlite backup") {
		t.Fatalf("doctor heal output missing expected actions: %q", stdout.String())
	}
	backups, err := filepath.Glob(filepath.Join(dataDir, "backups", "health", "*.db"))
	if err != nil {
		t.Fatalf("glob backups: %v", err)
	}
	if len(backups) == 0 {
		t.Fatalf("doctor heal did not create backup in %s", dataDir)
	}
	hooksRaw, err := os.ReadFile(filepath.Join(homeDir, ".codex", "hooks.json"))
	if err != nil {
		t.Fatalf("read repaired codex hooks: %v", err)
	}
	if !strings.Contains(string(hooksRaw), `"silent": true`) {
		t.Fatalf("codex hooks were not repaired with silent mode: %s", hooksRaw)
	}
}

func TestResolveFactoryDirsAcceptsCurrentV2Dir(t *testing.T) {
	v2Root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("resolve v2 root: %v", err)
	}
	t.Chdir(v2Root)

	repoRoot, resolvedV2Root := resolveFactoryDirs(".")
	if filepath.Clean(resolvedV2Root) != filepath.Clean(v2Root) {
		t.Fatalf("expected v2 root %q, got %q", v2Root, resolvedV2Root)
	}
	if _, err := os.Stat(filepath.Join(repoRoot, "README.md")); err != nil {
		t.Fatalf("resolved repo root %q is invalid: %v", repoRoot, err)
	}
}

func TestDoctorDetectsMissingCodexHookStatusMessages(t *testing.T) {
	t.Parallel()

	homeDir := t.TempDir()
	configPath := filepath.Join(homeDir, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("create codex dir: %v", err)
	}
	if err := os.WriteFile(configPath, []byte(`[features]
codex_hooks = true

[mcp_servers.kerebrom]
command = "/tmp/kerebrom"
args = ["mcp", "--tools=agent"]
`), 0o600); err != nil {
		t.Fatalf("write codex config: %v", err)
	}
	hooksPath := filepath.Join(homeDir, ".codex", "hooks.json")
	if err := os.WriteFile(hooksPath, []byte(`{
		"hooks": {
			"UserPromptSubmit": [{"hooks": [{"type": "command", "command": "/tmp/user-prompt-submit.sh"}]}],
			"Stop": [{"hooks": [{"type": "command", "command": "/tmp/session-stop.sh"}]}]
		}
	}`), 0o600); err != nil {
		t.Fatalf("write hooks: %v", err)
	}

	var report doctorReport
	runAgentConfigDoctorChecks(homeDir, &report, "")

	for _, check := range report.Checks {
		if check.Name == "codex hook status messages" {
			if check.Status != "WARN" || !strings.Contains(check.Detail, "Updating Kerebrom memory...") {
				t.Fatalf("unexpected hook status message check: %+v", check)
			}
			return
		}
	}
	t.Fatalf("missing codex hook status message check: %+v", report.Checks)
}

func TestDoctorDetectsMissingCodexHookSilentMode(t *testing.T) {
	t.Parallel()

	homeDir := t.TempDir()
	configPath := filepath.Join(homeDir, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("create codex dir: %v", err)
	}
	if err := os.WriteFile(configPath, []byte(`[features]
codex_hooks = true

[mcp_servers.kerebrom]
command = "/tmp/kerebrom"
args = ["mcp", "--tools=agent"]
`), 0o600); err != nil {
		t.Fatalf("write codex config: %v", err)
	}
	hooksPath := filepath.Join(homeDir, ".codex", "hooks.json")
	if err := os.WriteFile(hooksPath, []byte(`{
		"hooks": {
			"SessionStart": [{"hooks": [{"type": "command", "command": "/tmp/session-start.sh", "statusMessage": "Loading Kerebrom memory..."}]}],
			"UserPromptSubmit": [{"hooks": [{"type": "command", "command": "/tmp/user-prompt-submit.sh", "statusMessage": "Updating Kerebrom memory..."}]}],
			"Stop": [{"hooks": [{"type": "command", "command": "/tmp/session-stop.sh", "statusMessage": "Closing Kerebrom session..."}]}]
		}
	}`), 0o600); err != nil {
		t.Fatalf("write hooks: %v", err)
	}

	var report doctorReport
	runAgentConfigDoctorChecks(homeDir, &report, "")

	for _, check := range report.Checks {
		if check.Name == "codex hook silent mode" {
			if check.Status != "WARN" || !strings.Contains(check.Detail, "UserPromptSubmit/user-prompt-submit.sh") {
				t.Fatalf("unexpected hook silent mode check: %+v", check)
			}
			return
		}
	}
	t.Fatalf("missing codex hook silent mode check: %+v", report.Checks)
}

func TestDoctorDetectsMissingClaudeHookStatusMessages(t *testing.T) {
	t.Parallel()

	homeDir := t.TempDir()
	settingsPath := filepath.Join(homeDir, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatalf("create claude settings dir: %v", err)
	}
	if err := os.WriteFile(settingsPath, []byte(`{
		"permissions": {"allow": ["mcp__Kerebrom__context", "mcp__Kerebrom__remember"]},
		"hooks": {
			"UserPromptSubmit": [{"hooks": [{"type": "command", "command": "/tmp/user-prompt-submit.sh"}]}],
			"Stop": [{"hooks": [{"type": "command", "command": "/tmp/session-stop.sh"}]}]
		}
	}`), 0o600); err != nil {
		t.Fatalf("write claude settings: %v", err)
	}

	var report doctorReport
	runAgentConfigDoctorChecks(homeDir, &report, "")

	for _, check := range report.Checks {
		if check.Name == "claude hook status messages" {
			if check.Status != "WARN" || !strings.Contains(check.Detail, "Saving Kerebrom learnings...") {
				t.Fatalf("unexpected hook status message check: %+v", check)
			}
			return
		}
	}
	t.Fatalf("missing claude hook status message check: %+v", report.Checks)
}

func TestDoctorAutoAgentChecksOnlyDetectedConfigs(t *testing.T) {
	t.Parallel()

	homeDir := t.TempDir()
	desktopPath := doctorClaudeDesktopConfigPath(homeDir)
	if err := os.MkdirAll(filepath.Dir(desktopPath), 0o755); err != nil {
		t.Fatalf("create claude desktop dir: %v", err)
	}
	if err := os.WriteFile(desktopPath, []byte(`{"mcpServers":{"Kerebrom":{"command":"/tmp/kerebrom","args":["mcp","--tools=agent"]}}}`), 0o600); err != nil {
		t.Fatalf("write claude desktop config: %v", err)
	}

	var report doctorReport
	runAgentConfigDoctorChecks(homeDir, &report, "auto")

	if got := report.overallStatus(); got != "PASS" {
		t.Fatalf("auto doctor checks should pass with only detected Claude Desktop config, got %s: %+v", got, report.Checks)
	}
	for _, check := range report.Checks {
		if strings.HasPrefix(check.Name, "codex ") || strings.HasPrefix(check.Name, "claude code ") {
			t.Fatalf("auto doctor should not check missing non-detected client %q: %+v", check.Name, report.Checks)
		}
	}
}

func TestDoctorExplicitAgentChecksRequestedTarget(t *testing.T) {
	t.Parallel()

	homeDir := t.TempDir()
	var report doctorReport
	runAgentConfigDoctorChecks(homeDir, &report, "codex")

	foundCodex := false
	for _, check := range report.Checks {
		if check.Name == "codex mcp config" {
			foundCodex = true
			if check.Status != "WARN" || !strings.Contains(check.Detail, "not found") {
				t.Fatalf("explicit codex check should warn on missing config, got %+v", check)
			}
		}
		if strings.HasPrefix(check.Name, "claude ") {
			t.Fatalf("explicit codex check should not inspect Claude configs: %+v", report.Checks)
		}
	}
	if !foundCodex {
		t.Fatalf("explicit codex check did not inspect Codex config: %+v", report.Checks)
	}
}
