package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ulianbass/kerebrom/internal/config"
)

func TestRunDoctorHealPrunesHealthBackups(t *testing.T) {
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
		"--title", "doctor retention seed",
		"--content", "seed observation for retained doctor backups",
		"--project", "doctor-test",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("seed save failed: code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}

	for i := 0; i < 5; i++ {
		stdout.Reset()
		stderr.Reset()
		code = Run([]string{
			"doctor", "heal",
			"--home", homeDir,
			"--project-dir", v2Root,
			"--skip-setup",
			"--backup-retention", "3",
			"--backup-max-bytes", "0",
		}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("doctor heal iteration %d failed: code=%d stdout=%q stderr=%q", i, code, stdout.String(), stderr.String())
		}
	}

	backups, err := filepath.Glob(filepath.Join(dataDir, "backups", "health", "*.db"))
	if err != nil {
		t.Fatalf("glob health backups: %v", err)
	}
	if len(backups) != 3 {
		t.Fatalf("health backup count=%d, want 3: %v", len(backups), backups)
	}
	for _, backup := range backups {
		info, err := os.Stat(backup)
		if err != nil {
			t.Fatalf("stat retained backup %s: %v", backup, err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("backup %s mode=%#o, want 0600", backup, got)
		}
	}
}

func TestRunDoctorWatchOnceUsesBackupRetention(t *testing.T) {
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
		"--title", "doctor watch retention seed",
		"--content", "seed observation for doctor watch backup retention",
		"--project", "doctor-test",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("seed save failed: code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}

	for i := 0; i < 4; i++ {
		stdout.Reset()
		stderr.Reset()
		code = Run([]string{
			"doctor", "watch",
			"--once",
			"--home", homeDir,
			"--project-dir", v2Root,
			"--skip-setup",
			"--backup-retention", "2",
			"--backup-max-bytes", "0",
		}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("doctor watch iteration %d failed: code=%d stdout=%q stderr=%q", i, code, stdout.String(), stderr.String())
		}
	}

	backups, err := filepath.Glob(filepath.Join(dataDir, "backups", "health", "*.db"))
	if err != nil {
		t.Fatalf("glob health backups: %v", err)
	}
	if len(backups) != 2 {
		t.Fatalf("health backup count after watch=%d, want 2: %v", len(backups), backups)
	}
}

func TestAcquireDoctorLockRecoversStaleDeadOwner(t *testing.T) {
	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "kerebrom.db")
	lockPath := filepath.Join(dataDir, "doctor.lock")
	if err := os.Mkdir(lockPath, 0o700); err != nil {
		t.Fatalf("create stale doctor lock: %v", err)
	}
	startedAt := time.Now().UTC().Add(-defaultDoctorLockStaleAfter - time.Minute).Format(time.RFC3339)
	if err := os.WriteFile(filepath.Join(lockPath, "owner"), []byte("pid=999999\nstarted_at="+startedAt+"\n"), 0o600); err != nil {
		t.Fatalf("write stale lock owner: %v", err)
	}

	got, unlock, err := acquireDoctorLock(dbPath)
	if err != nil {
		t.Fatalf("acquire should recover stale lock: %v", err)
	}
	defer unlock()
	if got != lockPath {
		t.Fatalf("lock path=%q, want %q", got, lockPath)
	}
	raw, err := os.ReadFile(filepath.Join(lockPath, "owner"))
	if err != nil {
		t.Fatalf("read recovered lock owner: %v", err)
	}
	if strings.Contains(string(raw), "pid=999999") {
		t.Fatalf("stale owner was not replaced: %q", string(raw))
	}
}

func TestAcquireDoctorLockBlocksActiveOwner(t *testing.T) {
	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "kerebrom.db")

	_, unlock, err := acquireDoctorLock(dbPath)
	if err != nil {
		t.Fatalf("first lock acquire failed: %v", err)
	}
	defer unlock()

	_, _, err = acquireDoctorLock(dbPath)
	if err == nil {
		t.Fatal("second lock acquire succeeded while first lock was active")
	}
	if !strings.Contains(err.Error(), "Doctor Health Mode run is active") {
		t.Fatalf("unexpected active lock error: %v", err)
	}
}
