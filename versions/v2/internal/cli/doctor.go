package cli

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/ulianbass/kerebrom/internal/config"
	"github.com/ulianbass/kerebrom/internal/setup"
	"github.com/ulianbass/kerebrom/internal/store/sqlite"
	"github.com/ulianbass/kerebrom/internal/version"
)

type doctorReport struct {
	Status     string        `json:"status"`
	Version    string        `json:"version"`
	DBPath     string        `json:"db_path"`
	ProjectDir string        `json:"project_dir,omitempty"`
	Checks     []doctorCheck `json:"checks"`
}

type doctorCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

type doctorHealResult struct {
	Status     string             `json:"status"`
	Mode       string             `json:"mode"`
	Version    string             `json:"version"`
	DBPath     string             `json:"db_path"`
	ProjectDir string             `json:"project_dir,omitempty"`
	BackupPath string             `json:"backup_path,omitempty"`
	Actions    []doctorHealAction `json:"actions"`
	Report     doctorReport       `json:"report"`
}

type doctorHealAction struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

type requiredCodexHookCommand struct {
	Event  string
	Script string
}

type requiredCodexHookStatusMessage struct {
	Event    string
	Script   string
	Messages []string
}

const (
	defaultDoctorBackupRetentionCount = 24
	defaultDoctorBackupMaxBytes       = int64(1 << 30)
	defaultDoctorLockStaleAfter       = 6 * time.Hour
)

type doctorBackupPolicy struct {
	RetentionCount int
	MaxBytes       int64
}

type doctorLockOwner struct {
	PID       int
	StartedAt time.Time
}

var requiredCodexHookStatusMessages = []requiredCodexHookStatusMessage{
	{
		Event:  "SessionStart",
		Script: "session-start.sh",
		Messages: []string{
			"Loading Kerebrom memory...",
			"Cargando memoria de Kerebrom...",
		},
	},
	{
		Event:  "UserPromptSubmit",
		Script: "user-prompt-submit.sh",
		Messages: []string{
			"Updating Kerebrom memory...",
			"Actualizando memoria de Kerebrom...",
			"Guardando prompt en Kerebrom...",
		},
	},
	{
		Event:  "Stop",
		Script: "session-stop.sh",
		Messages: []string{
			"Closing Kerebrom session...",
			"Cerrando sesion de Kerebrom...",
			"Cerrando sesión de Kerebrom...",
		},
	},
}

var requiredCodexSilentHookCommands = []requiredCodexHookCommand{
	{Event: "SessionStart", Script: "session-start.sh"},
	{Event: "UserPromptSubmit", Script: "user-prompt-submit.sh"},
	{Event: "Stop", Script: "session-stop.sh"},
}

var requiredClaudeHookStatusMessages = []string{
	"Loading Kerebrom memory...",
	"Recovering Kerebrom context...",
	"Updating Kerebrom memory...",
	"Saving Kerebrom learnings...",
	"Closing Kerebrom session...",
}

func runDoctor(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		command := strings.ToLower(strings.TrimSpace(args[0]))
		rest := args[1:]
		switch command {
		case "status", "check":
			return runDoctorCheck(ensureDoctorBoolFlag(rest, "--deep"), stdout, stderr, "Doctor")
		case "report":
			return runDoctorCheck(ensureDoctorBoolFlag(ensureDoctorBoolFlag(rest, "--deep"), "--json"), stdout, stderr, "Doctor")
		case "heal", "health":
			return runDoctorHeal(rest, stdout, stderr)
		case "watch":
			return runDoctorWatch(rest, stdout, stderr)
		case "help", "--help", "-h":
			writeDoctorHelp(stdout)
			return 0
		default:
			fmt.Fprintf(stderr, "unknown doctor command %q\n\n", args[0])
			writeDoctorHelp(stderr)
			return 2
		}
	}

	return runDoctorCheck(args, stdout, stderr, "doctor")
}

func runDoctorCheck(args []string, stdout, stderr io.Writer, label string) int {
	fs := newFlagSet("doctor", stderr)
	deep := fs.Bool("deep", false, "Run factory, vehicle, runtime, and agent configuration checks")
	jsonOutput := fs.Bool("json", false, "Print machine-readable JSON")
	homeDir := fs.String("home", "", "Override home directory")
	projectDir := fs.String("project-dir", "", "Override repository/factory directory")
	dbPath := fs.String("db", "", "Override SQLite database path")
	setupAgent := fs.String("setup-agent", "", "Agent setup profile to verify; defaults to detected installed configs")
	if err := fs.Parse(reorderFlagArgs(args, map[string]bool{"deep": true, "json": true})); err != nil {
		return 2
	}

	resolvedHome, resolvedProject, resolvedDB := resolveDoctorPaths(*homeDir, *projectDir, *dbPath)
	report := buildDoctorReport(context.Background(), *deep, resolvedHome, resolvedProject, resolvedDB, *setupAgent)

	return finishDoctor(report, *jsonOutput, stdout, label)
}

func resolveDoctorPaths(homeDir string, projectDir string, dbPath string) (string, string, string) {
	defaults := config.LoadDefaults()
	dbPath = strings.TrimSpace(dbPath)
	if dbPath == "" {
		dbPath = defaults.DBPath()
	}
	homeDir = strings.TrimSpace(homeDir)
	if homeDir == "" {
		if home, err := os.UserHomeDir(); err == nil {
			homeDir = home
		}
	}
	projectDir = strings.TrimSpace(projectDir)
	if projectDir == "" {
		if cwd, err := os.Getwd(); err == nil {
			projectDir = cwd
		}
	}
	return homeDir, projectDir, dbPath
}

func buildDoctorReport(ctx context.Context, deep bool, homeDir string, projectDir string, dbPath string, setupAgent string) doctorReport {
	report := doctorReport{
		Version:    version.Full(),
		DBPath:     strings.TrimSpace(dbPath),
		ProjectDir: strings.TrimSpace(projectDir),
	}

	report.addFileCheck("runtime db file", report.DBPath, false)

	store, err := sqlite.Open(sqlite.Config{Path: report.DBPath})
	if err != nil {
		report.add("sqlite open", "FAIL", err.Error())
		return report
	}
	defer func() {
		_ = store.Close()
	}()
	preRepairStaleSessions := -1
	if count, ok := preInitStaleSessionCount(ctx, store.DB()); ok {
		preRepairStaleSessions = count
	}
	if err := store.Init(ctx); err != nil {
		report.add("sqlite init", "FAIL", err.Error())
		return report
	}
	report.add("sqlite init", "PASS", "schema loaded and runtime migrations applied")

	runStoreDoctorChecks(ctx, store, &report, preRepairStaleSessions)
	if deep {
		runVehicleDoctorChecks(strings.TrimSpace(homeDir), &report)
		runAgentConfigDoctorChecks(strings.TrimSpace(homeDir), &report, setupAgent)
		runFactoryDoctorChecks(strings.TrimSpace(projectDir), &report)
	}

	return report
}

