package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
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
	assertTableExists(t, store.DB(), "observation_events")
	assertTableExists(t, store.DB(), "observations_fts")
	assertTableExists(t, store.DB(), "user_prompts")
	assertTableExists(t, store.DB(), "prompts_fts")
	assertTableExists(t, store.DB(), "sync_chunks")
	assertTableExists(t, store.DB(), "project_aliases")
}

func TestTrustLedgerRecordsObservationLifecycle(t *testing.T) {
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

	observation, err := store.SaveObservation(ctx, ObservationInput{
		Type:     "decision",
		Title:    "Trust ledger",
		Content:  "Kerebrom should track the lifecycle of durable memory.",
		Project:  "Proyecto Kerebrom",
		ToolName: "test",
	})
	if err != nil {
		t.Fatalf("save observation: %v", err)
	}
	if _, err := store.SaveObservation(ctx, ObservationInput{
		Type:     "decision",
		Title:    "Trust ledger",
		Content:  "Kerebrom should track the lifecycle of durable memory.",
		Project:  "Proyecto Kerebrom",
		ToolName: "test",
	}); err != nil {
		t.Fatalf("reassert observation: %v", err)
	}
	if _, err := store.UpdateObservation(ctx, UpdateObservationInput{
		ID:      observation.ID,
		Content: "Kerebrom should track create, reassert, update, and delete lifecycle events.",
	}); err != nil {
		t.Fatalf("update observation: %v", err)
	}
	if err := store.DeleteObservation(ctx, DeleteObservationInput{ID: observation.ID}); err != nil {
		t.Fatalf("soft delete observation: %v", err)
	}

	events, err := store.ListObservationEvents(ctx, observation.ID, 10)
	if err != nil {
		t.Fatalf("list observation events: %v", err)
	}
	eventTypes := map[string]bool{}
	for _, event := range events {
		eventTypes[event.EventType] = true
	}
	for _, expected := range []string{"created", "reasserted", "updated", "soft_deleted"} {
		if !eventTypes[expected] {
			t.Fatalf("missing trust ledger event %q in %+v", expected, events)
		}
	}
}

