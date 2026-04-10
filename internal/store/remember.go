package store

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/ulianbass/kerebrom/internal/embed"
	"github.com/ulianbass/kerebrom/internal/privacy"
)

// Remember stores a new memory with deduplication and PII scrubbing.
func (s *Store) Remember(content, project, kind, source string, importance, confidence float64, tags []string, metadata map[string]any) (*RememberResult, error) {
	if project == "" {
		project = DefaultProject
	}
	if kind == "" {
		kind = "episodic"
	}
	if source == "" {
		source = "mcp"
	}
	if tags == nil {
		tags = []string{}
	}
	if metadata == nil {
		metadata = map[string]any{}
	}

	// PII scrubbing
	scrubbed, piiHits := privacy.ScrubSensitive(content)
	hash := contentHash(scrubbed)
	ts := now()

	// Generate embedding for semantic search.
	embVec, err := s.embedder.Embed(scrubbed)
	if err != nil {
		embVec = make([]float32, s.embedder.Dimensions())
	}
	embBlob := embed.SerializeEmbedding(embVec)
	// Cache it.
	s.cache.Put(hash, embVec)

	piiLabels := make([]string, len(piiHits))
	for i, h := range piiHits {
		piiLabels[i] = h.Label
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	// Dedup layer 1: exact content hash match.
	var existingID int64
	err = tx.QueryRow(
		`SELECT id FROM memories WHERE project = ? AND content_hash = ? AND invalid_at IS NULL LIMIT 1`,
		project, hash,
	).Scan(&existingID)
	if err == nil {
		// Duplicate found — increment counter.
		_, _ = tx.Exec(`UPDATE memories SET duplicate_count = duplicate_count + 1, updated_at = ? WHERE id = ?`, ts, existingID)
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		mem, _ := s.GetMemory(existingID)
		return &RememberResult{Inserted: false, Memory: mem}, nil
	}
	if err != sql.ErrNoRows {
		return nil, fmt.Errorf("dedup check: %w", err)
	}

	// Insert new memory.
	result, err := tx.Exec(`
		INSERT INTO memories (
			project, content, raw_content, kind, source,
			importance, confidence, access_count, duplicate_count,
			is_redacted, pii_hits, embedding,
			created_at, updated_at, valid_at, tags, metadata, content_hash
		) VALUES (?, ?, ?, ?, ?, ?, ?, 0, 0, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		project, scrubbed, content, kind, source,
		importance, confidence,
		len(piiHits) > 0, marshalJSON(piiLabels), embBlob,
		ts, ts, ts, marshalJSON(tags), marshalJSON(metadata), hash,
	)
	if err != nil {
		return nil, fmt.Errorf("insert memory: %w", err)
	}

	id, _ := result.LastInsertId()

	// Extract entities and relations from content.
	s.processEntitiesAndRelations(tx, id, project, scrubbed)

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	// Track tokens (write-only: served = 0).
	if s.tracker != nil {
		s.tracker.Track("remember", content, "", project, 1, nil)
	}

	mem := &MemoryRecord{
		ID:          id,
		Project:     project,
		Content:     scrubbed,
		RawContent:  content,
		Kind:        kind,
		Source:      source,
		Importance:  importance,
		Confidence:  confidence,
		IsRedacted:  len(piiHits) > 0,
		PIIHits:     piiLabels,
		Tags:        tags,
		Metadata:    metadata,
		CreatedAt:   ts,
		UpdatedAt:   ts,
		ValidAt:     ts,
		ContentHash: hash,
	}
	return &RememberResult{Inserted: true, Memory: mem}, nil
}

// GetMemory retrieves a single memory by ID.
func (s *Store) GetMemory(id int64) (*MemoryRecord, error) {
	row := s.db.QueryRow(`
		SELECT id, project, content, raw_content, kind, source,
		       importance, confidence, access_count, duplicate_count,
		       is_redacted, pii_hits, created_at, updated_at, valid_at,
		       invalid_at, tags, metadata, content_hash
		FROM memories WHERE id = ?`, id)
	return scanMemory(row)
}

type scanner interface {
	Scan(dest ...any) error
}

func scanMemory(row scanner) (*MemoryRecord, error) {
	var m MemoryRecord
	var piiJSON, tagsJSON, metaJSON string
	var invalidAt sql.NullString

	err := row.Scan(
		&m.ID, &m.Project, &m.Content, &m.RawContent, &m.Kind, &m.Source,
		&m.Importance, &m.Confidence, &m.AccessCount, &m.DuplicateCount,
		&m.IsRedacted, &piiJSON, &m.CreatedAt, &m.UpdatedAt, &m.ValidAt,
		&invalidAt, &tagsJSON, &metaJSON, &m.ContentHash,
	)
	if err != nil {
		return nil, err
	}
	if invalidAt.Valid {
		m.InvalidAt = &invalidAt.String
	}
	json.Unmarshal([]byte(piiJSON), &m.PIIHits)
	json.Unmarshal([]byte(tagsJSON), &m.Tags)
	json.Unmarshal([]byte(metaJSON), &m.Metadata)
	if m.PIIHits == nil {
		m.PIIHits = []string{}
	}
	if m.Tags == nil {
		m.Tags = []string{}
	}
	if m.Metadata == nil {
		m.Metadata = map[string]any{}
	}
	return &m, nil
}
