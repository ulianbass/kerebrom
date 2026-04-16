package mcptransport

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/ulianbass/kerebrom/internal/config"
	promptfilter "github.com/ulianbass/kerebrom/internal/prompt"
	"github.com/ulianbass/kerebrom/internal/store/sqlite"
	"github.com/ulianbass/kerebrom/internal/version"
)

type Config struct {
	DefaultProject string
}

type HTTPConfig struct {
	Addr      string
	Path      string
	AuthToken string
}

type Server struct {
	store     *sqlite.Store
	server    *mcpserver.MCPServer
	config    Config
	allowlist map[string]bool
	activity  *SessionActivity
}

func NewServer(store *sqlite.Store) *Server {
	return NewServerWithConfig(store, Config{}, nil)
}

func NewServerWithTools(store *sqlite.Store, allowlist map[string]bool) *Server {
	return NewServerWithConfig(store, Config{}, allowlist)
}

func NewServerWithConfig(store *sqlite.Store, cfg Config, allowlist map[string]bool) *Server {
	srv := &Server{
		store:     store,
		config:    cfg,
		allowlist: allowlist,
		activity:  NewSessionActivity(10 * time.Minute),
		server: mcpserver.NewMCPServer(
			config.AppName,
			version.Version,
			mcpserver.WithInstructions(memoryProtocolText()),
			mcpserver.WithToolCapabilities(false),
			mcpserver.WithPromptCapabilities(false),
			mcpserver.WithResourceCapabilities(false, false),
			mcpserver.WithRecovery(),
			mcpserver.WithResourceRecovery(),
		),
	}

	srv.registerTools()
	srv.registerPromptsAndResources()
	return srv
}

var profileAgentTools = map[string]bool{
	"mem_save":              true,
	"mem_search":            true,
	"mem_update":            true,
	"mem_context":           true,
	"mem_bootstrap":         true,
	"mem_get_observation":   true,
	"mem_save_prompt":       true,
	"mem_session_summary":   true,
	"mem_session_start":     true,
	"mem_session_end":       true,
	"mem_suggest_topic_key": true,
	"mem_capture_passive":   true,
	"recall":                true,
}

var profileAdminTools = map[string]bool{
	"mem_delete":         true,
	"mem_stats":          true,
	"mem_timeline":       true,
	"mem_merge_projects": true,
}

var (
	readOnlyTool = mcp.ToolAnnotation{
		ReadOnlyHint:    boolPtr(true),
		DestructiveHint: boolPtr(false),
		IdempotentHint:  boolPtr(true),
		OpenWorldHint:   boolPtr(false),
	}
	writeTool = mcp.ToolAnnotation{
		ReadOnlyHint:    boolPtr(false),
		DestructiveHint: boolPtr(false),
		IdempotentHint:  boolPtr(false),
		OpenWorldHint:   boolPtr(false),
	}
	destructiveTool = mcp.ToolAnnotation{
		ReadOnlyHint:    boolPtr(false),
		DestructiveHint: boolPtr(true),
		IdempotentHint:  boolPtr(false),
		OpenWorldHint:   boolPtr(false),
	}
)

func boolPtr(value bool) *bool {
	return &value
}

func ResolveTools(input string) map[string]bool {
	input = strings.TrimSpace(input)
	if input == "" || input == "all" {
		return nil
	}

	resolved := map[string]bool{}
	for _, token := range strings.Split(input, ",") {
		token = strings.TrimSpace(token)
		switch token {
		case "":
			continue
		case "all":
			return nil
		case "agent":
			for name := range profileAgentTools {
				resolved[name] = true
			}
		case "admin":
			for name := range profileAdminTools {
				resolved[name] = true
			}
		default:
			resolved[token] = true
		}
	}
	if len(resolved) == 0 {
		return nil
	}
	return resolved
}

type SessionActivity struct {
	mu         sync.Mutex
	sessions   map[string]*activityState
	nudgeAfter time.Duration
	now        func() time.Time
}

type activityState struct {
	startedAt     time.Time
	lastSaveAt    time.Time
	toolCallCount int
	saveCount     int
}

func NewSessionActivity(nudgeAfter time.Duration) *SessionActivity {
	if nudgeAfter <= 0 {
		nudgeAfter = 10 * time.Minute
	}
	return &SessionActivity{
		sessions:   map[string]*activityState{},
		nudgeAfter: nudgeAfter,
		now:        time.Now,
	}
}

func (a *SessionActivity) RecordToolCall(sessionID string) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	state := a.getOrCreateLocked(sessionID)
	state.toolCallCount++
}

func (a *SessionActivity) RecordSave(sessionID string) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	state := a.getOrCreateLocked(sessionID)
	state.saveCount++
	state.lastSaveAt = a.now().UTC()
}

func (a *SessionActivity) ClearSession(sessionID string) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.sessions, strings.TrimSpace(sessionID))
}

