// Package dashboard serves the D3.js force-directed graph visualization.
package dashboard

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"

	"github.com/ulianbass/kerebrom/internal/store"
)

//go:embed ui/*
var uiFS embed.FS

// RegisterRoutes mounts the dashboard on the given mux.
func RegisterRoutes(mux *http.ServeMux, s *store.Store, project string) {
	// Serve embedded UI files.
	sub, _ := fs.Sub(uiFS, "ui")
	mux.Handle("/", http.FileServer(http.FS(sub)))

	// Graph data API.
	mux.HandleFunc("/api/graph", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		data, err := s.BuildGraphData(project)
		if err != nil {
			w.WriteHeader(500)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(data)
	})
}

// ListenAndServe starts the dashboard server.
func ListenAndServe(addr string, s *store.Store, project string) error {
	mux := http.NewServeMux()
	RegisterRoutes(mux, s, project)
	fmt.Printf("Kerebrom Dashboard — http://%s\n", addr)
	return http.ListenAndServe(addr, mux)
}
