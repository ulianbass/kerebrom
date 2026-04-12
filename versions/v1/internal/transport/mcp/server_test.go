package mcptransport

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/ulianbass/kerebrom/internal/store/sqlite"
)

func TestServerExposesDesktopMemoryProtocol(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t, ctx)
	mcpClient := newTestClient(t, ctx, store)

	prompts, err := mcpClient.ListPrompts(ctx, mcp.ListPromptsRequest{})
	if err != nil {
		t.Fatalf("list prompts: %v", err)
	}
	if len(prompts.Prompts) != 1 {
		t.Fatalf("expected 1 prompt, got %d", len(prompts.Prompts))
	}
	if prompts.Prompts[0].Name != "kerebrom_memory_protocol" {
		t.Fatalf("unexpected prompt: %#v", prompts.Prompts[0])
	}

	var promptReq mcp.GetPromptRequest
	promptReq.Params.Name = "kerebrom_memory_protocol"
	prompt, err := mcpClient.GetPrompt(ctx, promptReq)
	if err != nil {
		t.Fatalf("get prompt: %v", err)
	}
	if len(prompt.Messages) != 1 {
		t.Fatalf("expected one protocol message, got %d", len(prompt.Messages))
	}
	content, ok := prompt.Messages[0].Content.(mcp.TextContent)
	if !ok {
		t.Fatalf("expected prompt text content, got %T", prompt.Messages[0].Content)
	}
	assertProtocolText(t, content.Text)

	resources, err := mcpClient.ListResources(ctx, mcp.ListResourcesRequest{})
	if err != nil {
		t.Fatalf("list resources: %v", err)
	}
	if len(resources.Resources) != 1 {
		t.Fatalf("expected 1 resource, got %d", len(resources.Resources))
	}
	if resources.Resources[0].URI != "kerebrom://memory-protocol" {
		t.Fatalf("unexpected resource: %#v", resources.Resources[0])
	}

	var resourceReq mcp.ReadResourceRequest
	resourceReq.Params.URI = "kerebrom://memory-protocol"
	resource, err := mcpClient.ReadResource(ctx, resourceReq)
	if err != nil {
		t.Fatalf("read resource: %v", err)
	}
	if len(resource.Contents) != 1 {
		t.Fatalf("expected one protocol resource, got %d", len(resource.Contents))
	}
	resourceContent, ok := resource.Contents[0].(mcp.TextResourceContents)
	if !ok {
		t.Fatalf("expected text resource content, got %T", resource.Contents[0])
	}
	assertProtocolText(t, resourceContent.Text)
}