func (a *SessionActivity) NudgeIfNeeded(sessionID string) string {
	if a == nil {
		return ""
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	state, ok := a.sessions[strings.TrimSpace(sessionID)]
	if !ok {
		return ""
	}

	now := a.now().UTC()
	if now.Sub(state.startedAt) < a.nudgeAfter {
		return ""
	}
	if state.saveCount == 0 && state.toolCallCount <= 5 {
		return ""
	}

	reference := state.lastSaveAt
	if reference.IsZero() {
		reference = state.startedAt
	}
	if now.Sub(reference) < a.nudgeAfter {
		return ""
	}

	minutes := int(now.Sub(reference).Minutes())
	return fmt.Sprintf("Kerebrom memory reminder: no mem_save call for this MCP session in %d minutes. If the work produced durable decisions, bugfixes, constraints, or discoveries, save a distilled memory now.", minutes)
}

func (a *SessionActivity) ActivityScore(sessionID string) string {
	if a == nil {
		return ""
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	state, ok := a.sessions[strings.TrimSpace(sessionID)]
	if !ok {
		return ""
	}

	score := fmt.Sprintf("Session activity: %d tool calls, %d saves", state.toolCallCount, state.saveCount)
	if state.saveCount == 0 && state.toolCallCount > 5 {
		score += ". High activity with no saves; persist important decisions before closing."
	}
	return score
}

func (a *SessionActivity) getOrCreateLocked(sessionID string) *activityState {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		sessionID = "mcp:default"
	}
	state, ok := a.sessions[sessionID]
	if !ok {
		state = &activityState{startedAt: a.now().UTC()}
		a.sessions[sessionID] = state
	}
	return state
}

func (s *Server) MCPServer() *mcpserver.MCPServer {
	return s.server
}

func (s *Server) ServeStdio() error {
	return mcpserver.ServeStdio(s.server)
}

func (s *Server) ServeStreamableHTTP(cfg HTTPConfig) error {
	addr := strings.TrimSpace(cfg.Addr)
	if addr == "" {
		addr = "127.0.0.1:7437"
	}

	httpServer := &http.Server{
		Addr:              addr,
		Handler:           s.StreamableHTTPMux(cfg.Path, cfg.AuthToken),
		ReadHeaderTimeout: 5 * time.Second,
	}
	return httpServer.ListenAndServe()
}

func (s *Server) StreamableHTTPMux(path string, authToken string) http.Handler {
	endpoint := normalizeEndpointPath(path)
	streamable := mcpserver.NewStreamableHTTPServer(s.server)

	mux := http.NewServeMux()
	mux.Handle(endpoint, bearerAuthMiddleware(authToken, streamable))
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":  "ok",
			"service": "kerebrom-mcp",
			"path":    endpoint,
		})
	})
	return mux
}

func normalizeEndpointPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return "/mcp"
	}
	path = "/" + strings.Trim(path, "/")
	if path == "/" {
		return "/mcp"
	}
	return path
}

