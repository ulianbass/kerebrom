package httptransport

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ulianbass/kerebrom/internal/contextgov"
	projectmeta "github.com/ulianbass/kerebrom/internal/project"
	promptfilter "github.com/ulianbass/kerebrom/internal/prompt"
	"github.com/ulianbass/kerebrom/internal/store/sqlite"
	"github.com/ulianbass/kerebrom/internal/version"
)

type Server struct {
	store *sqlite.Store
	mux   *http.ServeMux
}

type sessionRequest struct {
	ID        string `json:"id"`
	Project   string `json:"project"`
	Directory string `json:"directory"`
}

type endSessionRequest struct {
	Summary string `json:"summary"`
}

type observationRequest struct {
	SessionID string `json:"session_id"`
	Type      string `json:"type"`
	Title     string `json:"title"`
	Content   string `json:"content"`
	ToolName  string `json:"tool_name"`
	Project   string `json:"project"`
	Scope     string `json:"scope"`
	TopicKey  string `json:"topic_key"`
}

type promptRequest struct {
	SessionID string `json:"session_id"`
	Content   string `json:"content"`
	Project   string `json:"project"`
}

type healthResponse struct {
	Status  string `json:"status"`
	Service string `json:"service"`
	Version string `json:"version"`
}

func NewServer(store *sqlite.Store) *Server {
	server := &Server{
		store: store,
		mux:   http.NewServeMux(),
	}

	server.routes()
	return server
}

func (s *Server) Handler() http.Handler {
	return s.mux
}

func (s *Server) ListenAndServe(addr string) error {
	httpServer := &http.Server{
		Addr:              addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	return httpServer.ListenAndServe()
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /health", s.handleHealth)
	s.mux.HandleFunc("POST /sessions", s.handleCreateSession)
	s.mux.HandleFunc("GET /sessions/recent", s.handleRecentSessions)
	s.mux.HandleFunc("GET /sessions/", s.handleGetSessionRoute)
	s.mux.HandleFunc("POST /sessions/", s.handleEndSession)
	s.mux.HandleFunc("POST /observations", s.handleCreateObservation)
	s.mux.HandleFunc("GET /observations/", s.handleGetObservation)
	s.mux.HandleFunc("PATCH /observations/", s.handleUpdateObservation)
	s.mux.HandleFunc("DELETE /observations/", s.handleDeleteObservation)
	s.mux.HandleFunc("POST /prompts", s.handleCreatePrompt)
	s.mux.HandleFunc("GET /prompts/recent", s.handleRecentPrompts)
	s.mux.HandleFunc("GET /prompts/search", s.handleSearchPrompts)
	s.mux.HandleFunc("GET /search", s.handleSearch)
	s.mux.HandleFunc("GET /timeline", s.handleTimeline)
	s.mux.HandleFunc("GET /context", s.handleContext)
	s.mux.HandleFunc("GET /export", s.handleExport)
	s.mux.HandleFunc("POST /import", s.handleImport)
	s.mux.HandleFunc("GET /projects", s.handleProjects)
	s.mux.HandleFunc("GET /stats", s.handleStats)
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, healthResponse{
		Status:  "ok",
		Service: "kerebrom",
		Version: version.Version,
	})
}

func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	var payload sessionRequest
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	if err := s.store.StartSession(r.Context(), sqlite.StartSessionInput{
		ID:        payload.ID,
		Project:   payload.Project,
		Directory: payload.Directory,
		StartedAt: time.Now().UTC(),
	}); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"id":        payload.ID,
		"project":   payload.Project,
		"directory": payload.Directory,
		"status":    "active",
	})
}

func (s *Server) handleRecentSessions(w http.ResponseWriter, r *http.Request) {
	limit, err := parseLimitQuery(r, 10)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	sessions, err := s.store.ListSessions(r.Context(), r.URL.Query().Get("project"), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"count":    len(sessions),
		"sessions": sessions,
	})
}

func (s *Server) handleEndSession(w http.ResponseWriter, r *http.Request) {
	sessionID, ok := parseSessionPath(r.URL.Path, "end")
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Errorf("route not found"))
		return
	}

	var payload endSessionRequest
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	if err := s.store.EndSession(r.Context(), sqlite.EndSessionInput{
		ID:      sessionID,
		Summary: payload.Summary,
		EndedAt: time.Now().UTC(),
	}); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"id":      sessionID,
		"status":  "completed",
		"summary": payload.Summary,
	})
}

func (s *Server) handleGetSessionRoute(w http.ResponseWriter, r *http.Request) {
	sessionID, suffix, ok := parseSessionRoute(r.URL.Path)
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Errorf("route not found"))
		return
	}

	switch suffix {
	case "":
		s.handleGetSession(w, r, sessionID)
	case "summary":
		s.handleGetSessionSummary(w, r, sessionID)
	default:
		writeError(w, http.StatusNotFound, fmt.Errorf("route not found"))
	}
}

