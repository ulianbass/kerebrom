package mcptransport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/ulianbass/kerebrom/internal/store/sqlite"
)

// ----- Protocol surface -----

// TestServerDoesNotExposePromptsOrResources locks in the v2 design
// decision documented at server.go: registering MCP prompts or
// resources for the memory workflow signals to the model that the
// system is opt-in (a thing the user "loads" or "reads"). The protocol
// must travel exclusively via WithInstructions and the per-tool
// descriptions, both of which the model treats as part of its
// operating environment rather than as user-invocable surfaces.
func TestServerDoesNotExposePromptsOrResources(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t, ctx)
	mcpClient := newTestClient(t, ctx, store)

	prompts, err := mcpClient.ListPrompts(ctx, mcp.ListPromptsRequest{})
	if err == nil && len(prompts.Prompts) > 0 {
		t.Fatalf("Kerebrom v2 must not expose MCP prompts; got %#v", prompts.Prompts)
	}

	resources, err := mcpClient.ListResources(ctx, mcp.ListResourcesRequest{})
	if err == nil && len(resources.Resources) > 0 {
		t.Fatalf("Kerebrom v2 must not expose MCP resources; got %#v", resources.Resources)
	}
}

func TestServerInitializeReturnsMemoryInstructions(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t, ctx)
	server := NewServer(store).MCPServer()

	mcpClient, err := client.NewInProcessClient(server)
	if err != nil {
		t.Fatalf("new in-process client: %v", err)
	}
	t.Cleanup(func() { _ = mcpClient.Close() })

	if err := mcpClient.Start(ctx); err != nil {
		t.Fatalf("start in-process client: %v", err)
	}

	var initReq mcp.InitializeRequest
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{Name: "kerebrom-test", Version: "1.0.0"}
	initResult, err := mcpClient.Initialize(ctx, initReq)
	if err != nil {
		t.Fatalf("initialize client: %v", err)
	}
	assertProtocolText(t, initResult.Instructions)
	if !strings.Contains(initResult.Instructions, "local source of truth") {
		t.Fatalf("initialize instructions missing authority rule: %s", initResult.Instructions)
	}
	if !strings.Contains(initResult.Instructions, "Before answering ANY user message") {
		t.Fatalf("initialize instructions missing every-turn context rule: %s", initResult.Instructions)
	}
}

// ----- Tool registration -----

func TestServerRegistersSevenSemanticTools(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t, ctx)
	mcpClient := newTestClient(t, ctx, store)

	tools, err := mcpClient.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}

	expected := map[string]struct {
		readOnly    bool
		destructive bool
	}{
		"context":  {readOnly: false, destructive: false},
		"recall":   {readOnly: true, destructive: false},
		"remember": {readOnly: false, destructive: false},
		"summary":  {readOnly: false, destructive: false},
		"forget":   {readOnly: false, destructive: true},
		"timeline": {readOnly: true, destructive: false},
		"projects": {readOnly: false, destructive: false},
	}
	if len(tools.Tools) != len(expected) {
		t.Fatalf("expected %d tools, got %d: %v", len(expected), len(tools.Tools), toolNames(tools.Tools))
	}
	for name, ann := range expected {
		assertToolAnnotation(t, tools.Tools, name, ann.readOnly, ann.destructive)
	}

	assertToolDescriptionContains(t, tools.Tools, "context", "ALWAYS call BEFORE")
	assertToolDescriptionContains(t, tools.Tools, "context", "EVERY user message")
	assertToolDescriptionContains(t, tools.Tools, "recall", "ALWAYS call BEFORE")
	assertToolDescriptionContains(t, tools.Tools, "remember", "PROACTIVELY")
	assertToolDescriptionContains(t, tools.Tools, "remember", "What / Why / Where / Learned")
	assertToolDescriptionContains(t, tools.Tools, "summary", "goals, decisions, changes, risks")
	assertToolDescriptionContains(t, tools.Tools, "timeline", "chronological")
	assertToolDescriptionContains(t, tools.Tools, "forget", "Soft delete")
	assertToolDescriptionContains(t, tools.Tools, "projects", "Consolidate")
}