func runDoctorHeal(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("doctor heal", stderr)
	jsonOutput := fs.Bool("json", false, "Print machine-readable JSON")
	homeDir := fs.String("home", "", "Override home directory")
	projectDir := fs.String("project-dir", "", "Override repository/factory directory")
	dbPath := fs.String("db", "", "Override SQLite database path")
	setupAgent := fs.String("setup-agent", "auto", "Agent setup profile to repair")
	binaryPath := fs.String("binary-path", "", "Binary path written into repaired agent configs")
	skipSetup := fs.Bool("skip-setup", false, "Skip AI-client setup repair")
	skipBackup := fs.Bool("skip-backup", false, "Skip the pre-heal SQLite backup")
	backupRetention := fs.Int("backup-retention", defaultDoctorBackupRetentionCount, "Number of Health Mode SQLite backups to retain")
	backupMaxBytes := fs.Int64("backup-max-bytes", defaultDoctorBackupMaxBytes, "Maximum total bytes for retained Health Mode backups; 0 disables the byte cap")
	if err := fs.Parse(reorderFlagArgs(args, map[string]bool{"json": true, "skip-setup": true, "skip-backup": true})); err != nil {
		return 2
	}
	if *backupRetention < 1 {
		fmt.Fprintln(stderr, "doctor heal backup-retention must be at least 1")
		return 2
	}
	if *backupMaxBytes < 0 {
		fmt.Fprintln(stderr, "doctor heal backup-max-bytes must be 0 or greater")
		return 2
	}

	resolvedHome, resolvedProject, resolvedDB := resolveDoctorPaths(*homeDir, *projectDir, *dbPath)
	result := doctorHealResult{
		Mode:       "Health Mode",
		Version:    version.Full(),
		DBPath:     resolvedDB,
		ProjectDir: resolvedProject,
	}
	ctx := context.Background()

	lockPath, unlock, err := acquireDoctorLock(resolvedDB)
	if err != nil {
		result.addAction("single authority lock", "FAIL", err.Error())
		return finishDoctorHeal(result, *jsonOutput, stdout)
	}
	defer unlock()
	result.addAction("single authority lock", "PASS", lockPath)

	if *skipBackup {
		result.addAction("sqlite backup", "SKIP", "disabled by --skip-backup")
	} else {
		backupPath, pruned, err := createDoctorSQLiteBackup(ctx, resolvedDB, doctorBackupPolicy{
			RetentionCount: *backupRetention,
			MaxBytes:       *backupMaxBytes,
		})
		if err != nil {
			result.addAction("sqlite backup", "FAIL", err.Error())
			return finishDoctorHeal(result, *jsonOutput, stdout)
		}
		if backupPath == "" {
			result.addAction("sqlite backup", "SKIP", "runtime database does not exist yet")
		} else {
			result.BackupPath = backupPath
			detail := backupPath
			if pruned > 0 {
				detail = fmt.Sprintf("%s (pruned %d old backup(s))", backupPath, pruned)
			}
			result.addAction("sqlite backup", "PASS", detail)
		}
	}

	if err := runDoctorRuntimeRepair(ctx, resolvedDB); err != nil {
		result.addAction("runtime repair", "FAIL", err.Error())
		return finishDoctorHeal(result, *jsonOutput, stdout)
	}
	result.addAction("runtime repair", "PASS", "schema, lifecycle, duplicates, semantic clock, trust ledger, and file modes repaired")

	if err := runDoctorFTSRepair(ctx, resolvedDB); err != nil {
		result.addAction("fts rebuild", "FAIL", err.Error())
		return finishDoctorHeal(result, *jsonOutput, stdout)
	}
	result.addAction("fts rebuild", "PASS", "observations_fts and prompts_fts rebuilt from source tables")

	if *skipSetup {
		result.addAction("agent setup repair", "SKIP", "disabled by --skip-setup")
	} else {
		detail, err := runDoctorSetupRepair(*setupAgent, resolvedHome, resolvedProject, *binaryPath)
		if err != nil {
			result.addAction("agent setup repair", "FAIL", err.Error())
			return finishDoctorHeal(result, *jsonOutput, stdout)
		}
		result.addAction("agent setup repair", "PASS", detail)
	}

	report := buildDoctorReport(ctx, true, resolvedHome, resolvedProject, resolvedDB, *setupAgent)
	report.Status = report.overallStatus()
	result.Report = report
	return finishDoctorHeal(result, *jsonOutput, stdout)
}

func runDoctorWatch(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("doctor watch", stderr)
	interval := fs.Duration("interval", 30*time.Minute, "Health Mode interval")
	once := fs.Bool("once", false, "Run one Health Mode cycle and exit")
	jsonOutput := fs.Bool("json", false, "Print machine-readable JSON for each cycle")
	homeDir := fs.String("home", "", "Override home directory")
	projectDir := fs.String("project-dir", "", "Override repository/factory directory")
	dbPath := fs.String("db", "", "Override SQLite database path")
	setupAgent := fs.String("setup-agent", "auto", "Agent setup profile to repair")
	binaryPath := fs.String("binary-path", "", "Binary path written into repaired agent configs")
	skipSetup := fs.Bool("skip-setup", false, "Skip AI-client setup repair")
	skipBackup := fs.Bool("skip-backup", false, "Skip the pre-heal SQLite backup")
	backupRetention := fs.Int("backup-retention", defaultDoctorBackupRetentionCount, "Number of Health Mode SQLite backups to retain")
	backupMaxBytes := fs.Int64("backup-max-bytes", defaultDoctorBackupMaxBytes, "Maximum total bytes for retained Health Mode backups; 0 disables the byte cap")
	if err := fs.Parse(reorderFlagArgs(args, map[string]bool{"once": true, "json": true, "skip-setup": true, "skip-backup": true})); err != nil {
		return 2
	}
	if !*once && *interval < time.Minute {
		fmt.Fprintln(stderr, "doctor watch interval must be at least 1m")
		return 2
	}
	if *backupRetention < 1 {
		fmt.Fprintln(stderr, "doctor watch backup-retention must be at least 1")
		return 2
	}
	if *backupMaxBytes < 0 {
		fmt.Fprintln(stderr, "doctor watch backup-max-bytes must be 0 or greater")
		return 2
	}

	healArgs := []string{}
	if *jsonOutput {
		healArgs = append(healArgs, "--json")
	}
	if strings.TrimSpace(*homeDir) != "" {
		healArgs = append(healArgs, "--home", *homeDir)
	}
	if strings.TrimSpace(*projectDir) != "" {
		healArgs = append(healArgs, "--project-dir", *projectDir)
	}
	if strings.TrimSpace(*dbPath) != "" {
		healArgs = append(healArgs, "--db", *dbPath)
	}
	if strings.TrimSpace(*setupAgent) != "" {
		healArgs = append(healArgs, "--setup-agent", *setupAgent)
	}
	if strings.TrimSpace(*binaryPath) != "" {
		healArgs = append(healArgs, "--binary-path", *binaryPath)
	}
	if *skipSetup {
		healArgs = append(healArgs, "--skip-setup")
	}
	if *skipBackup {
		healArgs = append(healArgs, "--skip-backup")
	} else {
		healArgs = append(healArgs, "--backup-retention", strconv.Itoa(*backupRetention))
		healArgs = append(healArgs, "--backup-max-bytes", strconv.FormatInt(*backupMaxBytes, 10))
	}

	for {
		code := runDoctorHeal(healArgs, stdout, stderr)
		if *once {
			return code
		}
		if code != 0 {
			fmt.Fprintf(stderr, "doctor watch cycle failed with exit code %d\n", code)
		}
		time.Sleep(*interval)
	}
}

func ensureDoctorBoolFlag(args []string, flag string) []string {
	if hasDoctorFlag(args, flag) {
		return args
	}
	out := make([]string, 0, len(args)+1)
	out = append(out, flag)
	out = append(out, args...)
	return out
}

