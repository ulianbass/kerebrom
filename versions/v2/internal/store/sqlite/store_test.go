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
	assertTableExists(t, store.DB(), "project_aliases")
}

func TestInitRepairsEndedActiveSessions(t *testing.T) {
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

	_, err = store.DB().ExecContext(ctx, `
		INSERT INTO sessions (id, project, directory, started_at, ended_at, status)
		VALUES ('session-1', 'project', '.', '2026-04-12T00:00:00Z', '2026-04-12T01:00:00Z', 'active')
	`)
	if err != nil {
		t.Fatalf("insert inconsistent session: %v", err)
	}

	if err := store.Init(ctx); err != nil {
		t.Fatalf("repair init: %v", err)
	}

	session, err := store.GetSession(ctx, "session-1")
	if err != nil {
		t.Fatalf("get repaired session: %v", err)
	}
	if session.Status != "completed" {
		t.Fatalf("expected completed status after repair, got %+v", session)
	}
}

func TestInitAutoClosesStaleActiveSessions(t *testing.T) {
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

	_, err = store.DB().ExecContext(ctx, `
		INSERT INTO sessions (id, project, directory, started_at, status)
		VALUES ('stale-session', 'project', '.', '2000-01-01T00:00:00Z', 'active')
	`)
	if err != nil {
		t.Fatalf("insert stale session: %v", err)
	}

	if err := store.Init(ctx); err != nil {
		t.Fatalf("stale repair init: %v", err)
	}

	session, err := store.GetSession(ctx, "stale-session")
	if err != nil {
		t.Fatalf("get stale session: %v", err)
	}
	if session.Status != "completed" {
		t.Fatalf("expected stale session to be completed, got %+v", session)
	}
	if session.Summary != "Auto-closed by Kerebrom after 24h without activity." {
		t.Fatalf("unexpected stale session summary: %+v", session)
	}
}