func TestAgentProfileExposesSixTools(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t, ctx)
	server := NewServerWithTools(store, ResolveTools("agent")).MCPServer()
	mcpClient := newTestClientForServer(t, ctx, server)

	tools, err := mcpClient.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	wantNames := []string{"context", "recall", "remember", "summary", "forget", "timeline"}
	if len(tools.Tools) != len(wantNames) {
		t.Fatalf("expected agent profile to expose %d tools, got %d: %v", len(wantNames), len(tools.Tools), toolNames(tools.Tools))
	}
	for _, name := range wantNames {
		if !toolExists(tools.Tools, name) {
			t.Fatalf("agent profile missing tool %s; got %v", name, toolNames(tools.Tools))
		}
	}
	if toolExists(tools.Tools, "projects") {
		t.Fatalf("agent profile should not expose admin tool 'projects'")
	}
}

func TestAgentProfileInstructionsMatchVisibleTools(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t, ctx)
	server := NewServerWithTools(store, ResolveTools("agent")).MCPServer()
	instructions := initializeInstructions(t, ctx, server)

	if strings.Contains(instructions, "projects — administrative consolidation") {
		t.Fatalf("agent instructions advertise hidden projects tool as core: %s", instructions)
	}
	if !strings.Contains(instructions, "projects is an admin-only tool") {
		t.Fatalf("agent instructions should explain projects is admin-only: %s", instructions)
	}
	if !strings.Contains(instructions, "tool_search") {
		t.Fatalf("agent instructions must preserve deferred tool loading guidance: %s", instructions)
	}
}

// ----- The cycle: context → remember → recall → summary -----

func TestCycleContextRememberRecallSummary(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t, ctx)
	mcpClient := newTestClient(t, ctx, store)

	// 1. context: open or resume the session.
	contextResult := callTool(t, ctx, mcpClient, "context", map[string]any{
		"prompt":     "Quiero discutir el plan de Falage para el siguiente trimestre",
		"project":    "falage",
		"session_id": "test-cycle-1",
	})
	contextPayload := mustStructuredMap(t, contextResult)
	if contextPayload["session_id"] != "test-cycle-1" {
		t.Fatalf("expected session_id echo, got %v", contextPayload["session_id"])
	}
	if _, ok := contextPayload["recent_observations"]; !ok {
		t.Fatalf("context payload missing recent_observations: %#v", contextPayload)
	}
	governor, ok := contextPayload["context_governor"].(map[string]any)
	if !ok || governor["primary_clock"] != "valid_at" {
		t.Fatalf("context payload missing context_governor valid_at policy: %#v", contextPayload)
	}

	// 2. remember: persist a durable fact.
	rememberResult := callTool(t, ctx, mcpClient, "remember", map[string]any{
		"title":      "Decision Falage Q2",
		"content":    "**What**: Reducir intraday y enfocar daily. **Why**: PSR mejor en daily. **Where**: backtest/strategies/. **Learned**: cost scenario importa más que la lógica.",
		"type":       "decision",
		"project":    "falage",
		"topic_key":  "falage/q2-decision",
		"session_id": "test-cycle-1",
	})
	remembered := mustNestedMap(t, mustStructuredMap(t, rememberResult), "observation")
	if int(remembered["id"].(float64)) <= 0 {
		t.Fatalf("expected positive observation id, got %v", remembered["id"])
	}

	// 3. recall: find the saved observation by query.
	recallResult := callTool(t, ctx, mcpClient, "recall", map[string]any{
		"query":   "decision Falage Q2",
		"project": "falage",
	})
	recallPayload := mustStructuredMap(t, recallResult)
	matches, ok := recallPayload["matches"].([]any)
	if !ok || len(matches) == 0 {
		t.Fatalf("expected at least one recall match, got %#v", recallPayload["matches"])
	}

	// 4. summary: close the session with a wrap-up.
	summaryResult := callTool(t, ctx, mcpClient, "summary", map[string]any{
		"session_id": "test-cycle-1",
		"content":    "Plan Q2 cerrado: foco en daily, costos validados.",
		"project":    "falage",
	})
	summaryPayload := mustStructuredMap(t, summaryResult)
	if _, ok := summaryPayload["session"]; !ok {
		t.Fatalf("summary payload missing session block: %#v", summaryPayload)
	}
}