func hasDoctorFlag(args []string, flag string) bool {
	name := strings.TrimPrefix(flag, "--")
	for _, arg := range args {
		if arg == "--" {
			return false
		}
		if arg == "--"+name || strings.HasPrefix(arg, "--"+name+"=") {
			return true
		}
	}
	return false
}

func writeDoctorHelp(w io.Writer) {
	fmt.Fprintf(w, "usage: kerebrom doctor [--deep] [--json] [--home PATH] [--project-dir PATH] [--db PATH] [--setup-agent %s]\n", strings.Join(setup.SupportedAgents(), "|"))
	fmt.Fprintf(w, "       kerebrom doctor status [--json] [--home PATH] [--project-dir PATH] [--db PATH] [--setup-agent %s]\n", strings.Join(setup.SupportedAgents(), "|"))
	fmt.Fprintf(w, "       kerebrom doctor report [--json] [--home PATH] [--project-dir PATH] [--db PATH] [--setup-agent %s]\n", strings.Join(setup.SupportedAgents(), "|"))
	fmt.Fprintf(w, "       kerebrom doctor heal [--json] [--home PATH] [--project-dir PATH] [--db PATH] [--setup-agent %s] [--binary-path PATH] [--skip-setup] [--skip-backup] [--backup-retention N] [--backup-max-bytes N]\n", strings.Join(setup.SupportedAgents(), "|"))
	fmt.Fprintln(w, "       kerebrom doctor watch [--interval 30m] [--once] [heal flags]")
}

func acquireDoctorLock(dbPath string) (string, func(), error) {
	baseDir := sqliteDataDirForDoctor(dbPath)
	if baseDir == "" {
		return "", func() {}, fmt.Errorf("db directory unavailable")
	}
	if err := os.MkdirAll(baseDir, 0o700); err != nil {
		return "", func() {}, fmt.Errorf("create doctor lock directory parent: %w", err)
	}
	lockPath := filepath.Join(baseDir, "doctor.lock")
	if err := os.Mkdir(lockPath, 0o700); err != nil {
		if os.IsExist(err) {
			recovered, detail, recoverErr := recoverStaleDoctorLock(lockPath, time.Now().UTC())
			if recoverErr != nil {
				return "", func() {}, recoverErr
			}
			if !recovered {
				return "", func() {}, fmt.Errorf("another Kerebrom Doctor Health Mode run is active: %s (%s)", lockPath, detail)
			}
			if err := os.Mkdir(lockPath, 0o700); err != nil {
				if os.IsExist(err) {
					return "", func() {}, fmt.Errorf("another Kerebrom Doctor Health Mode run became active: %s", lockPath)
				}
				return "", func() {}, fmt.Errorf("create doctor lock after stale recovery: %w", err)
			}
		} else {
			return "", func() {}, fmt.Errorf("create doctor lock: %w", err)
		}
	}
	meta := []byte(fmt.Sprintf("pid=%d\nstarted_at=%s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339)))
	_ = os.WriteFile(filepath.Join(lockPath, "owner"), meta, 0o600)
	unlock := func() {
		_ = os.Remove(filepath.Join(lockPath, "owner"))
		_ = os.Remove(lockPath)
	}
	return lockPath, unlock, nil
}

func recoverStaleDoctorLock(lockPath string, now time.Time) (bool, string, error) {
	recoverable, detail := doctorLockRecoverable(lockPath, now)
	if !recoverable {
		return false, detail, nil
	}
	stalePath := fmt.Sprintf("%s.stale-%s-%d", lockPath, now.Format("20060102T150405Z"), os.Getpid())
	if err := os.Rename(lockPath, stalePath); err != nil {
		if os.IsNotExist(err) {
			return true, detail, nil
		}
		return false, detail, fmt.Errorf("recover stale doctor lock: %w", err)
	}
	if err := os.RemoveAll(stalePath); err != nil {
		return false, detail, fmt.Errorf("remove stale doctor lock: %w", err)
	}
	return true, detail, nil
}

func doctorLockRecoverable(lockPath string, now time.Time) (bool, string) {
	owner, err := readDoctorLockOwner(lockPath)
	if err == nil {
		if !owner.StartedAt.IsZero() {
			age := now.Sub(owner.StartedAt)
			if age >= defaultDoctorLockStaleAfter {
				return true, fmt.Sprintf("owner age %s exceeds %s", age.Round(time.Second), defaultDoctorLockStaleAfter)
			}
			if age < 0 {
				return false, "owner started_at is in the future"
			}
		}
		if owner.PID > 0 {
			alive, known := doctorProcessAlive(owner.PID)
			if known && !alive {
				return true, fmt.Sprintf("owner pid %d is not running", owner.PID)
			}
			if known && alive {
				return false, fmt.Sprintf("owner pid %d is still running", owner.PID)
			}
		}
		if !owner.StartedAt.IsZero() {
			age := now.Sub(owner.StartedAt)
			return false, fmt.Sprintf("owner age %s is below stale threshold %s", age.Round(time.Second), defaultDoctorLockStaleAfter)
		}
	}

	info, statErr := os.Stat(lockPath)
	if statErr != nil {
		if os.IsNotExist(statErr) {
			return true, "lock disappeared before recovery"
		}
		return false, fmt.Sprintf("lock metadata unavailable: %v", statErr)
	}
	age := now.Sub(info.ModTime())
	if age >= defaultDoctorLockStaleAfter {
		return true, fmt.Sprintf("lock directory age %s exceeds %s", age.Round(time.Second), defaultDoctorLockStaleAfter)
	}
	if err != nil {
		return false, fmt.Sprintf("owner metadata unavailable and lock age %s is below stale threshold %s: %v", age.Round(time.Second), defaultDoctorLockStaleAfter, err)
	}
	return false, fmt.Sprintf("lock age %s is below stale threshold %s", age.Round(time.Second), defaultDoctorLockStaleAfter)
}

func readDoctorLockOwner(lockPath string) (doctorLockOwner, error) {
	raw, err := os.ReadFile(filepath.Join(lockPath, "owner"))
	if err != nil {
		return doctorLockOwner{}, err
	}
	var owner doctorLockOwner
	for _, line := range strings.Split(string(raw), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch key {
		case "pid":
			pid, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil {
				return doctorLockOwner{}, fmt.Errorf("parse doctor lock pid: %w", err)
			}
			owner.PID = pid
		case "started_at":
			startedAt, err := time.Parse(time.RFC3339, strings.TrimSpace(value))
			if err != nil {
				return doctorLockOwner{}, fmt.Errorf("parse doctor lock started_at: %w", err)
			}
			owner.StartedAt = startedAt.UTC()
		}
	}
	return owner, nil
}

func doctorProcessAlive(pid int) (bool, bool) {
	if pid <= 0 {
		return false, false
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return false, false
	}
	err = process.Signal(syscall.Signal(0))
	if err == nil {
		return true, true
	}
	if errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH) {
		return false, true
	}
	if errors.Is(err, syscall.EPERM) {
		return true, true
	}
	return false, false
}

func createDoctorSQLiteBackup(ctx context.Context, dbPath string, policy doctorBackupPolicy) (string, int, error) {
	if strings.TrimSpace(dbPath) == "" {
		return "", 0, fmt.Errorf("db path is required")
	}
	if _, err := os.Stat(dbPath); err != nil {
		if os.IsNotExist(err) {
			return "", 0, nil
		}
		return "", 0, fmt.Errorf("inspect runtime database: %w", err)
	}
	backupDir := filepath.Join(sqliteDataDirForDoctor(dbPath), "backups", "health")
	if err := os.MkdirAll(backupDir, 0o700); err != nil {
		return "", 0, fmt.Errorf("create backup directory: %w", err)
	}
	if err := os.Chmod(backupDir, 0o700); err != nil {
		return "", 0, fmt.Errorf("secure backup directory: %w", err)
	}

	backupPath, err := nextDoctorBackupPath(backupDir)
	if err != nil {
		return "", 0, err
	}
	store, err := sqlite.Open(sqlite.Config{Path: dbPath})
	if err != nil {
		return "", 0, fmt.Errorf("open runtime database for backup: %w", err)
	}
	defer func() {
		_ = store.Close()
	}()
	if _, err := store.DB().ExecContext(ctx, "VACUUM INTO "+sqliteStringLiteral(backupPath)); err != nil {
		_ = os.Remove(backupPath)
		return "", 0, fmt.Errorf("create SQLite snapshot backup: %w", err)
	}
	if err := os.Chmod(backupPath, 0o600); err != nil {
		return "", 0, fmt.Errorf("secure backup file: %w", err)
	}
	pruned, err := pruneDoctorBackups(backupDir, backupPath, policy)
	if err != nil {
		return "", 0, err
	}
	return backupPath, pruned, nil
}

func nextDoctorBackupPath(backupDir string) (string, error) {
	stamp := time.Now().UTC().Format("20060102T150405Z")
	for i := 0; i < 100; i++ {
		name := fmt.Sprintf("kerebrom-health-%s.db", stamp)
		if i > 0 {
			name = fmt.Sprintf("kerebrom-health-%s-%02d.db", stamp, i)
		}
		path := filepath.Join(backupDir, name)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return path, nil
		} else if err != nil {
			return "", fmt.Errorf("inspect backup path: %w", err)
		}
	}
	return "", fmt.Errorf("could not allocate unique backup path in %s", backupDir)
}

