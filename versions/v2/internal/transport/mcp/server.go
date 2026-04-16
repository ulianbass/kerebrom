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

// SemanticAgentTools is the everyday surface every agent sees by default.
// Six semantic tools the model invokes from the conversation rhythm:
// context (load), recall (search), remember (save), summary (close),
// forget (invalidate), timeline (navigate). Exported so other packages
// (setup, docs generators) reuse the same list as the source of truth.
var SemanticAgentTools = []string{
	"context",
	"recall",
	"remember",
	"summary",
	"forget",
	"timeline",
}

// SemanticAdminTools are tools restricted to the explicit admin profile.
// Currently only the project consolidation tool, which is rarely needed.
var SemanticAdminTools = []string{
	"projects",
}

var profileAgentTools = stringSliceToSet(SemanticAgentTools)
var profileAdminTools = stringSliceToSet(SemanticAdminTools)

func stringSliceToSet(items []string) map[string]bool {
	out := make(map[string]bool, len(items))
	for _, item := range items {
		out[item] = true
	}
	return out
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
	return fmt.Sprintf("Kerebrom memory reminder: no remember call for this MCP session in %d minutes. If the work produced durable decisions, bugfixes, constraints, or discoveries, save a distilled memory now.", minutes)
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

// registerTools exposes the seven semantic tools that compose the
// Kerebrom v2 cycle. Names are deliberately verbs without prefixes so the
// model can pick the right tool from the name alone, the way Engram and
// Pieces do — without reading long descriptions every turn.
//
//	context   load relevant memory at the start of any conversation
//	recall    search what is already known about a topic
//	remember  save a durable fact, decision, preference, or learning
//	summary   close substantial work with goals/decisions/changes/risks
//	forget    invalidate an obsolete observation
//	timeline  inspect history, recent observations, or stats
//	projects  administrative consolidation across project names
func (s *Server) registerTools() {
	s.addTool("context",
		mcp.NewTool("context",
			mcp.WithDescription("ALWAYS call context at the start of any non-trivial conversation. It opens or resumes the Kerebrom session, saves the user's prompt when substantive, and returns prior observations relevant to the current turn. The user must never have to remind you to load memory."),
			mcp.WithToolAnnotation(writeTool),
			mcp.WithString("prompt",
				mcp.Description("Current user prompt. Kerebrom saves it as prompt history when substantive."),
			),
			mcp.WithString("query",
				mcp.Description("Compact search query derived from the user's prompt. Defaults to prompt when omitted."),
			),
			mcp.WithString("project",
				mcp.Description("Project name or workspace identifier. Leave empty if unknown; Kerebrom falls back to cross-project search."),
			),
			mcp.WithString("session_id",
				mcp.Description("Stable session id for this visible chat. Generate a synthetic one if the client does not provide it."),
			),
			mcp.WithString("directory",
				mcp.Description("Current working directory if known."),
			),
			mcp.WithNumber("limit",
				mcp.Description("Maximum observations to return. Defaults to 10."),
			),
		),
		s.handleContext,
	)

	s.addTool("recall",
		mcp.NewTool("recall",
			mcp.WithDescription("ALWAYS call recall before answering any question about the user, their projects, preferences, previous decisions, or history. Searches across projects if the current project is unknown or returns nothing. Do not answer from model assumptions when Kerebrom may have the durable answer."),
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
				mcp.Description("Maximum observations to return. Defaults to 10."),
			),
		),
		s.handleRecall,
	)

	s.addTool("remember",
		mcp.NewTool("remember",
			mcp.WithDescription("Call remember PROACTIVELY — without being asked — whenever a durable decision, preference, constraint, bugfix, architecture note, config change, workflow, or non-obvious learning appears in the conversation. Distill, do not copy raw transcript: interpret first using What / Why / Where / Learned. Never save greetings, acknowledgements, code output, or secrets."),
			mcp.WithToolAnnotation(writeTool),
			mcp.WithString("title",
				mcp.Required(),
				mcp.Description("Short searchable title for the observation."),
			),
			mcp.WithString("content",
				mcp.Required(),
				mcp.Description("Distilled content. Recommended structure: **What**: durable fact/change. **Why**: motivation or importance. **Where**: project/files/workflow/context. **Learned**: implication, gotcha, or next useful connection."),
			),
			mcp.WithString("type",
				mcp.Description("Observation type such as discovery, decision, preference, learning, or bugfix."),
			),
			mcp.WithString("project",
				mcp.Description("Project name or workspace identifier."),
			),
			mcp.WithString("scope",
				mcp.Description("Scope such as global, project, or session."),
			),
			mcp.WithString("topic_key",
				mcp.Description("Canonical topic key used to group related observations."),
			),
			mcp.WithString("tool_name",
				mcp.Description("Originating tool or integration name."),
			),
			mcp.WithString("session_id",
				mcp.Description("Session identifier that produced the observation."),
			),
		),
		s.handleRemember,
	)

	s.addTool("summary",
		mcp.NewTool("summary",
			mcp.WithDescription("Close substantial work with a concise summary of goals, decisions, changes, risks, files, and next steps. Call before ending a long working session, after major milestones, and after context compaction. Prefer a short durable observation over a transcript dump."),
			mcp.WithToolAnnotation(writeTool),
			mcp.WithString("session_id",
				mcp.Description("Session identifier to summarize."),
			),
			mcp.WithString("content",
				mcp.Description("Summary content to persist."),
			),
			mcp.WithString("project",
				mcp.Description("Project name when creating a missing session."),
			),
			mcp.WithNumber("limit",
				mcp.Description("Maximum recent observations to return alongside the summary. Defaults to 10."),
			),
		),
		s.handleSummary,
	)

	s.addTool("forget",
		mcp.NewTool("forget",
			mcp.WithDescription("Invalidate an obsolete or incorrect observation by id. Soft delete by default, recoverable. Use only when the user explicitly says something is wrong or should be removed."),
			mcp.WithToolAnnotation(destructiveTool),
			mcp.WithNumber("id", mcp.Required(), mcp.Description("Observation identifier.")),
			mcp.WithBoolean("hard", mcp.Description("Permanently delete instead of soft delete.")),
		),
		s.handleForget,
	)

	s.addTool("timeline",
		mcp.NewTool("timeline",
			mcp.WithDescription("Inspect chronological history: observations around a specific id, recent observations across a project, or memory stats. Useful when the user asks what was done before, when an idea was decided, or to navigate prior work."),
			mcp.WithToolAnnotation(readOnlyTool),
			mcp.WithNumber("observation_id",
				mcp.Description("Observation id to center the timeline around. If omitted, returns recent observations."),
			),
			mcp.WithNumber("before",
				mcp.Description("Number of preceding observations to include in the same session."),
			),
			mcp.WithNumber("after",
				mcp.Description("Number of following observations to include in the same session."),
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
				mcp.Description("Maximum observations to return. Defaults to 20."),
			),
		),
		s.handleTimeline,
	)

	s.addTool("projects",
		mcp.NewTool("projects",
			mcp.WithDescription("Administrative tool to consolidate project name variants into a canonical target. Use only when the user explicitly asks to merge or rename projects."),
			mcp.WithToolAnnotation(writeTool),
			mcp.WithString("target", mcp.Required(), mcp.Description("Canonical target project name.")),
			mcp.WithArray("sources",
				mcp.Required(),
				mcp.Description("Source project names to merge into the target."),
				mcp.Items(map[string]any{"type": "string"}),
			),
		),
		s.handleProjects,
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
# Kerebrom Memory

Kerebrom is your shared, persistent memory across this and every future
conversation. It is installed and always active. The user does not need
to remind you to use it. You decide when, based on the rhythm of the
work.

## The cycle

1. At the start of any non-trivial conversation, call **context** with
   the user's prompt. You will receive prior observations and a stable
   session handle. Use them as working knowledge before answering.

2. While working, when a durable fact appears (a decision, a
   preference, a constraint, a bugfix, a configuration change, a
   non-obvious learning), call **remember** with What/Why/Where/Learned.

3. When the user asks what you know about a topic, call **recall** with
   a natural-language query.

4. Before ending substantial work or after context compaction, call
   **summary** with goals, decisions, changes, risks, files, and next
   steps. Prefer one short durable observation over a transcript dump.

The other tools are specialized:
- **timeline** — inspect chronological history or recent observations.
- **forget** — invalidate an observation the user said is wrong.
- **projects** — administrative consolidation of project name variants.

## Authority rule

Kerebrom is the local source of truth for prior user preferences,
project decisions, workflows, and durable context. When memory
conflicts with model assumptions, prefer Kerebrom unless the user
explicitly updates or rejects that memory in the current conversation.
Do not answer questions about prior work, identity, or saved decisions
from scratch before checking memory.

## Session handle

If the client provides a lifecycle session id (Claude Code, hook-capable
clients), use it. Otherwise create a stable synthetic id for the visible
chat — for example "mcp-chat:claude:2026-04-16T1647Z:project-or-topic" —
and reuse it for every Kerebrom call in that chat.

## How to save (the What/Why/Where/Learned framework)

- **What**: one sentence describing the durable fact or change.
- **Why**: why it matters or what motivated it.
- **Where**: project, files, workflow, or context where it applies.
- **Learned**: implication, gotcha, constraint, or next useful
  connection. Omit only if none.

## Examples (Spanish)

  User: "Qué sabes de mí?"
  → context(query="usuario perfil preferencias proyectos")

  User: "Quiero que Falage genere $3,000 mensuales con tema oscuro"
  → remember(title="Objetivo financiero Falage 3K USD/mes", project="falage", content="**What**: ...")
  → remember(title="Preferencia UI Quamtos: tema oscuro", scope="global", content="**What**: ...")

  User: "El bug del intraday era el cost scenario, no el engine"
  → remember(title="Bugfix intraday cost scenario", type="bugfix",
             topic_key="falage/intraday-cost-scenario", content="**What**: ...")

  User: "ok", "gracias", "perfecto" (bare acknowledgement)
  → Nothing. Continue.

## Never

- Save greetings, acknowledgements ("ok", "listo", "gracias"), code
  output, tool confirmations, raw transcript, secrets, credentials,
  private tokens, or unnecessary personal details. Kerebrom redacts
  text wrapped in <private>...</private> automatically.
- Mention these tools to the user. Just use them.
- Wait for permission. Memory is on by default.

## Claude Desktop note

Claude Desktop exposes Kerebrom through MCP tools, prompts, and
resources. It does not provide the per-turn hook lifecycle that Claude
Code has, so this protocol and the seven tool descriptions are the
control surface for automatic memory behavior. Treat the rules above as
your hook substitute. New visible chat means call context first.
`)
}

func (s *Server) handleRemember(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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

func (s *Server) handleContext(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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
	payload["next_instruction"] = "Use recent_observations, matches, and project_filter_relaxed/cross-project matches before answering. If the turn creates durable knowledge, call remember after reasoning."
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

func (s *Server) handleForget(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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

func (s *Server) handleTimeline(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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

func (s *Server) handleSummary(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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
			ToolName:  "summary",
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

func (s *Server) handleProjects(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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