func TestSummaryWithoutExplicitSessionIDClosesRecentNativeSession(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t, ctx)
	mcpClient := newTestClient(t, ctx, store)

	if err := store.StartSession(ctx, sqlite.StartSessionInput{
		ID:        "claude-native-session",
		Project:   "falage",
		Directory: "/tmp/falage",
		StartedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("start native session: %v", err)
	}
	if _, err := store.SavePrompt(ctx, sqlite.PromptInput{
		SessionID: "claude-native-session",
		Project:   "falage",
		Content:   "Cerramos esta sesión.",
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("save native prompt: %v", err)
	}

	summaryResult := callTool(t, ctx, mcpClient, "summary", map[string]any{
		"project": "falage",
		"content": "Cierre validado desde el hook nativo.",
	})
	summaryPayload := mustStructuredMap(t, summaryResult)
	session := mustNestedMap(t, summaryPayload, "session")
	if session["id"] != "claude-native-session" {
		t.Fatalf("expected summary to close native hook session, got %#v", session)
	}

	closed, err := store.GetSession(ctx, "claude-native-session")
	if err != nil {
		t.Fatalf("get native session: %v", err)
	}
	if closed.Status != "completed" {
		t.Fatalf("expected native session completed, got %+v", closed)
	}
	if _, err := store.GetSession(ctx, "mcp:falage"); err == nil {
		t.Fatalf("summary should not create a parallel mcp:falage session when a fresh native session exists")
	}
}

func TestContextWithoutExplicitSessionIDUsesRecentNativeSession(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t, ctx)
	mcpClient := newTestClient(t, ctx, store)

	if err := store.StartSession(ctx, sqlite.StartSessionInput{
		ID:        "native-context-session",
		Project:   "falage",
		Directory: "/tmp/falage",
		StartedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("start native session: %v", err)
	}

	contextResult := callTool(t, ctx, mcpClient, "context", map[string]any{
		"project": "falage",
		"prompt":  "Necesito retomar el cierre de sesión.",
	})
	contextPayload := mustStructuredMap(t, contextResult)
	if contextPayload["session_id"] != "native-context-session" {
		t.Fatalf("expected context to use recent native session, got %#v", contextPayload["session_id"])
	}

	promptCount, err := store.CountSessionPrompts(ctx, "native-context-session")
	if err != nil {
		t.Fatalf("count native prompts: %v", err)
	}
	if promptCount != 1 {
		t.Fatalf("expected context prompt on native session, got %d", promptCount)
	}
}

func TestContextWithoutExplicitSessionIDIgnoresOldNativeSession(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t, ctx)
	mcpClient := newTestClient(t, ctx, store)

	if err := store.StartSession(ctx, sqlite.StartSessionInput{
		ID:        "old-native-session",
		Project:   "falage",
		Directory: "/tmp/falage",
		StartedAt: time.Now().UTC().Add(-recentNativeSessionTTL - time.Minute),
	}); err != nil {
		t.Fatalf("start old native session: %v", err)
	}

	contextResult := callTool(t, ctx, mcpClient, "context", map[string]any{
		"project": "falage",
		"prompt":  "Abramos una conversación limpia.",
	})
	contextPayload := mustStructuredMap(t, contextResult)
	if contextPayload["session_id"] != "mcp:falage" {
		t.Fatalf("expected context to fall back to mcp session, got %#v", contextPayload["session_id"])
	}

	oldPromptCount, err := store.CountSessionPrompts(ctx, "old-native-session")
	if err != nil {
		t.Fatalf("count old native prompts: %v", err)
	}
	if oldPromptCount != 0 {
		t.Fatalf("old native session should not receive new prompts, got %d", oldPromptCount)
	}
}

func TestSummaryWithoutContentStillClosesSession(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t, ctx)
	mcpClient := newTestClient(t, ctx, store)

	if err := store.StartSession(ctx, sqlite.StartSessionInput{
		ID:      "session-to-close",
		Project: "kerebrom",
	}); err != nil {
		t.Fatalf("start session: %v", err)
	}

	callTool(t, ctx, mcpClient, "summary", map[string]any{
		"session_id": "session-to-close",
		"project":    "kerebrom",
	})

	session, err := store.GetSession(ctx, "session-to-close")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if session.Status != "completed" {
		t.Fatalf("expected completed session, got %+v", session)
	}
	if !strings.Contains(session.Summary, "without an explicit summary") {
		t.Fatalf("expected fallback summary, got %+v", session)
	}
}