func pruneDoctorBackups(backupDir string, currentPath string, policy doctorBackupPolicy) (int, error) {
	if policy.RetentionCount < 1 {
		policy.RetentionCount = defaultDoctorBackupRetentionCount
	}
	if policy.MaxBytes < 0 {
		policy.MaxBytes = defaultDoctorBackupMaxBytes
	}

	entries, err := os.ReadDir(backupDir)
	if err != nil {
		return 0, fmt.Errorf("list health backups: %w", err)
	}
	type backupFile struct {
		Path    string
		ModTime time.Time
		Size    int64
	}
	backups := make([]backupFile, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, "kerebrom-health-") || !strings.HasSuffix(name, ".db") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return 0, fmt.Errorf("inspect health backup %s: %w", name, err)
		}
		backups = append(backups, backupFile{
			Path:    filepath.Join(backupDir, name),
			ModTime: info.ModTime(),
			Size:    info.Size(),
		})
	}

	currentPath = filepath.Clean(currentPath)
	sort.Slice(backups, func(i, j int) bool {
		left := filepath.Clean(backups[i].Path)
		right := filepath.Clean(backups[j].Path)
		if left == currentPath {
			return true
		}
		if right == currentPath {
			return false
		}
		if backups[i].ModTime.Equal(backups[j].ModTime) {
			return backups[i].Path > backups[j].Path
		}
		return backups[i].ModTime.After(backups[j].ModTime)
	})

	var kept int
	var totalBytes int64
	var pruned int
	for _, backup := range backups {
		keep := kept < policy.RetentionCount
		if keep && policy.MaxBytes > 0 && kept > 0 && totalBytes+backup.Size > policy.MaxBytes {
			keep = false
		}
		if keep {
			kept++
			totalBytes += backup.Size
			continue
		}
		if err := os.Remove(backup.Path); err != nil && !os.IsNotExist(err) {
			return pruned, fmt.Errorf("prune health backup %s: %w", backup.Path, err)
		}
		pruned++
	}
	return pruned, nil
}

func sqliteStringLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func runDoctorRuntimeRepair(ctx context.Context, dbPath string) error {
	store, err := sqlite.Open(sqlite.Config{Path: dbPath})
	if err != nil {
		return fmt.Errorf("open runtime database: %w", err)
	}
	defer func() {
		_ = store.Close()
	}()
	if err := store.Init(ctx); err != nil {
		return fmt.Errorf("sqlite init repair: %w", err)
	}
	return nil
}

func runDoctorFTSRepair(ctx context.Context, dbPath string) error {
	store, err := sqlite.Open(sqlite.Config{Path: dbPath})
	if err != nil {
		return fmt.Errorf("open runtime database for FTS repair: %w", err)
	}
	defer func() {
		_ = store.Close()
	}()
	if err := store.Init(ctx); err != nil {
		return fmt.Errorf("sqlite init before FTS repair: %w", err)
	}
	for _, table := range []string{"observations_fts", "prompts_fts"} {
		exists, err := tableExists(ctx, store.DB(), table)
		if err != nil {
			return err
		}
		if !exists {
			continue
		}
		query := fmt.Sprintf("INSERT INTO %s(%s) VALUES('rebuild')", table, table)
		if _, err := store.DB().ExecContext(ctx, query); err != nil {
			return fmt.Errorf("rebuild %s: %w", table, err)
		}
	}
	return nil
}

func runFTSIntegrityDoctorCheck(ctx context.Context, db *sql.DB, report *doctorReport, table string) {
	exists, err := tableExists(ctx, db, table)
	if err != nil {
		report.add(table+" integrity", "FAIL", err.Error())
		return
	}
	if !exists {
		report.add(table+" integrity", "FAIL", "missing table")
		return
	}
	query := fmt.Sprintf("INSERT INTO %s(%s, rank) VALUES('integrity-check', 1)", table, table)
	if _, err := db.ExecContext(ctx, query); err != nil {
		report.add(table+" integrity", "FAIL", err.Error())
		return
	}
	report.add(table+" integrity", "PASS", "integrity-check clean")
}

func runDoctorSetupRepair(agent string, homeDir string, projectDir string, binaryPath string) (string, error) {
	agent = strings.TrimSpace(agent)
	if agent == "" {
		agent = "auto"
	}
	resolvedBinaryPath, err := resolveDoctorBinaryPath(homeDir, binaryPath)
	if err != nil {
		return "", err
	}
	result, err := setup.Run(agent, setup.Options{
		HomeDir:    strings.TrimSpace(homeDir),
		ProjectDir: strings.TrimSpace(projectDir),
		BinaryPath: resolvedBinaryPath,
	})
	if err != nil {
		return "", fmt.Errorf("repair %s setup: %w", agent, err)
	}
	return fmt.Sprintf("configured %s (%d file(s))", result.Agent, len(result.Files)), nil
}

