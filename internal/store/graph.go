package store

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/ulianbass/kerebrom/internal/capture"
)

// ExclusivePredicates: only one active relation per (subject, predicate).
var ExclusivePredicates = map[string]bool{
	"name": true, "lives_in": true, "from": true,
	"works_at": true, "role": true,
}

// Entity represents a node in the knowledge graph.
type Entity struct {
	ID            int64  `json:"id"`
	Project       string `json:"project"`
	Name          string `json:"name"`
	CanonicalName string `json:"canonical_name"`
	EntityType    string `json:"entity_type"`
	MemoryCount   int    `json:"memory_count"`
}

// Fact represents an active relation triple.
type Fact struct {
	ID         int64   `json:"id"`
	Subject    string  `json:"subject"`
	Predicate  string  `json:"predicate"`
	Object     string  `json:"object"`
	Confidence float64 `json:"confidence"`
	ValidAt    string  `json:"valid_at"`
	InvalidAt  *string `json:"invalid_at"`
}

// processEntitiesAndRelations extracts entities and relations from content
// and links them to the given memory. Called inside Remember's transaction.
func (s *Store) processEntitiesAndRelations(tx *sql.Tx, memoryID int64, project, content string) error {
	ts := now()

	// Extract entities.
	entityNames := capture.ExtractEntities(content)
	for _, name := range entityNames {
		entityID, err := s.ensureEntity(tx, project, name, capture.InferEntityType(name), ts)
		if err != nil {
			continue
		}
		tx.Exec(`INSERT OR IGNORE INTO memory_entities (memory_id, entity_id, role) VALUES (?, ?, 'mention')`,
			memoryID, entityID)
		tx.Exec(`INSERT OR IGNORE INTO entity_references (entity_id, memory_id, direction, created_at) VALUES (?, ?, 'mention', ?)`,
			entityID, memoryID, ts)
	}

	// Extract relation candidates.
	candidates := capture.ExtractRelationCandidates(content)
	for _, cand := range candidates {
		// Ensure the "user" subject entity.
		subjectID, err := s.ensureEntity(tx, project, "user", "person", ts)
		if err != nil {
			continue
		}
		// Ensure the object entity.
		objectID, err := s.ensureEntity(tx, project, cand.Object, cand.EntityType, ts)
		if err != nil {
			continue
		}
		// Record support evidence.
		s.recordRelationSupport(tx, project, subjectID, cand.Predicate, objectID, cand.Confidence, memoryID, ts)
		// Refresh active relation state.
		s.refreshRelationState(tx, project, subjectID, cand.Predicate, ts)
	}

	return nil
}