func TestContextFromWeakDefaultProjectSearchesAcrossProjects(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t, ctx)
	falageObservation, err := store.SaveObservation(ctx, sqlite.ObservationInput{
		Type:      "decision",
		Title:     "Usuario anticipa 0 PASS en combinator post-2020",
		Content:   "**What**: NQ PreLondon combinator post-2020 termina en 0 PASS y se decide evaluar salto directo.",
		Project:   "Proyecto Falage",
		Scope:     "project",
		TopicKey:  "phase4-close-go-live-nq-prelondon",
		CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("save falage observation: %v", err)
	}

	server := NewServerWithConfig(store, Config{DefaultProject: "/"}, ResolveTools("agent")).MCPServer()
	mcpClient := newTestClientForServer(t, ctx, server)

	contextResult := callTool(t, ctx, mcpClient, "context", map[string]any{
		"prompt": "busca la última información guardada hoy sobre NQ PreLondon 0 PASS post-2020",
		"query":  "última información guardada hoy NQ PreLondon 0 PASS post-2020 combinator",
		"limit":  10,
	})
	payload := mustStructuredMap(t, contextResult)
	if payload["project_filter"] != "" {
		t.Fatalf("weak default project should use cross-project lookup, got filter %v", payload["project_filter"])
	}
	if payload["project_filter_relaxed"] != true {
		t.Fatalf("weak default project should mark project_filter_relaxed=true: %#v", payload)
	}
	assertObservationInPayload(t, payload, "recent_observations", falageObservation.ID)
	assertObservationInPayload(t, payload, "matches", falageObservation.ID)
}

func TestRecallFromStrongProjectAlsoSurfacesBetterCrossProjectMatches(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t, ctx)
	if _, err := store.SaveObservation(ctx, sqlite.ObservationInput{
		Type:    "learning",
		Title:   "Kerebrom generic memory",
		Content: "**What**: Kerebrom guarda información de proyecto y memoria global hoy.",
		Project: "Proyecto Kerebrom",
		Scope:   "project",
	}); err != nil {
		t.Fatalf("save generic project observation: %v", err)
	}
	falageObservation, err := store.SaveObservation(ctx, sqlite.ObservationInput{
		Type:     "decision",
		Title:    "Usuario anticipa 0 PASS en combinator post-2020",
		Content:  "**What**: Falage NQ PreLondon combinator post-2020 confirma 0 PASS y cambia el siguiente paso.",
		Project:  "Proyecto Falage",
		Scope:    "project",
		TopicKey: "phase4-close-go-live-nq-prelondon",
	})
	if err != nil {
		t.Fatalf("save falage observation: %v", err)
	}

	mcpClient := newTestClient(t, ctx, store)
	recallResult := callTool(t, ctx, mcpClient, "recall", map[string]any{
		"query":   "última información guardada hoy NQ PreLondon 0 PASS post-2020 combinator proyecto Falage",
		"project": "Proyecto Kerebrom",
		"limit":   10,
	})
	payload := mustStructuredMap(t, recallResult)
	if payload["project_filter_relaxed"] != true {
		t.Fatalf("expected strong-project recall to report relaxed cross-project matches: %#v", payload)
	}
	assertObservationInPayload(t, payload, "matches", falageObservation.ID)
}

func TestRecallFromStrongProjectPrefersExactProjectBeforeRelaxedMatches(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t, ctx)
	kerebromObservation, err := store.SaveObservation(ctx, sqlite.ObservationInput{
		Type:      "decision",
		Title:     "Kerebrom audit memory routing",
		Content:   "**What**: Kerebrom audit memory routing must keep exact project matches first.",
		Project:   "Proyecto Kerebrom",
		Scope:     "project",
		CreatedAt: time.Now().UTC().Add(-time.Hour),
	})
	if err != nil {
		t.Fatalf("save kerebrom observation: %v", err)
	}
	falageObservation, err := store.SaveObservation(ctx, sqlite.ObservationInput{
		Type:      "decision",
		Title:     "Falage audit memory routing",
		Content:   "**What**: Falage audit memory routing is a related but different project match.",
		Project:   "Proyecto Falage",
		Scope:     "project",
		CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("save falage observation: %v", err)
	}

	mcpClient := newTestClient(t, ctx, store)
	recallResult := callTool(t, ctx, mcpClient, "recall", map[string]any{
		"query":   "audit memory routing",
		"project": "Proyecto Kerebrom",
		"limit":   10,
	})
	payload := mustStructuredMap(t, recallResult)
	if payload["project_filter_relaxed"] != true {
		t.Fatalf("expected relaxed cross-project fill to be reported: %#v", payload)
	}
	matches, ok := payload["matches"].([]any)
	if !ok || len(matches) < 2 {
		t.Fatalf("expected exact and relaxed matches, got %#v", payload["matches"])
	}
	first, ok := matches[0].(map[string]any)
	if !ok {
		t.Fatalf("expected first match map, got %#v", matches[0])
	}
	if int64(first["id"].(float64)) != kerebromObservation.ID {
		t.Fatalf("expected exact project observation %d first before relaxed %d, got %#v", kerebromObservation.ID, falageObservation.ID, matches)
	}
	assertObservationInPayload(t, payload, "matches", falageObservation.ID)
}