func TestInitMigratesLegacyObservationsWithSemanticClock(t *testing.T) {
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

	if _, err := store.DB().ExecContext(ctx, `
		CREATE TABLE observations (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT,
			type TEXT NOT NULL,
			title TEXT NOT NULL,
			content TEXT NOT NULL,
			tool_name TEXT,
			project TEXT NOT NULL,
			scope TEXT NOT NULL DEFAULT 'project',
			topic_key TEXT,
			normalized_hash TEXT NOT NULL DEFAULT '',
			revision_count INTEGER NOT NULL DEFAULT 0,
			duplicate_count INTEGER NOT NULL DEFAULT 0,
			last_seen_at TEXT,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			deleted_at TEXT
		)
	`); err != nil {
		t.Fatalf("create legacy observations table: %v", err)
	}
	if _, err := store.DB().ExecContext(ctx, `
		INSERT INTO observations (type, title, content, project, created_at, updated_at)
		VALUES ('decision', 'Legacy note', 'Legacy content', 'legacy', '2026-01-01T00:00:00Z', '2026-04-01T00:00:00Z')
	`); err != nil {
		t.Fatalf("insert legacy observation: %v", err)
	}
	if _, err := store.DB().ExecContext(ctx, `
		CREATE VIRTUAL TABLE observations_fts USING fts5(
			title,
			content,
			tool_name,
			type,
			project,
			content='observations',
			content_rowid='id'
		)
	`); err != nil {
		t.Fatalf("create legacy observations fts: %v", err)
	}
	if _, err := store.DB().ExecContext(ctx, `
		INSERT INTO observations_fts(rowid, title, content, tool_name, type, project)
		VALUES (1, 'Legacy note', 'Legacy content', '', 'decision', 'legacy')
	`); err != nil {
		t.Fatalf("seed legacy observations fts: %v", err)
	}

	if err := store.Init(ctx); err != nil {
		t.Fatalf("migrate legacy store: %v", err)
	}

	observation, err := store.GetObservation(ctx, 1)
	if err != nil {
		t.Fatalf("get migrated observation: %v", err)
	}
	if observation.ValidAt != observation.CreatedAt {
		t.Fatalf("legacy metadata update should not become semantic recency: %+v", observation)
	}
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

func TestRetrievalUsesSemanticValidityClock(t *testing.T) {
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

	oldTime := mustParseTime(t, "2026-01-01T00:00:00Z")
	newTime := mustParseTime(t, "2026-04-01T00:00:00Z")

	oldObservation, err := store.SaveObservation(ctx, ObservationInput{
		Type:      "decision",
		Title:     "Falage strategy outcome",
		Content:   "Falage strategy outcome was initially thought to be valid. Falage strategy outcome valid valid valid.",
		Project:   "Proyecto Falage",
		CreatedAt: oldTime,
	})
	if err != nil {
		t.Fatalf("save old observation: %v", err)
	}
	newObservation, err := store.SaveObservation(ctx, ObservationInput{
		Type:      "bugfix",
		Title:     "Falage strategy outcome corrected",
		Content:   "Falage strategy outcome was corrected after validation; the corrected result is the source of truth.",
		Project:   "Proyecto Falage",
		CreatedAt: newTime,
	})
	if err != nil {
		t.Fatalf("save new observation: %v", err)
	}

	results, err := store.SearchObservations(ctx, SearchOptions{
		Query:   "Falage strategy outcome",
		Project: "Proyecto Falage",
		Limit:   5,
	})
	if err != nil {
		t.Fatalf("search observations: %v", err)
	}
	if len(results) < 2 {
		t.Fatalf("expected both observations, got %+v", results)
	}
	if results[0].ID != newObservation.ID {
		t.Fatalf("expected semantically latest observation %d before old observation %d, got %+v", newObservation.ID, oldObservation.ID, results)
	}

	recent, err := store.ListObservations(ctx, ListObservationOptions{
		Project: "Proyecto Falage",
		Limit:   5,
	})
	if err != nil {
		t.Fatalf("list observations: %v", err)
	}
	if len(recent) == 0 || recent[0].ID != newObservation.ID {
		t.Fatalf("expected recent list to use valid_at and return %d first, got %+v", newObservation.ID, recent)
	}
}

func TestTopicKeyUpdateRefreshesSemanticValidityClock(t *testing.T) {
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

	createdAt := mustParseTime(t, "2026-01-01T00:00:00Z")
	original, err := store.SaveObservation(ctx, ObservationInput{
		Type:      "decision",
		Title:     "Claude Chat behavior",
		Content:   "Claude Chat behavior was believed to be fully automatic.",
		Project:   "Proyecto Kerebrom",
		TopicKey:  "kerebrom/claude-chat/behavior",
		CreatedAt: createdAt,
	})
	if err != nil {
		t.Fatalf("save original observation: %v", err)
	}
	updated, err := store.SaveObservation(ctx, ObservationInput{
		Type:     "bugfix",
		Title:    "Claude Chat behavior corrected",
		Content:  "Claude Chat behavior is host-bounded; Kerebrom can instruct but cannot force every tool call.",
		Project:  "Proyecto Kerebrom",
		TopicKey: "kerebrom/claude-chat/behavior",
	})
	if err != nil {
		t.Fatalf("save updated observation: %v", err)
	}

	if updated.ID != original.ID {
		t.Fatalf("same topic_key should update existing observation, got original=%d updated=%d", original.ID, updated.ID)
	}
	if updated.ValidAt == "" || updated.ValidAt == updated.CreatedAt {
		t.Fatalf("semantic correction should refresh valid_at without changing created_at: %+v", updated)
	}
}

func TestProjectMergeDoesNotMakeHistoricalKnowledgeSemanticallyRecent(t *testing.T) {
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

	oldTime := mustParseTime(t, "2026-01-01T00:00:00Z")
	newTime := mustParseTime(t, "2026-04-01T00:00:00Z")
	oldObservation, err := store.SaveObservation(ctx, ObservationInput{
		Type:      "discovery",
		Title:     "Legacy Kerebrom note",
		Content:   "Historical Kerebrom note before project consolidation.",
		Project:   "Kerebrom",
		CreatedAt: oldTime,
	})
	if err != nil {
		t.Fatalf("save old observation: %v", err)
	}
	newObservation, err := store.SaveObservation(ctx, ObservationInput{
		Type:      "release",
		Title:     "Kerebrom current release",
		Content:   "Current Kerebrom release after project consolidation.",
		Project:   "Proyecto Kerebrom",
		CreatedAt: newTime,
	})
	if err != nil {
		t.Fatalf("save new observation: %v", err)
	}

	if _, err := store.MergeProjects(ctx, []string{"Kerebrom"}, "Proyecto Kerebrom"); err != nil {
		t.Fatalf("merge project: %v", err)
	}

	oldAfterMerge, err := store.GetObservation(ctx, oldObservation.ID)
	if err != nil {
		t.Fatalf("get old observation after merge: %v", err)
	}
	if oldAfterMerge.ValidAt != oldObservation.ValidAt {
		t.Fatalf("project merge should not change semantic valid_at: before=%+v after=%+v", oldObservation, oldAfterMerge)
	}

	recent, err := store.ListObservations(ctx, ListObservationOptions{
		Project: "Proyecto Kerebrom",
		Limit:   5,
	})
	if err != nil {
		t.Fatalf("list observations: %v", err)
	}
	if len(recent) == 0 || recent[0].ID != newObservation.ID {
		t.Fatalf("expected current observation %d before merged historical observation %d, got %+v", newObservation.ID, oldObservation.ID, recent)
	}
}

func TestProjectMergeRecomputesObservationHashes(t *testing.T) {
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

	mergedObservation, err := store.SaveObservation(ctx, ObservationInput{
		Type:     "decision",
		Title:    "Kerebrom project hash",
		Content:  "Project consolidation must not leave stale normalized hashes.",
		Project:  "Kerebrom",
		Scope:    "project",
		TopicKey: "kerebrom/project-hash",
	})
	if err != nil {
		t.Fatalf("save source observation: %v", err)
	}

	if _, err := store.MergeProjects(ctx, []string{"Kerebrom"}, "Proyecto Kerebrom"); err != nil {
		t.Fatalf("merge project: %v", err)
	}

	afterMerge, err := store.GetObservation(ctx, mergedObservation.ID)
	if err != nil {
		t.Fatalf("get merged observation: %v", err)
	}
	if afterMerge.Project != "proyecto-kerebrom" {
		t.Fatalf("expected canonical project after merge, got %+v", afterMerge)
	}
	if afterMerge.ValidAt != mergedObservation.ValidAt {
		t.Fatalf("project merge should preserve semantic valid_at: before=%+v after=%+v", mergedObservation, afterMerge)
	}

	reasserted, err := store.SaveObservation(ctx, ObservationInput{
		Type:     "decision",
		Title:    "Kerebrom project hash",
		Content:  "Project consolidation must not leave stale normalized hashes.",
		Project:  "Proyecto Kerebrom",
		Scope:    "project",
		TopicKey: "kerebrom/project-hash",
	})
	if err != nil {
		t.Fatalf("save canonical duplicate after merge: %v", err)
	}
	if reasserted.ID != mergedObservation.ID {
		t.Fatalf("expected recomputed hash/topic to reuse merged observation %d, got %+v", mergedObservation.ID, reasserted)
	}
	if reasserted.DuplicateCount == 0 && reasserted.RevisionCount == 0 {
		t.Fatalf("expected duplicate reassertion or topic update to touch merged observation, got %+v", reasserted)
	}
}

func TestProjectMergeDeduplicatesEquivalentActiveObservations(t *testing.T) {
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

	sourceObservation, err := store.SaveObservation(ctx, ObservationInput{
		Type:    "decision",
		Title:   "Same project fact",
		Content: "The same durable fact existed under a project variant.",
		Project: "Kerebrom",
	})
	if err != nil {
		t.Fatalf("save source observation: %v", err)
	}
	targetObservation, err := store.SaveObservation(ctx, ObservationInput{
		Type:    "decision",
		Title:   "Same project fact",
		Content: "The same durable fact existed under a project variant.",
		Project: "Proyecto Kerebrom",
	})
	if err != nil {
		t.Fatalf("save target observation: %v", err)
	}

	if _, err := store.MergeProjects(ctx, []string{"Kerebrom"}, "Proyecto Kerebrom"); err != nil {
		t.Fatalf("merge project: %v", err)
	}

	sourceAfter, err := store.GetObservation(ctx, sourceObservation.ID)
	if err != nil {
		t.Fatalf("get source after merge: %v", err)
	}
	if sourceAfter.DeletedAt == "" {
		t.Fatalf("expected source duplicate to be soft-deleted, got %+v", sourceAfter)
	}
	targetAfter, err := store.GetObservation(ctx, targetObservation.ID)
	if err != nil {
		t.Fatalf("get target after merge: %v", err)
	}
	if targetAfter.DuplicateCount == 0 {
		t.Fatalf("expected target duplicate count to increase, got %+v", targetAfter)
	}

	active, err := store.ListObservations(ctx, ListObservationOptions{Project: "Proyecto Kerebrom", Limit: 10})
	if err != nil {
		t.Fatalf("list active observations: %v", err)
	}
	if len(active) != 1 || active[0].ID != targetObservation.ID {
		t.Fatalf("expected only target observation to remain active, got %+v", active)
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

func TestEndSessionWithSummaryObservationPersistsRecallableSummary(t *testing.T) {
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
		ID:        "session-summary-1",
		Project:   "Proyecto Kerebrom",
		Directory: ".",
	}); err != nil {
		t.Fatalf("start session: %v", err)
	}

	observation, saved, err := store.EndSessionWithSummaryObservation(ctx, EndSessionInput{
		ID:      "session-summary-1",
		Summary: "Release maintenance finished with security and recall fixes.",
	}, "session-end")
	if err != nil {
		t.Fatalf("end session with summary observation: %v", err)
	}
	if !saved {
		t.Fatalf("expected a summary observation to be saved")
	}
	if observation.Type != "session_summary" || observation.TopicKey != "session/session-summary-1" {
		t.Fatalf("unexpected summary observation: %+v", observation)
	}

	results, err := store.SearchObservations(ctx, SearchOptions{
		Query:   "security and recall fixes",
		Project: "Proyecto Kerebrom",
		Limit:   5,
	})
	if err != nil {
		t.Fatalf("search summary observation: %v", err)
	}
	if len(results) == 0 || results[0].ID != observation.ID {
		t.Fatalf("summary observation should be recallable, got %#v", results)
	}
}

func TestEndSessionWithSummaryObservationDoesNotDowngradeCompletedSummary(t *testing.T) {
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
		ID:        "session-summary-idempotent",
		Project:   "Proyecto Kerebrom",
		Directory: ".",
	}); err != nil {
		t.Fatalf("start session: %v", err)
	}

	first, saved, err := store.EndSessionWithSummaryObservation(ctx, EndSessionInput{
		ID:      "session-summary-idempotent",
		Summary: "Useful final summary with decisions, risks, and next steps.",
	}, "summary")
	if err != nil {
		t.Fatalf("first end session: %v", err)
	}
	if !saved {
		t.Fatalf("expected first summary observation to be saved")
	}

	second, saved, err := store.EndSessionWithSummaryObservation(ctx, EndSessionInput{
		ID:      "session-summary-idempotent",
		Summary: "",
	}, "kerebrom_hook_session_stop")
	if err != nil {
		t.Fatalf("second end session: %v", err)
	}
	if !saved {
		t.Fatalf("expected existing summary observation to be returned")
	}
	if second.ID != first.ID || second.Content != first.Content {
		t.Fatalf("empty second close should preserve first summary: first=%+v second=%+v", first, second)
	}

	session, err := store.GetSession(ctx, "session-summary-idempotent")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if session.Summary != first.Content {
		t.Fatalf("session summary was downgraded: %+v", session)
	}
}

func mustParseTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatalf("parse time %q: %v", value, err)
	}
	return parsed
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
