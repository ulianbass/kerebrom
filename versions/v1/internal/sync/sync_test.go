package sync

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ulianbass/kerebrom/internal/store/sqlite"
)

func TestExportImportRoundTrip(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	source := openTestStore(t, ctx)
	target := openTestStore(t, ctx)
	syncDir := filepath.Join(t.TempDir(), ".kerebrom")

	if err := source.StartSession(ctx, sqlite.StartSessionInput{
		ID:        "session-1",
		Project:   "Proyecto Kerebrom",
		Directory: "/tmp/project",
	}); err != nil {
		t.Fatalf("start session: %v", err)
	}

	observation, err := source.SaveObservation(ctx, sqlite.ObservationInput{
		SessionID: "session-1",
		Type:      "decision",
		Title:     "Redact secrets",
		Content:   "Use <private>sk-secret</private> only outside memory.",
		Project:   "Proyecto Kerebrom",
	})
	if err != nil {
		t.Fatalf("save observation: %v", err)
	}
	if strings.Contains(observation.Content, "sk-secret") || !strings.Contains(observation.Content, "[REDACTED]") {
		t.Fatalf("private tag was not redacted: %+v", observation)
	}

	if _, err := source.SavePrompt(ctx, sqlite.PromptInput{
		SessionID: "session-1",
		Content:   "Close parity for Kerebrom v1.",
		Project:   "Proyecto Kerebrom",
	}); err != nil {
		t.Fatalf("save prompt: %v", err)
	}

	exported, err := Export(ctx, source, Options{Dir: syncDir, Project: "Proyecto Kerebrom"})
	if err != nil {
		t.Fatalf("sync export: %v", err)
	}
	if !exported.Created || exported.Chunk.ID == "" {
		t.Fatalf("expected created chunk, got %+v", exported)
	}

	status, err := Inspect(ctx, source, syncDir)
	if err != nil {
		t.Fatalf("source sync status: %v", err)
	}
	if status.ChunkCount != 1 || status.PendingImports != 0 {
		t.Fatalf("unexpected source status: %+v", status)
	}

	imported, err := Import(ctx, target, Options{Dir: syncDir})
	if err != nil {
		t.Fatalf("sync import: %v", err)
	}
	if imported.Imported != 1 || imported.Counts.Observations != 1 || imported.Counts.Prompts != 1 {
		t.Fatalf("unexpected import result: %+v", imported)
	}

	stats, err := target.Stats(ctx, "Proyecto Kerebrom")
	if err != nil {
		t.Fatalf("target stats: %v", err)
	}
	if stats.ObservationCount != 1 || stats.PromptCount != 1 {
		t.Fatalf("unexpected target stats: %+v", stats)
	}

	imported, err = Import(ctx, target, Options{Dir: syncDir})
	if err != nil {
		t.Fatalf("sync import again: %v", err)
	}
	if imported.Imported != 0 || imported.Skipped != 1 {
		t.Fatalf("expected idempotent import skip, got %+v", imported)
	}
}

func openTestStore(t *testing.T, ctx context.Context) *sqlite.Store {
	t.Helper()

	store, err := sqlite.Open(sqlite.Config{Path: filepath.Join(t.TempDir(), "kerebrom.db")})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	if err := store.Init(ctx); err != nil {
		t.Fatalf("init store: %v", err)
	}
	return store
}
