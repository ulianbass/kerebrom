package contextgov

import (
	"testing"

	"github.com/ulianbass/kerebrom/internal/store/sqlite"
)

func TestBuildReportsPolicyAndConflictCandidates(t *testing.T) {
	recent := []sqlite.Observation{
		{ID: 1, TopicKey: "project/fact", ValidAt: "2026-01-01T00:00:00Z"},
		{ID: 2, TopicKey: "project/fact", ValidAt: "2026-04-01T00:00:00Z"},
	}
	matches := []sqlite.Observation{
		{ID: 3, TopicKey: "", ValidAt: "2026-04-02T00:00:00Z"},
	}

	bundle := Build(recent, matches, "proyecto-kerebrom", false)
	if bundle.PrimaryClock != "valid_at" {
		t.Fatalf("expected valid_at primary clock, got %+v", bundle)
	}
	if bundle.UntopicedMatchCount != 1 {
		t.Fatalf("expected one untopiced match, got %+v", bundle)
	}
	if len(bundle.ConflictCandidates) != 1 {
		t.Fatalf("expected one conflict candidate, got %+v", bundle)
	}
	if bundle.ConflictCandidates[0].LatestObservationID != 2 {
		t.Fatalf("expected latest observation 2, got %+v", bundle.ConflictCandidates[0])
	}
}