// ----- Forget -----

func TestForgetSoftDeleteHidesObservation(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t, ctx)
	mcpClient := newTestClient(t, ctx, store)

	_ = callTool(t, ctx, mcpClient, "context", map[string]any{
		"prompt":     "abrir sesión",
		"project":    "scratch",
		"session_id": "test-forget",
	})
	saved := mustNestedMap(t, mustStructuredMap(t, callTool(t, ctx, mcpClient, "remember", map[string]any{
		"title":      "Temporal observation",
		"content":    "**What**: dato a invalidar.",
		"project":    "scratch",
		"session_id": "test-forget",
	})), "observation")
	id := int(saved["id"].(float64))

	forgetResult := callTool(t, ctx, mcpClient, "forget", map[string]any{
		"id": id,
	})
	forgetPayload := mustStructuredMap(t, forgetResult)
	if forgetPayload["deleted"] != true {
		t.Fatalf("expected deleted=true, got %v", forgetPayload["deleted"])
	}

	// recall should no longer surface the soft-deleted observation.
	recallResult := callTool(t, ctx, mcpClient, "recall", map[string]any{
		"query":   "Temporal observation",
		"project": "scratch",
	})
	matches, _ := mustStructuredMap(t, recallResult)["matches"].([]any)
	for _, m := range matches {
		obs, _ := m.(map[string]any)
		if int(obs["id"].(float64)) == id {
			t.Fatalf("forgotten observation %d still appears in recall", id)
		}
	}
}

func TestForgetHardDeleteRequiresAdminProfile(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t, ctx)
	mcpClient := newTestClientForServer(t, ctx, NewServerWithTools(store, ResolveTools("agent")).MCPServer())

	saved := mustNestedMap(t, mustStructuredMap(t, callTool(t, ctx, mcpClient, "remember", map[string]any{
		"title":      "Protected observation",
		"content":    "**What**: default agent profile must not hard-delete this memory.",
		"project":    "scratch",
		"session_id": "test-hard-forget",
	})), "observation")
	id := int64(saved["id"].(float64))

	result := callTool(t, ctx, mcpClient, "forget", map[string]any{
		"id":   id,
		"hard": true,
	})
	if !result.IsError {
		t.Fatalf("expected hard delete to be rejected in agent profile, got %#v", result)
	}

	observation, err := store.GetObservation(ctx, id)
	if err != nil {
		t.Fatalf("hard-delete rejection should preserve observation: %v", err)
	}
	if observation.DeletedAt != "" {
		t.Fatalf("hard-delete rejection should not soft-delete observation: %+v", observation)
	}
}

// ----- Timeline -----

func TestTimelineReturnsRecentObservations(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t, ctx)
	mcpClient := newTestClient(t, ctx, store)

	_ = callTool(t, ctx, mcpClient, "context", map[string]any{
		"prompt":     "abrir",
		"project":    "tl",
		"session_id": "test-timeline",
	})
	titles := []string{"obs alpha", "obs beta", "obs gamma"}
	for _, title := range titles {
		_ = callTool(t, ctx, mcpClient, "remember", map[string]any{
			"title":      title,
			"content":    "**What**: " + title,
			"project":    "tl",
			"session_id": "test-timeline",
		})
	}

	timelineResult := callTool(t, ctx, mcpClient, "timeline", map[string]any{
		"project": "tl",
		"limit":   10,
	})
	payload := mustStructuredMap(t, timelineResult)
	obs, ok := payload["observations"].([]any)
	if !ok || len(obs) < 3 {
		t.Fatalf("expected at least 3 observations in timeline, got %#v", payload)
	}
}

