// Package mcp implements the MCP (Model Context Protocol) server for Kerebrom.
// 9 tools exposed over stdio JSON-RPC.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/ulianbass/kerebrom/internal/store"
)

// NewServer creates an MCP server wired to the given Store.
func NewServer(s *store.Store, project string) *server.MCPServer {
	srv := server.NewMCPServer(
		"kerebrom",
		"2.0.0",
		server.WithToolCapabilities(true),
	)

	// 1. remember
	srv.AddTool(
		mcp.NewTool("remember",
			mcp.WithDescription("Store a new memory. Call proactively after decisions, bug fixes, discoveries."),
			mcp.WithString("content", mcp.Required(), mcp.Description("The memory content to store")),
			mcp.WithString("kind", mcp.Description("Memory tier: core, episodic, semantic, procedural"), mcp.Enum("core", "episodic", "semantic", "procedural")),
			mcp.WithNumber("importance", mcp.Description("Importance score 0-1 (default 0.5)")),
			mcp.WithNumber("confidence", mcp.Description("Confidence score 0-1 (default 0.8)")),
			mcp.WithArray("tags", mcp.Description("Observation tags"), mcp.WithStringItems()),
			mcp.WithObject("metadata", mcp.Description("Additional JSON metadata")),
		),
		makeRememberHandler(s, project),
	)

	// 2. recall
	srv.AddTool(
		mcp.NewTool("recall",
			mcp.WithDescription("Search memories by semantic + keyword + graph query. Use to retrieve relevant context."),
			mcp.WithString("query", mcp.Required(), mcp.Description("Natural-language search query")),
			mcp.WithNumber("limit", mcp.Description("Max memories to return (default 5)")),
		),
		makeRecallHandler(s, project),
	)

	// 3. forget
	srv.AddTool(
		mcp.NewTool("forget",
			mcp.WithDescription("Invalidate a specific memory by ID"),
			mcp.WithNumber("memory_id", mcp.Required(), mcp.Description("The ID of the memory to invalidate")),
		),
		makeForgetHandler(s, project),
	)

	// 4. context
	srv.AddTool(
		mcp.NewTool("context",
			mcp.WithDescription("Build a rich recall context — returns facts and relevant memories in progressive disclosure."),
			mcp.WithString("query", mcp.Required(), mcp.Description("Natural-language context query")),
			mcp.WithNumber("limit", mcp.Description("Max memories in context (default 5)")),
			mcp.WithNumber("layer", mcp.Description("Disclosure layer: 1=compact, 2=summary, 3=full detail")),
		),
		makeContextHandler(s, project),
	)

	// 5. entities
	srv.AddTool(
		mcp.NewTool("entities",
			mcp.WithDescription("List known entities in the knowledge graph"),
			mcp.WithNumber("limit", mcp.Description("Max entities to return (default 20)")),
		),
		makeEntitiesHandler(s, project),
	)

	// 6. facts
	srv.AddTool(
		mcp.NewTool("facts",
			mcp.WithDescription("List active semantic facts (subject → predicate → object)"),
			mcp.WithNumber("limit", mcp.Description("Max facts to return (default 20)")),
		),
		makeFactsHandler(s, project),
	)

	// 7. query
	srv.AddTool(
		mcp.NewTool("query",
			mcp.WithDescription("Structured query with filters: kind, tags, importance, dates, metadata"),
			mcp.WithString("kind", mcp.Description("Filter by kind"), mcp.Enum("core", "episodic", "semantic", "procedural")),
			mcp.WithArray("tags", mcp.Description("Filter by tags"), mcp.WithStringItems()),
			mcp.WithNumber("importance_min", mcp.Description("Minimum importance (0-1)")),
			mcp.WithNumber("importance_max", mcp.Description("Maximum importance (0-1)")),
			mcp.WithNumber("limit", mcp.Description("Max results (default 20)")),
		),
		makeQueryHandler(s, project),
	)

	// 8. gaps
	srv.AddTool(
		mcp.NewTool("gaps",
			mcp.WithDescription("List unresolved references — knowledge gaps in the graph"),
			mcp.WithNumber("limit", mcp.Description("Max gaps to return (default 20)")),
		),
		makeGapsHandler(s, project),
	)

	// 9. sopor
	srv.AddTool(
		mcp.NewTool("sopor",
			mcp.WithDescription("Consolidate memories from Claude Code session transcripts"),
			mcp.WithString("session", mcp.Description("Which transcript: 'latest', 'all', or a session ID")),
			mcp.WithString("project_path", mcp.Description("Filesystem path of the project")),
		),
		makeSoporHandler(s, project),
	)

	return srv
}

// --- Tool handlers ---