func resolveDoctorBinaryPath(homeDir string, binaryPath string) (string, error) {
	binaryPath = strings.TrimSpace(binaryPath)
	if binaryPath != "" {
		return binaryPath, nil
	}
	candidates := []string{}
	if strings.TrimSpace(homeDir) != "" {
		candidates = append(candidates,
			filepath.Join(homeDir, "local", "bin", "kerebrom"),
			filepath.Join(homeDir, ".local", "bin", "kerebrom"),
		)
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	if path, err := stableExecutablePath(); err == nil {
		return path, nil
	}
	return "", fmt.Errorf("could not resolve kerebrom binary path; pass --binary-path")
}

func sqliteDataDirForDoctor(dbPath string) string {
	dir := filepath.Dir(strings.TrimSpace(dbPath))
	if dir == "" || dir == "." || dir == string(filepath.Separator) {
		return ""
	}
	return dir
}

func runStoreDoctorChecks(ctx context.Context, store *sqlite.Store, report *doctorReport, preRepairStaleSessions int) {
	if value, err := scalarString(ctx, store.DB(), `PRAGMA integrity_check`); err != nil {
		report.add("sqlite integrity", "FAIL", err.Error())
	} else if value != "ok" {
		report.add("sqlite integrity", "FAIL", value)
	} else {
		report.add("sqlite integrity", "PASS", "integrity_check=ok")
	}

	if count, err := foreignKeyIssueCount(ctx, store.DB()); err != nil {
		report.add("sqlite foreign keys", "FAIL", err.Error())
	} else if count > 0 {
		report.add("sqlite foreign keys", "FAIL", fmt.Sprintf("%d foreign key issue(s)", count))
	} else {
		report.add("sqlite foreign keys", "PASS", "foreign_key_check clean")
	}

	for _, table := range []string{"sessions", "observations", "observation_events", "observations_fts", "user_prompts", "prompts_fts", "project_aliases", "sync_chunks"} {
		if exists, err := tableExists(ctx, store.DB(), table); err != nil {
			report.add("schema "+table, "FAIL", err.Error())
		} else if !exists {
			report.add("schema "+table, "FAIL", "missing table")
		} else {
			report.add("schema "+table, "PASS", "present")
		}
	}

	if stats, err := store.Stats(ctx, ""); err != nil {
		report.add("runtime stats", "FAIL", err.Error())
	} else {
		report.add("runtime stats", "PASS", fmt.Sprintf("sessions=%d active=%d observations=%d prompts=%d projects=%d", stats.SessionCount, stats.ActiveSessionCount, stats.ObservationCount, stats.PromptCount, stats.ProjectCount))
	}

	if count, err := scalarInt(ctx, store.DB(), `SELECT COUNT(*) FROM observations WHERE trim(COALESCE(valid_at, '')) = ''`); err != nil {
		report.add("semantic clock", "FAIL", err.Error())
	} else if count > 0 {
		report.add("semantic clock", "FAIL", fmt.Sprintf("%d observation(s) missing valid_at", count))
	} else {
		report.add("semantic clock", "PASS", "all observations have valid_at")
	}

	if count, err := scalarInt(ctx, store.DB(), `
		SELECT COUNT(*)
		FROM observations o
		WHERE NOT EXISTS (
			SELECT 1 FROM observation_events e WHERE e.observation_id = o.id
		)
	`); err != nil {
		report.add("trust ledger coverage", "FAIL", err.Error())
	} else if count > 0 {
		report.add("trust ledger coverage", "FAIL", fmt.Sprintf("%d observation(s) without ledger events", count))
	} else {
		report.add("trust ledger coverage", "PASS", "every observation has at least one ledger event")
	}

	if count, err := store.ObservationEventCount(ctx); err != nil {
		report.add("trust ledger count", "FAIL", err.Error())
	} else {
		report.add("trust ledger count", "PASS", fmt.Sprintf("events=%d", count))
	}

	if count, err := scalarInt(ctx, store.DB(), `
		SELECT COUNT(*)
		FROM (
			SELECT normalized_hash
			FROM observations
			WHERE deleted_at IS NULL AND normalized_hash != ''
			GROUP BY normalized_hash
			HAVING COUNT(*) > 1
		)
	`); err != nil {
		report.add("active duplicate observations", "FAIL", err.Error())
	} else if count > 0 {
		report.add("active duplicate observations", "FAIL", fmt.Sprintf("%d duplicate hash group(s)", count))
	} else {
		report.add("active duplicate observations", "PASS", "no active duplicate normalized hashes")
	}

	if count, err := scalarInt(ctx, store.DB(), `SELECT COUNT(*) FROM observations_fts`); err != nil {
		report.add("observations fts", "FAIL", err.Error())
	} else {
		report.add("observations fts", "PASS", fmt.Sprintf("fts_rows=%d", count))
	}
	runFTSIntegrityDoctorCheck(ctx, store.DB(), report, "observations_fts")

	if count, err := scalarInt(ctx, store.DB(), `SELECT COUNT(*) FROM prompts_fts`); err != nil {
		report.add("prompts fts", "FAIL", err.Error())
	} else {
		report.add("prompts fts", "PASS", fmt.Sprintf("fts_rows=%d", count))
	}
	runFTSIntegrityDoctorCheck(ctx, store.DB(), report, "prompts_fts")

	if preRepairStaleSessions > 0 {
		report.add("stale active sessions pre-repair", "WARN", fmt.Sprintf("%d stale active session(s) were auto-closed during sqlite init", preRepairStaleSessions))
	} else if preRepairStaleSessions == 0 {
		report.add("stale active sessions pre-repair", "PASS", "no stale active sessions before repair")
	}

	cutoff := time.Now().UTC().Add(-24 * time.Hour).Format(time.RFC3339)
	if count, err := scalarInt(ctx, store.DB(), `
		SELECT COUNT(*)
		FROM sessions
		WHERE status = 'active'
		  AND (
			SELECT MAX(activity_at)
			FROM (
				SELECT sessions.started_at AS activity_at
				UNION ALL
				SELECT observations.updated_at AS activity_at
				FROM observations
				WHERE observations.deleted_at IS NULL
				  AND COALESCE(observations.session_id, '') = sessions.id
				UNION ALL
				SELECT user_prompts.created_at AS activity_at
				FROM user_prompts
				WHERE COALESCE(user_prompts.session_id, '') = sessions.id
			)
		  ) < ?
	`, cutoff); err != nil {
		report.add("stale active sessions", "FAIL", err.Error())
	} else if count > 0 {
		report.add("stale active sessions", "WARN", fmt.Sprintf("%d active session(s) older than 24h", count))
	} else {
		report.add("stale active sessions", "PASS", "no stale active sessions")
	}

	if aliases, err := store.ListProjectAliases(ctx); err != nil {
		report.add("project aliases", "FAIL", err.Error())
	} else {
		status := "PASS"
		detail := fmt.Sprintf("aliases=%d", len(aliases))
		for _, alias := range aliases {
			if _, err := store.ResolveProject(ctx, alias.Alias); err != nil {
				status = "FAIL"
				detail = err.Error()
				break
			}
		}
		report.add("project aliases", status, detail)
	}
}

func preInitStaleSessionCount(ctx context.Context, db *sql.DB) (int, bool) {
	exists, err := tableExists(ctx, db, "sessions")
	if err != nil || !exists {
		return 0, false
	}
	cutoff := time.Now().UTC().Add(-24 * time.Hour).Format(time.RFC3339)
	count, err := scalarInt(ctx, db, `
		SELECT COUNT(*)
		FROM sessions
		WHERE status = 'active'
		  AND (
			SELECT MAX(activity_at)
			FROM (
				SELECT sessions.started_at AS activity_at
				UNION ALL
				SELECT observations.updated_at AS activity_at
				FROM observations
				WHERE observations.deleted_at IS NULL
				  AND COALESCE(observations.session_id, '') = sessions.id
				UNION ALL
				SELECT user_prompts.created_at AS activity_at
				FROM user_prompts
				WHERE COALESCE(user_prompts.session_id, '') = sessions.id
			)
		  ) < ?
	`, cutoff)
	if err != nil {
		return 0, false
	}
	return count, true
}

func runVehicleDoctorChecks(homeDir string, report *doctorReport) {
	report.add("running binary", "PASS", version.Full())
	if homeDir == "" {
		report.add("home directory", "WARN", "could not resolve home directory")
		return
	}
	installed := filepath.Join(homeDir, "local", "bin", "kerebrom")
	linked := filepath.Join(homeDir, ".local", "bin", "kerebrom")
	report.addFileCheck("installed binary", installed, false)
	report.addFileCheck("binary symlink", linked, false)
	if target, err := os.Readlink(linked); err == nil {
		status := "PASS"
		if target != installed {
			status = "WARN"
		}
		report.add("binary symlink target", status, target)
	}
}

type doctorAgentTargets struct {
	Codex         bool
	ClaudeCode    bool
	ClaudeDesktop bool
	GeminiCLI     bool
	OpenCode      bool
	Cursor        bool
	Windsurf      bool
	VSCode        bool
}

func (t doctorAgentTargets) any() bool {
	return t.Codex || t.ClaudeCode || t.ClaudeDesktop || t.GeminiCLI || t.OpenCode || t.Cursor || t.Windsurf || t.VSCode
}

func runAgentConfigDoctorChecks(homeDir string, report *doctorReport, setupAgent string) {
	if homeDir == "" {
		report.add("agent configs", "WARN", "home directory unavailable")
		return
	}
	targets := resolveDoctorAgentTargets(homeDir, setupAgent)
	if !targets.any() {
		report.add("agent configs", "SKIP", "no installed or targeted AI client configs detected")
		return
	}

	if targets.Codex {
		runCodexDoctorChecks(homeDir, report)
	}
	if targets.ClaudeCode {
		runClaudeCodeDoctorChecks(homeDir, report)
	}
	if targets.ClaudeDesktop {
		runClaudeDesktopDoctorChecks(homeDir, report)
	}
	if targets.GeminiCLI {
		runGeminiDoctorChecks(homeDir, report)
	}
	if targets.OpenCode {
		runOpenCodeDoctorChecks(homeDir, report)
	}
	if targets.Cursor {
		runCursorDoctorChecks(homeDir, report)
	}
	if targets.Windsurf {
		runWindsurfDoctorChecks(homeDir, report)
	}
	if targets.VSCode {
		runVSCodeDoctorChecks(homeDir, report)
	}
}

func runCodexDoctorChecks(homeDir string, report *doctorReport) {
	codexConfigPath := filepath.Join(homeDir, ".codex", "config.toml")
	checkContains(report, "codex mcp config", codexConfigPath, []string{"mcp_servers.kerebrom", "kerebrom", "mcp"})
	checkContains(report, "codex hooks enabled", codexConfigPath, []string{"codex_hooks = true"})
	codexHooksPath := filepath.Join(homeDir, ".codex", "hooks.json")
	checkCodexHookStatusMessages(report, "codex hook status messages", codexHooksPath)
	checkCodexHookSilentMode(report, "codex hook silent mode", codexHooksPath)
	checkNoContains(report, "codex user preferences clean", filepath.Join(homeDir, ".codex", "AGENTS.md"), []string{"KEREBROM:START"})
}

func runClaudeCodeDoctorChecks(homeDir string, report *doctorReport) {
	claudeSettingsPath := filepath.Join(homeDir, ".claude", "settings.json")
	checkContains(report, "claude code settings", claudeSettingsPath, []string{"mcp__Kerebrom__context", "mcp__Kerebrom__remember"})
	checkContains(report, "claude hook status messages", claudeSettingsPath, requiredClaudeHookStatusMessages)
	checkNoContains(report, "claude global instructions clean", filepath.Join(homeDir, ".claude", "CLAUDE.md"), []string{"KEREBROM:START"})
}

func runClaudeDesktopDoctorChecks(homeDir string, report *doctorReport) {
	checkContains(report, "claude desktop mcp config", doctorClaudeDesktopConfigPath(homeDir), []string{"Kerebrom", "kerebrom", "mcp"})
}

func runGeminiDoctorChecks(homeDir string, report *doctorReport) {
	checkContains(report, "gemini mcp config", filepath.Join(homeDir, ".gemini", "settings.json"), []string{"kerebrom", "mcpServers", "mcp"})
	checkContains(report, "gemini system instructions", filepath.Join(homeDir, ".gemini", "system.md"), []string{"Kerebrom Memory", "ACTIVE PROTOCOL"})
	checkContains(report, "gemini env enables system md", filepath.Join(homeDir, ".gemini", ".env"), []string{"GEMINI_SYSTEM_MD=1"})
}

func runOpenCodeDoctorChecks(homeDir string, report *doctorReport) {
	checkContains(report, "opencode mcp config", filepath.Join(homeDir, ".config", "opencode", "opencode.json"), []string{"kerebrom", "mcp", "enabled"})
	checkContains(report, "opencode memory instructions", filepath.Join(homeDir, ".config", "opencode", "kerebrom-memory.md"), []string{"Kerebrom Memory", "ACTIVE PROTOCOL"})
}

func runCursorDoctorChecks(homeDir string, report *doctorReport) {
	checkContains(report, "cursor mcp config", filepath.Join(homeDir, ".cursor", "mcp.json"), []string{"kerebrom", "mcpServers", "mcp"})
	checkContains(report, "cursor rule", filepath.Join(homeDir, ".cursor", "rules", "kerebrom.mdc"), []string{"Kerebrom", "context", "remember"})
}

func runWindsurfDoctorChecks(homeDir string, report *doctorReport) {
	checkContains(report, "windsurf mcp config", filepath.Join(homeDir, ".codeium", "windsurf", "mcp_config.json"), []string{"kerebrom", "mcpServers", "mcp"})
	checkContains(report, "windsurf rules", filepath.Join(homeDir, ".windsurfrules"), []string{"Kerebrom Memory", "ACTIVE PROTOCOL"})
}

func runVSCodeDoctorChecks(homeDir string, report *doctorReport) {
	configDir := doctorVSCodeUserConfigDir(homeDir)
	checkContains(report, "vscode mcp config", filepath.Join(configDir, "mcp.json"), []string{"kerebrom", "servers", "mcp"})
	checkContains(report, "vscode memory instructions", filepath.Join(configDir, "prompts", "kerebrom-memory.instructions.md"), []string{"Kerebrom Memory", "ACTIVE PROTOCOL"})
}

func resolveDoctorAgentTargets(homeDir string, setupAgent string) doctorAgentTargets {
	agent := normalizeDoctorSetupAgent(setupAgent)
	switch agent {
	case "all":
		return doctorAgentTargets{Codex: true, ClaudeCode: true, ClaudeDesktop: true, GeminiCLI: true, OpenCode: true, Cursor: true, Windsurf: true, VSCode: true}
	case "codex":
		return doctorAgentTargets{Codex: true}
	case "claude":
		return doctorAgentTargets{ClaudeCode: true, ClaudeDesktop: true}
	case "claude-code":
		return doctorAgentTargets{ClaudeCode: true}
	case "claude-desktop":
		return doctorAgentTargets{ClaudeDesktop: true}
	case "gemini-cli":
		return doctorAgentTargets{GeminiCLI: true}
	case "opencode":
		return doctorAgentTargets{OpenCode: true}
	case "cursor":
		return doctorAgentTargets{Cursor: true}
	case "windsurf":
		return doctorAgentTargets{Windsurf: true}
	case "vscode":
		return doctorAgentTargets{VSCode: true}
	default:
		return detectDoctorAgentTargets(homeDir)
	}
}

func detectDoctorAgentTargets(homeDir string) doctorAgentTargets {
	return doctorAgentTargets{
		Codex: pathExistsForDoctor(filepath.Join(homeDir, ".codex", "config.toml")) ||
			pathExistsForDoctor(filepath.Join(homeDir, ".codex", "hooks.json")),
		ClaudeCode: pathExistsForDoctor(filepath.Join(homeDir, ".claude", "settings.json")) ||
			pathExistsForDoctor(filepath.Join(homeDir, ".claude", "mcp.json")),
		ClaudeDesktop: pathExistsForDoctor(doctorClaudeDesktopConfigPath(homeDir)),
		GeminiCLI: pathExistsForDoctor(filepath.Join(homeDir, ".gemini", "settings.json")) ||
			pathExistsForDoctor(filepath.Join(homeDir, ".gemini", "system.md")),
		OpenCode: pathExistsForDoctor(filepath.Join(homeDir, ".config", "opencode", "opencode.json")) ||
			pathExistsForDoctor(filepath.Join(homeDir, ".config", "opencode", "kerebrom-memory.md")),
		Cursor: pathExistsForDoctor(filepath.Join(homeDir, ".cursor", "mcp.json")) ||
			pathExistsForDoctor(filepath.Join(homeDir, ".cursor", "rules", "kerebrom.mdc")),
		Windsurf: pathExistsForDoctor(filepath.Join(homeDir, ".codeium", "windsurf", "mcp_config.json")) ||
			pathExistsForDoctor(filepath.Join(homeDir, ".windsurfrules")),
		VSCode: pathExistsForDoctor(filepath.Join(doctorVSCodeUserConfigDir(homeDir), "mcp.json")) ||
			pathExistsForDoctor(filepath.Join(doctorVSCodeUserConfigDir(homeDir), "prompts", "kerebrom-memory.instructions.md")),
	}
}

func normalizeDoctorSetupAgent(agent string) string {
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

func pathExistsForDoctor(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func doctorClaudeDesktopConfigPath(homeDir string) string {
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(homeDir, "Library", "Application Support", "Claude", "claude_desktop_config.json")
	case "windows":
		return filepath.Join(homeDir, "AppData", "Roaming", "Claude", "claude_desktop_config.json")
	default:
		return filepath.Join(homeDir, ".config", "Claude", "claude_desktop_config.json")
	}
}

func doctorVSCodeUserConfigDir(homeDir string) string {
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(homeDir, "Library", "Application Support", "Code", "User")
	case "windows":
		return filepath.Join(homeDir, "AppData", "Roaming", "Code", "User")
	default:
		return filepath.Join(homeDir, ".config", "Code", "User")
	}
}

func runFactoryDoctorChecks(projectDir string, report *doctorReport) {
	if projectDir == "" {
		report.add("factory directory", "WARN", "not provided")
		return
	}
	repoRoot, v2Root := resolveFactoryDirs(projectDir)
	report.addFileCheck("factory versions/v2", filepath.Join(v2Root, "manifest.json"), true)

	manifestPath := filepath.Join(v2Root, "manifest.json")
	versionPath := filepath.Join(v2Root, "internal", "version", "version.go")
	manifestVersion := ""
	if raw, err := os.ReadFile(manifestPath); err == nil {
		var manifest struct {
			Semver string `json:"semver"`
		}
		if err := json.Unmarshal(raw, &manifest); err == nil {
			manifestVersion = strings.TrimSpace(manifest.Semver)
		}
	}
	if raw, err := os.ReadFile(versionPath); err != nil {
		report.add("factory version alignment", "FAIL", err.Error())
	} else if manifestVersion == "" || !strings.Contains(string(raw), `Version   = "`+manifestVersion+`"`) {
		report.add("factory version alignment", "FAIL", fmt.Sprintf("manifest semver %q does not match version.go", manifestVersion))
	} else {
		report.add("factory version alignment", "PASS", manifestVersion)
	}

	publicFiles := []string{
		filepath.Join(repoRoot, "README.md"),
		filepath.Join(repoRoot, "README.es.md"),
		filepath.Join(repoRoot, "AGENTS.md"),
		filepath.Join(repoRoot, "CLAUDE.md"),
		filepath.Join(repoRoot, "docs", "AI_AGENT_INSTALL.md"),
		filepath.Join(repoRoot, "docs", "AI_AGENT_INSTALL.es.md"),
		filepath.Join(v2Root, "README.md"),
		filepath.Join(v2Root, "README.es.md"),
		filepath.Join(v2Root, "docs", "architecture-v2.md"),
		filepath.Join(v2Root, "docs", "product-spec-v2.md"),
	}
	if hits := filesContaining(publicFiles, "/Users/"); len(hits) > 0 {
		report.add("public docs private paths", "FAIL", strings.Join(hits, ", "))
	} else {
		report.add("public docs private paths", "PASS", "no /Users/ paths in public docs checked")
	}

	if _, err := os.Stat(filepath.Join(v2Root, "bin", "kerebrom")); err == nil {
		report.add("factory build artifact", "WARN", "versions/v2/bin/kerebrom exists; ignored build artifact, not release source")
	} else if os.IsNotExist(err) {
		report.add("factory build artifact", "PASS", "no ignored binary artifact present")
	} else {
		report.add("factory build artifact", "WARN", err.Error())
	}
}

func resolveFactoryDirs(projectDir string) (repoRoot string, v2Root string) {
	projectDir = filepath.Clean(strings.TrimSpace(projectDir))
	if projectDir == "" {
		return projectDir, filepath.Join(projectDir, "versions", "v2")
	}
	if projectDir == "." {
		if cwd, err := os.Getwd(); err == nil {
			projectDir = cwd
		}
	}
	if _, err := os.Stat(filepath.Join(projectDir, "manifest.json")); err == nil {
		if _, err := os.Stat(filepath.Join(projectDir, "internal", "version", "version.go")); err == nil {
			return filepath.Clean(filepath.Join(projectDir, "..", "..")), projectDir
		}
	}
	return projectDir, filepath.Join(projectDir, "versions", "v2")
}

func finishDoctor(report doctorReport, jsonOutput bool, stdout io.Writer, label string) int {
	report.Status = report.overallStatus()
	if jsonOutput {
		raw, _ := json.MarshalIndent(report, "", "  ")
		fmt.Fprintln(stdout, string(raw))
	} else {
		if strings.TrimSpace(label) == "" {
			label = "doctor"
		}
		fmt.Fprintf(stdout, "Kerebrom %s: %s\n", label, report.Status)
		fmt.Fprintf(stdout, "version: %s\n", report.Version)
		fmt.Fprintf(stdout, "db: %s\n", report.DBPath)
		if report.ProjectDir != "" {
			fmt.Fprintf(stdout, "factory: %s\n", report.ProjectDir)
		}
		for _, check := range report.Checks {
			fmt.Fprintf(stdout, "[%s] %s - %s\n", check.Status, check.Name, check.Detail)
		}
	}
	if report.Status == "FAIL" {
		return 1
	}
	return 0
}

func finishDoctorHeal(result doctorHealResult, jsonOutput bool, stdout io.Writer) int {
	if len(result.Report.Checks) > 0 {
		result.Report.Status = result.Report.overallStatus()
	}
	result.Status = result.overallStatus()
	if jsonOutput {
		raw, _ := json.MarshalIndent(result, "", "  ")
		fmt.Fprintln(stdout, string(raw))
	} else {
		fmt.Fprintf(stdout, "Kerebrom Doctor Health Mode: %s\n", result.Status)
		fmt.Fprintf(stdout, "version: %s\n", result.Version)
		fmt.Fprintf(stdout, "db: %s\n", result.DBPath)
		if result.ProjectDir != "" {
			fmt.Fprintf(stdout, "factory: %s\n", result.ProjectDir)
		}
		if result.BackupPath != "" {
			fmt.Fprintf(stdout, "backup: %s\n", result.BackupPath)
		}
		fmt.Fprintln(stdout, "actions:")
		for _, action := range result.Actions {
			fmt.Fprintf(stdout, "[%s] %s - %s\n", action.Status, action.Name, action.Detail)
		}
		if len(result.Report.Checks) > 0 {
			fmt.Fprintln(stdout, "verification:")
			for _, check := range result.Report.Checks {
				fmt.Fprintf(stdout, "[%s] %s - %s\n", check.Status, check.Name, check.Detail)
			}
		}
	}
	if result.Status == "FAIL" {
		return 1
	}
	return 0
}

func (r *doctorReport) add(name string, status string, detail string) {
	r.Checks = append(r.Checks, doctorCheck{
		Name:   name,
		Status: status,
		Detail: detail,
	})
}

func (r *doctorReport) addFileCheck(name string, path string, required bool) {
	if strings.TrimSpace(path) == "" {
		r.add(name, "WARN", "path unavailable")
		return
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) && !required {
			r.add(name, "WARN", path+" not found")
			return
		}
		r.add(name, "FAIL", err.Error())
		return
	}
	r.add(name, "PASS", path)
}

