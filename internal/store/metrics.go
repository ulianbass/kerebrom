package store

import "fmt"

// ImpactMetrics contains the 3 useful metrics for the dashboard.
type ImpactMetrics struct {
	Repetition RepetitionMetric `json:"repetition"`
	Coverage   CoverageMetric   `json:"coverage"`
	Lifecycle  LifecycleMetric  `json:"lifecycle"`
}

// RepetitionMetric: how many unique queries got high-confidence answers.
// "42 preguntas respondidas sin que repitieras informacion"
type RepetitionMetric struct {
	TotalRecalls     int     `json:"total_recalls"`      // total recall operations
	HighConfidence   int     `json:"high_confidence"`     // recalls with score > 0.5 (useful)
	UniqueQueries    int     `json:"unique_queries"`      // distinct queries asked
	HitRate          float64 `json:"hit_rate"`            // high_confidence / total (0-1)
	QuestionsAnswered string `json:"questions_answered"`  // human-readable
}

// CoverageMetric: what % of queried entities does Kerebrom know.
type CoverageMetric struct {
	TotalEntities    int     `json:"total_entities"`
	ConnectedMemories int    `json:"connected_memories"` // entities linked to >=2 memories
	UnresolvedGaps   int     `json:"unresolved_gaps"`
	CoveragePercent  float64 `json:"coverage_percent"`   // (total - gaps) / total
	Summary          string  `json:"summary"`            // human-readable
}

// LifecycleMetric: do memories get used before they decay/die.
type LifecycleMetric struct {
	TotalEpisodic     int     `json:"total_episodic"`
	NeverAccessed     int     `json:"never_accessed"`      // access_count = 0
	AccessedOnce      int     `json:"accessed_once"`       // access_count >= 1
	Consolidated      int     `json:"consolidated"`        // consolidated_at IS NOT NULL
	Zombified         int     `json:"zombified"`           // invalid_at IS NOT NULL (decayed)
	UsefulPercent     float64 `json:"useful_percent"`      // accessed / total
	WastePercent      float64 `json:"waste_percent"`       // never_accessed zombified / total
	Summary           string  `json:"summary"`
}

// BuildImpactMetrics computes the 3 useful metrics from existing data.
func (s *Store) BuildImpactMetrics(project string) (*ImpactMetrics, error) {
	if project == "" {
		project = DefaultProject
	}
	m := &ImpactMetrics{}

	// 1. Repetition: analyze token_stats for recall quality.
	s.db.QueryRow(`SELECT COUNT(*) FROM token_stats WHERE operation = 'recall'`).
		Scan(&m.Repetition.TotalRecalls)

	// High confidence = recalls that returned substantial content (tokens_saved > 100).
	s.db.QueryRow(`SELECT COUNT(*) FROM token_stats WHERE operation = 'recall' AND tokens_saved > 100`).
		Scan(&m.Repetition.HighConfidence)

	// Unique queries: approximate by counting distinct token_input lengths
	// (we don't store the query text in token_stats, but we can count distinct operations).
	m.Repetition.UniqueQueries = m.Repetition.TotalRecalls // approximate: each recall = 1 query

	if m.Repetition.TotalRecalls > 0 {
		m.Repetition.HitRate = float64(m.Repetition.HighConfidence) / float64(m.Repetition.TotalRecalls)
	}
	m.Repetition.QuestionsAnswered = fmt.Sprintf(
		"%d de %d consultas respondidas con contexto util (%.0f%%)",
		m.Repetition.HighConfidence, m.Repetition.TotalRecalls,
		m.Repetition.HitRate*100)

	// 2. Coverage: entities known vs gaps.
	s.db.QueryRow(`SELECT COUNT(*) FROM entities WHERE project = ?`, project).
		Scan(&m.Coverage.TotalEntities)

	s.db.QueryRow(`
		SELECT COUNT(*) FROM (
			SELECT e.id FROM entities e
			JOIN memory_entities me ON e.id = me.entity_id
			WHERE e.project = ?
			GROUP BY e.id HAVING COUNT(me.memory_id) >= 2
		)`, project).
		Scan(&m.Coverage.ConnectedMemories)

	s.db.QueryRow(`SELECT COUNT(*) FROM unresolved_references WHERE project = ? AND resolved_at IS NULL`, project).
		Scan(&m.Coverage.UnresolvedGaps)

	if m.Coverage.TotalEntities > 0 {
		known := m.Coverage.TotalEntities - m.Coverage.UnresolvedGaps
		if known < 0 {
			known = m.Coverage.TotalEntities
		}
		m.Coverage.CoveragePercent = float64(known) / float64(known+m.Coverage.UnresolvedGaps) * 100
	}
	m.Coverage.Summary = fmt.Sprintf(
		"%d entidades conocidas, %d con multiples memorias, %d huecos sin resolver",
		m.Coverage.TotalEntities, m.Coverage.ConnectedMemories, m.Coverage.UnresolvedGaps)

	// 3. Lifecycle: do episodic memories get used before dying.
	s.db.QueryRow(`SELECT COUNT(*) FROM memories WHERE project = ? AND kind = 'episodic'`, project).
		Scan(&m.Lifecycle.TotalEpisodic)

	s.db.QueryRow(`SELECT COUNT(*) FROM memories WHERE project = ? AND kind = 'episodic' AND access_count = 0 AND invalid_at IS NULL`, project).
		Scan(&m.Lifecycle.NeverAccessed)

	s.db.QueryRow(`SELECT COUNT(*) FROM memories WHERE project = ? AND kind = 'episodic' AND access_count >= 1`, project).
		Scan(&m.Lifecycle.AccessedOnce)

	s.db.QueryRow(`SELECT COUNT(*) FROM memories WHERE project = ? AND kind = 'episodic' AND consolidated_at IS NOT NULL`, project).
		Scan(&m.Lifecycle.Consolidated)

	s.db.QueryRow(`SELECT COUNT(*) FROM memories WHERE project = ? AND kind = 'episodic' AND invalid_at IS NOT NULL`, project).
		Scan(&m.Lifecycle.Zombified)

	if m.Lifecycle.TotalEpisodic > 0 {
		m.Lifecycle.UsefulPercent = float64(m.Lifecycle.AccessedOnce) / float64(m.Lifecycle.TotalEpisodic) * 100
		zombieNeverUsed := 0
		s.db.QueryRow(`SELECT COUNT(*) FROM memories WHERE project = ? AND kind = 'episodic' AND invalid_at IS NOT NULL AND access_count = 0`, project).
			Scan(&zombieNeverUsed)
		m.Lifecycle.WastePercent = float64(zombieNeverUsed) / float64(m.Lifecycle.TotalEpisodic) * 100
	}
	m.Lifecycle.Summary = fmt.Sprintf(
		"%.0f%% de episodicas consultadas al menos 1 vez, %.0f%% murieron sin usarse",
		m.Lifecycle.UsefulPercent, m.Lifecycle.WastePercent)

	return m, nil
}
