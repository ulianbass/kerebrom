package project

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestDetectFallsBackToDirectoryName(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Proyecto Kerebrom")

	if got := Detect(dir); got != "proyecto-kerebrom" {
		t.Fatalf("Detect() = %q, want %q", got, "proyecto-kerebrom")
	}
}

func TestDetectDoesNotTreatRootOrHomeAsStrongProjects(t *testing.T) {
	if got := Detect(filepath.VolumeName(os.TempDir()) + string(filepath.Separator)); got != "default" {
		t.Fatalf("Detect(root) = %q, want default", got)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("home directory unavailable")
	}
	if got := Detect(home); got != "default" {
		t.Fatalf("Detect(home) = %q, want default", got)
	}
}

func TestProjectLookupFilterTreatsWeakNamesAsCrossProject(t *testing.T) {
	tests := []string{"", ".", "/", "default", "home"}
	for _, input := range tests {
		if got := LookupFilter(input); got != "" {
			t.Fatalf("LookupFilter(%q) = %q, want empty cross-project filter", input, got)
		}
	}

	if got := LookupFilter("Proyecto Falage"); got != "proyecto-falage" {
		t.Fatalf("LookupFilter(strong project) = %q, want proyecto-falage", got)
	}
}

func TestDetectPrefersGitRemoteName(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "remote", "add", "origin", "git@github.com:ulianbass/kerebrom.git")

	if got := Detect(dir); got != "kerebrom" {
		t.Fatalf("Detect() = %q, want %q", got, "kerebrom")
	}
}

func TestRepoNameFromRemote(t *testing.T) {
	tests := map[string]string{
		"git@github.com:ulianbass/kerebrom.git":       "kerebrom",
		"https://github.com/ulianbass/kerebrom.git":   "kerebrom",
		"https://gitlab.com/group/subgroup/tooling":   "tooling",
		"ssh://git@example.com/team/project_name.git": "project_name",
	}

	for remote, want := range tests {
		if got := repoNameFromRemote(remote); got != want {
			t.Fatalf("repoNameFromRemote(%q) = %q, want %q", remote, got, want)
		}
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()

	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
}
