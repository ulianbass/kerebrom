package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ulianbass/kerebrom/internal/store/sqlite"
)

func TestRunHookUserPromptSubmitEnsuresSessionAndAddsContext(t *testing.T) {
	store := newHookTestStore(t)
	ctx := context.Background()

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := runHookUserPromptSubmit(ctx, store, map[string]any{
		"session_id": "claude-session-1",
		"cwd":        filepath.Join("Users", "example", "Proyecto Kerebrom"),
		"prompt":     "Analiza por qué Claude no siempre activa la memoria.",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("hook failed: code=%d stderr=%q", code, stderr.String())
	}

	session, err := store.GetSession(ctx, "claude-session-1")
	if err != nil {
		t.Fatalf("expected hook to create missing session: %v", err)
	}
	if session.Project != "proyecto-kerebrom" {
		t.Fatalf("unexpected project: %#v", session)
	}

	prompts, err := store.ListPrompts(ctx, "proyecto-kerebrom", 5)
	if err != nil {
		t.Fatalf("list prompts: %v", err)
	}
	if len(prompts) != 1 || !strings.Contains(prompts[0].Content, "Claude no siempre") {
		t.Fatalf("prompt was not saved correctly: %#v", prompts)
	}

	var output map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &output); err != nil {
		t.Fatalf("hook output is not JSON: %v output=%q", err, stdout.String())
	}
	specific, ok := output["hookSpecificOutput"].(map[string]any)
	if !ok {
		t.Fatalf("missing hookSpecificOutput: %#v", output)
	}
	if specific["hookEventName"] != "UserPromptSubmit" {
		t.Fatalf("unexpected hook event name: %#v", specific)
	}
	if !strings.Contains(specific["additionalContext"].(string), "Kerebrom reminder") {
		t.Fatalf("missing additionalContext reminder: %#v", specific)
	}
}

func TestRunHookUserPromptSubmitSupportsCamelCasePrompt(t *testing.T) {
	store := newHookTestStore(t)
	ctx := context.Background()

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := runHookUserPromptSubmit(ctx, store, map[string]any{
		"sessionId":  "claude-session-2",
		"project":    "proyecto-kerebrom",
		"userPrompt": "Guarda este prompt aunque llegue como camelCase.",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("hook failed: code=%d stderr=%q", code, stderr.String())
	}

	prompts, err := store.ListPrompts(ctx, "proyecto-kerebrom", 5)
	if err != nil {
		t.Fatalf("list prompts: %v", err)
	}
	if len(prompts) != 1 || !strings.Contains(prompts[0].Content, "camelCase") {
		t.Fatalf("camelCase prompt was not saved: %#v", prompts)
	}
}

func newHookTestStore(t *testing.T) *sqlite.Store {
	t.Helper()

	store, err := sqlite.Open(sqlite.Config{Path: filepath.Join(t.TempDir(), "kerebrom.db")})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatalf("close store: %v", err)
		}
	})
	if err := store.Init(context.Background()); err != nil {
		t.Fatalf("init store: %v", err)
	}
	return store
}
