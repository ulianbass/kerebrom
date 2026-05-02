package sync

import (
	"context"
	"os"
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
	if err := os.MkdirAll(filepath.Join(syncDir, ChunksDir), 0o755); err != nil {
		t.Fatalf("precreate permissive sync dirs: %v", err)
	}

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
	assertPathMode(t, syncDir, 0o700)
	assertPathMode(t, filepath.Join(syncDir, ChunksDir), 0o700)
	assertPathMode(t, filepath.Join(syncDir, ManifestFile), 0o600)
	assertPathMode(t, filepath.Join(syncDir, exported.Chunk.File), 0o600)

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

func TestSaveManifestAndWriteGzipSecureExistingFiles(t *testing.T) {
	t.Parallel()

	syncDir := filepath.Join(t.TempDir(), ".kerebrom")
	if err := os.MkdirAll(syncDir, 0o755); err != nil {
		t.Fatalf("create sync dir: %v", err)
	}
	manifestPath := filepath.Join(syncDir, ManifestFile)
	if err := os.WriteFile(manifestPath, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("precreate manifest: %v", err)
	}
	if err := SaveManifest(syncDir, Manifest{Chunks: []Chunk{{ID: "chunk-1", File: "chunks/chunk-1.jsonl.gz"}}}); err != nil {
		t.Fatalf("save manifest: %v", err)
	}
	assertPathMode(t, syncDir, 0o700)
	assertPathMode(t, manifestPath, 0o600)

	chunkPath := filepath.Join(syncDir, ChunksDir, "chunk-1.jsonl.gz")
	if err := os.MkdirAll(filepath.Dir(chunkPath), 0o755); err != nil {
		t.Fatalf("create chunk dir: %v", err)
	}
	if err := os.WriteFile(chunkPath, []byte("old"), 0o644); err != nil {
		t.Fatalf("precreate chunk: %v", err)
	}
	if err := writeGzip(chunkPath, []byte("new")); err != nil {
		t.Fatalf("write gzip: %v", err)
	}
	assertPathMode(t, chunkPath, 0o600)
}

func TestImportWithProjectFiltersBroadChunks(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	source := openTestStore(t, ctx)
	target := openTestStore(t, ctx)
	syncDir := filepath.Join(t.TempDir(), ".kerebrom")

	if _, err := source.SaveObservation(ctx, sqlite.ObservationInput{
		Type:    "decision",
		Title:   "Kerebrom sync fact",
		Content: "Only Kerebrom memory should import through the project filter.",
		Project: "Proyecto Kerebrom",
	}); err != nil {
		t.Fatalf("save Kerebrom observation: %v", err)
	}
	if _, err := source.SaveObservation(ctx, sqlite.ObservationInput{
		Type:    "decision",
		Title:   "Falage sync fact",
		Content: "Falage memory should stay out of a Kerebrom project import.",
		Project: "Proyecto Falage",
	}); err != nil {
		t.Fatalf("save Falage observation: %v", err)
	}
	if _, err := source.SavePrompt(ctx, sqlite.PromptInput{
		Content: "Kerebrom project prompt.",
		Project: "Proyecto Kerebrom",
	}); err != nil {
		t.Fatalf("save Kerebrom prompt: %v", err)
	}
	if _, err := source.SavePrompt(ctx, sqlite.PromptInput{
		Content: "Falage project prompt.",
		Project: "Proyecto Falage",
	}); err != nil {
		t.Fatalf("save Falage prompt: %v", err)
	}

	exported, err := Export(ctx, source, Options{Dir: syncDir, All: true})
	if err != nil {
		t.Fatalf("sync export all: %v", err)
	}
	if !exported.Chunk.All {
		t.Fatalf("expected broad all-project chunk, got %+v", exported.Chunk)
	}

	imported, err := Import(ctx, target, Options{Dir: syncDir, Project: "Proyecto Kerebrom"})
	if err != nil {
		t.Fatalf("sync import project: %v", err)
	}
	if imported.Imported != 1 || imported.Counts.Observations != 1 || imported.Counts.Prompts != 1 {
		t.Fatalf("unexpected filtered import result: %+v", imported)
	}

	kerebromStats, err := target.Stats(ctx, "Proyecto Kerebrom")
	if err != nil {
		t.Fatalf("Kerebrom stats: %v", err)
	}
	if kerebromStats.ObservationCount != 1 || kerebromStats.PromptCount != 1 {
		t.Fatalf("expected only Kerebrom data, got %+v", kerebromStats)
	}
	falageStats, err := target.Stats(ctx, "Proyecto Falage")
	if err != nil {
		t.Fatalf("Falage stats: %v", err)
	}
	if falageStats.ObservationCount != 0 || falageStats.PromptCount != 0 {
		t.Fatalf("project-filtered import leaked Falage data: %+v", falageStats)
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

func assertPathMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode=%#o, want %#o", path, got, want)
	}
}
