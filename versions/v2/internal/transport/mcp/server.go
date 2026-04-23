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
	"github.com/ulianbass/kerebrom/internal/contextgov"
	projectmeta "github.com/ulianbass/kerebrom/internal/project"
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

const recentNativeSessionTTL = 10 * time.Minute

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
			mcpserver.WithInstructions(memoryProtocolText(allowlist)),
			mcpserver.WithToolCapabilities(false),
			mcpserver.WithPromptCapabilities(false),
			mcpserver.WithResourceCapabilities(false, false),
			mcpserver.WithRecovery(),
			mcpserver.WithResourceRecovery(),
		),
	}

	srv.registerTools()
	// MCP prompts and resources are intentionally NOT registered. Doing
	// so signals to the model that the memory workflow is opt-in (a thing
	// the user "loads" or "reads"), which empirically caused Chat and
	// Cowork surfaces in Claude Desktop to never invoke the tools
	// proactively. The protocol text is delivered exclusively through the
	// MCP `instructions` field at handshake time and through the
	// per-tool descriptions, both of which the model treats as part of
	// its operating system rather than as user-invocable resources.
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
			mcp.WithDescription("Load Kerebrom memory. ALWAYS call BEFORE answering EVERY user message when this tool is available, including short or ambiguous prompts. Opens or resumes the session, saves only substantive prompts, and returns a context_governor plus prior observations ordered by semantic valid_at so newer corrections outrank stale facts."),
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
			mcp.WithDescription("Search Kerebrom memory. ALWAYS call BEFORE answering questions about the user, their projects, preferences, history, or prior decisions. Results include context_governor guidance and prioritize semantic valid_at; when memories conflict, prefer the latest corrected/validated observation."),
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
			mcp.WithDescription("Save a new memory. Call PROACTIVELY after decisions, preferences, bugfixes, constraints, configuration changes, or non-obvious learnings — do not wait to be asked. Distill with What / Why / Where / Learned. Never save greetings, transcripts, or secrets."),
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
				mcp.Description("Canonical topic key used to group related observations. Reuse the same topic_key for corrections so Kerebrom updates the canonical memory instead of preserving contradictions as equal facts."),
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
			mcp.WithDescription("Close substantial work. Call BEFORE ending a session, after major milestones, and after context compaction. Persist a short structured summary: goals, decisions, changes, risks, files, next steps."),
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
			mcp.WithDescription("Invalidate an obsolete observation by id. Soft delete by default. Use only when the user says something is wrong."),
			mcp.WithToolAnnotation(destructiveTool),
			mcp.WithNumber("id", mcp.Required(), mcp.Description("Observation identifier.")),
			mcp.WithBoolean("hard", mcp.Description("Permanently delete instead of soft delete.")),
		),
		s.handleForget,
	)

	s.addTool("timeline",
		mcp.NewTool("timeline",
			mcp.WithDescription("Inspect chronological history by semantic valid_at. Observations around a specific id, or recent observations across a project. Use when the user asks what was done before or whether a fact was later corrected."),
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
			mcp.WithDescription("Consolidate project name variants into a canonical target. Use only when the user explicitly asks to merge or rename projects."),
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

func memoryProtocolText(allowlist map[string]bool) string {
	return strings.TrimSpace(fmt.Sprintf(`
Kerebrom provides persistent memory that survives across sessions and is shared across all your AI tools. It is installed and active right now. Do not wait for the user to ask for it.

CORE TOOLS (always available — use without asking):
%s

SPECIALIZED TOOLS:
%s

MANDATORY BEHAVIORS — follow these on EVERY interaction:

1. ACTIVATE ON EVERY USER MESSAGE: Before answering ANY user message, call context immediately with a query derived from the prompt. This applies even when the prompt is short, ambiguous, casual, or looks like an acknowledgement. Use the returned observations as working knowledge before reasoning. If context returns no useful memory, continue normally.

2. SAVE ALWAYS: After EVERY user message that introduces a durable fact (a decision, a preference, a constraint, a bugfix, a configuration change, a non-obvious learning), call remember. Distill, do not transcribe.

3. RECALL ON DEMAND: When the user asks about a specific topic, call recall before answering.

4. SUMMARY AT CLOSE: Before ending substantial work, after context compaction, or when the user says "cerramos sesión", "sesión cerrada", "close this session", or equivalent, call summary with goals, decisions, changes, risks, files, next steps. Use the same session_id returned by context whenever the client exposes one. Do not save the bare close phrase itself as durable memory.

5. CHRONOLOGY RULE: Treat valid_at as the semantic timestamp of the memory. When two memories conflict, prefer the newest corrected/validated observation unless the user explicitly says an older memory is still authoritative. If you are saving a correction, reuse the same topic_key whenever possible so the old fact is updated instead of stored as an equal contradiction.

6. CONTEXT GOVERNOR: Every context/recall payload includes context_governor. Follow its sequence exactly: think -> search -> analyze -> answer. If it reports conflict_candidates, call timeline or recall again before making a claim that depends on those memories.

HOW TO SAVE — the What / Why / Where / Learned framework:

  **What**: one sentence describing the durable fact or change.
  **Why**: why it matters or what motivated it.
  **Where**: project, files, workflow, or context where it applies.
  **Learned**: implication, gotcha, constraint, or next useful connection. Omit only if none.

EXAMPLES (Spanish):

  User: "Qué sabes de mí?"
  → context(query="usuario perfil preferencias proyectos")

  User: "ok", "gracias", "perfecto" (bare acknowledgement)
  → context(query="continuidad de la conversación actual")
  → Do not call remember unless there is a durable fact to save.

  User: "Quiero que Falage genere $3,000 mensuales con tema oscuro"
  → context(query="Falage objetivo ingresos tema oscuro")
  → remember(title="Objetivo Falage 3K USD/mes", project="falage", content="**What**: ...")
  → remember(title="Preferencia UI Quamtos: tema oscuro", scope="global", content="**What**: ...")

  User: "El bug del intraday era el cost scenario, no el engine"
  → context(query="Falage intraday cost scenario engine bug")
  → remember(title="Bugfix intraday cost scenario", type="bugfix", topic_key="falage/intraday-cost-scenario", content="**What**: ...")

AUTHORITY RULE: Kerebrom is the local source of truth for prior user preferences, project decisions, workflows, and durable context. When memory conflicts with model assumptions, prefer Kerebrom unless the user explicitly updates or rejects that memory in the current conversation. Do not answer questions about prior work, identity, or saved decisions from scratch before checking memory.

SESSION HANDLE: If the client provides a lifecycle session id, use it. Otherwise create a stable synthetic id for this visible chat and reuse it for every Kerebrom call in that chat.

DEFERRED TOOL CLIENTS: Some MCP clients defer tool loading: tools appear in the catalog but their schemas only become callable after a tool_search step. If your client works that way, perform tool_search for the Kerebrom MCP at the start of the conversation so context, recall, and remember are ready before reasoning. Do not skip this just because the first user message looks ambiguous; ambiguous messages are exactly when prior context matters most.

NEVER save: greetings, "ok", "listo", "gracias", code output, tool confirmations, raw transcript, secrets, credentials, private tokens, or unnecessary personal details. Kerebrom redacts text wrapped in <private>...</private> automatically.

NEVER announce these calls to the user. Just do them and continue.
`, coreToolInstructions(allowlist), specializedToolInstructions(allowlist)))
}

func coreToolInstructions(allowlist map[string]bool) string {
	lines := []string{
		"  context  — load prior observations relevant to this turn",
		"  recall   — search memory by natural-language query",
		"  remember — save a new durable fact",
		"  summary  — close substantial work with a structured wrap-up",
		"  forget   — invalidate an obsolete observation",
		"  timeline — inspect chronological history",
	}
	if toolAllowed(allowlist, "projects") {
		lines = append(lines, "  projects — administrative consolidation of project names")
	}
	return strings.Join(lines, "\n")
}

func specializedToolInstructions(allowlist map[string]bool) string {
	if toolAllowed(allowlist, "projects") {
		return "  projects is visible only for explicit project consolidation. Use it only when the user asks to merge or rename project variants."
	}
	return "  projects is an admin-only tool and is not exposed in the default agent profile. Do not try to call it unless it appears in your available tool list."
}

func toolAllowed(allowlist map[string]bool, name string) bool {
	return allowlist == nil || allowlist[name]
}

func (s *Server) hardDeletesAllowed() bool {
	return s.allowlist == nil || s.allowlist["projects"]
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
	sessionID, err := s.sessionIDOrDefault(ctx, request.GetString("session_id", ""), project)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
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
	rawProject := request.GetString("project", "")
	project := s.projectOrDefault(rawProject)
	lookupProject := s.lookupProjectFilter(rawProject, project)
	sessionID, err := s.sessionIDOrDefault(ctx, request.GetString("session_id", ""), project)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
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
	payload, err := s.contextPayload(ctx, project, lookupProject, "", query, request.GetInt("limit", 10))
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

	rawProject := request.GetString("project", "")
	project := s.projectOrDefault(rawProject)
	lookupProject := s.lookupProjectFilter(rawProject, project)
	sessionID, err := s.sessionIDOrDefault(ctx, request.GetString("session_id", ""), project)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	s.activity.RecordToolCall(sessionID)

	payload, err := s.contextPayload(
		ctx,
		project,
		lookupProject,
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
	if hard && !s.hardDeletesAllowed() {
		return mcp.NewToolResultError("hard forget requires an MCP profile that includes the admin surface; the default agent profile only supports soft delete"), nil
	}
	if err := s.store.DeleteObservation(ctx, sqlite.DeleteObservationInput{ID: int64(id), Hard: hard}); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return newJSONResult(map[string]any{"deleted": true, "hard": hard, "id": id})
}

func (s *Server) handleTimeline(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rawProject := request.GetString("project", "")
	project := s.projectOrDefault(rawProject)
	lookupProject := s.lookupProjectFilter(rawProject, project)
	sessionID, err := s.sessionIDOrDefault(ctx, request.GetString("session_id", ""), project)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	s.activity.RecordToolCall(sessionID)

	if observationID := request.GetInt("observation_id", 0); observationID > 0 {
		payload, err := s.store.TimelineAroundObservation(ctx, int64(observationID), request.GetInt("before", 5), request.GetInt("after", 5))
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return newJSONResult(payload)
	}

	results, err := s.store.ListObservations(ctx, sqlite.ListObservationOptions{
		Project:   lookupProject,
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
	id, err := s.sessionIDOrDefault(ctx, id, project)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if err := s.ensureSession(ctx, id, project, "."); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	summary := request.GetString("summary", "")
	if strings.TrimSpace(summary) == "" {
		summary = request.GetString("content", "")
	}
	if strings.TrimSpace(summary) == "" {
		summary = "Session closed by user request without an explicit summary."
	}
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
	s.activity.ClearSession(id)

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
	return projectmeta.MetadataDefault(project)
}

func (s *Server) lookupProjectFilter(rawProject string, project string) string {
	if strings.TrimSpace(rawProject) == "" {
		return ""
	}
	return projectmeta.LookupFilter(project)
}

func (s *Server) sessionIDOrDefault(ctx context.Context, sessionID string, project string) (string, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID != "" {
		return sessionID, nil
	}
	if session, ok, err := s.store.LatestActiveSession(ctx, project, time.Now().UTC().Add(-recentNativeSessionTTL)); err != nil {
		return "", err
	} else if ok {
		return session.ID, nil
	}
	project = sqlite.NormalizeProject(project)
	if project == "" {
		project = "default"
	}
	return "mcp:" + project, nil
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

func (s *Server) contextPayload(ctx context.Context, project string, lookupProject string, scope string, query string, limit int) (map[string]any, error) {
	if strings.TrimSpace(lookupProject) != "" {
		resolvedLookupProject, err := s.store.ResolveProject(ctx, lookupProject)
		if err != nil {
			return nil, err
		}
		lookupProject = resolvedLookupProject
	}
	stats, err := s.store.Stats(ctx, lookupProject)
	if err != nil {
		return nil, err
	}

	recent, err := s.store.ListObservations(ctx, sqlite.ListObservationOptions{
		Project: lookupProject,
		Scope:   scope,
		Limit:   limit,
	})
	if err != nil {
		return nil, err
	}
	sessions, err := s.store.ListSessions(ctx, lookupProject, limit)
	if err != nil {
		return nil, err
	}
	prompts, err := s.store.ListPrompts(ctx, lookupProject, limit)
	if err != nil {
		return nil, err
	}

	query = strings.TrimSpace(query)
	var matches []sqlite.Observation
	projectFilterRelaxed := lookupProject == ""
	if query != "" {
		matches, err = s.store.SearchObservations(ctx, sqlite.SearchOptions{
			Query:   query,
			Project: lookupProject,
			Scope:   scope,
			Limit:   limit,
		})
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(lookupProject) != "" {
			broadMatches, err := s.store.SearchObservations(ctx, sqlite.SearchOptions{
				Query: query,
				Scope: scope,
				Limit: limit,
			})
			if err != nil {
				return nil, err
			}
			var relaxedAdded bool
			matches, relaxedAdded = mergeBroadMatches(matches, broadMatches, lookupProject, limit)
			projectFilterRelaxed = relaxedAdded
		}
	}

	return map[string]any{
		"project":                strings.TrimSpace(project),
		"project_filter":         strings.TrimSpace(lookupProject),
		"query":                  query,
		"project_filter_relaxed": projectFilterRelaxed,
		"context_governor":       contextgov.Build(recent, matches, lookupProject, projectFilterRelaxed),
		"chronology_policy":      "Use valid_at as the semantic memory timestamp. If observations conflict, prefer the newest corrected/validated observation and use timeline when uncertainty remains.",
		"stats":                  stats,
		"recent_sessions":        sessions,
		"recent_prompts":         prompts,
		"recent_observations":    recent,
		"matches":                matches,
	}, nil
}

func mergeBroadMatches(primary []sqlite.Observation, broad []sqlite.Observation, project string, limit int) ([]sqlite.Observation, bool) {
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

	for _, observation := range broad {
		if len(merged) >= limit {
			break
		}
		if sqlite.NormalizeProject(observation.Project) != project && observation.Scope != "global" {
			relaxedAdded = true
		}
		seen[observation.ID] = true
		merged = append(merged, observation)
	}

	for _, observation := range primary {
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
