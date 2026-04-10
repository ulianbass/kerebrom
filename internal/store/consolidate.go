package store

import (
	"fmt"
	"strings"

	"github.com/ulianbass/kerebrom/internal/embed"
)

// ConsolidateResult summarizes clustering output.
type ConsolidateResult struct {
	Clusters             int `json:"clusters"`
	NewSemanticMemories  int `json:"new_semantic_memories"`
	ConsolidatedEpisodes int `json:"consolidated_episodes"`
}

// Consolidate clusters similar episodic memories into semantic ones.
// Uses SAVEPOINTs per cluster for partial atomicity (fixes Python limitation).
func (s *Store) Consolidate(project string, similarityThreshold float64, minClusterSize int) (*ConsolidateResult, error) {
	if project == "" {
		project = DefaultProject
	}
	if similarityThreshold <= 0 {
		similarityThreshold = 0.80
	}
	if minClusterSize <= 0 {
		minClusterSize = 2
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Load unconsolidated episodic memories with embeddings.
	rows, err := s.db.Query(`
		SELECT id, content, embedding, importance
		FROM memories
		WHERE project = ? AND kind = 'episodic'
		  AND invalid_at IS NULL AND consolidated_at IS NULL
		ORDER BY created_at DESC
		LIMIT 500`, project)
	if err != nil {
		return nil, fmt.Errorf("load episodics: %w", err)
	}
	defer rows.Close()

	type item struct {
		id         int64
		content    string
		embedding  []float32
		importance float64
	}
	var items []item
	for rows.Next() {
		var it item
		var embBlob []byte
		rows.Scan(&it.id, &it.content, &embBlob, &it.importance)
		it.embedding = embed.DeserializeEmbedding(embBlob)
		if it.embedding != nil {
			items = append(items, it)
		}
	}

	if len(items) < minClusterSize {
		return &ConsolidateResult{}, nil
	}

	// Greedy clustering by embedding similarity.
	assigned := map[int64]bool{}
	var clusters [][]item

	for i := 0; i < len(items); i++ {
		if assigned[items[i].id] {
			continue
		}
		cluster := []item{items[i]}
		assigned[items[i].id] = true

		for j := i + 1; j < len(items); j++ {
			if assigned[items[j].id] {
				continue
			}
			sim := embed.CosineSimilarity(items[i].embedding, items[j].embedding)
			if float64(sim) >= similarityThreshold {
				cluster = append(cluster, items[j])
				assigned[items[j].id] = true
			}
		}
		if len(cluster) >= minClusterSize {
			clusters = append(clusters, cluster)
		}
	}

	result := &ConsolidateResult{Clusters: len(clusters)}

	tx, err := s.db.Begin()
	if err != nil {
		return result, err
	}
	defer tx.Rollback()

	ts := now()

	for ci, cluster := range clusters {
		// SAVEPOINT per cluster for partial atomicity.
		spName := fmt.Sprintf("cluster_%d", ci)
		tx.Exec("SAVEPOINT " + spName)

		// Build summary content from cluster members.
		var parts []string
		var maxImp float64
		for _, it := range cluster {
			parts = append(parts, it.content)
			if it.importance > maxImp {
				maxImp = it.importance
			}
		}
		summary := strings.Join(parts, " | ")
		if len(summary) > 2000 {
			summary = summary[:2000]
		}

		// Compute centroid embedding.
		vecs := make([][]float32, len(cluster))
		for i, it := range cluster {
			vecs[i] = it.embedding
		}
		centroid := averageEmbedding(vecs)
		embBlob := embed.SerializeEmbedding(centroid)
		hash := contentHash(summary)

		// Insert new semantic memory.
		res, err := tx.Exec(`
			INSERT INTO memories (
				project, content, raw_content, kind, source,
				importance, confidence, access_count, duplicate_count,
				is_redacted, pii_hits, embedding,
				created_at, updated_at, valid_at, tags, metadata, content_hash
			) VALUES (?, ?, ?, 'semantic', 'consolidation', ?, 0.8, 0, 0, 0, '[]', ?, ?, ?, ?, '["consolidated"]', '{}', ?)`,
			project, summary, summary,
			maxImp*0.8, embBlob, ts, ts, ts, hash,
		)
		if err != nil {
			tx.Exec("ROLLBACK TO " + spName)
			continue
		}
		result.NewSemanticMemories++

		newID, _ := res.LastInsertId()

		// Mark episodic sources as consolidated (don't invalidate — reduce importance).
		for _, it := range cluster {
			tx.Exec(`UPDATE memories SET consolidated_at = ?, importance = importance * 0.5, updated_at = ? WHERE id = ?`,
				ts, ts, it.id)
			// Link entities from source to new semantic memory.
			eRows, _ := tx.Query(`SELECT entity_id, role FROM memory_entities WHERE memory_id = ?`, it.id)
			if eRows != nil {
				for eRows.Next() {
					var entID int64
					var role string
					eRows.Scan(&entID, &role)
					tx.Exec(`INSERT OR IGNORE INTO memory_entities (memory_id, entity_id, role) VALUES (?, ?, ?)`,
						newID, entID, role)
				}
				eRows.Close()
			}
			result.ConsolidatedEpisodes++
		}

		tx.Exec("RELEASE " + spName)
	}

	return result, tx.Commit()
}

// averageEmbedding computes the L2-normalized centroid of N vectors.
func averageEmbedding(vecs [][]float32) []float32 {
	if len(vecs) == 0 {
		return nil
	}
	dims := len(vecs[0])
	centroid := make([]float32, dims)
	for _, v := range vecs {
		for i, x := range v {
			centroid[i] += x
		}
	}
	n := float32(len(vecs))
	for i := range centroid {
		centroid[i] /= n
	}
	embed.NormalizeL2(centroid)
	return centroid
}