func (r doctorReport) overallStatus() string {
	status := "PASS"
	for _, check := range r.Checks {
		switch check.Status {
		case "FAIL":
			return "FAIL"
		case "WARN":
			if status != "FAIL" {
				status = "WARN"
			}
		}
	}
	return status
}

func (r *doctorHealResult) addAction(name string, status string, detail string) {
	r.Actions = append(r.Actions, doctorHealAction{
		Name:   name,
		Status: status,
		Detail: detail,
	})
}

func (r doctorHealResult) overallStatus() string {
	status := "PASS"
	for _, action := range r.Actions {
		switch action.Status {
		case "FAIL":
			return "FAIL"
		case "WARN":
			status = "WARN"
		}
	}
	if len(r.Report.Checks) > 0 {
		switch r.Report.overallStatus() {
		case "FAIL":
			return "FAIL"
		case "WARN":
			status = "WARN"
		}
	}
	return status
}

func checkContains(report *doctorReport, name string, path string, requiredNeedles []string) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			report.add(name, "WARN", path+" not found")
			return
		}
		report.add(name, "FAIL", err.Error())
		return
	}
	content := string(raw)
	missing := []string{}
	for _, needle := range requiredNeedles {
		if !strings.Contains(content, needle) {
			missing = append(missing, needle)
		}
	}
	if len(missing) > 0 {
		report.add(name, "WARN", "missing: "+strings.Join(missing, ", "))
		return
	}
	report.add(name, "PASS", path)
}

