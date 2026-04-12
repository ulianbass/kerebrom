package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	projectdetect "github.com/ulianbass/kerebrom/internal/project"
	"github.com/ulianbass/kerebrom/internal/store/sqlite"
	syncstore "github.com/ulianbass/kerebrom/internal/sync"
)

func runHook(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: kerebrom hook <session-start|user-prompt-submit|subagent-stop|session-stop|post-compaction>")
		return 2
	}

	payload, err := readHookPayload(os.Stdin)
	if err != nil {
		fmt.Fprintf(stderr, "read hook input: %v\n", err)
		return 1
	}

	store, closeFn, err := openStore(context.Background())
	if err != nil {
		fmt.Fprintf(stderr, "open store: %v\n", err)
		return 1
	}
	defer closeFn()

	switch strings.ToLower(strings.TrimSpace(args[0])) {
	case "session-start":
		return runHookSessionStart(context.Background(), store, payload, stdout, stderr, false)
	case "post-compaction":
		return runHookSessionStart(context.Background(), store, payload, stdout, stderr, true)
	case "user-prompt-submit":
		return runHookUserPromptSubmit(context.Background(), store, payload, stdout, stderr)
	case "subagent-stop":
		return runHookSubagentStop(context.Background(), store, payload, stdout, stderr)
	case "session-stop":
		return runHookSessionStop(context.Background(), store, payload, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown hook %q\n", args[0])
		return 2
	}
}