// ensureEntity creates or retrieves an entity by canonical name.
func (s *Store) ensureEntity(tx *sql.Tx, project, name, entityType, ts string) (int64, error) {
	canonical := capture.CanonicalizeEntity(name)
	if canonical == "" {
		return 0, fmt.Errorf("empty canonical name")
	}

	var id int64
	err := tx.QueryRow(
		`SELECT id FROM entities WHERE project = ? AND canonical_name = ?`,
		project, canonical,
	).Scan(&id)
	if err == nil {
		return id, nil
	}
	if err != sql.ErrNoRows {
		return 0, err
	}

	result, err := tx.Exec(
		`INSERT INTO entities (project, name, canonical_name, entity_type, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		project, name, canonical, entityType, ts, ts,
	)
	if err != nil {
		// Race condition: another goroutine inserted first.
		if strings.Contains(err.Error(), "UNIQUE") {
			tx.QueryRow(`SELECT id FROM entities WHERE project = ? AND canonical_name = ?`,
				project, canonical).Scan(&id)
			return id, nil
		}
		return 0, err
	}
	id, _ = result.LastInsertId()
	return id, nil
}

// recordRelationSupport adds evidence for a relation.
func (s *Store) recordRelationSupport(tx *sql.Tx, project string, subjectID int64, predicate string, objectID int64, confidence float64, memoryID int64, ts string) {
	tx.Exec(`
		INSERT OR IGNORE INTO relation_supports
			(project, subject_entity_id, predicate, object_entity_id, confidence, source_memory_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		project, subjectID, predicate, objectID, confidence, memoryID, ts, ts,
	)
}

// refreshRelationState recalculates active relations for a (subject, predicate).
// FIX P0: Uses a single query (no fetchone/fetchall split).
func (s *Store) refreshRelationState(tx *sql.Tx, project string, subjectID int64, predicate, ts string) {
	// Gather all supports with valid source memories.
	rows, err := tx.Query(`
		SELECT rs.object_entity_id, rs.confidence
		FROM relation_supports rs
		JOIN memories m ON rs.source_memory_id = m.id
		WHERE rs.project = ? AND rs.subject_entity_id = ? AND rs.predicate = ?
		  AND m.invalid_at IS NULL`,
		project, subjectID, predicate,
	)
	if err != nil {
		return
	}
	defer rows.Close()

	// Aggregate confidence per object: max + 0.05 per extra support.
	type objectScore struct {
		maxConf float64
		count   int
	}
	scores := map[int64]*objectScore{}
	for rows.Next() {
		var objID int64
		var conf float64
		rows.Scan(&objID, &conf)
		if s, ok := scores[objID]; ok {
			if conf > s.maxConf {
				s.maxConf = conf
			}
			s.count++
		} else {
			scores[objID] = &objectScore{maxConf: conf, count: 1}
		}
	}

	if len(scores) == 0 {
		// No valid supports — invalidate all active relations.
		tx.Exec(`UPDATE relations SET invalid_at = ?, updated_at = ?
			WHERE project = ? AND subject_entity_id = ? AND predicate = ? AND invalid_at IS NULL`,
			ts, ts, project, subjectID, predicate)
		return
	}

	// Find the best candidate.
	var bestObj int64
	var bestConf float64
	for objID, s := range scores {
		aggregated := s.maxConf + 0.05*float64(s.count-1)
		if aggregated > bestConf {
			bestConf = aggregated
			bestObj = objID
		}
	}

	// For exclusive predicates, only keep the best.
	if ExclusivePredicates[predicate] {
		// Invalidate all except best.
		tx.Exec(`UPDATE relations SET invalid_at = ?, updated_at = ?
			WHERE project = ? AND subject_entity_id = ? AND predicate = ? AND object_entity_id != ? AND invalid_at IS NULL`,
			ts, ts, project, subjectID, predicate, bestObj)
	}

	// Upsert the winning relation.
	var existingID int64
	err = tx.QueryRow(`SELECT id FROM relations
		WHERE project = ? AND subject_entity_id = ? AND predicate = ? AND object_entity_id = ? AND invalid_at IS NULL`,
		project, subjectID, predicate, bestObj).Scan(&existingID)
	if err == sql.ErrNoRows {
		// Pick any source memory from the supports.
		var srcMem int64
		tx.QueryRow(`SELECT source_memory_id FROM relation_supports
			WHERE project = ? AND subject_entity_id = ? AND predicate = ? AND object_entity_id = ?
			LIMIT 1`, project, subjectID, predicate, bestObj).Scan(&srcMem)
		tx.Exec(`INSERT INTO relations
			(project, subject_entity_id, predicate, object_entity_id, confidence, source_memory_id, valid_at, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			project, subjectID, predicate, bestObj, bestConf, srcMem, ts, ts, ts)
	} else if err == nil {
		tx.Exec(`UPDATE relations SET confidence = ?, updated_at = ? WHERE id = ?`, bestConf, ts, existingID)
	}
}

// ListEntities returns significant entities for a project.
func (s *Store) ListEntities(project string, limit int) ([]Entity, error) {
	if project == "" {
		project = DefaultProject
	}
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(`
		SELECT e.id, e.project, e.name, e.canonical_name, e.entity_type,
		       COUNT(me.memory_id) as memory_count
		FROM entities e
		LEFT JOIN memory_entities me ON e.id = me.entity_id
		WHERE e.project = ?
		GROUP BY e.id
		HAVING memory_count > 0
		ORDER BY memory_count DESC
		LIMIT ?`, project, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entities []Entity
	for rows.Next() {
		var e Entity
		rows.Scan(&e.ID, &e.Project, &e.Name, &e.CanonicalName, &e.EntityType, &e.MemoryCount)
		entities = append(entities, e)
	}
	if entities == nil {
		entities = []Entity{}
	}
	return entities, nil
}

// ListFacts returns active semantic relations (subject → predicate → object).
func (s *Store) ListFacts(project string, limit int) ([]Fact, error) {
	if project == "" {
		project = DefaultProject
	}
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(`
		SELECT r.id, se.name, r.predicate, oe.name, r.confidence, r.valid_at, r.invalid_at
		FROM relations r
		JOIN entities se ON r.subject_entity_id = se.id
		JOIN entities oe ON r.object_entity_id = oe.id
		WHERE r.project = ? AND r.invalid_at IS NULL
		ORDER BY r.confidence DESC
		LIMIT ?`, project, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var facts []Fact
	for rows.Next() {
		var f Fact
		var invalidAt sql.NullString
		rows.Scan(&f.ID, &f.Subject, &f.Predicate, &f.Object, &f.Confidence, &f.ValidAt, &invalidAt)
		if invalidAt.Valid {
			f.InvalidAt = &invalidAt.String
		}
		facts = append(facts, f)
	}
	if facts == nil {
		facts = []Fact{}
	}
	return facts, nil
}

// getEntityIDsForMemory returns entity IDs linked to a memory.
func (s *Store) getEntityIDsForMemory(memoryID int64) []int64 {
	rows, err := s.db.Query(`SELECT entity_id FROM memory_entities WHERE memory_id = ?`, memoryID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		ids = append(ids, id)
	}
	return ids
}
