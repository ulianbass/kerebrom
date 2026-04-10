package store

import (
	"time"
)

// parseTime parses RFC3339 timestamps stored in SQLite.
func parseTime(s string) (time.Time, error) {
	return time.Parse(time.RFC3339, s)
}

// timeSince returns duration since t (using UTC now).
func timeSince(t time.Time) time.Duration {
	return time.Now().UTC().Sub(t)
}

// AutoMaintain runs decay and consolidation if enough time has passed.
func (s *Store) AutoMaintain(project string) {
	if project == "" {
		project = DefaultProject
	}
	// Check last maintenance run.
	var lastRun string
	err := s.db.QueryRow(
		`SELECT last_run_at FROM maintenance_log WHERE project = ? AND task = 'auto_maintain'`,
		project,
	).Scan(&lastRun)

	if err == nil {
		t, _ := parseTime(lastRun)
		if timeSince(t) < 24*time.Hour {
			return // too recent
		}
	}

	// Run decay (unlocked — callers should not hold s.mu).
	s.ApplyDecay(project)

	// Update log.
	s.db.Exec(`INSERT OR REPLACE INTO maintenance_log (project, task, last_run_at) VALUES (?, 'auto_maintain', ?)`,
		project, now())
}