func checkCodexHookStatusMessages(report *doctorReport, name string, path string) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			report.add(name, "WARN", path+" not found")
			return
		}
		report.add(name, "FAIL", err.Error())
		return
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		report.add(name, "WARN", "invalid Codex hooks JSON: "+err.Error())
		return
	}
	hooks, ok := payload["hooks"].(map[string]any)
	if !ok {
		report.add(name, "WARN", "missing hooks map")
		return
	}
	missing := []string{}
	for _, required := range requiredCodexHookStatusMessages {
		if !codexHookCommandHasAllowedString(hooks, required.Event, required.Script, "statusMessage", required.Messages) {
			missing = append(missing, required.Event+"/"+required.Script)
		}
	}
	if len(missing) > 0 {
		report.add(name, "WARN", "missing localized statusMessage for: "+strings.Join(missing, ", "))
		return
	}
	report.add(name, "PASS", path)
}

func checkCodexHookSilentMode(report *doctorReport, name string, path string) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			report.add(name, "WARN", path+" not found")
			return
		}
		report.add(name, "FAIL", err.Error())
		return
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		report.add(name, "WARN", "invalid Codex hooks JSON: "+err.Error())
		return
	}
	hooks, ok := payload["hooks"].(map[string]any)
	if !ok {
		report.add(name, "WARN", "missing hooks map")
		return
	}
	missing := []string{}
	for _, required := range requiredCodexSilentHookCommands {
		if !codexHookCommandHasBool(hooks, required.Event, required.Script, "silent", true) {
			missing = append(missing, required.Event+"/"+required.Script)
		}
	}
	if len(missing) > 0 {
		report.add(name, "WARN", "missing silent=true for: "+strings.Join(missing, ", "))
		return
	}
	report.add(name, "PASS", path)
}

