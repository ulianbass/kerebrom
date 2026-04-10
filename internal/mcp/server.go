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

// Annotation helpers.
var (
	readOnly    = mcp.ToolAnnotation{ReadOnlyHint: boolPtr(true), DestructiveHint: boolPtr(false), IdempotentHint: boolPtr(true)}
	writeOp     = mcp.ToolAnnotation{ReadOnlyHint: boolPtr(false), DestructiveHint: boolPtr(false), IdempotentHint: boolPtr(false)}
	destructive = mcp.ToolAnnotation{ReadOnlyHint: boolPtr(false), DestructiveHint: boolPtr(true), IdempotentHint: boolPtr(false)}
)

func boolPtr(b bool) *bool { return &b }

// NewServer creates an MCP server wired to the given Store.
func NewServer(s *store.Store, project string) *server.MCPServer {
	srv := server.NewMCPServer(
		"kerebrom",
		"2.0.0",
		server.WithToolCapabilities(true),
		server.WithInstructions(serverInstructions),
	)

	// === CORE TOOLS (always available, auto-approved) ===

	srv.AddTool(
		mcp.NewTool("recall",
			mcp.WithDescription("Search memories. ALWAYS call BEFORE answering questions about the user, their projects, or history."),
			mcp.WithToolAnnotation(readOnly),
			mcp.WithString("query", mcp.Required(), mcp.Description("Natural-language search query")),
			mcp.WithNumber("limit", mcp.Description("Max memories to return (default 5)")),
		),
		makeRecallHandler(s, project),
	)

	srv.AddTool(
		mcp.NewTool("remember",
			mcp.WithDescription("Store a new memory. Call PROACTIVELY after decisions, bug fixes, discoveries — do not wait to be asked."),
			mcp.WithToolAnnotation(writeOp),
			mcp.WithString("content", mcp.Required(), mcp.Description("The memory content to store")),
			mcp.WithString("kind", mcp.Description("Memory tier: core, episodic, semantic, procedural"), mcp.Enum("core", "episodic", "semantic", "procedural")),
			mcp.WithNumber("importance", mcp.Description("Importance score 0-1 (default 0.5)")),
			mcp.WithNumber("confidence", mcp.Description("Confidence score 0-1 (default 0.8)")),
			mcp.WithArray("tags", mcp.Description("Observation tags"), mcp.WithStringItems()),
			mcp.WithObject("metadata", mcp.Description("Additional JSON metadata")),
		),
		makeRememberHandler(s, project),
	)

	srv.AddTool(
		mcp.NewTool("context",
			mcp.WithDescription("Load full context bundle (facts + memories). Call at session start."),
			mcp.WithToolAnnotation(readOnly),
			mcp.WithString("query", mcp.Required(), mcp.Description("Natural-language context query")),
			mcp.WithNumber("limit", mcp.Description("Max memories in context (default 5)")),
			mcp.WithNumber("layer", mcp.Description("Disclosure layer: 1=compact, 2=summary, 3=full detail")),
		),
		makeContextHandler(s, project),
	)

	srv.AddTool(
		mcp.NewTool("entities",
			mcp.WithDescription("List known people, projects, concepts in the knowledge graph."),
			mcp.WithToolAnnotation(readOnly),
			mcp.WithNumber("limit", mcp.Description("Max entities to return (default 20)")),
		),
		makeEntitiesHandler(s, project),
	)

	srv.AddTool(
		mcp.NewTool("facts",
			mcp.WithDescription("List semantic relations (who → relation → what)."),
			mcp.WithToolAnnotation(readOnly),
			mcp.WithNumber("limit", mcp.Description("Max facts to return (default 20)")),
		),
		makeFactsHandler(s, project),
	)

	// === SECONDARY TOOLS ===

	srv.AddTool(
		mcp.NewTool("forget",
			mcp.WithDescription("Invalidate a specific memory by ID."),
			mcp.WithToolAnnotation(destructive),
			mcp.WithNumber("memory_id", mcp.Required(), mcp.Description("The ID of the memory to invalidate")),
		),
		makeForgetHandler(s, project),
	)

	srv.AddTool(
		mcp.NewTool("query",
			mcp.WithDescription("Structured query with filters: kind, tags, importance."),
			mcp.WithToolAnnotation(readOnly),
			mcp.WithString("kind", mcp.Description("Filter by kind"), mcp.Enum("core", "episodic", "semantic", "procedural")),
			mcp.WithArray("tags", mcp.Description("Filter by tags"), mcp.WithStringItems()),
			mcp.WithNumber("importance_min", mcp.Description("Minimum importance (0-1)")),
			mcp.WithNumber("importance_max", mcp.Description("Maximum importance (0-1)")),
			mcp.WithNumber("limit", mcp.Description("Max results (default 20)")),
		),
		makeQueryHandler(s, project),
	)

	srv.AddTool(
		mcp.NewTool("gaps",
			mcp.WithDescription("List unresolved knowledge gaps."),
			mcp.WithToolAnnotation(readOnly),
			mcp.WithNumber("limit", mcp.Description("Max gaps to return (default 20)")),
		),
		makeGapsHandler(s, project),
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

// serverInstructions — aggressive proactive behavior.
// More directive than Engram: evaluate EVERY message, not just decisions.
const serverInstructions = `Kerebrom provides persistent memory that survives across sessions and is shared across all AI tools.

CORE TOOLS (always available — use without asking):
  recall — search memories
  remember — save new information
  context — load facts + memories at session start
  entities — list known people, projects, concepts
  facts — list semantic relations

MANDATORY BEHAVIORS — follow these on EVERY interaction:

1. RECALL FIRST: Before answering ANY question, call recall with a relevant query.

2. SAVE ALWAYS: After EVERY user message, extract and save any valuable information. Examples:
   - "quiero que genere $3000 mensuales con tema negro" → save TWO facts:
     remember("Objetivo financiero Falage: $3,000-$5,000 USD mensuales", kind="core")
     remember("Preferencia UI Quamtos: tema oscuro/negro", kind="core")
   - "hoy me siento cansado, estuve toda la noche trabajando" → save:
     remember("Estado del usuario 10/abr: trabajó toda la noche, cansado", kind="episodic")

3. CONTEXT AT START: On the first message of a conversation, call context.

HOW TO SAVE — write memories as natural, readable facts:

  GOOD: "Ulian Bass, de Guatemala. Prefiere español neutro, nunca voseo argentino."
  GOOD: "Proyecto Falage: objetivo USD 3,000-5,000 mensuales con trading sistematico."
  GOOD: "Prefiero tema oscuro para Quamtos. Full-width, sin margenes laterales."
  GOOD: "Tengo tres perros: Mickey, Satoshi y Max."

  BAD: "[PERSONAL] What: Ulian Bass. Why: identity. Where: all. Learned: use Ulian."
  BAD: "El usuario dijo que no le gusta el blanco porque yo puse blanco..."

Rules:
  - Write as if explaining to a colleague. Natural language, first person when possible.
  - Start with a clear title/topic — not a template tag.
  - One fact per remember call.
  - Use kind="core" for identity and preferences (never decay).
  - Use kind="episodic" for events and sessions.
  - Use kind="semantic" for technical facts and decisions.
  - Never save: greetings, "ok", "listo", code output, confirmations.`
