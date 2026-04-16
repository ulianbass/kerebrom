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
	"mem_get_observation":   true,
	"mem_save_prompt":       true,
	"mem_session_summary":   true,
	"mem_session_start":     true,
	"mem_session_end":       true,
	"mem_suggest_topic_key": true,
	"mem_capture_passive":   true,
}

var profileAdminTools = map[string]bool{
	"mem_delete":         true,
	"mem_stats":          true,
	"mem_timeline":       true,
	"mem_merge_projects": true,
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
	s.addTool("mem_save",
		mcp.NewTool("mem_save",
			mcp.WithDescription("Persist an agent-distilled observation into Kerebrom's shared local memory whenever a durable decision, preference, bugfix, project fact, workflow, or lesson appears. Do not store raw transcript; interpret first using What / Why / Where / Learned."),
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
			mcp.WithNumber("id", mcp.Required(), mcp.Description("Observation identifier.")),
			mcp.WithBoolean("hard", mcp.Description("Permanently delete instead of soft delete.")),
		),
		s.handleMemDelete,
	)

	s.addTool("mem_suggest_topic_key",
		mcp.NewTool("mem_suggest_topic_key",
			mcp.WithDescription("Suggest a stable topic key from type, title, or content."),
			mcp.WithString("type", mcp.Description("Observation type or family.")),
			mcp.WithString("title", mcp.Description("Observation title.")),
			mcp.WithString("content", mcp.Description("Fallback content.")),
		),
		s.handleMemSuggestTopicKey,
	)

	s.addTool("mem_context",
		mcp.NewTool("mem_context",
			mcp.WithDescription("Build a context bundle with stats, recent observations, and optional search matches. Use at the start of every non-trivial turn, before substantial coding or architecture work, and after context compaction."),
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
			mcp.WithString("project",
				mcp.Description("Optional project filter."),
			),
		),
		s.handleMemStats,
	)

	s.addTool("mem_save_prompt",
		mcp.NewTool("mem_save_prompt",
			mcp.WithDescription("Save a user prompt for future context. In MCP-only clients such as Claude Desktop, call this at the start of each non-trivial user turn before reasoning, then call mem_context."),
			mcp.WithString("content", mcp.Required(), mcp.Description("User prompt content.")),
			mcp.WithString("project", mcp.Description("Project filter.")),
			mcp.WithString("session_id", mcp.Description("Optional session id.")),
		),
		s.handleMemSavePrompt,
	)

	s.addTool("mem_session_summary",
		mcp.NewTool("mem_session_summary",
			mcp.WithDescription("Save or retrieve an end-of-session summary plus recent observations. Use before ending substantial work and after long context shifts so future agents can resume quickly."),
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
			mcp.WithDescription("Start or refresh a local Kerebrom session."),
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
		),
		s.handleMemSessionStart,
	)

	s.addTool("mem_session_end",
		mcp.NewTool("mem_session_end",
			mcp.WithDescription("Mark a Kerebrom session as completed and persist its summary."),
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
			mcp.WithString("content", mcp.Required(), mcp.Description("Text output to inspect.")),
			mcp.WithString("project", mcp.Description("Project name.")),
			mcp.WithString("session_id", mcp.Description("Optional session id.")),
		),
		s.handleMemCapturePassive,
	)

	s.addTool("mem_merge_projects",
		mcp.NewTool("mem_merge_projects",
			mcp.WithDescription("Merge source project variants into a canonical target project."),
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

Kerebrom is the local persistent memory layer shared by configured AI clients. Use it proactively; do not wait for the user to ask for memory unless the turn is trivial.

Authority rule:
1. Treat Kerebrom as the local source of truth for prior user preferences, project decisions, workflows, and durable context.
2. If Kerebrom memory conflicts with model assumptions or generic prior knowledge, prefer Kerebrom unless the user explicitly updates or rejects that memory in the current conversation.
3. Do not answer questions about previous work, identity, preferences, saved decisions, or project history from scratch before checking Kerebrom.

For every non-trivial user turn:
1. Call mem_save_prompt with the user's prompt, project/workspace when known, and session_id when available.
2. Call mem_context with the current project and a compact query derived from the user's request.
3. Use returned observations as working context before answering or editing files.

During work:
1. Call mem_save when a durable decision, preference, bugfix, architecture fact, workflow, or project lesson appears.
2. Distill memories instead of copying raw transcript. Use this structure: What, Why, Where, Learned.
3. Do not store secrets, credentials, private tokens, or unnecessary personal details.

Before ending substantial work:
1. Call mem_session_summary with a concise summary of goals, decisions, changes, risks, files, and next steps.
2. Prefer a short durable observation over a long transcript dump.

Claude Desktop note: Claude Desktop exposes Kerebrom through MCP tools, prompts, and resources. It does not provide the Claude Code per-turn hook lifecycle, so this protocol and the mem_* tool descriptions are the control surface for automatic memory behavior.
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

	prompt, err := s.store.SavePrompt(ctx, sqlite.PromptInput{
		SessionID: sessionID,
		Content:   content,
		Project:   project,
		CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return newJSONResult(map[string]any{"saved": true, "prompt": prompt})
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

	return newJSONResult(map[string]any{
		"started": true,
		"session": session,
	})
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
	}

	return map[string]any{
		"project":             strings.TrimSpace(project),
		"query":               query,
		"stats":               stats,
		"recent_sessions":     sessions,
		"recent_prompts":      prompts,
		"recent_observations": recent,
		"matches":             matches,
	}, nil
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
