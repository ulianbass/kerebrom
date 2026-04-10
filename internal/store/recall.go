package store

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"

	"github.com/ulianbass/kerebrom/internal/capture"
	"github.com/ulianbass/kerebrom/internal/embed"
)

// Recall performs hybrid search. In Phase 1, only FTS5 is used.
// Phases 2+ will add semantic and graph signals.
func (s *Store) Recall(query, project string, opts RecallOptions) ([]*MemoryRecord, error) {
	if project == "" {
		project = DefaultProject
	}
	if opts.Limit <= 0 {
		opts.Limit = 5
	}
	weights := opts.Weights
	if weights == nil {
		w := DefaultRecallWeights
		weights = &w
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Load candidate memories.
	validFilter := "AND invalid_at IS NULL"
	if opts.IncludeInactive {
		validFilter = ""
	}
	rows, err := s.db.Query(fmt.Sprintf(`
		SELECT id, project, content, raw_content, kind, source,
		       importance, confidence, access_count, duplicate_count,
		       is_redacted, pii_hits, created_at, updated_at, valid_at,
		       invalid_at, tags, metadata, content_hash
		FROM memories
		WHERE project = ? %s
		ORDER BY created_at DESC
		LIMIT 500`, validFilter), project)
	if err != nil {
		return nil, fmt.Errorf("query memories: %w", err)
	}
	defer rows.Close()

	var memories []*MemoryRecord
	for rows.Next() {
		m, err := scanMemory(rows)
		if err != nil {
			continue
		}
		memories = append(memories, m)
	}

	// FIX P1: safe return on empty project.
	if len(memories) == 0 {
		return []*MemoryRecord{}, nil
	}

	// Compute query embedding for semantic search.
	queryEmb, _ := s.embedder.Embed(query)

	// Extract query entities for graph scoring.
	queryEntities := capture.ExtractEntities(query)
	// Also use tokenized words as entity candidates.
	for _, tok := range tokenize(query) {
		queryEntities = append(queryEntities, tok)
	}
	queryEntitySet := map[string]bool{}
	for _, e := range queryEntities {
		queryEntitySet[capture.CanonicalizeEntity(e)] = true
	}

	// Build memory→entity map for graph scoring.
	memoryEntityMap := map[int64][]int64{}
	entityNames := map[int64]string{}
	{
		eRows, err := s.db.Query(`
			SELECT me.memory_id, me.entity_id, e.canonical_name
			FROM memory_entities me
			JOIN entities e ON me.entity_id = e.id
			WHERE e.project = ?`, project)
		if err == nil {
			defer eRows.Close()
			for eRows.Next() {
				var memID, entID int64
				var canonical string
				eRows.Scan(&memID, &entID, &canonical)
				memoryEntityMap[memID] = append(memoryEntityMap[memID], entID)
				entityNames[entID] = canonical
			}
		}
	}

	// Signal 1: FTS5 BM25 keyword search.
	ftsQuery := buildFTSQuery(query)
	ftsScores := map[int64]float64{}
	maxFTS := 0.0

	if ftsQuery != "" {
		ftsRows, err := s.db.Query(`
			SELECT rowid, rank FROM memories_fts
			WHERE memories_fts MATCH ?
			ORDER BY rank
			LIMIT 100`, ftsQuery)
		if err == nil {
			defer ftsRows.Close()
			for ftsRows.Next() {
				var id int64
				var rank float64
				ftsRows.Scan(&id, &rank)
				// FTS5 rank is negative (more negative = more relevant).
				score := -rank
				ftsScores[id] = score
				if score > maxFTS {
					maxFTS = score
				}
			}
		}
	}

	// Score each memory.
	wTotal := weights.Recency + weights.Importance + weights.Frequency +
		weights.Semantic + weights.Graph + weights.Keyword
	if wTotal == 0 {
		wTotal = 1
	}

	for _, mem := range memories {
		// Keyword score (normalized to [0,1]).
		kwScore := 0.0
		if maxFTS > 0 {
			if s, ok := ftsScores[mem.ID]; ok {
				kwScore = s / maxFTS
			}
		}
		mem.KeywordScore = kwScore

		// Signal 2: Semantic similarity via embeddings.
		memEmb, cached := s.cache.Get(mem.ContentHash)
		if !cached && mem.ContentHash != "" {
			// Load from DB.
			var embBlob []byte
			s.db.QueryRow(`SELECT embedding FROM memories WHERE id = ?`, mem.ID).Scan(&embBlob)
			memEmb = embed.DeserializeEmbedding(embBlob)
			if memEmb != nil {
				s.cache.Put(mem.ContentHash, memEmb)
			}
		}
		semScore := float64(0)
		if queryEmb != nil && memEmb != nil && len(queryEmb) == len(memEmb) {
			semScore = float64(embed.CosineSimilarity(queryEmb, memEmb)+1) / 2 // normalize to [0,1]
		}
		mem.SemanticSimilarity = semScore

		// Signal 3: Graph entity overlap.
		graphScore := float64(0)
		if len(queryEntitySet) > 0 {
			matched := 0
			for _, entID := range memoryEntityMap[mem.ID] {
				if queryEntitySet[entityNames[entID]] {
					matched++
				}
			}
			graphScore = float64(matched) / float64(len(queryEntitySet))
		}
		mem.GraphScore = graphScore

		// Recency: exponential decay.
		createdAt, _ := time.Parse(time.RFC3339, mem.CreatedAt)
		ageDays := time.Since(createdAt).Hours() / 24
		recency := math.Exp(-0.08 * ageDays)

		// Frequency: log-scaled access count.
		frequency := clamp(math.Log1p(float64(mem.AccessCount))/4.0, 0, 1)

		// Final score (all signals normalized to [0,1]).
		mem.Score = (weights.Recency*recency +
			weights.Importance*clamp(mem.Importance, 0, 1) +
			weights.Frequency*frequency +
			weights.Semantic*clamp(semScore, 0, 1) +
			weights.Graph*clamp(graphScore, 0, 1) +
			weights.Keyword*clamp(kwScore, 0, 1)) / wTotal
	}

	// Sort by score descending.
	sort.Slice(memories, func(i, j int) bool {
		return memories[i].Score > memories[j].Score
	})

	// Touch: update access count + last_accessed_at.
	if opts.Touch {
		ts := now()
		for _, mem := range memories[:min(opts.Limit, len(memories))] {
			s.db.Exec(`UPDATE memories SET access_count = access_count + 1, last_accessed_at = ? WHERE id = ?`, ts, mem.ID)
		}
	}

	// Reactivation: if top score is weak, check dormant memories.
	if opts.Reactivate && len(memories) > 0 && memories[0].Score < 0.4 && queryEmb != nil {
		reactivated := s.tryReactivate(queryEmb, project, opts.Limit)
		if len(reactivated) > 0 {
			memories = append(memories, reactivated...)
			sort.Slice(memories, func(i, j int) bool {
				return memories[i].Score > memories[j].Score
			})
		}
	}

	if len(memories) > opts.Limit {
		memories = memories[:opts.Limit]
	}

	// Track tokens (read: served = output content).
	if s.tracker != nil && opts.Touch {
		var outputBuilder strings.Builder
		for _, m := range memories {
			outputBuilder.WriteString(m.Content)
			outputBuilder.WriteString("\n")
		}
		s.tracker.Track("recall", query, outputBuilder.String(), project, len(memories), nil)
	}

	return memories, nil
}

// tryReactivate searches invalidated memories for strong semantic matches.
// If found, reactivates them (clears invalid_at, boosts importance).
func (s *Store) tryReactivate(queryEmb []float32, project string, limit int) []*MemoryRecord {
	rows, err := s.db.Query(`
		SELECT id, content, embedding, importance, content_hash
		FROM memories
		WHERE project = ? AND invalid_at IS NOT NULL
		ORDER BY created_at DESC LIMIT 200`, project)
	if err != nil {
		return nil
	}
	defer rows.Close()

	ts := now()
	var reactivated []*MemoryRecord

	for rows.Next() {
		var id int64
		var content string
		var embBlob []byte
		var importance float64
		var hash string
		rows.Scan(&id, &content, &embBlob, &importance, &hash)

		memEmb := embed.DeserializeEmbedding(embBlob)
		if memEmb == nil || len(memEmb) != len(queryEmb) {
			continue
		}
		sim := float64(embed.CosineSimilarity(queryEmb, memEmb)+1) / 2
		if sim >= ReactivationThreshold {
			// Reactivate!
			newImp := math.Max(0.3, importance*2.0)
			s.db.Exec(`UPDATE memories SET invalid_at = NULL, importance = ?, updated_at = ? WHERE id = ?`,
				newImp, ts, id)
			mem, _ := s.GetMemory(id)
			if mem != nil {
				mem.SemanticSimilarity = sim
				mem.Score = sim * 0.8 // give reactivated memories a decent score
				reactivated = append(reactivated, mem)
			}
			if len(reactivated) >= limit {
				break
			}
		}
	}
	return reactivated
}

// buildFTSQuery creates a safe FTS5 MATCH expression from user query.
func buildFTSQuery(text string) string {
	tokens := tokenize(text)
	if len(tokens) == 0 {
		return ""
	}
	// Limit to 8 tokens.
	if len(tokens) > 8 {
		tokens = tokens[:8]
	}
	quoted := make([]string, len(tokens))
	for i, t := range tokens {
		quoted[i] = `"` + t + `"`
	}
	return strings.Join(quoted, " OR ")
}

var nonAlphaRe = regexp.MustCompile(`[^\p{L}\p{N}]+`)

// tokenize splits text into lowercase tokens without diacritics.
func tokenize(text string) []string {
	text = strings.ToLower(text)
	text = removeDiacritics(text)
	parts := nonAlphaRe.Split(text, -1)
	var tokens []string
	for _, p := range parts {
		if len(p) >= 2 {
			tokens = append(tokens, p)
		}
	}
	return tokens
}

// removeDiacritics strips accents (á→a, ñ→n, etc.).
func removeDiacritics(s string) string {
	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	result, _, _ := transform.String(t, s)
	return result
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