func TestProjectAliasesResolveWritesReadsAndMerges(t *testing.T) {
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

	if _, err := store.SetProjectAliases(ctx, []string{"Falage", "Quamtos"}, "Proyecto Falage"); err != nil {
		t.Fatalf("set project aliases: %v", err)
	}

	if err := store.StartSession(ctx, StartSessionInput{
		ID:      "alias-session",
		Project: "Falage",
	}); err != nil {
		t.Fatalf("start aliased session: %v", err)
	}
	session, err := store.GetSession(ctx, "alias-session")
	if err != nil {
		t.Fatalf("get aliased session: %v", err)
	}
	if session.Project != "proyecto-falage" {
		t.Fatalf("expected aliased session project, got %+v", session)
	}

	observation, err := store.SaveObservation(ctx, ObservationInput{
		SessionID: "alias-session",
		Type:      "decision",
		Title:     "Quamtos belongs to Falage",
		Content:   "Quamtos memories should resolve through the Falage project alias.",
		Project:   "Quamtos",
	})
	if err != nil {
		t.Fatalf("save aliased observation: %v", err)
	}
	if observation.Project != "proyecto-falage" {
		t.Fatalf("expected aliased observation project, got %+v", observation)
	}

	results, err := store.SearchObservations(ctx, SearchOptions{
		Query:   "Quamtos Falage",
		Project: "Falage",
		Limit:   5,
	})
	if err != nil {
		t.Fatalf("search aliased observations: %v", err)
	}
	if len(results) != 1 || results[0].ID != observation.ID {
		t.Fatalf("expected alias search to return observation %d, got %+v", observation.ID, results)
	}

	if _, err := store.SavePrompt(ctx, PromptInput{
		SessionID: "alias-session",
		Content:   "Falage prompt through alias.",
		Project:   "Falage",
	}); err != nil {
		t.Fatalf("save aliased prompt: %v", err)
	}

	stats, err := store.Stats(ctx, "Quamtos")
	if err != nil {
		t.Fatalf("aliased stats: %v", err)
	}
	if stats.SessionCount != 1 || stats.ObservationCount != 1 || stats.PromptCount != 1 {
		t.Fatalf("unexpected aliased stats: %+v", stats)
	}

	if _, err := store.SaveObservation(ctx, ObservationInput{
		Title:   "Kerebrom variant",
		Content: "Kerebrom project variant before consolidation.",
		Project: "Kerebrom",
	}); err != nil {
		t.Fatalf("save pre-merge observation: %v", err)
	}
	if _, err := store.MergeProjects(ctx, []string{"Kerebrom"}, "Proyecto Kerebrom"); err != nil {
		t.Fatalf("merge project variant: %v", err)
	}

	aliases, err := store.ListProjectAliases(ctx)
	if err != nil {
		t.Fatalf("list aliases: %v", err)
	}
	aliasTargets := map[string]string{}
	for _, alias := range aliases {
		aliasTargets[alias.Alias] = alias.Target
	}
	if aliasTargets["kerebrom"] != "proyecto-kerebrom" {
		t.Fatalf("expected merge to persist kerebrom alias, got %+v", aliasTargets)
	}

	prompt, err := store.SavePrompt(ctx, PromptInput{
		Content: "Future Kerebrom writes should use the canonical project.",
		Project: "Kerebrom",
	})
	if err != nil {
		t.Fatalf("save post-merge prompt through alias: %v", err)
	}
	if prompt.Project != "proyecto-kerebrom" {
		t.Fatalf("expected prompt to resolve through merge alias, got %+v", prompt)
	}
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

	exists, err := store.SessionExists(ctx, "session-1")
	if err != nil {
		t.Fatalf("check session exists: %v", err)
	}
	if !exists {
		t.Fatalf("expected session-1 to exist")
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

	topicResults, err := store.SearchObservations(ctx, SearchOptions{
		Query:   "architecture/shared-memory",
		Project: "Proyecto Kerebrom",
		Limit:   5,
	})
	if err != nil {
		t.Fatalf("search observations by topic key: %v", err)
	}
	if len(topicResults) != 1 || topicResults[0].ID != observation.ID {
		t.Fatalf("expected topic key search to return observation %d, got %+v", observation.ID, topicResults)
	}

	globalObservation, err := store.SaveObservation(ctx, ObservationInput{
		Type:     "preference",
		Title:    "Preferencia OBLIGATORIA: todos los outputs en español",
		Content:  "El usuario exige que toda comunicación y respuestas sean en ESPAÑOL.",
		Project:  "Proyecto Falage",
		Scope:    "global",
		TopicKey: "user-language-preference",
	})
	if err != nil {
		t.Fatalf("save global observation: %v", err)
	}

	globalResults, err := store.SearchObservations(ctx, SearchOptions{
		Query:   "idioma español",
		Project: "Proyecto Kerebrom",
		Limit:   5,
	})
	if err != nil {
		t.Fatalf("search global observations from another project: %v", err)
	}
	if len(globalResults) == 0 || globalResults[0].ID != globalObservation.ID {
		t.Fatalf("expected cross-project global observation %d, got %+v", globalObservation.ID, globalResults)
	}

	globalTopicResults, err := store.SearchObservations(ctx, SearchOptions{
		Query:   "user-language-preference",
		Project: "Proyecto Kerebrom",
		Limit:   5,
	})
	if err != nil {
		t.Fatalf("search global topic key without slash: %v", err)
	}
	if len(globalTopicResults) != 1 || globalTopicResults[0].ID != globalObservation.ID {
		t.Fatalf("expected global topic key search to return observation %d, got %+v", globalObservation.ID, globalTopicResults)
	}

	projectOnlyResults, err := store.SearchObservations(ctx, SearchOptions{
		Query:   "español",
		Project: "Proyecto Kerebrom",
		Scope:   "project",
		Limit:   5,
	})
	if err != nil {
		t.Fatalf("search project-only observations: %v", err)
	}
	if len(projectOnlyResults) != 0 {
		t.Fatalf("expected project-only search to exclude global observations, got %+v", projectOnlyResults)
	}

	recentWithGlobal, err := store.ListObservations(ctx, ListObservationOptions{
		Project: "Proyecto Kerebrom",
		Limit:   5,
	})
	if err != nil {
		t.Fatalf("list observations with global visibility: %v", err)
	}
	if len(recentWithGlobal) == 0 || recentWithGlobal[0].ID != globalObservation.ID {
		t.Fatalf("expected recent list to include cross-project global observation %d, got %+v", globalObservation.ID, recentWithGlobal)
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

	prompt, err := store.SavePrompt(ctx, PromptInput{
		SessionID: "session-1",
		Content:   "Remember to keep hook lifecycle idempotent.",
		Project:   "Proyecto Kerebrom",
	})
	if err != nil {
		t.Fatalf("save prompt: %v", err)
	}
	if prompt.SessionID != "session-1" {
		t.Fatalf("expected prompt to be tied to session-1, got %+v", prompt)
	}
	promptCount, err := store.CountSessionPrompts(ctx, "session-1")
	if err != nil {
		t.Fatalf("count session prompts: %v", err)
	}
	if promptCount != 1 {
		t.Fatalf("expected 1 session prompt, got %d", promptCount)
	}

	if err := store.EndSession(ctx, EndSessionInput{
		ID:      "session-1",
		Summary: "Initial memory workflow complete.",
	}); err != nil {
		t.Fatalf("end session: %v", err)
	}
	if err := store.EndSession(ctx, EndSessionInput{
		ID:      "session-that-never-existed",
		Summary: "retry-safe no-op",
	}); err != nil {
		t.Fatalf("end missing session should be idempotent: %v", err)
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
