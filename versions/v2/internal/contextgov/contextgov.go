package contextgov

import (
	"sort"
	"strings"

	"github.com/ulianbass/kerebrom/internal/store/sqlite"
)

// Bundle is the compact contract returned with every Kerebrom context payload.
// It is intentionally small: agents should be nudged into the right retrieval
// behavior without spending the recovered context window on policy text.
type Bundle struct {
	Policy                 string              `json:"policy"`
	RequiredSequence       []string            `json:"required_sequence"`
	PrimaryClock           string              `json:"primary_clock"`
	DecisionRules          []string            `json:"decision_rules"`
	ProjectFilter          string              `json:"project_filter"`
	ProjectFilterRelaxed   bool                `json:"project_filter_relaxed"`
	RecentCount            int                 `json:"recent_count"`
	MatchCount             int                 `json:"match_count"`
	CanonicalTopicCount    int                 `json:"canonical_topic_count"`
	UntopicedMatchCount    int                 `json:"untopiced_match_count"`
	ConflictCandidates     []ConflictCandidate `json:"conflict_candidates,omitempty"`
	TrustLedgerExpectation string              `json:"trust_ledger_expectation"`
}

type ConflictCandidate struct {
	TopicKey              string  `json:"topic_key"`
	LatestObservationID   int64   `json:"latest_observation_id"`
	LatestValidAt         string  `json:"latest_valid_at"`
	OlderObservationIDs   []int64 `json:"older_observation_ids"`
	ResolutionInstruction string  `json:"resolution_instruction"`
}

func Build(recent []sqlite.Observation, matches []sqlite.Observation, projectFilter string, projectFilterRelaxed bool) Bundle {
	combined := uniqueObservations(append(append([]sqlite.Observation{}, matches...), recent...))
	conflicts := conflictCandidates(combined)

	return Bundle{
		Policy:           "think -> search -> analyze -> answer",
		RequiredSequence: []string{"context", "recall/timeline if the user asks about prior work or if memories conflict", "answer", "remember if the turn produced durable knowledge", "summary at close"},
		PrimaryClock:     "valid_at",
		DecisionRules: []string{
			"Use matches before broad recency when a query is present.",
			"Use recent_observations to recover current project direction.",
			"Prefer the newest valid_at when memories conflict.",
			"Use timeline before answering if returned memories disagree or appear stale.",
			"Reuse topic_key when saving corrections so the canonical memory is updated.",
		},
		ProjectFilter:          strings.TrimSpace(projectFilter),
		ProjectFilterRelaxed:   projectFilterRelaxed,
		RecentCount:            len(recent),
		MatchCount:             len(matches),
		CanonicalTopicCount:    canonicalTopicCount(combined),
		UntopicedMatchCount:    untopicedCount(matches),
		ConflictCandidates:     conflicts,
		TrustLedgerExpectation: "Every observation should have a local trust-ledger trail of create/update/reassert/delete/import events.",
	}
}

func SummaryText(bundle Bundle) string {
	status := "strict"
	if bundle.ProjectFilterRelaxed {
		status = "relaxed-cross-project"
	}
	return "policy=" + bundle.Policy +
		" primary_clock=" + bundle.PrimaryClock +
		" project_filter=" + defaultString(bundle.ProjectFilter, "<all>") +
		" mode=" + status
}

func uniqueObservations(observations []sqlite.Observation) []sqlite.Observation {
	seen := map[int64]bool{}
	out := make([]sqlite.Observation, 0, len(observations))
	for _, observation := range observations {
		if observation.ID <= 0 || seen[observation.ID] {
			continue
		}
		seen[observation.ID] = true
		out = append(out, observation)
	}
	return out
}

func conflictCandidates(observations []sqlite.Observation) []ConflictCandidate {
	byTopic := map[string][]sqlite.Observation{}
	for _, observation := range observations {
		topicKey := strings.TrimSpace(observation.TopicKey)
		if topicKey == "" {
			continue
		}
		byTopic[topicKey] = append(byTopic[topicKey], observation)
	}

	var conflicts []ConflictCandidate
	for topicKey, group := range byTopic {
		if len(group) < 2 {
			continue
		}
		sort.SliceStable(group, func(i, j int) bool {
			if group[i].ValidAt == group[j].ValidAt {
				return group[i].ID > group[j].ID
			}
			return group[i].ValidAt > group[j].ValidAt
		})
		older := make([]int64, 0, len(group)-1)
		for _, observation := range group[1:] {
			older = append(older, observation.ID)
		}
		conflicts = append(conflicts, ConflictCandidate{
			TopicKey:              topicKey,
			LatestObservationID:   group[0].ID,
			LatestValidAt:         group[0].ValidAt,
			OlderObservationIDs:   older,
			ResolutionInstruction: "Prefer latest_observation_id, then call timeline if the answer depends on the correction history.",
		})
	}
	sort.SliceStable(conflicts, func(i, j int) bool {
		if conflicts[i].LatestValidAt == conflicts[j].LatestValidAt {
			return conflicts[i].TopicKey < conflicts[j].TopicKey
		}
		return conflicts[i].LatestValidAt > conflicts[j].LatestValidAt
	})
	return conflicts
}

func canonicalTopicCount(observations []sqlite.Observation) int {
	topics := map[string]bool{}
	for _, observation := range observations {
		topicKey := strings.TrimSpace(observation.TopicKey)
		if topicKey != "" {
			topics[topicKey] = true
		}
	}
	return len(topics)
}

func untopicedCount(observations []sqlite.Observation) int {
	count := 0
	for _, observation := range observations {
		if strings.TrimSpace(observation.TopicKey) == "" {
			count++
		}
	}
	return count
}

func defaultString(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
