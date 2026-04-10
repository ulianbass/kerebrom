package store

import (
	"fmt"
	"math"
)

// DecayResult summarizes the outcome of ApplyDecay.
type DecayResult struct {
	TotalActive       int     `json:"total_active"`
	Decayed           int     `json:"decayed"`
	InvalidatedZombie int     `json:"invalidated_zombies"`
	HalfLifeDays      float64 `json:"half_life_days"`
	Kind              string  `json:"kind,omitempty"`
}

// ApplyDecay runs FSRS-inspired exponential decay on all active memories.
// Memories whose importance drops below DormantImportance are invalidated.
func (s *Store) ApplyDecay(project string) ([]DecayResult, error) {
	if project == "" {
		project = DefaultProject
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	var results []DecayResult
	for kind, halfLife := range HalfLifeByKind {
		r, err := s.applyDecayForKind(project, kind, halfLife)
		if err != nil {
			return results, fmt.Errorf("decay %s: %w", kind, err)
		}
		results = append(results, *r)
	}
	return results, nil
}

func (s *Store) applyDecayForKind(project, kind string, halfLifeDays float64) (*DecayResult, error) {
	ts := now()
	lambda := math.Ln2 / halfLifeDays

	rows, err := s.db.Query(`
		SELECT id, importance, access_count,
		       COALESCE(last_decay_at, created_at) as baseline,
		       created_at
		FROM memories
		WHERE project = ? AND kind = ? AND invalid_at IS NULL`,
		project, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := &DecayResult{Kind: kind, HalfLifeDays: halfLifeDays}
	type memRow struct {
		id          int64
		importance  float64
		accessCount int
		baseline    string
		createdAt   string
	}
	var mems []memRow
	for rows.Next() {
		var m memRow
		rows.Scan(&m.id, &m.importance, &m.accessCount, &m.baseline, &m.createdAt)
		mems = append(mems, m)
		result.TotalActive++
	}

	for _, m := range mems {
		baselineT, err := parseTime(m.baseline)
		if err != nil {
			continue
		}
		ageDays := timeSince(baselineT).Hours() / 24
		if ageDays < 0.01 {
			continue // too recent to decay
		}

		// Reinforcement: frequent access slows decay.
		reinforcement := 1.0 + 0.15*math.Log1p(float64(m.accessCount))
		newImportance := m.importance * math.Exp(-lambda*ageDays/reinforcement)

		if newImportance < DormantImportance {
			// Invalidate zombie memory.
			s.db.Exec(`UPDATE memories SET invalid_at = ?, importance = ?, last_decay_at = ?, updated_at = ? WHERE id = ?`,
				ts, newImportance, ts, ts, m.id)
			result.InvalidatedZombie++
			result.Decayed++
		} else if math.Abs(newImportance-m.importance) > 0.001 {
			s.db.Exec(`UPDATE memories SET importance = ?, last_decay_at = ?, updated_at = ? WHERE id = ?`,
				newImportance, ts, ts, m.id)
			result.Decayed++
		}
	}
	return result, nil
}
