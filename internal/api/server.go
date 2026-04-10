// Package api provides an HTTP REST API for Kerebrom.
package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/ulianbass/kerebrom/internal/store"
)

// Server wraps the HTTP mux and the store.
type Server struct {
	store   *store.Store
	project string
	mux     *http.ServeMux
}

// NewServer creates an HTTP API server.
func NewServer(s *store.Store, project string) *Server {
	srv := &Server{store: s, project: project, mux: http.NewServeMux()}
	srv.registerRoutes()
	return srv
}

// Handler returns the http.Handler for use with http.ListenAndServe.
func (s *Server) Handler() http.Handler { return s.mux }

func (s *Server) registerRoutes() {
	s.mux.HandleFunc("/api/v1/health", s.handleHealth)
	s.mux.HandleFunc("/api/v1/memories", s.handleMemories)
	s.mux.HandleFunc("/api/v1/memories/", s.handleMemoryByID)
	s.mux.HandleFunc("/api/v1/recall", s.handleRecall)
	s.mux.HandleFunc("/api/v1/context", s.handleContext)
	s.mux.HandleFunc("/api/v1/entities", s.handleEntities)
	s.mux.HandleFunc("/api/v1/facts", s.handleFacts)
	s.mux.HandleFunc("/api/v1/gaps", s.handleGaps)
	s.mux.HandleFunc("/api/v1/tokens/summary", s.handleTokensSummary)
	s.mux.HandleFunc("/api/v1/graph/data", s.handleGraphData)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]string{"status": "ok", "version": "2.0.0"})
}

func (s *Server) handleMemories(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		kind := r.URL.Query().Get("kind")
		limit := queryInt(r, "limit", 20)
		memories, err := s.store.Query(s.project, kind, nil, 0, 0, limit)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, memories)
	case http.MethodPost:
		var req struct {
			Content    string         `json:"content"`
			Kind       string         `json:"kind"`
			Source     string         `json:"source"`
			Importance float64        `json:"importance"`
			Confidence float64        `json:"confidence"`
			Tags       []string       `json:"tags"`
			Metadata   map[string]any `json:"metadata"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		if req.Content == "" {
			http.Error(w, "content is required", http.StatusBadRequest)
			return
		}
		if req.Importance == 0 {
			req.Importance = 0.5
		}
		if req.Confidence == 0 {
			req.Confidence = 0.8
		}
		result, err := s.store.Remember(req.Content, s.project, req.Kind, req.Source, req.Importance, req.Confidence, req.Tags, req.Metadata)
		if err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusCreated)
		writeJSON(w, result)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleMemoryByID(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimPrefix(r.URL.Path, "/api/v1/memories/")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid memory ID", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodGet:
		mem, err := s.store.GetMemory(id)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		writeJSON(w, mem)
	case http.MethodDelete:
		n, err := s.store.Forget(s.project, id)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, map[string]int{"invalidated": n})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleRecall(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		query := r.URL.Query().Get("q")
		if query == "" {
			http.Error(w, "q parameter required", http.StatusBadRequest)
			return
		}
		limit := queryInt(r, "limit", 5)
		memories, err := s.store.Recall(query, s.project, store.RecallOptions{Limit: limit, Touch: true, Reactivate: true})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, memories)
		return
	}
	if r.Method == http.MethodPost {
		var req struct {
			Query string `json:"query"`
			Limit int    `json:"limit"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		if req.Query == "" {
			http.Error(w, "query is required", http.StatusBadRequest)
			return
		}
		if req.Limit <= 0 {
			req.Limit = 5
		}
		memories, err := s.store.Recall(req.Query, s.project, store.RecallOptions{Limit: req.Limit, Touch: true, Reactivate: true})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, memories)
		return
	}
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}

func (s *Server) handleContext(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	if query == "" {
		http.Error(w, "q parameter required", http.StatusBadRequest)
		return
	}
	limit := queryInt(r, "limit", 5)
	layer := queryInt(r, "layer", 2)
	result, err := s.store.BuildContext(query, s.project, limit, layer)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, result)
}

func (s *Server) handleEntities(w http.ResponseWriter, r *http.Request) {
	limit := queryInt(r, "limit", 20)
	entities, err := s.store.ListEntities(s.project, limit)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, entities)
}

func (s *Server) handleFacts(w http.ResponseWriter, r *http.Request) {
	limit := queryInt(r, "limit", 20)
	facts, err := s.store.ListFacts(s.project, limit)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, facts)
}

func (s *Server) handleGaps(w http.ResponseWriter, r *http.Request) {
	limit := queryInt(r, "limit", 20)
	gaps, err := s.store.ListGaps(s.project, limit)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, gaps)
}

func (s *Server) handleTokensSummary(w http.ResponseWriter, r *http.Request) {
	// Token tracker will be wired when store gets a tracker field.
	writeJSON(w, map[string]string{"status": "token tracking available via MCP"})
}

func (s *Server) handleGraphData(w http.ResponseWriter, r *http.Request) {
	data, err := s.store.BuildGraphData(s.project)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, data)
}

// --- Helpers ---

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, err error) {
	w.WriteHeader(http.StatusInternalServerError)
	writeJSON(w, map[string]string{"error": err.Error()})
}

func queryInt(r *http.Request, key string, def int) int {
	if v := r.URL.Query().Get(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

// ListenAndServe starts the API server on the given address.
func (s *Server) ListenAndServe(addr string) error {
	fmt.Printf("Kerebrom API — http://%s\n", addr)
	return http.ListenAndServe(addr, s.mux)
}