func bearerAuthMiddleware(authToken string, next http.Handler) http.Handler {
	authToken = strings.TrimSpace(authToken)
	if authToken == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+authToken && r.Header.Get("X-Kerebrom-Token") != authToken {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) addTool(name string, tool mcp.Tool, handler mcpserver.ToolHandlerFunc) {
	if s.allowlist != nil && !s.allowlist[name] {
		return
	}
	s.server.AddTool(tool, handler)
}

func (s *Server) registerTools() {
	s.addTool("mem_bootstrap",
		mcp.NewTool("mem_bootstrap",
			mcp.WithDescription("FIRST Kerebrom tool to call at the start of EVERY non-trivial user turn in MCP-only clients such as Claude Desktop Chat/Cowork. ALWAYS call before answering — do not wait for the user to ask. One call starts or refreshes the session, saves the prompt when substantive, retrieves memory context, and returns cross-project matches when the client cannot identify the right project."),
			mcp.WithToolAnnotation(writeTool),
			mcp.WithString("prompt",
				mcp.Description("Current user prompt. Kerebrom saves it as prompt history when substantive."),
			),
			mcp.WithString("query",
				mcp.Description("Compact search query derived from the user's prompt. Defaults to prompt when omitted."),
			),
			mcp.WithString("project",
				mcp.Description("Project name or workspace identifier. Leave empty if unknown; Kerebrom can fall back and search across projects."),
			),
			mcp.WithString("session_id",
				mcp.Description("Stable session id for this visible chat or agent session."),
			),
			mcp.WithString("directory",
				mcp.Description("Current working directory if known."),
			),
			mcp.WithNumber("limit",
				mcp.Description("Maximum memories to return. Defaults to 10."),
			),
		),
		s.handleMemBootstrap,
	)

	s.addTool("recall",
		mcp.NewTool("recall",
			mcp.WithDescription("ALWAYS call Recall before answering any question about the user, their projects, preferences, previous decisions, or history. This is the natural alias for fast prior context; it searches across projects if the current project is wrong or empty. Do not answer from model assumptions when Kerebrom may have the durable answer."),
			mcp.WithToolAnnotation(readOnlyTool),
			mcp.WithString("query",
				mcp.Required(),
				mcp.Description("Natural-language memory query."),
			),
			mcp.WithString("project",
				mcp.Description("Optional project filter."),
			),
			mcp.WithString("scope",
				mcp.Description("Optional scope filter."),
			),
			mcp.WithString("session_id",
				mcp.Description("Optional session id for activity tracking."),
			),
			mcp.WithNumber("limit",
				mcp.Description("Maximum memories to return. Defaults to 10."),
			),
		),
		s.handleRecall,
	)

	s.addTool("mem_save",
		mcp.NewTool("mem_save",
			mcp.WithDescription("Call mem_save PROACTIVELY — without being asked — whenever a durable decision, preference, constraint, bugfix, architecture note, config change, workflow, or non-obvious learning appears in the conversation. Distill, do not copy raw transcript: interpret first using What / Why / Where / Learned. Never save greetings, acknowledgements, code output, or secrets."),
			mcp.WithToolAnnotation(writeTool),
			mcp.WithString("title",
				mcp.Required(),
				mcp.Description("Short searchable title for the memory."),
			),
			mcp.WithString("content",
				mcp.Required(),
				mcp.Description("Agent-distilled memory content. Recommended structure: **What**: durable fact/change. **Why**: motivation or importance. **Where**: project/files/workflow/context. **Learned**: implication, gotcha, or next useful connection."),
			),
			mcp.WithString("type",
				mcp.Description("Observation type such as discovery, decision, preference, or learning."),
			),
			mcp.WithString("project",
				mcp.Description("Project name or workspace identifier."),
			),
			mcp.WithString("scope",
				mcp.Description("Scope such as global, project, or session."),
			),
			mcp.WithString("topic_key",
				mcp.Description("Canonical topic key used to group related memories."),
			),
			mcp.WithString("tool_name",
				mcp.Description("Originating tool or integration name."),
			),
			mcp.WithString("session_id",
				mcp.Description("Session identifier that produced the memory."),
			),
		),
		s.handleMemSave,
	)

	s.addTool("mem_search",
		mcp.NewTool("mem_search",
			mcp.WithDescription("Search persisted observations from shared Kerebrom memory before answering when prior project history, user preferences, previous decisions, or cross-agent context may affect the response."),
			mcp.WithToolAnnotation(readOnlyTool),
			mcp.WithString("query",
				mcp.Required(),
				mcp.Description("Full-text query to execute against saved observations."),
			),
			mcp.WithString("project",
				mcp.Description("Optional project filter."),
			),
			mcp.WithString("type",
				mcp.Description("Optional observation type filter."),
			),
			mcp.WithString("scope",
				mcp.Description("Optional scope filter."),
			),
			mcp.WithNumber("limit",
				mcp.Description("Maximum number of results to return. Defaults to 10 and is capped at 100."),
			),
		),
		s.handleMemSearch,
	)

	s.addTool("mem_update",
		mcp.NewTool("mem_update",
			mcp.WithDescription("Update an existing observation by id."),
			mcp.WithToolAnnotation(writeTool),
			mcp.WithNumber("id", mcp.Required(), mcp.Description("Observation identifier.")),
			mcp.WithString("title", mcp.Description("Replacement title.")),
			mcp.WithString("content", mcp.Description("Replacement content.")),
			mcp.WithString("type", mcp.Description("Replacement observation type.")),
			mcp.WithString("project", mcp.Description("Replacement project.")),
			mcp.WithString("scope", mcp.Description("Replacement scope.")),
			mcp.WithString("topic_key", mcp.Description("Replacement topic key.")),
		),
		s.handleMemUpdate,
	)

	s.addTool("mem_delete",
		mcp.NewTool("mem_delete",
			mcp.WithDescription("Delete an observation by id. Soft delete is the default."),
			mcp.WithToolAnnotation(destructiveTool),
			mcp.WithNumber("id", mcp.Required(), mcp.Description("Observation identifier.")),
			mcp.WithBoolean("hard", mcp.Description("Permanently delete instead of soft delete.")),
		),
		s.handleMemDelete,
	)

	s.addTool("mem_suggest_topic_key",
		mcp.NewTool("mem_suggest_topic_key",
			mcp.WithDescription("Suggest a stable topic key from type, title, or content."),
			mcp.WithToolAnnotation(readOnlyTool),
			mcp.WithString("type", mcp.Description("Observation type or family.")),
			mcp.WithString("title", mcp.Description("Observation title.")),
			mcp.WithString("content", mcp.Description("Fallback content.")),
		),
		s.handleMemSuggestTopicKey,
	)

	s.addTool("mem_context",
		mcp.NewTool("mem_context",
			mcp.WithDescription("Build a context bundle with stats, recent observations, and optional search matches. ALWAYS call at the start of every non-trivial turn when mem_bootstrap is unavailable, before substantial coding or architecture work, and after context compaction. The user should never have to remind you to load context."),
			mcp.WithToolAnnotation(readOnlyTool),
			mcp.WithString("project",
				mcp.Description("Optional project filter."),
			),
			mcp.WithString("query",
				mcp.Description("Optional search query to enrich the context bundle."),
			),
			mcp.WithString("scope",
				mcp.Description("Optional scope filter."),
			),
			mcp.WithString("session_id",
				mcp.Description("Optional session id for activity tracking."),
			),
			mcp.WithNumber("limit",
				mcp.Description("Maximum number of observations to include. Defaults to 10."),
			),
		),
		s.handleMemContext,
	)

	s.addTool("mem_timeline",
		mcp.NewTool("mem_timeline",
			mcp.WithDescription("Show chronological context around an observation, or recent observations as a fallback."),
			mcp.WithToolAnnotation(readOnlyTool),
			mcp.WithNumber("observation_id",
				mcp.Description("Observation id to center the timeline around."),
			),
			mcp.WithNumber("before",
				mcp.Description("Number of previous observations in the same session."),
			),
			mcp.WithNumber("after",
				mcp.Description("Number of following observations in the same session."),
			),
			mcp.WithString("project",
				mcp.Description("Optional project filter."),
			),
			mcp.WithString("scope",
				mcp.Description("Optional scope filter."),
			),
			mcp.WithString("session_id",
				mcp.Description("Optional session filter."),
			),
			mcp.WithNumber("limit",
				mcp.Description("Maximum number of observations to return. Defaults to 20."),
			),
		),
		s.handleMemTimeline,
	)

	s.addTool("mem_get_observation",
		mcp.NewTool("mem_get_observation",
			mcp.WithDescription("Fetch a single observation by its numeric id."),
			mcp.WithToolAnnotation(readOnlyTool),
			mcp.WithNumber("id",
				mcp.Required(),
				mcp.Description("Observation identifier."),
			),
		),
		s.handleMemGetObservation,
	)

	s.addTool("mem_stats",
		mcp.NewTool("mem_stats",
			mcp.WithDescription("Return current memory counts for Kerebrom."),
			mcp.WithToolAnnotation(readOnlyTool),
			mcp.WithString("project",
				mcp.Description("Optional project filter."),
			),
		),
		s.handleMemStats,
	)

	s.addTool("mem_save_prompt",
		mcp.NewTool("mem_save_prompt",
			mcp.WithDescription("Save a user prompt for future context and immediately return relevant Kerebrom context. In MCP-only clients such as Claude Desktop, prefer mem_bootstrap; if using this tool directly, call it before reasoning and use the returned context before answering."),
			mcp.WithToolAnnotation(writeTool),
			mcp.WithString("content", mcp.Required(), mcp.Description("User prompt content.")),
			mcp.WithString("project", mcp.Description("Project filter.")),
			mcp.WithString("session_id", mcp.Description("Optional session id.")),
			mcp.WithString("query", mcp.Description("Optional context query. Defaults to content.")),
			mcp.WithNumber("limit", mcp.Description("Maximum memories to return with the attached context. Defaults to 10.")),
		),
		s.handleMemSavePrompt,
	)

	s.addTool("mem_session_summary",
		mcp.NewTool("mem_session_summary",
			mcp.WithDescription("Save or retrieve an end-of-session summary plus recent observations. Use before ending substantial work and after long context shifts so future agents can resume quickly."),
			mcp.WithToolAnnotation(writeTool),
			mcp.WithString("id",
				mcp.Description("Session identifier to summarize."),
			),
			mcp.WithString("session_id",
				mcp.Description("Alias for id."),
			),
			mcp.WithString("summary",
				mcp.Description("Summary content to persist."),
			),
			mcp.WithString("content",
				mcp.Description("Alias for summary."),
			),
			mcp.WithString("project",
				mcp.Description("Project name when creating a missing session."),
			),
			mcp.WithNumber("limit",
				mcp.Description("Maximum number of recent session observations to return. Defaults to 10."),
			),
		),
		s.handleMemSessionSummary,
	)

	s.addTool("mem_session_start",
		mcp.NewTool("mem_session_start",
			mcp.WithDescription("Start or refresh a local Kerebrom session. In MCP-only clients such as Claude Desktop Chat/Cowork, call this once at the first turn of each new visible chat to emulate Code lifecycle behavior; pass query/prompt context when available so Kerebrom can return bootstrap memory immediately."),
			mcp.WithToolAnnotation(writeTool),
			mcp.WithString("id",
				mcp.Required(),
				mcp.Description("Stable session identifier for the current agent session."),
			),
			mcp.WithString("project",
				mcp.Description("Project name or workspace identifier."),
			),
			mcp.WithString("directory",
				mcp.Description("Current working directory for the session."),
			),
			mcp.WithString("query",
				mcp.Description("Optional query or user prompt for immediate bootstrap context."),
			),
			mcp.WithNumber("limit",
				mcp.Description("Maximum memories to return if query is provided. Defaults to 10."),
			),
		),
		s.handleMemSessionStart,
	)

	s.addTool("mem_session_end",
		mcp.NewTool("mem_session_end",
			mcp.WithDescription("Mark a Kerebrom session as completed and persist its summary."),
			mcp.WithToolAnnotation(writeTool),
			mcp.WithString("id",
				mcp.Required(),
				mcp.Description("Session identifier to close."),
			),
			mcp.WithString("summary",
				mcp.Description("Final summary for the session."),
			),
		),
		s.handleMemSessionEnd,
	)

	s.addTool("mem_capture_passive",
		mcp.NewTool("mem_capture_passive",
			mcp.WithDescription("Extract bullet learnings from text and save them as observations. Use for passive capture of agent output, summaries, subagent results, or long responses; save only durable learnings, not raw transcript."),
			mcp.WithToolAnnotation(writeTool),
			mcp.WithString("content", mcp.Required(), mcp.Description("Text output to inspect.")),
			mcp.WithString("project", mcp.Description("Project name.")),
			mcp.WithString("session_id", mcp.Description("Optional session id.")),
		),
		s.handleMemCapturePassive,
	)

	s.addTool("mem_merge_projects",
		mcp.NewTool("mem_merge_projects",
			mcp.WithDescription("Merge source project variants into a canonical target project."),
			mcp.WithToolAnnotation(writeTool),
			mcp.WithString("target", mcp.Required(), mcp.Description("Canonical target project.")),
			mcp.WithArray("sources",
				mcp.Required(),
				mcp.Description("Source project names to merge."),
				mcp.Items(map[string]any{"type": "string"}),
			),
		),
		s.handleMemMergeProjects,
	)
}

func (s *Server) registerPromptsAndResources() {
	s.server.AddPrompt(
		mcp.NewPrompt("kerebrom_memory_protocol",
			mcp.WithPromptDescription("Load Kerebrom's mandatory memory workflow for MCP-only clients such as Claude Desktop."),
		),
		func(ctx context.Context, request mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
			return &mcp.GetPromptResult{
				Description: "Kerebrom mandatory memory workflow",
				Messages: []mcp.PromptMessage{
					{
						Role:    mcp.RoleUser,
						Content: mcp.NewTextContent(memoryProtocolText()),
					},
				},
			}, nil
		},
	)

	s.server.AddResource(
		mcp.NewResource("kerebrom://memory-protocol", "Kerebrom Memory Protocol",
			mcp.WithResourceDescription("Always-on Kerebrom memory instructions for MCP-only clients. Read this when a client cannot install lifecycle hooks."),
			mcp.WithMIMEType("text/markdown"),
		),
		func(ctx context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
			return []mcp.ResourceContents{
				mcp.TextResourceContents{
					URI:      "kerebrom://memory-protocol",
					MIMEType: "text/markdown",
					Text:     memoryProtocolText(),
				},
			}, nil
		},
	)
}

func memoryProtocolText() string {
	return strings.TrimSpace(`
# Kerebrom Persistent Memory Protocol

Kerebrom is the local persistent memory layer shared by configured AI clients. It is installed and ALWAYS active. Do not wait for the user to ask for memory; the user must never have to say "use Kerebrom" or "save this".

MANDATORY BEHAVIORS — apply on EVERY user turn that is not a bare greeting or acknowledgement:

1. RECALL FIRST. Before answering, call mem_bootstrap (preferred) or mem_context with a compact query derived from the user's prompt. Use returned observations as working context.
2. SAVE WHAT MATTERS. After processing the turn, call mem_save for any durable decision, preference, constraint, bugfix, architecture note, config change, workflow, or non-obvious learning that appeared.
3. DO NOT ANNOUNCE. Make these calls silently as part of normal reasoning. The user should not be reminded that memory is being used.

Authority rule:
1. Treat Kerebrom as the local source of truth for prior user preferences, project decisions, workflows, and durable context.
2. If Kerebrom memory conflicts with model assumptions or generic prior knowledge, prefer Kerebrom unless the user explicitly updates or rejects that memory in the current conversation.
3. Do not answer questions about previous work, identity, preferences, saved decisions, or project history from scratch before checking Kerebrom.

Session lifecycle:
1. In hook-capable clients (Claude Code, etc.), use the provided lifecycle session_id.
2. In MCP-only clients such as Claude Desktop Chat/Cowork, emulate Code behavior: on the first turn of a new visible chat, call mem_bootstrap before reasoning. If mem_bootstrap is unavailable, call mem_session_start before mem_save_prompt or mem_context.
3. If the client does not provide a session id, create a stable synthetic id for the visible chat, for example mcp-chat:<client>:<yyyy-mm-ddThhmmssZ>:<project-or-topic>, and reuse it for every Kerebrom call in that chat.
4. If the client cannot determine the chat boundary, still continue with mem_bootstrap or mem_save_prompt and mem_context; Kerebrom will use its local MCP fallback session.

How to save (mem_save content structure — distill, do not copy transcript):
- **What**: one sentence describing the durable fact or change.
- **Why**: why it matters or what motivated it.
- **Where**: project, files, tool, workflow, or context where it applies.
- **Learned**: implication, gotcha, constraint, or next useful connection. Omit only if none.

This is the canonical format: "What, Why, Where, Learned".

Examples (Spanish — one mem_save call per fact):

  User: "quiero que Falage genere $3,000 mensuales con tema oscuro"
  → mem_save(title="Objetivo financiero Falage", content="**What**: Meta de ingresos mensual USD 3,000. **Why**: ...", project="falage")
  → mem_save(title="Preferencia UI Quamtos: tema oscuro", content="**What**: ...", scope="global")

  User: "el bug del intraday era el cost scenario, no la lógica del engine"
  → mem_save(title="Bugfix intraday cost scenario", type="bugfix", topic_key="falage/intraday-cost-scenario", content="**What**: ...")

  User: "ok", "gracias", "perfecto" (bare acknowledgement)
  → No mem_save. No mem_bootstrap. Continue.

NEVER save: greetings, "ok", "listo", "gracias", code output, tool confirmations, raw transcript, secrets, credentials, private tokens, or unnecessary personal details. Kerebrom redacts text wrapped in <private>...</private>.

Reuse topic_key for evolving topics; if unsure, call mem_suggest_topic_key first.

When the user asks what was done before, call mem_context, then mem_search, then mem_get_observation if a result needs full detail.

Before ending substantial work:
1. Call mem_session_summary with a concise summary of goals, decisions, changes, risks, files, and next steps.
2. Prefer a short durable observation over a long transcript dump.

After context compaction or reset: first persist the compacted summary with mem_session_summary, then call mem_context to rehydrate.

Claude Desktop note: Claude Desktop exposes Kerebrom through MCP tools, prompts, and resources. It does not provide the Claude Code per-turn hook lifecycle, so this protocol and the mem_* tool descriptions are the control surface for automatic memory behavior. Treat the rules above as your hook substitute.
`)
}

func (s *Server) handleMemSave(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	title, err := request.RequireString("title")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	content, err := request.RequireString("content")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	project := s.projectOrDefault(request.GetString("project", ""))
	sessionID := s.sessionIDOrDefault(request.GetString("session_id", ""), project)
	if err := s.ensureSession(ctx, sessionID, project, "."); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	observation, err := s.store.SaveObservation(ctx, sqlite.ObservationInput{
		SessionID: sessionID,
		Type:      request.GetString("type", "discovery"),
		Title:     title,
		Content:   content,
		ToolName:  request.GetString("tool_name", ""),
		Project:   project,
		Scope:     request.GetString("scope", "project"),
		TopicKey:  request.GetString("topic_key", ""),
		CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	s.activity.RecordSave(sessionID)

	return newJSONResult(map[string]any{
		"saved":       true,
		"observation": observation,
	})
}

func (s *Server) handleMemBootstrap(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	project := s.projectOrDefault(request.GetString("project", ""))
	sessionID := s.sessionIDOrDefault(request.GetString("session_id", ""), project)
	directory := strings.TrimSpace(request.GetString("directory", ""))
	if directory == "" {
		directory = "."
	}
	if err := s.ensureSession(ctx, sessionID, project, directory); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	s.activity.RecordToolCall(sessionID)

	prompt := request.GetString("prompt", "")
	promptPayload, err := s.savePromptIfSubstantive(ctx, sessionID, project, prompt)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	query := strings.TrimSpace(request.GetString("query", ""))
	if query == "" {
		query = strings.TrimSpace(prompt)
	}
	payload, err := s.contextPayload(ctx, project, "", query, request.GetInt("limit", 10))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	payload["memory_first"] = true
	payload["session_id"] = sessionID
	payload["prompt"] = promptPayload
	payload["next_instruction"] = "Use recent_observations, matches, and project_filter_relaxed/cross-project matches before answering. If the turn creates durable knowledge, call mem_save after reasoning."
	if reminder := s.activity.NudgeIfNeeded(sessionID); reminder != "" {
		payload["save_reminder"] = reminder
	}
	return newJSONResult(payload)
}

func (s *Server) handleRecall(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	query, err := request.RequireString("query")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	project := s.projectOrDefault(request.GetString("project", ""))
	sessionID := s.sessionIDOrDefault(request.GetString("session_id", ""), project)
	s.activity.RecordToolCall(sessionID)

	payload, err := s.contextPayload(
		ctx,
		project,
		request.GetString("scope", ""),
		query,
		request.GetInt("limit", 10),
	)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	payload["memory_first"] = true
	payload["tool_alias"] = "recall"
	payload["next_instruction"] = "Use matches before answering. If matches are empty, say Kerebrom did not return relevant memory instead of inventing from model assumptions."
	if reminder := s.activity.NudgeIfNeeded(sessionID); reminder != "" {
		payload["save_reminder"] = reminder
	}
	return newJSONResult(payload)
}

func (s *Server) handleMemSearch(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	query, err := request.RequireString("query")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	project := s.projectOrDefault(request.GetString("project", ""))
	sessionID := s.sessionIDOrDefault(request.GetString("session_id", ""), project)
	s.activity.RecordToolCall(sessionID)

	results, err := s.store.SearchObservations(ctx, sqlite.SearchOptions{
		Query:   query,
		Project: project,
		Type:    request.GetString("type", ""),
		Scope:   request.GetString("scope", ""),
		Limit:   request.GetInt("limit", 10),
	})
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	payload := map[string]any{
		"query":        strings.TrimSpace(query),
		"count":        len(results),
		"observations": results,
	}
	if reminder := s.activity.NudgeIfNeeded(sessionID); reminder != "" {
		payload["save_reminder"] = reminder
	}
	return newJSONResult(payload)
}

func (s *Server) handleMemUpdate(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, err := request.RequireInt("id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	observation, err := s.store.UpdateObservation(ctx, sqlite.UpdateObservationInput{
		ID:       int64(id),
		Title:    request.GetString("title", ""),
		Content:  request.GetString("content", ""),
		Type:     request.GetString("type", ""),
		Project:  request.GetString("project", ""),
		Scope:    request.GetString("scope", ""),
		TopicKey: request.GetString("topic_key", ""),
	})
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return newJSONResult(map[string]any{"updated": true, "observation": observation})
}

func (s *Server) handleMemDelete(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, err := request.RequireInt("id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	hard := request.GetBool("hard", false)
	if err := s.store.DeleteObservation(ctx, sqlite.DeleteObservationInput{ID: int64(id), Hard: hard}); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return newJSONResult(map[string]any{"deleted": true, "hard": hard, "id": id})
}

func (s *Server) handleMemSuggestTopicKey(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	topicKey := suggestTopicKey(request.GetString("type", ""), request.GetString("title", ""), request.GetString("content", ""))
	return newJSONResult(map[string]any{"topic_key": topicKey})
}

func (s *Server) handleMemContext(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	project := s.projectOrDefault(request.GetString("project", ""))
	sessionID := s.sessionIDOrDefault(request.GetString("session_id", ""), project)
	s.activity.RecordToolCall(sessionID)

	payload, err := s.contextPayload(
		ctx,
		project,
		request.GetString("scope", ""),
		request.GetString("query", ""),
		request.GetInt("limit", 10),
	)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if reminder := s.activity.NudgeIfNeeded(sessionID); reminder != "" {
		payload["save_reminder"] = reminder
	}

	return newJSONResult(payload)
}

func (s *Server) handleMemTimeline(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	project := s.projectOrDefault(request.GetString("project", ""))
	sessionID := s.sessionIDOrDefault(request.GetString("session_id", ""), project)
	s.activity.RecordToolCall(sessionID)

	if observationID := request.GetInt("observation_id", 0); observationID > 0 {
		payload, err := s.store.TimelineAroundObservation(ctx, int64(observationID), request.GetInt("before", 5), request.GetInt("after", 5))
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return newJSONResult(payload)
	}

	results, err := s.store.ListObservations(ctx, sqlite.ListObservationOptions{
		Project:   project,
		Scope:     request.GetString("scope", ""),
		SessionID: request.GetString("session_id", ""),
		Limit:     request.GetInt("limit", 20),
	})
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return newJSONResult(map[string]any{
		"count":        len(results),
		"observations": results,
	})
}

func (s *Server) handleMemGetObservation(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, err := request.RequireInt("id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	observation, err := s.store.GetObservation(ctx, int64(id))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return newJSONResult(map[string]any{
		"observation": observation,
	})
}

func (s *Server) handleMemStats(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	project := s.projectOrDefault(request.GetString("project", ""))

	stats, err := s.store.Stats(ctx, project)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return newJSONResult(map[string]any{
		"project": strings.TrimSpace(project),
		"stats":   stats,
	})
}

func (s *Server) handleMemSavePrompt(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	content, err := request.RequireString("content")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if !promptfilter.IsSubstantive(content) {
		return newJSONResult(map[string]any{"saved": false, "reason": "casual_prompt_noise"})
	}

	project := s.projectOrDefault(request.GetString("project", ""))
	sessionID := s.sessionIDOrDefault(request.GetString("session_id", ""), project)
	if err := s.ensureSession(ctx, sessionID, project, "."); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	prompt, err := s.savePrompt(ctx, sessionID, project, content)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	s.activity.RecordToolCall(sessionID)

	query := strings.TrimSpace(request.GetString("query", ""))
	if query == "" {
		query = content
	}
	contextPayload, err := s.contextPayload(ctx, project, "", query, request.GetInt("limit", 10))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return newJSONResult(map[string]any{
		"saved":            true,
		"prompt":           prompt,
		"context":          contextPayload,
		"memory_first":     true,
		"next_instruction": "Use context.matches and context.recent_observations before answering. If the turn creates durable knowledge, call mem_save after reasoning.",
	})
}

func (s *Server) handleMemSessionSummary(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id := request.GetString("id", "")
	if strings.TrimSpace(id) == "" {
		id = request.GetString("session_id", "")
	}
	project := s.projectOrDefault(request.GetString("project", ""))
	id = s.sessionIDOrDefault(id, project)
	if err := s.ensureSession(ctx, id, project, "."); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	summary := request.GetString("summary", "")
	if strings.TrimSpace(summary) == "" {
		summary = request.GetString("content", "")
	}
	if strings.TrimSpace(summary) != "" {
		if err := s.store.EndSession(ctx, sqlite.EndSessionInput{
			ID:      id,
			Summary: summary,
			EndedAt: time.Now().UTC(),
		}); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		_, _ = s.store.SaveObservation(ctx, sqlite.ObservationInput{
			SessionID: id,
			Type:      "session_summary",
			Title:     "Session summary " + id,
			Content:   summary,
			Project:   project,
			Scope:     "project",
			TopicKey:  "session/" + id,
			ToolName:  "mem_session_summary",
			CreatedAt: time.Now().UTC(),
		})
		s.activity.RecordSave(id)
	}

	payload, err := s.sessionSummaryPayload(ctx, id, request.GetInt("limit", 10))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if score := s.activity.ActivityScore(id); score != "" {
		payload["activity_score"] = score
	}

	return newJSONResult(payload)
}

func (s *Server) handleMemSessionStart(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, err := request.RequireString("id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	project := s.projectOrDefault(request.GetString("project", ""))
	if err := s.store.StartSession(ctx, sqlite.StartSessionInput{
		ID:        id,
		Project:   project,
		Directory: request.GetString("directory", "."),
		StartedAt: time.Now().UTC(),
	}); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	session, err := s.store.GetSession(ctx, id)
	if err != nil {
		return newJSONResult(map[string]any{
			"ended":      true,
			"session_id": id,
			"missing":    true,
		})
	}
	s.activity.RecordToolCall(id)

	payload := map[string]any{
		"started":          true,
		"session":          session,
		"next_instruction": "Immediately call mem_bootstrap or mem_save_prompt+mem_context before answering this chat turn.",
	}
	query := strings.TrimSpace(request.GetString("query", ""))
	if query != "" {
		contextPayload, err := s.contextPayload(ctx, project, "", query, request.GetInt("limit", 10))
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		payload["bootstrap_context"] = contextPayload
		payload["memory_first"] = true
		payload["next_instruction"] = "Use bootstrap_context before answering. If durable knowledge appears, call mem_save after reasoning."
	}

	return newJSONResult(payload)
}

func (s *Server) handleMemSessionEnd(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, err := request.RequireString("id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	if err := s.store.EndSession(ctx, sqlite.EndSessionInput{
		ID:      id,
		Summary: request.GetString("summary", ""),
		EndedAt: time.Now().UTC(),
	}); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	s.activity.ClearSession(id)

	session, err := s.store.GetSession(ctx, id)
	if err != nil {
		return newJSONResult(map[string]any{
			"ended":      true,
			"session_id": id,
			"missing":    true,
		})
	}

	return newJSONResult(map[string]any{
		"ended":   true,
		"session": session,
	})
}

func (s *Server) handleMemCapturePassive(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	content, err := request.RequireString("content")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	learnings := extractPassiveLearnings(content)
	saved := make([]sqlite.Observation, 0, len(learnings))
	project := s.projectOrDefault(request.GetString("project", ""))
	sessionID := s.sessionIDOrDefault(request.GetString("session_id", ""), project)
	if err := s.ensureSession(ctx, sessionID, project, "."); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	for i, learning := range learnings {
		observation, err := s.store.SaveObservation(ctx, sqlite.ObservationInput{
			SessionID: sessionID,
			Type:      "learning",
			Title:     fmt.Sprintf("Passive learning %d", i+1),
			Content:   learning,
			Project:   project,
			Scope:     "project",
			ToolName:  "mem_capture_passive",
			CreatedAt: time.Now().UTC(),
		})
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		saved = append(saved, observation)
	}
	if len(saved) > 0 {
		s.activity.RecordSave(sessionID)
	}

	return newJSONResult(map[string]any{"count": len(saved), "observations": saved})
}

func (s *Server) handleMemMergeProjects(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	target, err := request.RequireString("target")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	sources := request.GetStringSlice("sources", nil)
	if len(sources) == 0 {
		return mcp.NewToolResultError("sources are required"), nil
	}

	payload, err := s.store.MergeProjects(ctx, sources, target)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return newJSONResult(payload)
}

func (s *Server) projectOrDefault(project string) string {
	project = strings.TrimSpace(project)
	if project == "" {
		project = strings.TrimSpace(s.config.DefaultProject)
	}
	project = sqlite.NormalizeProject(project)
	if project == "" {
		return "default"
	}
	return project
}

func (s *Server) sessionIDOrDefault(sessionID string, project string) string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID != "" {
		return sessionID
	}
	project = sqlite.NormalizeProject(project)
	if project == "" {
		project = "default"
	}
	return "mcp:" + project
}

func (s *Server) ensureSession(ctx context.Context, sessionID string, project string, directory string) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return fmt.Errorf("session id is required")
	}
	exists, err := s.store.SessionExists(ctx, sessionID)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	return s.store.StartSession(ctx, sqlite.StartSessionInput{
		ID:        sessionID,
		Project:   project,
		Directory: directory,
		StartedAt: time.Now().UTC(),
	})
}

