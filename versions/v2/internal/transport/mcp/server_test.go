package mcptransport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

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

func mustNestedMap(t *testing.T, payload map[string]any, key string) map[string]any {
	t.Helper()

	value, ok := payload[key].(map[string]any)
	if !ok {
		t.Fatalf("expected nested map for %q, got %#v", key, payload[key])
	}
	return value
}