func (s *Server) handleGetSession(w http.ResponseWriter, r *http.Request, sessionID string) {
	session, err := s.store.GetSession(r.Context(), sessionID)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}

	writeJSON(w, http.StatusOK, session)
}

func (s *Server) handleGetSessionSummary(w http.ResponseWriter, r *http.Request, sessionID string) {
	limit, err := parseLimitQuery(r, 10)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	session, err := s.store.GetSession(r.Context(), sessionID)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}

	observationCount, err := s.store.CountSessionObservations(r.Context(), sessionID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	observations, err := s.store.ListObservations(r.Context(), sqlite.ListObservationOptions{
		SessionID: sessionID,
		Limit:     limit,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"session":             session,
		"observation_count":   observationCount,
		"recent_observations": observations,
	})
}

func (s *Server) handleCreateObservation(w http.ResponseWriter, r *http.Request) {
	var payload observationRequest
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	observation, err := s.store.SaveObservation(r.Context(), sqlite.ObservationInput{
		SessionID: payload.SessionID,
		Type:      payload.Type,
		Title:     payload.Title,
		Content:   payload.Content,
		ToolName:  payload.ToolName,
		Project:   payload.Project,
		Scope:     payload.Scope,
		TopicKey:  payload.TopicKey,
		CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	writeJSON(w, http.StatusCreated, observation)
}

func (s *Server) handleGetObservation(w http.ResponseWriter, r *http.Request) {
	observationID, ok := parseObservationPath(r.URL.Path)
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Errorf("route not found"))
		return
	}

	observation, err := s.store.GetObservation(r.Context(), observationID)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}

	writeJSON(w, http.StatusOK, observation)
}

func (s *Server) handleUpdateObservation(w http.ResponseWriter, r *http.Request) {
	observationID, ok := parseObservationPath(r.URL.Path)
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Errorf("route not found"))
		return
	}

	var payload observationRequest
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	observation, err := s.store.UpdateObservation(r.Context(), sqlite.UpdateObservationInput{
		ID:       observationID,
		Type:     payload.Type,
		Title:    payload.Title,
		Content:  payload.Content,
		ToolName: payload.ToolName,
		Project:  payload.Project,
		Scope:    payload.Scope,
		TopicKey: payload.TopicKey,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	writeJSON(w, http.StatusOK, observation)
}

func (s *Server) handleDeleteObservation(w http.ResponseWriter, r *http.Request) {
	observationID, ok := parseObservationPath(r.URL.Path)
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Errorf("route not found"))
		return
	}

	hard := strings.EqualFold(r.URL.Query().Get("hard"), "true")
	if err := s.store.DeleteObservation(r.Context(), sqlite.DeleteObservationInput{ID: observationID, Hard: hard}); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"id":      observationID,
		"deleted": true,
		"hard":    hard,
	})
}

func (s *Server) handleCreatePrompt(w http.ResponseWriter, r *http.Request) {
	var payload promptRequest
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if !promptfilter.IsSubstantive(payload.Content) {
		writeJSON(w, http.StatusOK, map[string]any{"saved": false, "reason": "casual_prompt_noise"})
		return
	}

	prompt, err := s.store.SavePrompt(r.Context(), sqlite.PromptInput{
		SessionID: payload.SessionID,
		Content:   payload.Content,
		Project:   payload.Project,
		CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	writeJSON(w, http.StatusCreated, prompt)
}

func (s *Server) handleRecentPrompts(w http.ResponseWriter, r *http.Request) {
	limit, err := parseLimitQuery(r, 10)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	prompts, err := s.store.ListPrompts(r.Context(), r.URL.Query().Get("project"), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"count":   len(prompts),
		"prompts": prompts,
	})
}

func (s *Server) handleSearchPrompts(w http.ResponseWriter, r *http.Request) {
	limit, err := parseLimitQuery(r, 10)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	prompts, err := s.store.SearchPrompts(r.Context(), r.URL.Query().Get("q"), r.URL.Query().Get("project"), limit)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"count":   len(prompts),
		"prompts": prompts,
	})
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	limit, err := parseLimitQuery(r, 10)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	project := projectmeta.LookupFilter(r.URL.Query().Get("project"))
	results, err := s.store.SearchObservations(r.Context(), sqlite.SearchOptions{
		Query:   r.URL.Query().Get("q"),
		Project: project,
		Type:    r.URL.Query().Get("type"),
		Scope:   r.URL.Query().Get("scope"),
		Limit:   limit,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"count":   len(results),
		"results": results,
	})
}

func (s *Server) handleTimeline(w http.ResponseWriter, r *http.Request) {
	if rawID := r.URL.Query().Get("observation_id"); rawID != "" {
		observationID, err := strconv.ParseInt(rawID, 10, 64)
		if err != nil || observationID <= 0 {
			writeError(w, http.StatusBadRequest, fmt.Errorf("invalid observation_id"))
			return
		}
		before := parseIntQuery(r, "before", 5)
		after := parseIntQuery(r, "after", 5)
		payload, err := s.store.TimelineAroundObservation(r.Context(), observationID, before, after)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, payload)
		return
	}

	limit, err := parseLimitQuery(r, 20)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	project := projectmeta.LookupFilter(r.URL.Query().Get("project"))
	results, err := s.store.ListObservations(r.Context(), sqlite.ListObservationOptions{
		Project:   project,
		Scope:     r.URL.Query().Get("scope"),
		SessionID: r.URL.Query().Get("session_id"),
		Limit:     limit,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"count":   len(results),
		"results": results,
	})
}