// ----- Projects (admin) -----

func TestProjectsMergesVariantsIntoCanonical(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t, ctx)
	mcpClient := newTestClient(t, ctx, store)

	_ = callTool(t, ctx, mcpClient, "context", map[string]any{
		"prompt":     "x",
		"project":    "alt-name",
		"session_id": "test-merge",
	})
	_ = callTool(t, ctx, mcpClient, "remember", map[string]any{
		"title":      "mark in alt-name",
		"content":    "**What**: variant project observation.",
		"project":    "alt-name",
		"session_id": "test-merge",
	})

	mergeResult := callTool(t, ctx, mcpClient, "projects", map[string]any{
		"target":  "canonical",
		"sources": []any{"alt-name"},
	})
	mergePayload := mustStructuredMap(t, mergeResult)
	if mergePayload["target"] != "canonical" {
		t.Fatalf("merge payload missing target=canonical: %#v", mergePayload)
	}
}

// ----- Streamable HTTP transport -----

func TestServerStreamableHTTPWithBearerAuth(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t, ctx)
	server := NewServer(store)

	const token = "secret-token-xyz"
	mux := server.StreamableHTTPMux("/mcp", token)
	httpServer := httptest.NewServer(mux)
	t.Cleanup(httpServer.Close)

	httpTransport, err := transport.NewStreamableHTTP(httpServer.URL+"/mcp",
		transport.WithHTTPHeaders(map[string]string{"Authorization": "Bearer " + token}),
	)
	if err != nil {
		t.Fatalf("new streamable http transport: %v", err)
	}
	mcpClient := client.NewClient(httpTransport)
	t.Cleanup(func() { _ = mcpClient.Close() })

	if err := mcpClient.Start(ctx); err != nil {
		t.Fatalf("start streamable http client: %v", err)
	}

	var initReq mcp.InitializeRequest
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{Name: "kerebrom-test", Version: "1.0.0"}
	if _, err := mcpClient.Initialize(ctx, initReq); err != nil {
		t.Fatalf("initialize over streamable http: %v", err)
	}

	tools, err := mcpClient.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		t.Fatalf("list tools over streamable http: %v", err)
	}
	if !toolExists(tools.Tools, "context") {
		t.Fatalf("expected 'context' over streamable http, got %v", toolNames(tools.Tools))
	}

	// Unauthorised request must be rejected.
	resp, err := http.Get(httpServer.URL + "/mcp")
	if err != nil {
		t.Fatalf("anonymous GET: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 for anonymous request, got %d", resp.StatusCode)
	}
}

// ----- Errors -----

func TestServerReturnsToolErrorForMissingRequiredArgument(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t, ctx)
	mcpClient := newTestClient(t, ctx, store)

	result := callTool(t, ctx, mcpClient, "remember", map[string]any{
		"title": "Incomplete observation",
	})
	if !result.IsError {
		t.Fatalf("expected tool error result, got %#v", result)
	}
}

// ----- Helpers -----

func assertProtocolText(t *testing.T, text string) {
	t.Helper()

	for _, want := range []string{
		"persistent memory",
		"context",
		"remember",
		"recall",
		"summary",
		"What / Why / Where / Learned",
		"MANDATORY BEHAVIORS",
		"visible chat",
		"DEFERRED TOOL CLIENTS",
		"tool_search",
		"user-owned custom-instruction",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("protocol text missing %q: %s", want, text)
		}
	}
}

func assertToolDescriptionContains(t *testing.T, tools []mcp.Tool, name string, want string) {
	t.Helper()

	for _, tool := range tools {
		if tool.Name != name {
			continue
		}
		if !strings.Contains(tool.Description, want) {
			t.Fatalf("tool %s description missing %q: %s", name, want, tool.Description)
		}
		return
	}
	t.Fatalf("tool %s not found", name)
}