func makeRememberHandler(s *store.Store, project string) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		content := getString(req, "content")
		if content == "" {
			return mcp.NewToolResultError("content is required"), nil
		}
		kind := getStringOr(req, "kind", "episodic")
		importance := getNumberOr(req, "importance", 0.5)
		confidence := getNumberOr(req, "confidence", 0.8)
		tags := getStringArray(req, "tags")
		metadata := getObject(req, "metadata")

		result, err := s.Remember(content, project, kind, "mcp", importance, confidence, tags, metadata)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("remember failed: %v", err)), nil
		}
		return jsonResult(result)
	}
}

func makeRecallHandler(s *store.Store, project string) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		query := getString(req, "query")
		if query == "" {
			return mcp.NewToolResultError("query is required"), nil
		}
		limit := int(getNumberOr(req, "limit", 5))
		memories, err := s.Recall(query, project, store.RecallOptions{
			Limit:      limit,
			Touch:      true,
			Reactivate: true,
		})
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("recall failed: %v", err)), nil
		}
		return jsonResult(memories)
	}
}

func makeForgetHandler(s *store.Store, project string) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		memoryID := int64(getNumberOr(req, "memory_id", 0))
		if memoryID == 0 {
			return mcp.NewToolResultError("memory_id is required"), nil
		}
		n, err := s.Forget(project, memoryID)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("forget failed: %v", err)), nil
		}
		return jsonResult(map[string]int{"invalidated": n})
	}
}

func makeContextHandler(s *store.Store, project string) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		query := getString(req, "query")
		if query == "" {
			return mcp.NewToolResultError("query is required"), nil
		}
		limit := int(getNumberOr(req, "limit", 5))
		layer := int(getNumberOr(req, "layer", 2))
		result, err := s.BuildContext(query, project, limit, layer)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("context failed: %v", err)), nil
		}
		return jsonResult(result)
	}
}

func makeEntitiesHandler(s *store.Store, project string) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		limit := int(getNumberOr(req, "limit", 20))
		entities, err := s.ListEntities(project, limit)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("entities failed: %v", err)), nil
		}
		return jsonResult(entities)
	}
}

func makeFactsHandler(s *store.Store, project string) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		limit := int(getNumberOr(req, "limit", 20))
		facts, err := s.ListFacts(project, limit)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("facts failed: %v", err)), nil
		}
		return jsonResult(facts)
	}
}

func makeQueryHandler(s *store.Store, project string) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		kind := getString(req, "kind")
		limit := int(getNumberOr(req, "limit", 20))
		// Simple query: filter by kind from the recall results.
		memories, err := s.Recall("", project, store.RecallOptions{Limit: limit})
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("query failed: %v", err)), nil
		}
		if kind != "" {
			var filtered []*store.MemoryRecord
			for _, m := range memories {
				if m.Kind == kind {
					filtered = append(filtered, m)
				}
			}
			memories = filtered
		}
		return jsonResult(memories)
	}
}

func makeGapsHandler(s *store.Store, project string) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		limit := int(getNumberOr(req, "limit", 20))
		gaps, err := s.ListGaps(project, limit)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("gaps failed: %v", err)), nil
		}
		return jsonResult(gaps)
	}
}

func makeSoporHandler(s *store.Store, project string) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		// Sopor will be fully implemented in Phase 5.
		return mcp.NewToolResultText("Sopor consolidation: not yet implemented in Go build. Coming in Phase 5."), nil
	}
}

// --- Helpers ---

func getString(req mcp.CallToolRequest, key string) string {
	if v, ok := req.GetArguments()[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func getStringOr(req mcp.CallToolRequest, key, def string) string {
	v := getString(req, key)
	if v == "" {
		return def
	}
	return v
}

func getNumberOr(req mcp.CallToolRequest, key string, def float64) float64 {
	if v, ok := req.GetArguments()[key]; ok {
		switch n := v.(type) {
		case float64:
			return n
		case int:
			return float64(n)
		}
	}
	return def
}

func getStringArray(req mcp.CallToolRequest, key string) []string {
	if v, ok := req.GetArguments()[key]; ok {
		if arr, ok := v.([]any); ok {
			var result []string
			for _, item := range arr {
				if s, ok := item.(string); ok {
					result = append(result, s)
				}
			}
			return result
		}
	}
	return nil
}

func getObject(req mcp.CallToolRequest, key string) map[string]any {
	if v, ok := req.GetArguments()[key]; ok {
		if m, ok := v.(map[string]any); ok {
			return m
		}
	}
	return nil
}

func jsonResult(v any) (*mcp.CallToolResult, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("json encode: %v", err)), nil
	}
	return mcp.NewToolResultText(string(b)), nil
}
