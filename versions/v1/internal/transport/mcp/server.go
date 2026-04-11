package mcptransport

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/ulianbass/kerebrom/internal/config"
	"github.com/ulianbass/kerebrom/internal/store/sqlite"
	"github.com/ulianbass/kerebrom/internal/version"
)

type Server struct {
	store  *sqlite.Store
	server *mcpserver.MCPServer
}

func NewServer(store *sqlite.Store) *Server {
	srv := &Server{
		store: store,
		server: mcpserver.NewMCPServer(
			config.AppName,
			version.Version,
			mcpserver.WithToolCapabilities(false),
			mcpserver.WithRecovery(),
		),
	}

	srv.registerTools()
	return srv
}

func (s *Server) MCPServer() *mcpserver.MCPServer {
	return s.server
}

func (s *Server) ServeStdio() error {
	return mcpserver.ServeStdio(s.server)
}

func (s *Server) registerTools() {
	s.server.AddTool(
		mcp.NewTool("mem_save",
			mcp.WithDescription("Persist an agent-distilled observation into Kerebrom's shared local memory. Do not store raw transcript; interpret first using What / Why / Where / Learned."),
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

	s.server.AddTool(
		mcp.NewTool("mem_search",
			mcp.WithDescription("Search persisted observations from shared Kerebrom memory."),
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

	s.server.AddTool(
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

	s.server.AddTool(
		mcp.NewTool("mem_delete",
			mcp.WithDescription("Delete an observation by id. Soft delete is the default."),
			mcp.WithNumber("id", mcp.Required(), mcp.Description("Observation identifier.")),
			mcp.WithBoolean("hard", mcp.Description("Permanently delete instead of soft delete.")),
		),
		s.handleMemDelete,
	)

	s.server.AddTool(
		mcp.NewTool("mem_suggest_topic_key",
			mcp.WithDescription("Suggest a stable topic key from type, title, or content."),
			mcp.WithString("type", mcp.Description("Observation type or family.")),
			mcp.WithString("title", mcp.Description("Observation title.")),
			mcp.WithString("content", mcp.Description("Fallback content.")),
		),
		s.handleMemSuggestTopicKey,
	)

	s.server.AddTool(
		mcp.NewTool("mem_context",
			mcp.WithDescription("Build a context bundle with stats, recent observations, and optional search matches."),
			mcp.WithString("project",
				mcp.Description("Optional project filter."),
			),
			mcp.WithString("query",
				mcp.Description("Optional search query to enrich the context bundle."),
			),
			mcp.WithString("scope",
				mcp.Description("Optional scope filter."),
			),
			mcp.WithNumber("limit",
				mcp.Description("Maximum number of observations to include. Defaults to 10."),
			),
		),
		s.handleMemContext,
	)

	s.server.AddTool(
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

	s.server.AddTool(
		mcp.NewTool("mem_get_observation",
			mcp.WithDescription("Fetch a single observation by its numeric id."),
			mcp.WithNumber("id",
				mcp.Required(),
				mcp.Description("Observation identifier."),
			),
		),
		s.handleMemGetObservation,
	)

	s.server.AddTool(
		mcp.NewTool("mem_stats",
			mcp.WithDescription("Return current memory counts for Kerebrom."),
			mcp.WithString("project",
				mcp.Description("Optional project filter."),
			),
		),
		s.handleMemStats,
	)

	s.server.AddTool(
		mcp.NewTool("mem_save_prompt",
			mcp.WithDescription("Save a user prompt for future context."),
			mcp.WithString("content", mcp.Required(), mcp.Description("User prompt content.")),
			mcp.WithString("project", mcp.Description("Project filter.")),
			mcp.WithString("session_id", mcp.Description("Optional session id.")),
		),
		s.handleMemSavePrompt,
	)

	s.server.AddTool(
		mcp.NewTool("mem_session_summary",
			mcp.WithDescription("Save or retrieve an end-of-session summary plus recent observations."),
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

	s.server.AddTool(
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

	s.server.AddTool(
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

	s.server.AddTool(
		mcp.NewTool("mem_capture_passive",
			mcp.WithDescription("Extract bullet learnings from text and save them as observations."),
			mcp.WithString("content", mcp.Required(), mcp.Description("Text output to inspect.")),
			mcp.WithString("project", mcp.Description("Project name.")),
			mcp.WithString("session_id", mcp.Description("Optional session id.")),
		),
		s.handleMemCapturePassive,
	)

	s.server.AddTool(
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

func (s *Server) handleMemSave(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	title, err := request.RequireString("title")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	content, err := request.RequireString("content")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	observation, err := s.store.SaveObservation(ctx, sqlite.ObservationInput{
		SessionID: request.GetString("session_id", ""),
		Type:      request.GetString("type", "discovery"),
		Title:     title,
		Content:   content,
		ToolName:  request.GetString("tool_name", ""),
		Project:   request.GetString("project", "default"),
		Scope:     request.GetString("scope", "project"),
		TopicKey:  request.GetString("topic_key", ""),
		CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

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

	results, err := s.store.SearchObservations(ctx, sqlite.SearchOptions{
		Query:   query,
		Project: request.GetString("project", ""),
		Type:    request.GetString("type", ""),
		Scope:   request.GetString("scope", ""),
		Limit:   request.GetInt("limit", 10),
	})
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return newJSONResult(map[string]any{
		"query":        strings.TrimSpace(query),
		"count":        len(results),
		"observations": results,
	})
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
	payload, err := s.contextPayload(
		ctx,
		request.GetString("project", ""),
		request.GetString("scope", ""),
		request.GetString("query", ""),
		request.GetInt("limit", 10),
	)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return newJSONResult(payload)
}

func (s *Server) handleMemTimeline(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if observationID := request.GetInt("observation_id", 0); observationID > 0 {
		payload, err := s.store.TimelineAroundObservation(ctx, int64(observationID), request.GetInt("before", 5), request.GetInt("after", 5))
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return newJSONResult(payload)
	}

	results, err := s.store.ListObservations(ctx, sqlite.ListObservationOptions{
		Project:   request.GetString("project", ""),
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
	project := request.GetString("project", "")

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

	prompt, err := s.store.SavePrompt(ctx, sqlite.PromptInput{
		SessionID: request.GetString("session_id", ""),
		Content:   content,
		Project:   request.GetString("project", "default"),
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
	if strings.TrimSpace(id) == "" {
		return mcp.NewToolResultError("session id is required"), nil
	}

	summary := request.GetString("summary", "")
	if strings.TrimSpace(summary) == "" {
		summary = request.GetString("content", "")
	}
	if strings.TrimSpace(summary) != "" {
		if _, err := s.store.GetSession(ctx, id); err != nil {
			_ = s.store.StartSession(ctx, sqlite.StartSessionInput{
				ID:        id,
				Project:   request.GetString("project", "default"),
				Directory: ".",
				StartedAt: time.Now().UTC(),
			})
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
			Project:   request.GetString("project", "default"),
			Scope:     "project",
			TopicKey:  "session/" + id,
			ToolName:  "mem_session_summary",
			CreatedAt: time.Now().UTC(),
		})
	}

	payload, err := s.sessionSummaryPayload(ctx, id, request.GetInt("limit", 10))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return newJSONResult(payload)
}

func (s *Server) handleMemSessionStart(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, err := request.RequireString("id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	if err := s.store.StartSession(ctx, sqlite.StartSessionInput{
		ID:        id,
		Project:   request.GetString("project", "default"),
		Directory: request.GetString("directory", "."),
		StartedAt: time.Now().UTC(),
	}); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	session, err := s.store.GetSession(ctx, id)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
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

	session, err := s.store.GetSession(ctx, id)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
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
	for i, learning := range learnings {
		observation, err := s.store.SaveObservation(ctx, sqlite.ObservationInput{
			SessionID: request.GetString("session_id", ""),
			Type:      "learning",
			Title:     fmt.Sprintf("Passive learning %d", i+1),
			Content:   learning,
			Project:   request.GetString("project", "default"),
			Scope:     "project",
			ToolName:  "mem_capture_passive",
			CreatedAt: time.Now().UTC(),
		})
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		saved = append(saved, observation)
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
