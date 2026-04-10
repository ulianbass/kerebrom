package store

import "fmt"

// Forget invalidates a memory by ID (soft delete).
func (s *Store) Forget(project string, memoryID int64) (int, error) {
	if project == "" {
		project = DefaultProject
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	ts := now()
	result, err := s.db.Exec(`
		UPDATE memories SET invalid_at = ?, updated_at = ?
		WHERE id = ? AND project = ? AND invalid_at IS NULL`,
		ts, ts, memoryID, project)
	if err != nil {
		return 0, fmt.Errorf("forget memory %d: %w", memoryID, err)
	}
	n, _ := result.RowsAffected()
	return int(n), nil
}

// ForgetByQuery invalidates memories matching a query.
func (s *Store) ForgetByQuery(project, query string) (int, error) {
	if project == "" {
		project = DefaultProject
	}
	ftsQuery := buildFTSQuery(query)
	if ftsQuery == "" {
		return 0, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	ts := now()
	result, err := s.db.Exec(`
		UPDATE memories SET invalid_at = ?, updated_at = ?
		WHERE id IN (
			SELECT rowid FROM memories_fts WHERE memories_fts MATCH ?
		) AND project = ? AND invalid_at IS NULL`,
		ts, ts, ftsQuery, project)
	if err != nil {
		return 0, fmt.Errorf("forget by query: %w", err)
	}
	n, _ := result.RowsAffected()
	return int(n), nil
}

// ForgetSensitive invalidates all memories flagged as containing PII.
func (s *Store) ForgetSensitive(project string) (int, error) {
	if project == "" {
		project = DefaultProject
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	ts := now()
	result, err := s.db.Exec(`
		UPDATE memories SET invalid_at = ?, updated_at = ?
		WHERE project = ? AND is_redacted = 1 AND invalid_at IS NULL`,
		ts, ts, project)
	if err != nil {
		return 0, fmt.Errorf("forget sensitive: %w", err)
	}
	n, _ := result.RowsAffected()
	return int(n), nil
}
