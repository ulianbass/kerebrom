package store

import "encoding/json"

// ContextResult is the progressive disclosure response.
type ContextResult struct {
	Query    string        `json:"query"`
	Layer    int           `json:"layer"`
	Facts    []Fact        `json:"facts"`
	Memories []ContextItem `json:"memories,omitempty"`
}

// ContextItem is a memory summary for context output.
type ContextItem struct {
	ID                 int64   `json:"id"`
	Content            string  `json:"content"`
	Kind               string  `json:"kind"`
	Score              float64 `json:"score"`
	SemanticSimilarity float64 `json:"semantic_similarity"`
}

// BuildContext returns a progressive disclosure context bundle.
// Layer 1: compact index (~facts only)
// Layer 2: facts + memory summaries
// Layer 3: facts + full memory content
//
// FIX P0: recall is called with trackTokens=false (internal path).
// Only BuildContext itself tracks tokens once.
func (s *Store) BuildContext(query, project string, limit, layer int) (*ContextResult, error) {
	if project == "" {
		project = DefaultProject
	}
	if limit <= 0 {
		limit = 5
	}
	if layer < 1 || layer > 3 {
		layer = 2
	}

	result := &ContextResult{
		Query: query,
		Layer: layer,
	}

	// Get facts (always included).
	facts, err := s.ListFacts(project, 20)
	if err != nil {
		return nil, err
	}
	result.Facts = facts

	if layer == 1 {
		return result, nil
	}

	// Recall memories (internal — no double tracking).
	memories, err := s.Recall(query, project, RecallOptions{
		Limit: limit * 3, // fetch more, then filter
		Touch: false,
	})
	if err != nil {
		return nil, err
	}

	// Build context items.
	shown := 0
	for _, m := range memories {
		if shown >= limit {
			break
		}
		content := m.Content
		if layer == 2 && len(content) > 300 {
			content = content[:300] + "..."
		}
		result.Memories = append(result.Memories, ContextItem{
			ID:                 m.ID,
			Content:            content,
			Kind:               m.Kind,
			Score:              m.Score,
			SemanticSimilarity: m.SemanticSimilarity,
		})
		shown++
	}
	if result.Memories == nil {
		result.Memories = []ContextItem{}
	}

	// Track tokens (FIX P0: only context tracks, not recall internally).
	if s.tracker != nil {
		outputBytes, _ := json.Marshal(result)
		s.tracker.Track("context", query, string(outputBytes), project, len(result.Memories), map[string]any{"layer": layer})
	}

	return result, nil
}