func TestServerToolsLifecycleAndSearch(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t, ctx)

	mcpClient := newTestClient(t, ctx, store)

	tools, err := mcpClient.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(tools.Tools) != 15 {
		t.Fatalf("expected 15 tools, got %d", len(tools.Tools))
	}
	assertToolDescriptionContains(t, tools.Tools, "mem_save_prompt", "Claude Desktop")
	assertToolDescriptionContains(t, tools.Tools, "mem_context", "start of every non-trivial turn")
	assertToolDescriptionContains(t, tools.Tools, "mem_save", "What / Why / Where / Learned")

	sessionStart := callTool(t, ctx, mcpClient, "mem_session_start", map[string]any{
		"id":        "session-1",
		"project":   "Proyecto Kerebrom",
		"directory": "/tmp/project",
	})
	sessionPayload := mustStructuredMap(t, sessionStart)
	if sessionPayload["started"] != true {
		t.Fatalf("expected started=true, got %#v", sessionPayload["started"])
	}

	startedSession := mustNestedMap(t, sessionPayload, "session")
	if startedSession["project"] != "proyecto-kerebrom" {
		t.Fatalf("expected normalized project, got %#v", startedSession["project"])
	}
	if startedSession["status"] != "active" {
		t.Fatalf("expected active status, got %#v", startedSession["status"])
	}

	saved := callTool(t, ctx, mcpClient, "mem_save", map[string]any{
		"title":      "Shared local memory",
		"content":    "Codex and Claude should read the same local store.",
		"type":       "decision",
		"project":    "Proyecto Kerebrom",
		"scope":      "project",
		"topic_key":  "architecture/shared-memory",
		"session_id": "session-1",
		"tool_name":  "codex",
	})
	savePayload := mustStructuredMap(t, saved)
	if savePayload["saved"] != true {
		t.Fatalf("expected saved=true, got %#v", savePayload["saved"])
	}
	savedObservation := mustNestedMap(t, savePayload, "observation")
	observationID := int(savedObservation["id"].(float64))

	search := callTool(t, ctx, mcpClient, "mem_search", map[string]any{
		"query":   "same local store",
		"project": "Proyecto Kerebrom",
		"limit":   5,
	})
	searchPayload := mustStructuredMap(t, search)
	if got := int(searchPayload["count"].(float64)); got != 1 {
		t.Fatalf("expected count=1, got %d", got)
	}

	observations, ok := searchPayload["observations"].([]any)
	if !ok || len(observations) != 1 {
		t.Fatalf("expected one structured observation, got %#v", searchPayload["observations"])
	}

	searchObservation, ok := observations[0].(map[string]any)
	if !ok {
		t.Fatalf("expected observation map, got %T", observations[0])
	}
	if searchObservation["project"] != "proyecto-kerebrom" {
		t.Fatalf("expected normalized observation project, got %#v", searchObservation["project"])
	}

	getObservation := callTool(t, ctx, mcpClient, "mem_get_observation", map[string]any{
		"id": observationID,
	})
	getObservationPayload := mustStructuredMap(t, getObservation)
	fetchedObservation := mustNestedMap(t, getObservationPayload, "observation")
	if int(fetchedObservation["id"].(float64)) != observationID {
		t.Fatalf("unexpected fetched observation: %#v", fetchedObservation)
	}

	timeline := callTool(t, ctx, mcpClient, "mem_timeline", map[string]any{
		"project": "Proyecto Kerebrom",
		"limit":   5,
	})
	timelinePayload := mustStructuredMap(t, timeline)
	if got := int(timelinePayload["count"].(float64)); got != 1 {
		t.Fatalf("expected timeline count=1, got %d", got)
	}

	contextResult := callTool(t, ctx, mcpClient, "mem_context", map[string]any{
		"project": "Proyecto Kerebrom",
		"query":   "same local store",
		"limit":   5,
	})
	contextPayload := mustStructuredMap(t, contextResult)
	matches, ok := contextPayload["matches"].([]any)
	if !ok || len(matches) != 1 {
		t.Fatalf("expected one context match, got %#v", contextPayload["matches"])
	}
	recent, ok := contextPayload["recent_observations"].([]any)
	if !ok || len(recent) != 1 {
		t.Fatalf("expected one recent observation, got %#v", contextPayload["recent_observations"])
	}

	statsResult := callTool(t, ctx, mcpClient, "mem_stats", map[string]any{
		"project": "Proyecto Kerebrom",
	})
	statsPayload := mustStructuredMap(t, statsResult)
	stats := mustNestedMap(t, statsPayload, "stats")
	if got := int(stats["observation_count"].(float64)); got != 1 {
		t.Fatalf("expected observationCount=1, got %d", got)
	}
	if got := int(stats["active_session_count"].(float64)); got != 1 {
		t.Fatalf("expected activeSessionCount=1, got %d", got)
	}

	sessionEnd := callTool(t, ctx, mcpClient, "mem_session_end", map[string]any{
		"id":      "session-1",
		"summary": "Shared memory workflow established.",
	})
	endPayload := mustStructuredMap(t, sessionEnd)
	if endPayload["ended"] != true {
		t.Fatalf("expected ended=true, got %#v", endPayload["ended"])
	}

	endedSession := mustNestedMap(t, endPayload, "session")
	if endedSession["status"] != "completed" {
		t.Fatalf("expected completed status, got %#v", endedSession["status"])
	}
	if endedSession["summary"] != "Shared memory workflow established." {
		t.Fatalf("unexpected session summary: %#v", endedSession["summary"])
	}

	sessionSummary := callTool(t, ctx, mcpClient, "mem_session_summary", map[string]any{
		"id":    "session-1",
		"limit": 5,
	})
	sessionSummaryPayload := mustStructuredMap(t, sessionSummary)
	if got := int(sessionSummaryPayload["observation_count"].(float64)); got != 1 {
		t.Fatalf("expected observation_count=1, got %d", got)
	}
	summarySession := mustNestedMap(t, sessionSummaryPayload, "session")
	if summarySession["status"] != "completed" {
		t.Fatalf("expected completed session summary status, got %#v", summarySession["status"])
	}

	suggestedTopic := callTool(t, ctx, mcpClient, "mem_suggest_topic_key", map[string]any{
		"type":  "architecture",
		"title": "Shared local memory",
	})
	suggestedTopicPayload := mustStructuredMap(t, suggestedTopic)
	if suggestedTopicPayload["topic_key"] != "architecture/shared-local-memory" {
		t.Fatalf("unexpected topic key: %#v", suggestedTopicPayload)
	}

	updated := callTool(t, ctx, mcpClient, "mem_update", map[string]any{
		"id":      observationID,
		"content": "Codex and Claude should read exactly the same local store.",
	})
	updatedPayload := mustStructuredMap(t, updated)
	updatedObservation := mustNestedMap(t, updatedPayload, "observation")
	if int(updatedObservation["revision_count"].(float64)) == 0 {
		t.Fatalf("expected revision count after update, got %#v", updatedObservation)
	}

	centeredTimeline := callTool(t, ctx, mcpClient, "mem_timeline", map[string]any{
		"observation_id": observationID,
		"before":         2,
		"after":          2,
	})
	centeredTimelinePayload := mustStructuredMap(t, centeredTimeline)
	if _, ok := centeredTimelinePayload["observation"].(map[string]any); !ok {
		t.Fatalf("expected centered timeline observation, got %#v", centeredTimelinePayload)
	}

	prompt := callTool(t, ctx, mcpClient, "mem_save_prompt", map[string]any{
		"session_id": "session-1",
		"project":    "Proyecto Kerebrom",
		"content":    "Close Engram parity for Kerebrom v1.",
	})
	promptPayload := mustStructuredMap(t, prompt)
	if promptPayload["saved"] != true {
		t.Fatalf("expected prompt saved=true, got %#v", promptPayload)
	}

	passive := callTool(t, ctx, mcpClient, "mem_capture_passive", map[string]any{
		"session_id": "session-1",
		"project":    "Proyecto Kerebrom",
		"content":    "## Key Learnings:\n\n1. MCP parity needs 15 tools\n2. Sync chunks should be content-addressed",
	})
	passivePayload := mustStructuredMap(t, passive)
	if got := int(passivePayload["count"].(float64)); got != 2 {
		t.Fatalf("expected 2 passive captures, got %d", got)
	}
	passiveObservations, ok := passivePayload["observations"].([]any)
	if !ok || len(passiveObservations) != 2 {
		t.Fatalf("unexpected passive observations: %#v", passivePayload["observations"])
	}
	passiveObservation, ok := passiveObservations[0].(map[string]any)
	if !ok {
		t.Fatalf("unexpected passive observation: %#v", passiveObservations[0])
	}

	deleted := callTool(t, ctx, mcpClient, "mem_delete", map[string]any{
		"id": int(passiveObservation["id"].(float64)),
	})
	deletedPayload := mustStructuredMap(t, deleted)
	if deletedPayload["deleted"] != true {
		t.Fatalf("expected deleted=true, got %#v", deletedPayload)
	}

	variant := callTool(t, ctx, mcpClient, "mem_save", map[string]any{
		"title":   "Variant project spelling",
		"content": "Project merge should consolidate memories.",
		"project": "Kerebrom Variant",
	})
	mustStructuredMap(t, variant)

	merged := callTool(t, ctx, mcpClient, "mem_merge_projects", map[string]any{
		"target":  "Proyecto Kerebrom",
		"sources": []string{"Kerebrom Variant"},
	})
	mergedPayload := mustStructuredMap(t, merged)
	if mergedPayload["target"] != "proyecto-kerebrom" {
		t.Fatalf("unexpected merge payload: %#v", mergedPayload)
	}
}

