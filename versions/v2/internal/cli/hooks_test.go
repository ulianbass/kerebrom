package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	additionalContext := specific["additionalContext"].(string)
	if !strings.Contains(additionalContext, "FIRST ACTION REQUIRED") {
		t.Fatalf("missing additionalContext reminder: %#v", specific)
	}
	if !strings.Contains(additionalContext, "Kerebrom Context") {
		t.Fatalf("missing injected Kerebrom context: %#v", specific)
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

func TestRunHookUserPromptSubmitSkipsCasualPromptNoise(t *testing.T) {
	store := newHookTestStore(t)
	ctx := context.Background()

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := runHookUserPromptSubmit(ctx, store, map[string]any{
		"session_id": "claude-session-noise",
		"project":    "proyecto-kerebrom",
		"prompt":     "Gracias.",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("hook failed: code=%d stderr=%q", code, stderr.String())
	}

	var output map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &output); err != nil {
		t.Fatalf("hook output is not JSON: %v output=%q", err, stdout.String())
	}
	if len(output) != 0 {
		t.Fatalf("expected no context injection for casual prompt, got %#v", output)
	}

	prompts, err := store.ListPrompts(ctx, "proyecto-kerebrom", 5)
	if err != nil {
		t.Fatalf("list prompts: %v", err)
	}
	if len(prompts) != 0 {
		t.Fatalf("casual prompt should not be stored, got %#v", prompts)
	}
	exists, err := store.SessionExists(ctx, "claude-session-noise")
	if err != nil {
		t.Fatalf("check session exists: %v", err)
	}
	if exists {
		t.Fatalf("casual prompt should not create a session")
	}
}

func TestRunHookUserPromptSubmitIsSilentAfterFirstPrompt(t *testing.T) {
	store := newHookTestStore(t)
	ctx := context.Background()

	payload := map[string]any{
		"session_id": "claude-session-silent",
		"project":    "proyecto-kerebrom",
		"prompt":     "Primer prompt de la sesión.",
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runHookUserPromptSubmit(ctx, store, payload, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("first hook failed: code=%d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "FIRST ACTION REQUIRED") {
		t.Fatalf("expected first prompt bootstrap context, got %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	payload["prompt"] = "Segundo prompt de la misma sesión."
	code = runHookUserPromptSubmit(ctx, store, payload, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("second hook failed: code=%d stderr=%q", code, stderr.String())
	}

	var output map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &output); err != nil {
		t.Fatalf("hook output is not JSON: %v output=%q", err, stdout.String())
	}
	if len(output) != 0 {
		t.Fatalf("expected silent subsequent prompt hook output, got %#v", output)
	}

	promptCount, err := store.CountSessionPrompts(ctx, "claude-session-silent")
	if err != nil {
		t.Fatalf("count session prompts: %v", err)
	}
	if promptCount != 2 {
		t.Fatalf("expected 2 prompts in one session, got %d", promptCount)
	}
	stats, err := store.Stats(ctx, "proyecto-kerebrom")
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if stats.SessionCount != 1 || stats.ActiveSessionCount != 1 {
		t.Fatalf("expected one active session, got %+v", stats)
	}
}

func TestRunHookUserPromptSubmitNudgesAfterQuietSaveWindow(t *testing.T) {
	store := newHookTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	if err := store.StartSession(ctx, sqlite.StartSessionInput{
		ID:        "claude-session-reminder",
		Project:   "proyecto-kerebrom",
		Directory: ".",
		StartedAt: now.Add(-6 * time.Minute),
	}); err != nil {
		t.Fatalf("start session: %v", err)
	}
	if _, err := store.SavePrompt(ctx, sqlite.PromptInput{
		SessionID: "claude-session-reminder",
		Content:   "Prompt anterior.",
		Project:   "proyecto-kerebrom",
		CreatedAt: now.Add(-5 * time.Minute),
	}); err != nil {
		t.Fatalf("save previous prompt: %v", err)
	}
	if _, err := store.SaveObservation(ctx, sqlite.ObservationInput{
		SessionID: "claude-session-reminder",
		Type:      "decision",
		Title:     "Old decision",
		Content:   "A durable decision was saved earlier in the session.",
		Project:   "proyecto-kerebrom",
		Scope:     "project",
		CreatedAt: now.Add(-16 * time.Minute),
	}); err != nil {
		t.Fatalf("save old observation: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runHookUserPromptSubmit(ctx, store, map[string]any{
		"session_id": "claude-session-reminder",
		"project":    "proyecto-kerebrom",
		"prompt":     "Nuevo prompt luego de un rato.",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("hook failed: code=%d stderr=%q", code, stderr.String())
	}

	var output map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &output); err != nil {
		t.Fatalf("hook output is not JSON: %v output=%q", err, stdout.String())
	}
	specific, ok := output["hookSpecificOutput"].(map[string]any)
	if !ok {
		t.Fatalf("missing reminder hookSpecificOutput: %#v", output)
	}
	additionalContext := specific["additionalContext"].(string)
	if !strings.Contains(additionalContext, "memory reminder") {
		t.Fatalf("missing save reminder: %#v", specific)
	}
}

func TestRunHookUserPromptSubmitNudgesLongSessionWithNoSavedObservation(t *testing.T) {
	store := newHookTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	if err := store.StartSession(ctx, sqlite.StartSessionInput{
		ID:        "claude-session-unsaved",
		Project:   "proyecto-kerebrom",
		Directory: ".",
		StartedAt: now.Add(-20 * time.Minute),
	}); err != nil {
		t.Fatalf("start session: %v", err)
	}
	for _, content := range []string{"Prompt uno.", "Prompt dos."} {
		if _, err := store.SavePrompt(ctx, sqlite.PromptInput{
			SessionID: "claude-session-unsaved",
			Content:   content,
			Project:   "proyecto-kerebrom",
			CreatedAt: now.Add(-18 * time.Minute),
		}); err != nil {
			t.Fatalf("save previous prompt: %v", err)
		}
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runHookUserPromptSubmit(ctx, store, map[string]any{
		"session_id": "claude-session-unsaved",
		"project":    "proyecto-kerebrom",
		"prompt":     "Tercer prompt con decisiones posibles.",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("hook failed: code=%d stderr=%q", code, stderr.String())
	}

	var output map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &output); err != nil {
		t.Fatalf("hook output is not JSON: %v output=%q", err, stdout.String())
	}
	specific, ok := output["hookSpecificOutput"].(map[string]any)
	if !ok {
		t.Fatalf("missing reminder hookSpecificOutput: %#v", output)
	}
	additionalContext := specific["additionalContext"].(string)
	if !strings.Contains(additionalContext, "memory reminder") {
		t.Fatalf("missing save reminder for unsaved long session: %#v", specific)
	}
}

func TestHookContextTextUsesCrossProjectLookupForWeakProject(t *testing.T) {
	store := newHookTestStore(t)
	ctx := context.Background()

	if _, err := store.SaveObservation(ctx, sqlite.ObservationInput{
		Type:    "decision",
		Title:   "Falage cross-project context",
		Content: "**What**: Falage memory should remain visible when hooks launch from a weak project.",
		Project: "proyecto-falage",
		Scope:   "project",
	}); err != nil {
		t.Fatalf("save observation: %v", err)
	}

	contextText, err := hookContextText(ctx, store, "/")
	if err != nil {
		t.Fatalf("hook context: %v", err)
	}
	if !strings.Contains(contextText, "project_filter=") {
		t.Fatalf("context should expose lookup filter: %q", contextText)
	}
	if !strings.Contains(contextText, "Falage cross-project context") {
		t.Fatalf("weak hook project should show cross-project memory: %q", contextText)
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
