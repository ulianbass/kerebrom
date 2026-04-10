package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/mark3labs/mcp-go/server"

	"github.com/ulianbass/kerebrom/internal/api"
	"github.com/ulianbass/kerebrom/internal/dashboard"
	mcpserver "github.com/ulianbass/kerebrom/internal/mcp"
	"github.com/ulianbass/kerebrom/internal/setup"
	"github.com/ulianbass/kerebrom/internal/store"
)

var version = "dev"

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	cmd := os.Args[1]
	switch cmd {
	case "init":
		cmdInit()
	case "remember":
		cmdRemember()
	case "recall":
		cmdRecall()
	case "forget":
		cmdForget()
	case "entities":
		cmdEntities()
	case "facts":
		cmdFacts()
	case "decay":
		cmdDecay()
	case "consolidate":
		cmdConsolidate()
	case "context":
		cmdContext()
	case "tokens":
		cmdTokens()
	case "query":
		cmdQuery()
	case "serve":
		cmdServe()
	case "api":
		cmdAPI()
	case "dashboard":
		cmdDashboard()
	case "setup":
		cmdSetup()
	case "version":
		fmt.Println("kerebrom", version)
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", cmd)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println(`kerebrom - AI memory with hybrid search, knowledge graph, and bio-inspired decay

Usage: kerebrom <command> [options]

Commands:
  init       Initialize the database
  remember   Store a memory
  recall     Search memories
  forget     Invalidate a memory
  version    Show version

Flags:
  --db PATH  Database path (default: ~/.kerebrom/kerebrom.db)`)
}

func getDBPath() string {
	for i, arg := range os.Args {
		if arg == "--db" && i+1 < len(os.Args) {
			return os.Args[i+1]
		}
	}
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".kerebrom")
	os.MkdirAll(dir, 0755)
	return filepath.Join(dir, "kerebrom.db")
}

func getFlag(name string) string {
	for i, arg := range os.Args {
		if arg == "--"+name && i+1 < len(os.Args) {
			return os.Args[i+1]
		}
	}
	return ""
}

func hasFlag(name string) bool {
	for _, arg := range os.Args {
		if arg == "--"+name {
			return true
		}
	}
	return false
}

func openStore() *store.Store {
	s, err := store.NewStore(getDBPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	return s
}

func cmdInit() {
	s := openStore()
	s.Close()
	fmt.Println("Database initialized at", getDBPath())
}

func cmdRemember() {
	args := nonFlagArgs()
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: kerebrom remember TEXT [--kind K] [--tags T1,T2] [--importance F]")
		os.Exit(1)
	}
	content := args[0]
	kind := getFlag("kind")
	if kind == "" {
		kind = "episodic"
	}
	importance := 0.5
	if v := getFlag("importance"); v != "" {
		importance, _ = strconv.ParseFloat(v, 64)
	}
	var tags []string
	if t := getFlag("tags"); t != "" {
		for _, tag := range splitComma(t) {
			tags = append(tags, tag)
		}
	}

	s := openStore()
	defer s.Close()

	result, err := s.Remember(content, getFlag("project"), kind, "cli", importance, 0.8, tags, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if result.Inserted {
		fmt.Printf("Stored memory #%d (%s)\n", result.Memory.ID, result.Memory.Kind)
	} else {
		fmt.Printf("Duplicate of #%d (count incremented)\n", result.Memory.ID)
	}
}

func cmdRecall() {
	args := nonFlagArgs()
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: kerebrom recall QUERY [--limit N] [--json]")
		os.Exit(1)
	}
	query := args[0]
	limit := 5
	if v := getFlag("limit"); v != "" {
		limit, _ = strconv.Atoi(v)
	}

	s := openStore()
	defer s.Close()

	memories, err := s.Recall(query, getFlag("project"), store.RecallOptions{
		Limit: limit,
		Touch: true,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if hasFlag("json") {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(memories)
		return
	}

	if len(memories) == 0 {
		fmt.Println("No memories found.")
		return
	}
	for _, m := range memories {
		fmt.Printf("#%d [%s] (score=%.3f) %s\n", m.ID, m.Kind, m.Score, truncateStr(m.Content, 120))
	}
}

func cmdForget() {
	idStr := getFlag("id")
	if idStr == "" {
		fmt.Fprintln(os.Stderr, "Usage: kerebrom forget --id ID")
		os.Exit(1)
	}
	id, _ := strconv.ParseInt(idStr, 10, 64)

	s := openStore()
	defer s.Close()

	n, err := s.Forget(getFlag("project"), id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Invalidated %d memory(ies)\n", n)
}

func cmdEntities() {
	s := openStore()
	defer s.Close()
	limit := 20
	if v := getFlag("limit"); v != "" {
		limit, _ = strconv.Atoi(v)
	}
	entities, err := s.ListEntities(getFlag("project"), limit)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if hasFlag("json") {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(entities)
		return
	}
	if len(entities) == 0 {
		fmt.Println("No entities found.")
		return
	}
	for _, e := range entities {
		fmt.Printf("  %s (%s) — %d memories\n", e.Name, e.EntityType, e.MemoryCount)
	}
}

func cmdFacts() {
	s := openStore()
	defer s.Close()
	limit := 20
	if v := getFlag("limit"); v != "" {
		limit, _ = strconv.Atoi(v)
	}
	facts, err := s.ListFacts(getFlag("project"), limit)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if hasFlag("json") {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(facts)
		return
	}
	if len(facts) == 0 {
		fmt.Println("No facts found.")
		return
	}
	for _, f := range facts {
		fmt.Printf("  %s --%s--> %s (conf=%.2f)\n", f.Subject, f.Predicate, f.Object, f.Confidence)
	}
}

func cmdDecay() {
	s := openStore()
	defer s.Close()
	results, err := s.ApplyDecay(getFlag("project"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	for _, r := range results {
		fmt.Printf("  %s: %d active, %d decayed, %d zombified (half-life=%.0f days)\n",
			r.Kind, r.TotalActive, r.Decayed, r.InvalidatedZombie, r.HalfLifeDays)
	}
}

func cmdConsolidate() {
	s := openStore()
	defer s.Close()
	result, err := s.Consolidate(getFlag("project"), 0.80, 2)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Clusters: %d, new semantic: %d, consolidated episodes: %d\n",
		result.Clusters, result.NewSemanticMemories, result.ConsolidatedEpisodes)
}

func cmdContext() {
	args := nonFlagArgs()
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: kerebrom context QUERY [--layer 1|2|3] [--limit N]")
		os.Exit(1)
	}
	layer := 2
	if v := getFlag("layer"); v != "" {
		layer, _ = strconv.Atoi(v)
	}
	limit := 5
	if v := getFlag("limit"); v != "" {
		limit, _ = strconv.Atoi(v)
	}
	s := openStore()
	defer s.Close()
	result, err := s.BuildContext(args[0], getFlag("project"), limit, layer)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(result)
}

func cmdTokens() {
	s := openStore()
	defer s.Close()
	// Token tracker not yet wired into store — show placeholder.
	fmt.Println("Token tracking: will be wired in Phase 4 (MCP server).")
	fmt.Println("Use --json for structured output once MCP is active.")
}

func cmdQuery() {
	s := openStore()
	defer s.Close()
	kind := getFlag("kind")
	limit := 20
	if v := getFlag("limit"); v != "" {
		limit, _ = strconv.Atoi(v)
	}
	memories, err := s.Query(getFlag("project"), kind, nil, 0, 0, limit)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if hasFlag("json") {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(memories)
		return
	}
	if len(memories) == 0 {
		fmt.Println("No memories found.")
		return
	}
	for _, m := range memories {
		fmt.Printf("#%d [%s] (imp=%.2f) %s\n", m.ID, m.Kind, m.Importance, truncateStr(m.Content, 100))
	}
}

func cmdServe() {
	s := openStore()
	project := getFlag("project")
	srv := mcpserver.NewServer(s, project)
	stdio := server.NewStdioServer(srv)
	if err := stdio.Listen(context.Background(), os.Stdin, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "MCP server error: %v\n", err)
		os.Exit(1)
	}
}

func cmdAPI() {
	s := openStore()
	project := getFlag("project")
	port := getFlag("port")
	if port == "" {
		port = "8080"
	}
	srv := api.NewServer(s, project)
	if err := srv.ListenAndServe("127.0.0.1:" + port); err != nil {
		fmt.Fprintf(os.Stderr, "API error: %v\n", err)
		os.Exit(1)
	}
}

func cmdDashboard() {
	s := openStore()
	project := getFlag("project")
	port := getFlag("port")
	if port == "" {
		port = "8420"
	}

	// Serve both dashboard UI and API on same port.
	mux := http.NewServeMux()
	dashboard.RegisterRoutes(mux, s, project)

	// Also mount /api/v1/* for API access.
	apiSrv := api.NewServer(s, project)
	mux.Handle("/api/v1/", apiSrv.Handler())

	addr := "127.0.0.1:" + port
	fmt.Printf("Kerebrom Dashboard — http://%s\n", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		fmt.Fprintf(os.Stderr, "Dashboard error: %v\n", err)
		os.Exit(1)
	}
}

func cmdSetup() {
	dbPath := getDBPath()
	binPath := setup.FindBinary()
	results := setup.Run(dbPath, binPath)
	for _, r := range results {
		status := "OK"
		if !r.OK {
			status = "SKIP"
		}
		fmt.Printf("  [%s] %s: %s\n", status, r.Tool, r.Message)
	}
}

// nonFlagArgs returns positional arguments (not --flag or their values).
func nonFlagArgs() []string {
	var args []string
	skip := false
	for _, arg := range os.Args[2:] {
		if skip {
			skip = false
			continue
		}
		if len(arg) > 0 && arg[0] == '-' {
			skip = true // skip next (the value)
			continue
		}
		args = append(args, arg)
	}
	return args
}

func splitComma(s string) []string {
	var parts []string
	for _, p := range filepath.SplitList(s) {
		p = trimStr(p)
		if p != "" {
			parts = append(parts, p)
		}
	}
	if len(parts) == 0 {
		// Fallback: split by comma.
		for _, p := range split(s, ",") {
			p = trimStr(p)
			if p != "" {
				parts = append(parts, p)
			}
		}
	}
	return parts
}

func split(s, sep string) []string {
	var result []string
	for _, p := range splitBy(s, sep) {
		result = append(result, p)
	}
	return result
}

func splitBy(s, sep string) []string {
	result := []string{}
	for {
		idx := indexOf(s, sep)
		if idx < 0 {
			result = append(result, s)
			break
		}
		result = append(result, s[:idx])
		s = s[idx+len(sep):]
	}
	return result
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func trimStr(s string) string {
	result := ""
	for _, r := range s {
		if r != ' ' && r != '\t' && r != '\n' {
			result += string(r)
		}
	}
	return result
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
