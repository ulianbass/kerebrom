package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ulianbass/kerebrom/internal/config"
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

	if !strings.Contains(stdout.String(), "v2.0.3") {
		t.Fatalf("version output missing version: %q", stdout.String())
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
