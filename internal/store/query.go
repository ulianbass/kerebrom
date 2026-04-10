package store

import "database/sql"

// Gap represents an unresolved reference (knowledge hole).
type Gap struct {
	ID             int64  `json:"id"`
	ReferenceText  string `json:"reference_text"`
	SourceMemoryID int64  `json:"source_memory_id"`
	CreatedAt      string `json:"created_at"`
}

// ListGaps returns unresolved references for a project.
func (s *Store) ListGaps(project string, limit int) ([]Gap, error) {
	if project == "" {
		project = DefaultProject
	}
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(`
		SELECT id, reference_text, source_memory_id, created_at
		FROM unresolved_references
		WHERE project = ? AND resolved_at IS NULL
		ORDER BY created_at DESC
		LIMIT ?`, project, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var gaps []Gap
	for rows.Next() {
		var g Gap
		rows.Scan(&g.ID, &g.ReferenceText, &g.SourceMemoryID, &g.CreatedAt)
		gaps = append(gaps, g)
	}
	if gaps == nil {
		gaps = []Gap{}
	}
	return gaps, nil
}

// Query performs a structured search with filters.
func (s *Store) Query(project, kind string, tags []string, importanceMin, importanceMax float64, limit int) ([]*MemoryRecord, error) {
	if project == "" {
		project = DefaultProject
	}
	if limit <= 0 {
		limit = 20
	}

	q := `SELECT id, project, content, raw_content, kind, source,
	       importance, confidence, access_count, duplicate_count,
	       is_redacted, pii_hits, created_at, updated_at, valid_at,
	       invalid_at, tags, metadata, content_hash
	FROM memories WHERE project = ? AND invalid_at IS NULL`
	args := []any{project}

	if kind != "" {
		q += " AND kind = ?"
		args = append(args, kind)
	}
	if importanceMin > 0 {
		q += " AND importance >= ?"
		args = append(args, importanceMin)
	}
	if importanceMax > 0 && importanceMax < 1 {
		q += " AND importance <= ?"
		args = append(args, importanceMax)
	}
	q += " ORDER BY created_at DESC LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var memories []*MemoryRecord
	for rows.Next() {
		m, err := scanMemory(rows)
		if err != nil {
			continue
		}
		// Filter by tags if specified.
		if len(tags) > 0 && !hasAnyTag(m.Tags, tags) {
			continue
		}
		memories = append(memories, m)
	}
	if memories == nil {
		memories = []*MemoryRecord{}
	}
	return memories, nil
}

func hasAnyTag(memTags, filterTags []string) bool {
	tagSet := map[string]bool{}
	for _, t := range memTags {
		tagSet[t] = true
	}
	for _, t := range filterTags {
		if tagSet[t] {
			return true
		}
	}
	return false
}

// Ensure scanMemory satisfies the interface for *sql.Rows.
var _ scanner = (*sql.Rows)(nil)