func assertToolAnnotation(t *testing.T, tools []mcp.Tool, name string, readOnly bool, destructive bool) {
	t.Helper()

	for _, tool := range tools {
		if tool.Name != name {
			continue
		}
		if tool.Annotations.ReadOnlyHint == nil || *tool.Annotations.ReadOnlyHint != readOnly {
			t.Fatalf("tool %s readOnlyHint mismatch: %#v", name, tool.Annotations)
		}
		if tool.Annotations.DestructiveHint == nil || *tool.Annotations.DestructiveHint != destructive {
			t.Fatalf("tool %s destructiveHint mismatch: %#v", name, tool.Annotations)
		}
		if tool.Annotations.OpenWorldHint == nil || *tool.Annotations.OpenWorldHint {
			t.Fatalf("tool %s should be closed-world/local: %#v", name, tool.Annotations)
		}
		return
	}
	t.Fatalf("tool %s not found", name)
}

func toolExists(tools []mcp.Tool, name string) bool {
	for _, tool := range tools {
		if tool.Name == name {
			return true
		}
	}
	return false
}

func toolNames(tools []mcp.Tool) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	return names
}

func openTestStore(t *testing.T, ctx context.Context) *sqlite.Store {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "kerebrom.db")
	store, err := sqlite.Open(sqlite.Config{Path: dbPath})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	if err := store.Init(ctx); err != nil {
		t.Fatalf("init store: %v", err)
	}
	return store
}

func newTestClient(t *testing.T, ctx context.Context, store *sqlite.Store) *client.Client {
	t.Helper()

	return newTestClientForServer(t, ctx, NewServer(store).MCPServer())
}

func newTestClientForServer(t *testing.T, ctx context.Context, server *mcpserver.MCPServer) *client.Client {
	t.Helper()

	mcpClient, err := client.NewInProcessClient(server)
	if err != nil {
		t.Fatalf("new in-process client: %v", err)
	}
	t.Cleanup(func() { _ = mcpClient.Close() })

	if err := mcpClient.Start(ctx); err != nil {
		t.Fatalf("start in-process client: %v", err)
	}

	var initReq mcp.InitializeRequest
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{Name: "kerebrom-test", Version: "1.0.0"}
	if _, err := mcpClient.Initialize(ctx, initReq); err != nil {
		t.Fatalf("initialize client: %v", err)
	}
	return mcpClient
}

func initializeInstructions(t *testing.T, ctx context.Context, server *mcpserver.MCPServer) string {
	t.Helper()

	mcpClient, err := client.NewInProcessClient(server)
	if err != nil {
		t.Fatalf("new in-process client: %v", err)
	}
	t.Cleanup(func() { _ = mcpClient.Close() })

	if err := mcpClient.Start(ctx); err != nil {
		t.Fatalf("start in-process client: %v", err)
	}

	var initReq mcp.InitializeRequest
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{Name: "kerebrom-test", Version: "1.0.0"}
	initResult, err := mcpClient.Initialize(ctx, initReq)
	if err != nil {
		t.Fatalf("initialize client: %v", err)
	}
	return initResult.Instructions
}

func callTool(t *testing.T, ctx context.Context, c toolCaller, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()

	var req mcp.CallToolRequest
	req.Params.Name = name
	req.Params.Arguments = args

	result, err := c.CallTool(ctx, req)
	if err != nil {
		t.Fatalf("call tool %s: %v", name, err)
	}
	return result
}

type toolCaller interface {
	CallTool(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error)
}

func mustStructuredMap(t *testing.T, result *mcp.CallToolResult) map[string]any {
	t.Helper()

	if result.IsError {
		t.Fatalf("unexpected tool error: %#v", result)
	}
	payload, ok := result.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("expected structured content map, got %T", result.StructuredContent)
	}
	return payload
}

func assertObservationInPayload(t *testing.T, payload map[string]any, key string, id int64) {
	t.Helper()

	items, ok := payload[key].([]any)
	if !ok {
		t.Fatalf("payload[%s] is not an observation array: %#v", key, payload[key])
	}
	for _, item := range items {
		observation, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if int64(observation["id"].(float64)) == id {
			return
		}
	}
	t.Fatalf("observation %d not found in payload[%s]: %#v", id, key, payload[key])
}

func mustNestedMap(t *testing.T, payload map[string]any, key string) map[string]any {
	t.Helper()

	value, ok := payload[key].(map[string]any)
	if !ok {
		t.Fatalf("expected nested map for %q, got %#v", key, payload[key])
	}
	return value
}