func readHookPayload(r io.Reader) (map[string]any, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	raw = []byte(strings.TrimSpace(string(raw)))
	if len(raw) == 0 {
		return map[string]any{}, nil
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func runHookSessionStart(ctx context.Context, store *sqlite.Store, payload map[string]any, stdout io.Writer, stderr io.Writer, compact bool) int {
	sessionID := hookString(payload, "session_id", "sessionId")
	cwd := hookString(payload, "cwd", "workspace", "project_dir")
	project := hookProject(payload, cwd)
	if sessionID != "" {
		if err := store.StartSession(ctx, sqlite.StartSessionInput{
			ID:        sessionID,
			Project:   project,
			Directory: defaultString(cwd, "."),
			StartedAt: time.Now().UTC(),
		}); err != nil {
			fmt.Fprintf(stderr, "start hook session: %v\n", err)
			return 1
		}
	}
	if cwd != "" {
		if _, err := os.Stat(filepath.Join(cwd, ".kerebrom", "manifest.json")); err == nil {
			_, _ = syncstore.Import(ctx, store, syncstore.Options{Dir: filepath.Join(cwd, ".kerebrom"), Project: project})
		}
	}

	contextText, err := hookContextText(ctx, store, project)
	if err != nil {
		fmt.Fprintf(stderr, "load hook context: %v\n", err)
		return 1
	}
	if compact {
		fmt.Fprintf(stdout, "%s\n\n%s\n", hookProtocol("Kerebrom memory recovered after compaction. First persist the compacted summary with mem_session_summary, then call mem_context before continuing."), contextText)
	} else {
		fmt.Fprintf(stdout, "%s\n\n%s\n", hookProtocol("Kerebrom memory is active for this session."), contextText)
	}
	return 0
}

func runHookUserPromptSubmit(ctx context.Context, store *sqlite.Store, payload map[string]any, stdout io.Writer, stderr io.Writer) int {
	content := hookString(payload, "prompt", "user_prompt", "userPrompt", "message", "text")
	sessionID := hookString(payload, "session_id", "sessionId")
	cwd := hookString(payload, "cwd", "workspace", "project_dir")
	project := hookProject(payload, cwd)
	session := sqlite.Session{}
	firstPrompt := false
	if content != "" {
		if sessionID != "" {
			exists, err := store.SessionExists(ctx, sessionID)
			if err != nil {
				fmt.Fprintf(stderr, "check hook session: %v\n", err)
				return 1
			}
			if !exists {
				if err := store.StartSession(ctx, sqlite.StartSessionInput{
					ID:        sessionID,
					Project:   project,
					Directory: defaultString(cwd, "."),
					StartedAt: time.Now().UTC(),
				}); err != nil {
					fmt.Fprintf(stderr, "ensure hook session: %v\n", err)
					return 1
				}
				firstPrompt = true
			} else {
				count, err := store.CountSessionPrompts(ctx, sessionID)
				if err != nil {
					fmt.Fprintf(stderr, "count hook session prompts: %v\n", err)
					return 1
				}
				firstPrompt = count == 0
			}
			currentSession, err := store.GetSession(ctx, sessionID)
			if err != nil {
				fmt.Fprintf(stderr, "load hook session: %v\n", err)
				return 1
			}
			session = currentSession
		}
		if _, err := store.SavePrompt(ctx, sqlite.PromptInput{
			SessionID: sessionID,
			Content:   content,
			Project:   project,
			CreatedAt: time.Now().UTC(),
		}); err != nil {
			fmt.Fprintf(stderr, "save hook prompt: %v\n", err)
			return 1
		}
	}
	if firstPrompt {
		contextText, err := hookContextText(ctx, store, project)
		if err != nil {
			fmt.Fprintf(stderr, "load prompt hook context: %v\n", err)
			contextText = ""
		}
		return writeHookAdditionalContext(stdout, hookUserPromptFirstMessageContext(project, contextText))
	}
	if shouldInjectPromptSaveReminder(ctx, store, project, session) {
		return writeHookAdditionalContext(stdout, hookUserPromptSaveReminder())
	}
	return writeHookJSON(stdout, map[string]any{})
}

func runHookSubagentStop(ctx context.Context, store *sqlite.Store, payload map[string]any, stdout io.Writer, stderr io.Writer) int {
	content := hookString(payload, "stdout", "output", "transcript", "result")
	if strings.TrimSpace(content) == "" {
		return 0
	}
	sessionID := hookString(payload, "session_id", "sessionId")
	cwd := hookString(payload, "cwd", "workspace", "project_dir")
	project := hookProject(payload, cwd)
	for i, learning := range extractHookLearnings(content) {
		if _, err := store.SaveObservation(ctx, sqlite.ObservationInput{
			SessionID: sessionID,
			Type:      "learning",
			Title:     fmt.Sprintf("Passive learning %d", i+1),
			Content:   learning,
			Project:   project,
			Scope:     "project",
			ToolName:  "kerebrom_hook_subagent_stop",
			CreatedAt: time.Now().UTC(),
		}); err != nil {
			fmt.Fprintf(stderr, "save passive hook learning: %v\n", err)
			return 1
		}
	}
	return 0
}

func runHookSessionStop(ctx context.Context, store *sqlite.Store, payload map[string]any, stdout io.Writer, stderr io.Writer) int {
	sessionID := hookString(payload, "session_id", "sessionId")
	if sessionID == "" {
		return 0
	}
	summary := hookString(payload, "summary")
	if err := store.EndSession(ctx, sqlite.EndSessionInput{ID: sessionID, Summary: summary, EndedAt: time.Now().UTC()}); err != nil {
		fmt.Fprintf(stderr, "end hook session: %v\n", err)
		return 1
	}
	return 0
}

func hookProtocol(status string) string {
	return strings.TrimSpace(fmt.Sprintf(`
## Kerebrom Persistent Memory - ACTIVE PROTOCOL

%s

Tools available: mem_save, mem_search, mem_context, mem_session_summary, mem_get_observation, mem_save_prompt, mem_capture_passive.

Automatic lifecycle is enabled:
- SessionStart creates or refreshes the local session and injects memory context.
- UserPromptSubmit saves user prompts and reminds you when durable memory should be saved.
- SubagentStop passively captures learnings from subagent output.
- Stop closes the session.

Mandatory behavior:
- Call mem_context at the start of substantive work or when the user references prior work.
- Call mem_save immediately after decisions, preferences, constraints, bugfixes, architecture notes, config changes, and non-obvious discoveries.
- Never save raw transcript as an observation. Interpret it first.
- Use this mem_save framework:
  **What**: one sentence describing the durable fact or change.
  **Why**: why it matters or what motivated it.
  **Where**: project, files, tool, workflow, or context where it applies.
  **Learned**: implication, gotcha, constraint, or next useful connection. Omit only if none.
- Call mem_session_summary before ending substantial work.
- Never store secrets. Kerebrom redacts text inside <private>...</private>.
`, status))
}

func hookContextText(ctx context.Context, store *sqlite.Store, project string) (string, error) {
	stats, err := store.Stats(ctx, project)
	if err != nil {
		return "", err
	}
	recent, err := store.ListObservations(ctx, sqlite.ListObservationOptions{Project: project, Limit: 8})
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "## Kerebrom Context\n\nproject=%s sessions=%d active_sessions=%d observations=%d prompts=%d\n", project, stats.SessionCount, stats.ActiveSessionCount, stats.ObservationCount, stats.PromptCount)
	if len(recent) == 0 {
		b.WriteString("\nNo prior observations yet.\n")
		return b.String(), nil
	}
	b.WriteString("\nRecent observations:\n")
	for _, observation := range recent {
		fmt.Fprintf(&b, "- [%d] %s: %s\n", observation.ID, observation.Title, oneLinePreview(observation.Content, 180))
	}
	return b.String(), nil
}

func hookUserPromptFirstMessageContext(project string, contextText string) string {
	var b strings.Builder
	fmt.Fprintf(&b, `## Kerebrom Prompt Memory Protocol

Kerebrom has already stored this first user prompt as prompt history for project %q.

FIRST ACTION REQUIRED:
- Use the Kerebrom context below before answering.
- If the user references prior work, a project, a bug, a feature, a preference, or a previous decision, call mem_context or mem_search before making assumptions.
- After handling the prompt, call mem_save for durable decisions, preferences, constraints, bugfixes, architecture notes, config changes, and non-obvious discoveries.
- Do not save raw transcript as an observation. Distill with What / Why / Where / Learned.

`, project)
	if strings.TrimSpace(contextText) != "" {
		b.WriteString(contextText)
	}
	return strings.TrimSpace(b.String())
}

func hookUserPromptSaveReminder() string {
	return "Kerebrom memory reminder: if this session produced decisions, preferences, constraints, bugfixes, architecture notes, config changes, or non-obvious discoveries, call mem_save now. Do not save raw transcript; distill with What / Why / Where / Learned."
}

func shouldInjectPromptSaveReminder(ctx context.Context, store *sqlite.Store, project string, session sqlite.Session) bool {
	if strings.TrimSpace(session.ID) == "" {
		return false
	}
	startedAt, err := time.Parse(time.RFC3339, session.StartedAt)
	if err != nil || time.Since(startedAt) < 5*time.Minute {
		return false
	}
	recent, err := store.ListObservations(ctx, sqlite.ListObservationOptions{Project: project, Limit: 1})
	if err != nil || len(recent) == 0 {
		return false
	}
	lastSaveAt := recent[0].UpdatedAt
	if strings.TrimSpace(lastSaveAt) == "" {
		lastSaveAt = recent[0].CreatedAt
	}
	parsedLastSaveAt, err := time.Parse(time.RFC3339, lastSaveAt)
	if err != nil {
		return false
	}
	return time.Since(parsedLastSaveAt) > 15*time.Minute
}

func writeHookAdditionalContext(stdout io.Writer, additionalContext string) int {
	return writeHookJSON(stdout, map[string]any{
		"hookSpecificOutput": map[string]any{
			"hookEventName":     "UserPromptSubmit",
			"additionalContext": additionalContext,
		},
	})
}

func hookProject(payload map[string]any, cwd string) string {
	if project := hookString(payload, "project", "project_name"); project != "" {
		return project
	}
	if cwd != "" {
		return projectdetect.Detect(cwd)
	}
	return "default"
}

func hookString(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := payload[key]; ok {
			if text, ok := value.(string); ok {
				return strings.TrimSpace(text)
			}
		}
	}
	return ""
}

func extractHookLearnings(content string) []string {
	lines := strings.Split(content, "\n")
	var learnings []string
	capturing := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)
		if strings.HasPrefix(lower, "## key learnings") ||
			strings.HasPrefix(lower, "key learnings") ||
			strings.HasPrefix(lower, "## aprendizajes") ||
			strings.HasPrefix(lower, "aprendizajes") {
			capturing = true
			continue
		}
		if capturing && strings.HasPrefix(trimmed, "## ") {
			break
		}
		if !capturing {
			continue
		}
		trimmed = strings.TrimLeft(trimmed, "-*0123456789. )\t")
		trimmed = strings.TrimSpace(trimmed)
		if trimmed != "" {
			learnings = append(learnings, trimmed)
		}
	}
	return learnings
}

func writeHookJSON(stdout io.Writer, payload map[string]any) int {
	raw, err := json.Marshal(payload)
	if err != nil {
		return 1
	}
	fmt.Fprintln(stdout, string(raw))
	return 0
}

func defaultString(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