func (s *Server) handleContext(w http.ResponseWriter, r *http.Request) {
	limit, err := parseLimitQuery(r, 10)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	rawProject := r.URL.Query().Get("project")
	project := projectmeta.LookupFilter(rawProject)
	if strings.TrimSpace(project) != "" {
		resolvedProject, err := s.store.ResolveProject(r.Context(), project)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		project = resolvedProject
	}
	scope := r.URL.Query().Get("scope")
	query := strings.TrimSpace(r.URL.Query().Get("q"))

	stats, err := s.store.Stats(r.Context(), project)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	recent, err := s.store.ListObservations(r.Context(), sqlite.ListObservationOptions{
		Project: project,
		Scope:   scope,
		Limit:   limit,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	sessions, err := s.store.ListSessions(r.Context(), project, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	prompts, err := s.store.ListPrompts(r.Context(), project, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	var matches []sqlite.Observation
	projectFilterRelaxed := project == ""
	if query != "" {
		matches, err = s.store.SearchObservations(r.Context(), sqlite.SearchOptions{
			Query:   query,
			Project: project,
			Scope:   scope,
			Limit:   limit,
		})
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if strings.TrimSpace(project) != "" {
			broadMatches, err := s.store.SearchObservations(r.Context(), sqlite.SearchOptions{
				Query: query,
				Scope: scope,
				Limit: limit,
			})
			if err != nil {
				writeError(w, http.StatusBadRequest, err)
				return
			}
			var relaxedAdded bool
			matches, relaxedAdded = mergeBroadMatches(matches, broadMatches, project, limit)
			projectFilterRelaxed = relaxedAdded
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"project":                strings.TrimSpace(rawProject),
		"project_filter":         strings.TrimSpace(project),
		"query":                  query,
		"project_filter_relaxed": projectFilterRelaxed,
		"context_governor":       contextgov.Build(recent, matches, project, projectFilterRelaxed),
		"chronology_policy":      "Use valid_at as the semantic memory timestamp. If observations conflict, prefer the newest corrected/validated observation and use timeline when uncertainty remains.",
		"stats":                  stats,
		"recent_sessions":        sessions,
		"recent_prompts":         prompts,
		"recent_observations":    recent,
		"matches":                matches,
	})
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

func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	data, err := s.store.ExportData(r.Context(), sqlite.ExportOptions{
		Project: r.URL.Query().Get("project"),
		Since:   r.URL.Query().Get("since"),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusOK, data)
}

func (s *Server) handleImport(w http.ResponseWriter, r *http.Request) {
	var data sqlite.ExportData
	if err := decodeJSON(r, &data); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	summary, err := s.store.ImportData(r.Context(), data)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	writeJSON(w, http.StatusOK, summary)
}

func (s *Server) handleProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := s.store.ListProjects(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"count":    len(projects),
		"projects": projects,
	})
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	stats, err := s.store.Stats(r.Context(), r.URL.Query().Get("project"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusOK, stats)
}

func parseSessionRoute(path string) (sessionID string, suffix string, ok bool) {
	path = strings.Trim(path, "/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 || len(parts) > 3 || parts[0] != "sessions" {
		return "", "", false
	}
	if strings.TrimSpace(parts[1]) == "" {
		return "", "", false
	}
	if len(parts) == 3 {
		return parts[1], parts[2], true
	}
	return parts[1], "", true
}

func parseSessionPath(path string, suffix string) (string, bool) {
	sessionID, actualSuffix, ok := parseSessionRoute(path)
	if !ok || actualSuffix != suffix {
		return "", false
	}
	return sessionID, true
}

func parseObservationPath(path string) (int64, bool) {
	path = strings.Trim(path, "/")
	parts := strings.Split(path, "/")
	if len(parts) != 2 || parts[0] != "observations" || strings.TrimSpace(parts[1]) == "" {
		return 0, false
	}

	value, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || value <= 0 {
		return 0, false
	}

	return value, true
}

func parseLimitQuery(r *http.Request, fallback int) (int, error) {
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			return 0, fmt.Errorf("invalid limit: %w", err)
		}
		return parsed, nil
	}

	return fallback, nil
}

func parseIntQuery(r *http.Request, key string, fallback int) int {
	if raw := r.URL.Query().Get(key); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err == nil {
			return parsed
		}
	}
	return fallback
}

func decodeJSON(r *http.Request, target any) error {
	defer r.Body.Close()

	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode json: %w", err)
	}

	return nil
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]any{
		"error": err.Error(),
	})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func InitStore(ctx context.Context, store *sqlite.Store) error {
	return store.Init(ctx)
}