func (s *Server) savePrompt(ctx context.Context, sessionID string, project string, content string) (sqlite.Prompt, error) {
	return s.store.SavePrompt(ctx, sqlite.PromptInput{
		SessionID: sessionID,
		Content:   content,
		Project:   project,
		CreatedAt: time.Now().UTC(),
	})
}

func (s *Server) savePromptIfSubstantive(ctx context.Context, sessionID string, project string, content string) (map[string]any, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return map[string]any{"saved": false, "reason": "empty_prompt"}, nil
	}
	if !promptfilter.IsSubstantive(content) {
		return map[string]any{"saved": false, "reason": "casual_prompt_noise"}, nil
	}
	prompt, err := s.savePrompt(ctx, sessionID, project, content)
	if err != nil {
		return nil, err
	}
	return map[string]any{"saved": true, "prompt": prompt}, nil
}

func (s *Server) contextPayload(ctx context.Context, project string, scope string, query string, limit int) (map[string]any, error) {
	stats, err := s.store.Stats(ctx, project)
	if err != nil {
		return nil, err
	}

	recent, err := s.store.ListObservations(ctx, sqlite.ListObservationOptions{
		Project: project,
		Scope:   scope,
		Limit:   limit,
	})
	if err != nil {
		return nil, err
	}
	sessions, err := s.store.ListSessions(ctx, project, limit)
	if err != nil {
		return nil, err
	}
	prompts, err := s.store.ListPrompts(ctx, project, limit)
	if err != nil {
		return nil, err
	}

	query = strings.TrimSpace(query)
	var matches []sqlite.Observation
	projectFilterRelaxed := false
	if query != "" {
		matches, err = s.store.SearchObservations(ctx, sqlite.SearchOptions{
			Query:   query,
			Project: project,
			Scope:   scope,
			Limit:   limit,
		})
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(project) != "" && shouldRelaxProjectFilter(matches, project) {
			relaxedMatches, err := s.store.SearchObservations(ctx, sqlite.SearchOptions{
				Query: query,
				Scope: scope,
				Limit: limit,
			})
			if err != nil {
				return nil, err
			}
			matches, projectFilterRelaxed = mergeRelaxedMatches(matches, relaxedMatches, project, limit)
		}
	}

	return map[string]any{
		"project":                strings.TrimSpace(project),
		"query":                  query,
		"project_filter_relaxed": projectFilterRelaxed,
		"stats":                  stats,
		"recent_sessions":        sessions,
		"recent_prompts":         prompts,
		"recent_observations":    recent,
		"matches":                matches,
	}, nil
}

