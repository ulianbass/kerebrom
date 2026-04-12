package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestOpenAndInitCreatesSchema(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "kerebrom.db")

	store, err := Open(Config{Path: dbPath})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	if err := store.Init(context.Background()); err != nil {
		t.Fatalf("init store: %v", err)
	}

	assertTableExists(t, store.DB(), "sessions")
	assertTableExists(t, store.DB(), "observations")
	assertTableExists(t, store.DB(), "observations_fts")
	assertTableExists(t, store.DB(), "user_prompts")
	assertTableExists(t, store.DB(), "prompts_fts")
	assertTableExists(t, store.DB(), "sync_chunks")
}

func TestSessionObservationSearchAndStats(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "kerebrom.db")

	store, err := Open(Config{Path: dbPath})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	if err := store.Init(ctx); err != nil {
		t.Fatalf("init store: %v", err)
	}

	if err := store.StartSession(ctx, StartSessionInput{
		ID:        "session-1",
		Project:   "Proyecto Kerebrom",
		Directory: "/tmp/project",
	}); err != nil {
		t.Fatalf("start session: %v", err)
	}

	session, err := store.GetSession(ctx, "session-1")
	if err != nil {
		t.Fatalf("get session after start: %v", err)
	}

	if session.Project != "proyecto-kerebrom" {
		t.Fatalf("expected normalized session project, got %q", session.Project)
	}

	observation, err := store.SaveObservation(ctx, ObservationInput{
		SessionID: "session-1",
		Type:      "decision",
		Title:     "Chose shared local memory",
		Content:   "Codex and Claude must share the same local store.",
		Project:   "Proyecto Kerebrom",
		Scope:     "project",
		TopicKey:  "architecture/shared-memory",
	})
	if err != nil {
		t.Fatalf("save observation: %v", err)
	}

	if observation.Project != "proyecto-kerebrom" {
		t.Fatalf("expected normalized project, got %q", observation.Project)
	}

	secondObservation, err := store.SaveObservation(ctx, ObservationInput{
		SessionID: "session-1",
		Type:      "learning",
		Title:     "Need context bundle",
		Content:   "Agents need recent observations and search matches to recover quickly.",
		Project:   "Proyecto Kerebrom",
		Scope:     "project",
	})
	if err != nil {
		t.Fatalf("save second observation: %v", err)
	}

	results, err := store.SearchObservations(ctx, SearchOptions{
		Query:   "shared local memory",
		Project: "Proyecto Kerebrom",
		Limit:   5,
	})
	if err != nil {
		t.Fatalf("search observations: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	if results[0].ID != observation.ID {
		t.Fatalf("expected observation id %d, got %d", observation.ID, results[0].ID)
	}

	listed, err := store.ListObservations(ctx, ListObservationOptions{
		Project:   "Proyecto Kerebrom",
		SessionID: "session-1",
		Limit:     5,
	})
	if err != nil {
		t.Fatalf("list observations: %v", err)
	}

	if len(listed) != 2 {
		t.Fatalf("expected 2 listed observations, got %d", len(listed))
	}

	if listed[0].ID != secondObservation.ID {
		t.Fatalf("expected most recent observation first, got %d", listed[0].ID)
	}

	count, err := store.CountSessionObservations(ctx, "session-1")
	if err != nil {
		t.Fatalf("count session observations: %v", err)
	}

	if count != 2 {
		t.Fatalf("expected 2 session observations, got %d", count)
	}

	stats, err := store.Stats(ctx, "Proyecto Kerebrom")
	if err != nil {
		t.Fatalf("stats: %v", err)
	}

	if stats.SessionCount != 1 || stats.ActiveSessionCount != 1 || stats.ObservationCount != 2 || stats.ProjectCount != 1 {
		t.Fatalf("unexpected stats: %+v", stats)
	}

	if err := store.EndSession(ctx, EndSessionInput{
		ID:      "session-1",
		Summary: "Initial memory workflow complete.",
	}); err != nil {
		t.Fatalf("end session: %v", err)
	}

	session, err = store.GetSession(ctx, "session-1")
	if err != nil {
		t.Fatalf("get session after end: %v", err)
	}

	if session.Status != "completed" {
		t.Fatalf("expected completed session status, got %+v", session)
	}

	if session.Summary != "Initial memory workflow complete." {
		t.Fatalf("unexpected session summary: %+v", session)
	}

	stats, err = store.Stats(ctx, "Proyecto Kerebrom")
	if err != nil {
		t.Fatalf("stats after session end: %v", err)
	}

	if stats.ActiveSessionCount != 0 {
		t.Fatalf("expected no active sessions after end, got %+v", stats)
	}

	if err := store.StartSession(ctx, StartSessionInput{
		ID:        "session-1",
		Project:   "Updated Project",
		Directory: "/tmp/updated",
	}); err != nil {
		t.Fatalf("restart completed session: %v", err)
	}

	session, err = store.GetSession(ctx, "session-1")
	if err != nil {
		t.Fatalf("get session after idempotent restart: %v", err)
	}
	if session.Status != "completed" {
		t.Fatalf("restart should not reactivate completed session, got %+v", session)
	}

	stats, err = store.Stats(ctx, "Proyecto Kerebrom")
	if err != nil {
		t.Fatalf("stats after idempotent restart: %v", err)
	}
	if stats.ActiveSessionCount != 0 {
		t.Fatalf("expected no active sessions after idempotent restart, got %+v", stats)
	}
}

func assertTableExists(t *testing.T, db *sql.DB, name string) {
	t.Helper()

	var actual string
	err := db.QueryRow(
		`SELECT name FROM sqlite_master WHERE type IN ('table', 'view') AND name = ?`,
		name,
	).Scan(&actual)
	if err != nil {
		t.Fatalf("find table %s: %v", name, err)
	}

	if actual != name {
		t.Fatalf("expected table %s, got %s", name, actual)
	}
}
