// Package dashboard serves the D3.js force-directed graph visualization.
package dashboard

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"strconv"
	"strings"

	"github.com/ulianbass/kerebrom/internal/store"
	"github.com/ulianbass/kerebrom/internal/tokens"
)

//go:embed ui/*
var uiFS embed.FS

// RegisterRoutes mounts the dashboard on the given mux.
func RegisterRoutes(mux *http.ServeMux, s *store.Store, project string) {
	// Create token tracker for stats endpoints.
	tracker := tokens.NewTracker(s.DB())

	// Serve embedded UI files.
	sub, _ := fs.Sub(uiFS, "ui")
	fileServer := http.FileServer(http.FS(sub))

	// Graph data API.
	mux.HandleFunc("/api/graph", func(w http.ResponseWriter, r *http.Request) {
		data, err := s.BuildGraphData(project)
		writeJSONDash(w, data, err)
	})

	// Memory detail (for detail panel).
	mux.HandleFunc("/api/memory/", func(w http.ResponseWriter, r *http.Request) {
		idStr := strings.TrimPrefix(r.URL.Path, "/api/memory/")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			http.Error(w, "invalid id", 400)
			return
		}
		mem, err := s.GetMemory(id)
		if err != nil {
			http.Error(w, "not found", 404)
			return
		}
		writeJSONDash(w, mem, nil)
	})

	// Stats endpoints for the stats tab.
	mux.HandleFunc("/api/stats/summary", func(w http.ResponseWriter, r *http.Request) {
		summary, err := tracker.Summary(project)
		if err != nil {
			writeJSONDash(w, nil, err)
			return
		}
		writeJSONDash(w, summary, nil)
	})

	mux.HandleFunc("/api/stats/timeline", func(w http.ResponseWriter, r *http.Request) {
		// Return empty timeline for now (token tracker records via MCP).
		writeJSONDash(w, []any{}, nil)
	})

	mux.HandleFunc("/api/stats/operations", func(w http.ResponseWriter, r *http.Request) {
		// Return empty operations breakdown for now.
		writeJSONDash(w, []any{}, nil)
	})

	// Default: serve static files.
	mux.Handle("/", fileServer)
}

func writeJSONDash(w http.ResponseWriter, v any, err ...error) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if len(err) > 0 && err[0] != nil {
		w.WriteHeader(500)
		json.NewEncoder(w).Encode(map[string]string{"error": err[0].Error()})
		return
	}
	json.NewEncoder(w).Encode(v)
}

// ListenAndServe starts the dashboard server.
func ListenAndServe(addr string, s *store.Store, project string) error {
	mux := http.NewServeMux()
	RegisterRoutes(mux, s, project)
	fmt.Printf("Kerebrom Dashboard — http://%s\n", addr)
	return http.ListenAndServe(addr, mux)
}