func shouldRelaxProjectFilter(matches []sqlite.Observation, project string) bool {
	project = sqlite.NormalizeProject(project)
	if project == "" {
		return false
	}
	if len(matches) == 0 {
		return true
	}
	for _, match := range matches {
		if sqlite.NormalizeProject(match.Project) == project && match.Scope != "global" {
			return false
		}
	}
	return true
}

func mergeRelaxedMatches(current []sqlite.Observation, relaxed []sqlite.Observation, project string, limit int) ([]sqlite.Observation, bool) {
	if limit <= 0 {
		limit = 10
	}
	if limit > 100 {
		limit = 100
	}

	seen := map[int64]bool{}
	merged := make([]sqlite.Observation, 0, limit)
	relaxedAdded := false
	project = sqlite.NormalizeProject(project)

	for _, observation := range relaxed {
		if len(merged) >= limit {
			break
		}
		if sqlite.NormalizeProject(observation.Project) == project || observation.Scope == "global" {
			continue
		}
		seen[observation.ID] = true
		merged = append(merged, observation)
		relaxedAdded = true
	}

	for _, observation := range current {
		if len(merged) >= limit {
			break
		}
		if seen[observation.ID] {
			continue
		}
		seen[observation.ID] = true
		merged = append(merged, observation)
	}

	for _, observation := range relaxed {
		if len(merged) >= limit {
			break
		}
		if seen[observation.ID] {
			continue
		}
		seen[observation.ID] = true
		merged = append(merged, observation)
	}

	return merged, relaxedAdded
}