func assertProtocolText(t *testing.T, text string) {
	t.Helper()

	for _, want := range []string{
		"Kerebrom Persistent Memory Protocol",
		"mem_save_prompt",
		"mem_context",
		"What, Why, Where, Learned",
		"Claude Desktop",
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

func TestServerReturnsToolErrorForMissingRequiredArgument(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t, ctx)
	mcpClient := newTestClient(t, ctx, store)

	result := callTool(t, ctx, mcpClient, "mem_save", map[string]any{
		"title": "Incomplete memory",
	})
	if !result.IsError {
		t.Fatalf("expected tool error result, got %#v", result)
	}
}

func openTestStore(t *testing.T, ctx context.Context) *sqlite.Store {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "kerebrom.db")
	store, err := sqlite.Open(sqlite.Config{Path: dbPath})
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

func newTestClient(t *testing.T, ctx context.Context, store *sqlite.Store) *client.Client {
	t.Helper()

	mcpClient, err := client.NewInProcessClient(NewServer(store).MCPServer())
	if err != nil {
		t.Fatalf("new in-process client: %v", err)
	}
	t.Cleanup(func() {
		_ = mcpClient.Close()
	})

	if err := mcpClient.Start(ctx); err != nil {
		t.Fatalf("start in-process client: %v", err)
	}

	var initReq mcp.InitializeRequest
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{
		Name:    "kerebrom-test-client",
		Version: "1.0.0",
	}
	initReq.Params.Capabilities = mcp.ClientCapabilities{}
	if _, err := mcpClient.Initialize(ctx, initReq); err != nil {
		t.Fatalf("initialize client: %v", err)
	}

	return mcpClient
}

func callTool(t *testing.T, ctx context.Context, client toolCaller, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()

	var req mcp.CallToolRequest
	req.Params.Name = name
	req.Params.Arguments = args

	result, err := client.CallTool(ctx, req)
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
