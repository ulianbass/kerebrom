package cli

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ulianbass/kerebrom/internal/config"
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

func runDoctor(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("doctor", stderr)
	deep := fs.Bool("deep", false, "Run factory, vehicle, runtime, and agent configuration checks")
	jsonOutput := fs.Bool("json", false, "Print machine-readable JSON")
	homeDir := fs.String("home", "", "Override home directory")
	projectDir := fs.String("project-dir", "", "Override repository/factory directory")
	dbPath := fs.String("db", "", "Override SQLite database path")
	if err := fs.Parse(reorderFlagArgs(args, map[string]bool{"deep": true, "json": true})); err != nil {
		return 2
	}

	defaults := config.LoadDefaults()
	if strings.TrimSpace(*dbPath) == "" {
		*dbPath = defaults.DBPath()
	}
	if strings.TrimSpace(*homeDir) == "" {
		if home, err := os.UserHomeDir(); err == nil {
			*homeDir = home
		}
	}
	if strings.TrimSpace(*projectDir) == "" {
		if cwd, err := os.Getwd(); err == nil {
			*projectDir = cwd
		}
	}

	report := doctorReport{
		Version:    version.Full(),
		DBPath:     strings.TrimSpace(*dbPath),
		ProjectDir: strings.TrimSpace(*projectDir),
	}
	ctx := context.Background()

	report.addFileCheck("runtime db file", report.DBPath, false)

	store, err := sqlite.Open(sqlite.Config{Path: report.DBPath})
	if err != nil {
		report.add("sqlite open", "FAIL", err.Error())
		return finishDoctor(report, *jsonOutput, stdout)
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
		return finishDoctor(report, *jsonOutput, stdout)
	}
	report.add("sqlite init", "PASS", "schema loaded and runtime migrations applied")

	runStoreDoctorChecks(ctx, store, &report, preRepairStaleSessions)
	if *deep {
		runVehicleDoctorChecks(strings.TrimSpace(*homeDir), &report)
		runAgentConfigDoctorChecks(strings.TrimSpace(*homeDir), &report)
		runFactoryDoctorChecks(strings.TrimSpace(*projectDir), &report)
	}

	return finishDoctor(report, *jsonOutput, stdout)
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

func runAgentConfigDoctorChecks(homeDir string, report *doctorReport) {
	if homeDir == "" {
		report.add("agent configs", "WARN", "home directory unavailable")
		return
	}
	checkContains(report, "codex mcp config", filepath.Join(homeDir, ".codex", "config.toml"), []string{"mcp_servers.kerebrom", "kerebrom", "mcp"})
	checkNoContains(report, "codex user preferences clean", filepath.Join(homeDir, ".codex", "AGENTS.md"), []string{"KEREBROM:START"})
	checkContains(report, "claude code settings", filepath.Join(homeDir, ".claude", "settings.json"), []string{"mcp__Kerebrom__context", "mcp__Kerebrom__remember"})
	checkNoContains(report, "claude global instructions clean", filepath.Join(homeDir, ".claude", "CLAUDE.md"), []string{"KEREBROM:START"})
	checkContains(report, "claude desktop mcp config", filepath.Join(homeDir, "Library", "Application Support", "Claude", "claude_desktop_config.json"), []string{"Kerebrom", "kerebrom", "mcp"})
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
	if projectDir == "" || projectDir == "." {
		return projectDir, filepath.Join(projectDir, "versions", "v2")
	}
	if _, err := os.Stat(filepath.Join(projectDir, "manifest.json")); err == nil {
		if _, err := os.Stat(filepath.Join(projectDir, "internal", "version", "version.go")); err == nil {
			return filepath.Clean(filepath.Join(projectDir, "..", "..")), projectDir
		}
	}
	return projectDir, filepath.Join(projectDir, "versions", "v2")
}

func finishDoctor(report doctorReport, jsonOutput bool, stdout io.Writer) int {
	report.Status = report.overallStatus()
	if jsonOutput {
		raw, _ := json.MarshalIndent(report, "", "  ")
		fmt.Fprintln(stdout, string(raw))
	} else {
		fmt.Fprintf(stdout, "Kerebrom doctor: %s\n", report.Status)
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