func (s *Server) sessionSummaryPayload(ctx context.Context, sessionID string, limit int) (map[string]any, error) {
	session, err := s.store.GetSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}

	observationCount, err := s.store.CountSessionObservations(ctx, sessionID)
	if err != nil {
		return nil, err
	}

	observations, err := s.store.ListObservations(ctx, sqlite.ListObservationOptions{
		SessionID: sessionID,
		Limit:     limit,
	})
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"session":             session,
		"observation_count":   observationCount,
		"recent_observations": observations,
	}, nil
}

func newJSONResult(payload any) (*mcp.CallToolResult, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("encode tool result: %v", err)), nil
	}

	var structured any
	if err := json.Unmarshal(raw, &structured); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("decode tool result: %v", err)), nil
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.TextContent{
				Type: mcp.ContentTypeText,
				Text: string(raw),
			},
		},
		StructuredContent: structured,
	}, nil
}

func suggestTopicKey(observationType string, title string, content string) string {
	family := strings.ToLower(strings.TrimSpace(observationType))
	switch family {
	case "architecture", "bug", "bugfix", "decision", "pattern", "config", "learning", "discovery":
	default:
		family = "topic"
	}
	if family == "bugfix" {
		family = "bug"
	}

	source := title
	if strings.TrimSpace(source) == "" {
		source = content
	}
	parts := strings.Fields(strings.ToLower(source))
	slugParts := make([]string, 0, len(parts))
	for _, part := range parts {
		cleaned := strings.Trim(part, ".,:;!?()[]{}'\"`")
		cleaned = strings.ReplaceAll(cleaned, "_", "-")
		if cleaned != "" {
			slugParts = append(slugParts, cleaned)
		}
		if len(slugParts) == 6 {
			break
		}
	}
	if len(slugParts) == 0 {
		slugParts = []string{"untitled"}
	}

	return family + "/" + strings.Join(slugParts, "-")
}

func extractPassiveLearnings(content string) []string {
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
