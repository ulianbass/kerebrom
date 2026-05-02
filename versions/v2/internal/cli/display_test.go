package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ulianbass/kerebrom/internal/store/sqlite"
)

func TestDisplayLabelHumanizesInternalIdentifiers(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"UserPromptSubmit": "User Prompt Submit",
		"SubagentStop":     "Subagent Stop",
		"session_summary":  "Session Summary",
		"soft_deleted":     "Soft Deleted",
		"project-merged":   "Project Merged",
		"completed":        "Completed",
		"Stop":             "Session Stop",
	}
	for input, want := range tests {
		if got := displayLabel(input); got != want {
			t.Fatalf("displayLabel(%q)=%q, want %q", input, got, want)
		}
	}
}

func TestHumanReadablePrintHelpersHideInternalIdentifiers(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	printObservation(&out, sqlite.Observation{
		ID:      1,
		Project: "Proyecto Kerebrom",
		Type:    "session_summary",
		Title:   "Session summary",
		Content: "Closed with useful context.",
		ValidAt: "2026-05-02T00:00:00Z",
	})
	printTimelinePayload(&out, map[string]any{
		"events": []sqlite.ObservationEvent{{
			ID:            2,
			ObservationID: 1,
			EventType:     "soft_deleted",
			CreatedAt:     "2026-05-02T00:01:00Z",
			Reason:        "Invalidated without removing history.",
		}},
	})
	printSession(&out, sqlite.Session{
		ID:        "session-1",
		Project:   "Proyecto Kerebrom",
		Status:    "completed",
		StartedAt: "2026-05-02T00:00:00Z",
	})

	text := out.String()
	for _, want := range []string{"Session Summary", "Soft Deleted", "Completed"} {
		if !strings.Contains(text, want) {
			t.Fatalf("output missing human label %q: %q", want, text)
		}
	}
	for _, raw := range []string{"session_summary", "soft_deleted"} {
		if strings.Contains(text, raw) {
			t.Fatalf("output leaked internal identifier %q: %q", raw, text)
		}
	}
}

func TestHookProtocolUsesHumanLifecycleLabels(t *testing.T) {
	t.Parallel()

	text := hookProtocol("Kerebrom memory is active.")
	for _, want := range []string{"Session start", "User prompt submit", "Subagent stop", "Session stop"} {
		if !strings.Contains(text, want) {
			t.Fatalf("protocol missing human lifecycle label %q: %q", want, text)
		}
	}
	for _, raw := range []string{"SessionStart", "UserPromptSubmit", "SubagentStop"} {
		if strings.Contains(text, raw) {
			t.Fatalf("protocol leaked internal hook identifier %q: %q", raw, text)
		}
	}
}