func codexHookCommandHasAllowedString(hooks map[string]any, event string, script string, field string, allowed []string) bool {
	rawEntries, ok := hooks[event].([]any)
	if !ok {
		return false
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
			value, _ := command[field].(string)
			if strings.HasSuffix(commandPath, script) && slices.Contains(allowed, value) {
				return true
			}
		}
	}
	return false
}

func codexHookCommandHasBool(hooks map[string]any, event string, script string, field string, want bool) bool {
	rawEntries, ok := hooks[event].([]any)
	if !ok {
		return false
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
			if !strings.Contains(commandPath, script) {
				continue
			}
			got, ok := command[field].(bool)
			return ok && got == want
		}
	}
	return false
}

func checkNoContains(report *doctorReport, name string, path string, forbiddenNeedles []string) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			report.add(name, "PASS", path+" not found")
			return
		}
		report.add(name, "FAIL", err.Error())
		return
	}
	content := string(raw)
	hits := []string{}
	for _, needle := range forbiddenNeedles {
		if strings.Contains(content, needle) {
			hits = append(hits, needle)
		}
	}
	if len(hits) > 0 {
		report.add(name, "WARN", "contains Kerebrom block in user-owned instructions: "+strings.Join(hits, ", "))
		return
	}
	report.add(name, "PASS", path)
}

func filesContaining(paths []string, needle string) []string {
	hits := []string{}
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if strings.Contains(string(raw), needle) {
			hits = append(hits, path)
		}
	}
	return hits
}

func scalarString(ctx context.Context, db *sql.DB, query string, args ...any) (string, error) {
	var value string
	if err := db.QueryRowContext(ctx, query, args...).Scan(&value); err != nil {
		return "", err
	}
	return value, nil
}

func scalarInt(ctx context.Context, db *sql.DB, query string, args ...any) (int, error) {
	var value int
	if err := db.QueryRowContext(ctx, query, args...).Scan(&value); err != nil {
		return 0, err
	}
	return value, nil
}

func foreignKeyIssueCount(ctx context.Context, db *sql.DB) (int, error) {
	rows, err := db.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		count++
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	return count, nil
}

func tableExists(ctx context.Context, db *sql.DB, table string) (bool, error) {
	var name string
	err := db.QueryRowContext(ctx, `
		SELECT name
		FROM sqlite_master
		WHERE type IN ('table', 'virtual table') AND name = ?
	`, table).Scan(&name)
	if err == nil {
		return true, nil
	}
	if err == sql.ErrNoRows {
		return false, nil
	}
	return false, err
}
