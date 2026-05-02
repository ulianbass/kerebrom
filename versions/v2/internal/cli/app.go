package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ulianbass/kerebrom/internal/config"
	"github.com/ulianbass/kerebrom/internal/contextgov"
	projectdetect "github.com/ulianbass/kerebrom/internal/project"
	"github.com/ulianbass/kerebrom/internal/setup"
	"github.com/ulianbass/kerebrom/internal/store/sqlite"
	syncstore "github.com/ulianbass/kerebrom/internal/sync"
	httptransport "github.com/ulianbass/kerebrom/internal/transport/http"
	mcptransport "github.com/ulianbass/kerebrom/internal/transport/mcp"
	"github.com/ulianbass/kerebrom/internal/updater"
	"github.com/ulianbass/kerebrom/internal/version"
)

func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		writeHelp(stdout)
		return 0
	}

	switch strings.ToLower(args[0]) {
	case "help", "--help", "-h":
		writeHelp(stdout)
		return 0
	case "version", "--version", "-v":
		fmt.Fprintln(stdout, version.Full())
		return 0
	case "setup":
		return runSetup(args[1:], stdout, stderr)
	case "hook":
		return runHook(args[1:], stdout, stderr)
	case "save":
		return runSave(args[1:], stdout, stderr)
	case "search":
		return runSearch(args[1:], stdout, stderr)
	case "context":
		return runContext(args[1:], stdout, stderr)
	case "timeline":
		return runTimeline(args[1:], stdout, stderr)
	case "stats":
		return runStats(args[1:], stdout, stderr)
	case "doctor":
		return runDoctor(args[1:], stdout, stderr)
	case "export":
		return runExport(args[1:], stdout, stderr)
	case "import":
		return runImport(args[1:], stdout, stderr)
	case "sync":
		return runSync(args[1:], stdout, stderr)
	case "projects":
		return runProjects(args[1:], stdout, stderr)
	case "tui":
		return runTUI(args[1:], stdout, stderr)
	case "session-start":
		return runSessionStart(args[1:], stdout, stderr)
	case "session-end":
		return runSessionEnd(args[1:], stdout, stderr)
	case "serve":
		return runServe(args[1:], stdout, stderr)
	case "mcp":
		return runMCP(args[1:], stdout, stderr)
	case "mcp-http":
		return runMCPHTTP(args[1:], stdout, stderr)
	case "update":
		return runUpdate(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n", args[0])
		writeHelp(stderr)
		return 1
	}
}

func runSave(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("save", stderr)
	title := fs.String("title", "", "Short searchable title")
	content := fs.String("content", "", "Observation content")
	observationType := fs.String("type", "discovery", "Observation type")
	project := fs.String("project", "default", "Normalized project name")
	scope := fs.String("scope", "project", "Observation scope")
	topicKey := fs.String("topic-key", "", "Canonical topic key")
	toolName := fs.String("tool-name", "", "Origin tool name")
	sessionID := fs.String("session-id", "", "Origin session id")
	if err := fs.Parse(reorderFlagArgs(args, nil)); err != nil {
		return 2
	}
	remaining := fs.Args()
	if strings.TrimSpace(*title) == "" && len(remaining) > 0 {
		*title = remaining[0]
	}
	if strings.TrimSpace(*content) == "" && len(remaining) > 1 {
		*content = strings.Join(remaining[1:], " ")
	}

	store, closeFn, err := openStore(context.Background())
	if err != nil {
		fmt.Fprintf(stderr, "open store: %v\n", err)
		return 1
	}
	defer closeFn()

	observation, err := store.SaveObservation(context.Background(), sqlite.ObservationInput{
		SessionID: *sessionID,
		Type:      *observationType,
		Title:     *title,
		Content:   *content,
		ToolName:  *toolName,
		Project:   *project,
		Scope:     *scope,
		TopicKey:  *topicKey,
	})
	if err != nil {
		fmt.Fprintf(stderr, "save observation: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "saved observation %d in project %s\n", observation.ID, observation.Project)
	return 0
}

func runSetup(args []string, stdout, stderr io.Writer) int {
	agent := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		agent = args[0]
		args = args[1:]
	}

	fs := newFlagSet("setup", stderr)
	homeDir := fs.String("home", "", "Override home directory for config files")
	projectDir := fs.String("project-dir", "", "Override project directory for generated context")
	binaryPath := fs.String("binary-path", "", "Override the kerebrom binary path written into agent configs")
	if err := fs.Parse(reorderFlagArgs(args, nil)); err != nil {
		return 2
	}

	remaining := fs.Args()
	if agent == "" {
		if len(remaining) != 1 {
			fmt.Fprintf(stderr, "usage: kerebrom setup <%s> [--home PATH] [--project-dir PATH] [--binary-path PATH]\n", strings.Join(setup.SupportedAgents(), "|"))
			return 2
		}
		agent = remaining[0]
		remaining = remaining[1:]
	}

	if len(remaining) != 0 {
		fmt.Fprintf(stderr, "usage: kerebrom setup <%s> [--home PATH] [--project-dir PATH] [--binary-path PATH]\n", strings.Join(setup.SupportedAgents(), "|"))
		return 2
	}

	resolvedBinaryPath := strings.TrimSpace(*binaryPath)
	if resolvedBinaryPath == "" {
		path, err := stableExecutablePath()
		if err != nil {
			fmt.Fprintf(stderr, "resolve binary path: %v\n", err)
			return 1
		}
		resolvedBinaryPath = path
	}

	result, err := setup.Run(agent, setup.Options{
		HomeDir:    strings.TrimSpace(*homeDir),
		ProjectDir: strings.TrimSpace(*projectDir),
		BinaryPath: resolvedBinaryPath,
	})
	if err != nil {
		fmt.Fprintf(stderr, "setup %s: %v\n", agent, err)
		return 1
	}

	fmt.Fprintf(stdout, "configured %s\n", result.Agent)
	for _, path := range result.Files {
		fmt.Fprintf(stdout, "- %s\n", path)
	}

	return 0
}

func runSearch(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("search", stderr)
	query := fs.String("query", "", "FTS query")
	project := fs.String("project", "", "Project filter")
	observationType := fs.String("type", "", "Observation type filter")
	scope := fs.String("scope", "", "Scope filter")
	limit := fs.Int("limit", 10, "Result limit")
	if err := fs.Parse(reorderFlagArgs(args, nil)); err != nil {
		return 2
	}
	if strings.TrimSpace(*query) == "" && len(fs.Args()) > 0 {
		*query = strings.Join(fs.Args(), " ")
	}
	lookupProject := projectdetect.LookupFilter(*project)

	store, closeFn, err := openStore(context.Background())
	if err != nil {
		fmt.Fprintf(stderr, "open store: %v\n", err)
		return 1
	}
	defer closeFn()

	lookupProject, err = store.ResolveProject(context.Background(), lookupProject)
	if err != nil {
		fmt.Fprintf(stderr, "resolve project: %v\n", err)
		return 1
	}

	results, err := store.SearchObservations(context.Background(), sqlite.SearchOptions{
		Query:   *query,
		Project: lookupProject,
		Type:    *observationType,
		Scope:   *scope,
		Limit:   *limit,
	})
	if err != nil {
		fmt.Fprintf(stderr, "search observations: %v\n", err)
		return 1
	}

	if len(results) == 0 {
		fmt.Fprintln(stdout, "no results")
		return 0
	}

	for _, result := range results {
		printObservation(stdout, result)
	}
	return 0
}

func runContext(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("context", stderr)
	query := fs.String("query", "", "Optional search query")
	project := fs.String("project", "", "Project filter")
	scope := fs.String("scope", "", "Scope filter")
	limit := fs.Int("limit", 10, "Result limit")
	if err := fs.Parse(reorderFlagArgs(args, nil)); err != nil {
		return 2
	}
	if strings.TrimSpace(*project) == "" && len(fs.Args()) > 0 {
		*project = fs.Args()[0]
	}
	lookupProject := projectdetect.LookupFilter(*project)

	store, closeFn, err := openStore(context.Background())
	if err != nil {
		fmt.Fprintf(stderr, "open store: %v\n", err)
		return 1
	}
	defer closeFn()

	lookupProject, err = store.ResolveProject(context.Background(), lookupProject)
	if err != nil {
		fmt.Fprintf(stderr, "resolve project: %v\n", err)
		return 1
	}

	stats, err := store.Stats(context.Background(), lookupProject)
	if err != nil {
		fmt.Fprintf(stderr, "load stats: %v\n", err)
		return 1
	}

	recent, err := store.ListObservations(context.Background(), sqlite.ListObservationOptions{
		Project: lookupProject,
		Scope:   *scope,
		Limit:   *limit,
	})
	if err != nil {
		fmt.Fprintf(stderr, "list observations: %v\n", err)
		return 1
	}
	sessions, err := store.ListSessions(context.Background(), lookupProject, *limit)
	if err != nil {
		fmt.Fprintf(stderr, "list sessions: %v\n", err)
		return 1
	}
	prompts, err := store.ListPrompts(context.Background(), lookupProject, *limit)
	if err != nil {
		fmt.Fprintf(stderr, "list prompts: %v\n", err)
		return 1
	}

	queryText := strings.TrimSpace(*query)
	var matches []sqlite.Observation
	if queryText != "" {
		matches, err = store.SearchObservations(context.Background(), sqlite.SearchOptions{
			Query:   queryText,
			Project: lookupProject,
			Scope:   *scope,
			Limit:   *limit,
		})
		if err != nil {
			fmt.Fprintf(stderr, "search observations: %v\n", err)
			return 1
		}
	}

	governor := contextgov.Build(recent, matches, lookupProject, lookupProject == "")
	fmt.Fprintf(stdout, "project=%s project_filter=%s query=%s\n", strings.TrimSpace(*project), strings.TrimSpace(lookupProject), queryText)
	fmt.Fprintf(stdout, "context_governor: %s canonical_topics=%d conflicts=%d\n",
		contextgov.SummaryText(governor),
		governor.CanonicalTopicCount,
		len(governor.ConflictCandidates),
	)
	fmt.Fprintf(stdout, "stats: sessions=%d active_sessions=%d observations=%d prompts=%d projects=%d\n",
		stats.SessionCount,
		stats.ActiveSessionCount,
		stats.ObservationCount,
		stats.PromptCount,
		stats.ProjectCount,
	)

	fmt.Fprintln(stdout, "recent:")
	if len(recent) == 0 {
		fmt.Fprintln(stdout, "  no recent observations")
	} else {
		for _, observation := range recent {
			printObservation(stdout, observation)
		}
	}

	fmt.Fprintln(stdout, "prompts:")
	if len(prompts) == 0 {
		fmt.Fprintln(stdout, "  no recent prompts")
	} else {
		for _, prompt := range prompts {
			printPrompt(stdout, prompt)
		}
	}

	fmt.Fprintln(stdout, "sessions:")
	if len(sessions) == 0 {
		fmt.Fprintln(stdout, "  no recent sessions")
	} else {
		for _, session := range sessions {
			fmt.Fprintf(stdout, "[session:%s] %s | %s | %s\n", session.ID, session.Project, displayLabel(session.Status), session.StartedAt)
		}
	}

	if queryText != "" {
		fmt.Fprintln(stdout, "matches:")
		if len(matches) == 0 {
			fmt.Fprintln(stdout, "  no matching observations")
		} else {
			for _, observation := range matches {
				printObservation(stdout, observation)
			}
		}
	}

	return 0
}

func runTimeline(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("timeline", stderr)
	project := fs.String("project", "", "Project filter")
	scope := fs.String("scope", "", "Scope filter")
	sessionID := fs.String("session-id", "", "Session filter")
	before := fs.Int("before", 5, "Observations before an id-centered timeline")
	after := fs.Int("after", 5, "Observations after an id-centered timeline")
	limit := fs.Int("limit", 20, "Result limit")
	if err := fs.Parse(reorderFlagArgs(args, nil)); err != nil {
		return 2
	}
	lookupProject := projectdetect.LookupFilter(*project)

	store, closeFn, err := openStore(context.Background())
	if err != nil {
		fmt.Fprintf(stderr, "open store: %v\n", err)
		return 1
	}
	defer closeFn()

	if len(fs.Args()) > 0 {
		observationID, err := strconv.ParseInt(fs.Args()[0], 10, 64)
		if err != nil || observationID <= 0 {
			fmt.Fprintf(stderr, "invalid observation id %q\n", fs.Args()[0])
			return 2
		}
		payload, err := store.TimelineAroundObservation(context.Background(), observationID, *before, *after)
		if err != nil {
			fmt.Fprintf(stderr, "timeline observation: %v\n", err)
			return 1
		}
		printTimelinePayload(stdout, payload)
		return 0
	}

	results, err := store.ListObservations(context.Background(), sqlite.ListObservationOptions{
		Project:   lookupProject,
		Scope:     *scope,
		SessionID: *sessionID,
		Limit:     *limit,
	})
	if err != nil {
		fmt.Fprintf(stderr, "list observations: %v\n", err)
		return 1
	}

	if len(results) == 0 {
		fmt.Fprintln(stdout, "no results")
		return 0
	}

	for _, result := range results {
		printObservation(stdout, result)
	}
	return 0
}

func runStats(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("stats", stderr)
	project := fs.String("project", "", "Project filter")
	if err := fs.Parse(reorderFlagArgs(args, nil)); err != nil {
		return 2
	}
	lookupProject := projectdetect.LookupFilter(*project)

	store, closeFn, err := openStore(context.Background())
	if err != nil {
		fmt.Fprintf(stderr, "open store: %v\n", err)
		return 1
	}
	defer closeFn()

	lookupProject, err = store.ResolveProject(context.Background(), lookupProject)
	if err != nil {
		fmt.Fprintf(stderr, "resolve project: %v\n", err)
		return 1
	}

	stats, err := store.Stats(context.Background(), lookupProject)
	if err != nil {
		fmt.Fprintf(stderr, "load stats: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "sessions=%d active_sessions=%d observations=%d prompts=%d projects=%d\n",
		stats.SessionCount,
		stats.ActiveSessionCount,
		stats.ObservationCount,
		stats.PromptCount,
		stats.ProjectCount,
	)
	return 0
}

func runExport(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("export", stderr)
	project := fs.String("project", "", "Project filter")
	output := fs.String("output", "", "Output file path")
	if err := fs.Parse(reorderFlagArgs(args, nil)); err != nil {
		return 2
	}
	if strings.TrimSpace(*output) == "" && len(fs.Args()) > 0 {
		*output = fs.Args()[0]
	}
	if strings.TrimSpace(*output) == "" {
		*output = "kerebrom-export.json"
	}

	store, closeFn, err := openStore(context.Background())
	if err != nil {
		fmt.Fprintf(stderr, "open store: %v\n", err)
		return 1
	}
	defer closeFn()

	data, err := store.ExportData(context.Background(), sqlite.ExportOptions{Project: *project})
	if err != nil {
		fmt.Fprintf(stderr, "export data: %v\n", err)
		return 1
	}

	content, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		fmt.Fprintf(stderr, "encode export: %v\n", err)
		return 1
	}
	content = append(content, '\n')

	if *output == "-" {
		if _, err := stdout.Write(content); err != nil {
			fmt.Fprintf(stderr, "write export: %v\n", err)
			return 1
		}
		return 0
	}

	if dir := filepath.Dir(*output); dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			fmt.Fprintf(stderr, "create export dir: %v\n", err)
			return 1
		}
		if err := os.Chmod(dir, 0o700); err != nil {
			fmt.Fprintf(stderr, "secure export dir: %v\n", err)
			return 1
		}
	}
	if err := os.WriteFile(*output, content, 0o600); err != nil {
		fmt.Fprintf(stderr, "write export: %v\n", err)
		return 1
	}
	if err := os.Chmod(*output, 0o600); err != nil {
		fmt.Fprintf(stderr, "secure export: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "exported %d aliases, %d sessions, %d observations, %d events, %d prompts to %s\n",
		len(data.ProjectAliases), len(data.Sessions), len(data.Observations), len(data.ObservationEvents), len(data.Prompts), *output)
	return 0
}

func runImport(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("import", stderr)
	if err := fs.Parse(reorderFlagArgs(args, nil)); err != nil {
		return 2
	}
	if len(fs.Args()) != 1 {
		fmt.Fprintln(stderr, "usage: kerebrom import <file>")
		return 2
	}

	content, err := os.ReadFile(fs.Args()[0])
	if err != nil {
		fmt.Fprintf(stderr, "read import file: %v\n", err)
		return 1
	}

	var data sqlite.ExportData
	if err := json.Unmarshal(content, &data); err != nil {
		fmt.Fprintf(stderr, "decode import file: %v\n", err)
		return 1
	}

	store, closeFn, err := openStore(context.Background())
	if err != nil {
		fmt.Fprintf(stderr, "open store: %v\n", err)
		return 1
	}
	defer closeFn()

	summary, err := store.ImportData(context.Background(), data)
	if err != nil {
		fmt.Fprintf(stderr, "import data: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "imported %d aliases, %d sessions, %d observations, %d events, %d prompts\n", summary.Aliases, summary.Sessions, summary.Observations, summary.Events, summary.Prompts)
	return 0
}

func runSync(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("sync", stderr)
	syncDir := fs.String("dir", ".kerebrom", "Sync directory")
	project := fs.String("project", "", "Project filter")
	allProjects := fs.Bool("all", false, "Export all projects")
	importMode := fs.Bool("import", false, "Import pending chunks")
	statusMode := fs.Bool("status", false, "Show sync status")
	if err := fs.Parse(reorderFlagArgs(args, map[string]bool{
		"all":    true,
		"import": true,
		"status": true,
	})); err != nil {
		return 2
	}

	store, closeFn, err := openStore(context.Background())
	if err != nil {
		fmt.Fprintf(stderr, "open store: %v\n", err)
		return 1
	}
	defer closeFn()

	opts := syncstore.Options{Dir: *syncDir, Project: *project, All: *allProjects}
	switch {
	case *statusMode:
		status, err := syncstore.Inspect(context.Background(), store, *syncDir)
		if err != nil {
			fmt.Fprintf(stderr, "sync status: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "sync dir=%s chunks=%d pending_imports=%d\n", status.Dir, status.ChunkCount, status.PendingImports)
		return 0
	case *importMode:
		result, err := syncstore.Import(context.Background(), store, opts)
		if err != nil {
			fmt.Fprintf(stderr, "sync import: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "imported_chunks=%d skipped_chunks=%d aliases=%d sessions=%d observations=%d events=%d prompts=%d\n",
			result.Imported, result.Skipped, result.Counts.Aliases, result.Counts.Sessions, result.Counts.Observations, result.Counts.Events, result.Counts.Prompts)
		return 0
	default:
		result, err := syncstore.Export(context.Background(), store, opts)
		if err != nil {
			fmt.Fprintf(stderr, "sync export: %v\n", err)
			return 1
		}
		if !result.Created {
			fmt.Fprintln(stdout, "no new memories to sync")
			return 0
		}
		fmt.Fprintf(stdout, "created sync chunk %s at %s\n", result.Chunk.ID, filepath.Join(*syncDir, result.Chunk.File))
		return 0
	}
}

func runProjects(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: kerebrom projects <list|aliases|alias|consolidate|prune>")
		return 2
	}

	store, closeFn, err := openStore(context.Background())
	if err != nil {
		fmt.Fprintf(stderr, "open store: %v\n", err)
		return 1
	}
	defer closeFn()

	switch strings.ToLower(args[0]) {
	case "list":
		projects, err := store.ListProjects(context.Background())
		if err != nil {
			fmt.Fprintf(stderr, "list projects: %v\n", err)
			return 1
		}
		if len(projects) == 0 {
			fmt.Fprintln(stdout, "no projects")
			return 0
		}
		for _, project := range projects {
			fmt.Fprintf(stdout, "%s sessions=%d observations=%d prompts=%d\n", project.Project, project.Sessions, project.Observations, project.Prompts)
		}
		return 0
	case "aliases":
		aliases, err := store.ListProjectAliases(context.Background())
		if err != nil {
			fmt.Fprintf(stderr, "list project aliases: %v\n", err)
			return 1
		}
		if len(aliases) == 0 {
			fmt.Fprintln(stdout, "no project aliases")
			return 0
		}
		for _, alias := range aliases {
			fmt.Fprintf(stdout, "%s -> %s\n", alias.Alias, alias.Target)
		}
		return 0
	case "alias":
		fs := newFlagSet("projects alias", stderr)
		target := fs.String("target", "", "Canonical target project")
		aliases := fs.String("aliases", "", "Comma-separated aliases")
		if err := fs.Parse(reorderFlagArgs(args[1:], nil)); err != nil {
			return 2
		}
		remaining := fs.Args()
		if strings.TrimSpace(*target) == "" && len(remaining) > 0 {
			*target = remaining[0]
			remaining = remaining[1:]
		}
		aliasList := splitCSV(*aliases)
		aliasList = append(aliasList, remaining...)
		payload, err := store.SetProjectAliases(context.Background(), aliasList, *target)
		if err != nil {
			fmt.Fprintf(stderr, "set project aliases: %v\n", err)
			return 1
		}
		raw, _ := json.Marshal(payload)
		fmt.Fprintln(stdout, string(raw))
		return 0
	case "consolidate":
		fs := newFlagSet("projects consolidate", stderr)
		target := fs.String("target", "", "Canonical target project")
		sources := fs.String("sources", "", "Comma-separated source projects")
		if err := fs.Parse(reorderFlagArgs(args[1:], nil)); err != nil {
			return 2
		}
		remaining := fs.Args()
		if strings.TrimSpace(*target) == "" && len(remaining) > 0 {
			*target = remaining[0]
			remaining = remaining[1:]
		}
		sourceList := splitCSV(*sources)
		sourceList = append(sourceList, remaining...)
		payload, err := store.MergeProjects(context.Background(), sourceList, *target)
		if err != nil {
			fmt.Fprintf(stderr, "consolidate projects: %v\n", err)
			return 1
		}
		raw, _ := json.Marshal(payload)
		fmt.Fprintln(stdout, string(raw))
		return 0
	case "prune":
		fs := newFlagSet("projects prune", stderr)
		dryRun := fs.Bool("dry-run", true, "Only show projects that would be pruned")
		if err := fs.Parse(reorderFlagArgs(args[1:], map[string]bool{"dry-run": true})); err != nil {
			return 2
		}
		projects, err := store.ListProjects(context.Background())
		if err != nil {
			fmt.Fprintf(stderr, "list projects: %v\n", err)
			return 1
		}
		count := 0
		for _, project := range projects {
			if project.Observations == 0 {
				count++
				fmt.Fprintf(stdout, "candidate %s sessions=%d prompts=%d\n", project.Project, project.Sessions, project.Prompts)
			}
		}
		if count == 0 {
			fmt.Fprintln(stdout, "no prune candidates")
			return 0
		}
		if *dryRun {
			fmt.Fprintln(stdout, "dry-run only; use project consolidation before destructive cleanup")
			return 0
		}
		fmt.Fprintln(stderr, "destructive project prune is intentionally not automatic in v2")
		return 1
	default:
		fmt.Fprintf(stderr, "unknown projects command %q\n", args[0])
		return 2
	}
}

func runTUI(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("tui", stderr)
	project := fs.String("project", "", "Project filter")
	limit := fs.Int("limit", 10, "Recent observation limit")
	if err := fs.Parse(reorderFlagArgs(args, nil)); err != nil {
		return 2
	}

	store, closeFn, err := openStore(context.Background())
	if err != nil {
		fmt.Fprintf(stderr, "open store: %v\n", err)
		return 1
	}
	defer closeFn()

	if err := printDashboard(context.Background(), store, stdout, *project, *limit); err != nil {
		fmt.Fprintf(stderr, "render tui: %v\n", err)
		return 1
	}

	info, err := os.Stdin.Stat()
	if err != nil || info.Mode()&os.ModeCharDevice == 0 {
		return 0
	}

	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, "Commands: search <query>, show <id>, timeline <id>, sessions, session <id>, prompts, recent, q")
	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Fprint(stdout, "kerebrom> ")
		if !scanner.Scan() {
			break
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if line == "q" || line == "quit" || line == "exit" {
			break
		}
		if err := handleTUICommand(context.Background(), store, stdout, line, *project, *limit); err != nil {
			fmt.Fprintf(stdout, "error: %v\n", err)
		}
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintf(stderr, "read tui input: %v\n", err)
		return 1
	}
	return 0
}

func runSessionStart(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("session-start", stderr)
	id := fs.String("id", "", "Session id")
	project := fs.String("project", "default", "Project name")
	directory := fs.String("directory", "", "Working directory")
	if err := fs.Parse(reorderFlagArgs(args, nil)); err != nil {
		return 2
	}

	if strings.TrimSpace(*directory) == "" {
		cwd, err := os.Getwd()
		if err == nil {
			*directory = cwd
		}
	}

	store, closeFn, err := openStore(context.Background())
	if err != nil {
		fmt.Fprintf(stderr, "open store: %v\n", err)
		return 1
	}
	defer closeFn()

	if err := store.StartSession(context.Background(), sqlite.StartSessionInput{
		ID:        *id,
		Project:   *project,
		Directory: *directory,
		StartedAt: time.Now().UTC(),
	}); err != nil {
		fmt.Fprintf(stderr, "start session: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "started session %s\n", strings.TrimSpace(*id))
	return 0
}

func runSessionEnd(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("session-end", stderr)
	id := fs.String("id", "", "Session id")
	summary := fs.String("summary", "", "Session summary")
	if err := fs.Parse(reorderFlagArgs(args, nil)); err != nil {
		return 2
	}

	store, closeFn, err := openStore(context.Background())
	if err != nil {
		fmt.Fprintf(stderr, "open store: %v\n", err)
		return 1
	}
	defer closeFn()

	if _, _, err := store.EndSessionWithSummaryObservation(context.Background(), sqlite.EndSessionInput{
		ID:      *id,
		Summary: *summary,
		EndedAt: time.Now().UTC(),
	}, "session-end"); err != nil {
		fmt.Fprintf(stderr, "end session: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "ended session %s\n", strings.TrimSpace(*id))
	return 0
}

func runServe(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("serve", stderr)
	addr := fs.String("addr", config.DefaultListenAddr(), "Listen address")
	authToken := fs.String("auth-token", "", "Bearer token required by remote clients; defaults to KEREBROM_REMOTE_TOKEN")
	allowPublicUnauthenticated := fs.Bool("allow-public-unauthenticated", false, "Allow non-loopback HTTP without an auth token")
	if err := fs.Parse(reorderFlagArgs(args, map[string]bool{"allow-public-unauthenticated": true})); err != nil {
		return 2
	}
	if len(fs.Args()) > 0 {
		candidate := fs.Args()[0]
		if _, err := strconv.Atoi(candidate); err == nil {
			*addr = "127.0.0.1:" + candidate
		} else {
			*addr = candidate
		}
	}

	token := strings.TrimSpace(*authToken)
	if token == "" {
		token = strings.TrimSpace(os.Getenv(config.RemoteTokenEnv))
	}
	if token == "" && !*allowPublicUnauthenticated && !isLoopbackListenAddr(*addr) {
		fmt.Fprintf(stderr, "serve auth token required for non-loopback address %q; set --auth-token, %s, or --allow-public-unauthenticated\n", *addr, config.RemoteTokenEnv)
		return 2
	}

	store, closeFn, err := openStore(context.Background())
	if err != nil {
		fmt.Fprintf(stderr, "open store: %v\n", err)
		return 1
	}
	defer closeFn()

	server := httptransport.NewServerWithAuth(store, token)
	fmt.Fprintf(stdout, "kerebrom HTTP server listening on %s\n", *addr)
	if token != "" {
		fmt.Fprintln(stdout, "auth: bearer token enabled")
	} else {
		fmt.Fprintln(stdout, "auth: disabled")
	}
	if err := server.ListenAndServe(*addr); err != nil {
		fmt.Fprintf(stderr, "serve error: %v\n", err)
		return 1
	}

	return 0
}

func runMCP(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("mcp", stderr)
	tools := fs.String("tools", "", "Tool profile or comma-separated tool allowlist: agent, admin, all, or tool names")
	project := fs.String("project", "", "Default project name for MCP clients that omit project")
	if err := fs.Parse(reorderFlagArgs(args, nil)); err != nil {
		return 2
	}

	store, closeFn, err := openStore(context.Background())
	if err != nil {
		fmt.Fprintf(stderr, "open store: %v\n", err)
		return 1
	}
	defer closeFn()

	defaultProject := detectMCPDefaultProject(*project)

	server := mcptransport.NewServerWithConfig(store, mcptransport.Config{DefaultProject: defaultProject}, mcptransport.ResolveTools(*tools))
	if err := server.ServeStdio(); err != nil {
		fmt.Fprintf(stderr, "mcp serve error: %v\n", err)
		return 1
	}

	return 0
}

func runMCPHTTP(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("mcp-http", stderr)
	addr := fs.String("addr", config.DefaultListenAddr(), "Listen address for Streamable HTTP MCP")
	path := fs.String("path", "/mcp", "MCP endpoint path")
	tools := fs.String("tools", "agent", "Tool profile or comma-separated tool allowlist: agent, admin, all, or tool names")
	project := fs.String("project", "", "Default project name for MCP clients that omit project")
	authToken := fs.String("auth-token", "", "Bearer token required by remote clients; defaults to KEREBROM_REMOTE_TOKEN")
	allowPublicUnauthenticated := fs.Bool("allow-public-unauthenticated", false, "Allow non-loopback HTTP without an auth token")
	if err := fs.Parse(reorderFlagArgs(args, map[string]bool{"allow-public-unauthenticated": true})); err != nil {
		return 2
	}
	if len(fs.Args()) > 0 {
		candidate := fs.Args()[0]
		if _, err := strconv.Atoi(candidate); err == nil {
			*addr = "127.0.0.1:" + candidate
		} else {
			*addr = candidate
		}
	}

	token := strings.TrimSpace(*authToken)
	if token == "" {
		token = strings.TrimSpace(os.Getenv(config.RemoteTokenEnv))
	}
	if token == "" && !*allowPublicUnauthenticated && !isLoopbackListenAddr(*addr) {
		fmt.Fprintf(stderr, "mcp-http auth token required for non-loopback address %q; set --auth-token, %s, or --allow-public-unauthenticated\n", *addr, config.RemoteTokenEnv)
		return 2
	}

	store, closeFn, err := openStore(context.Background())
	if err != nil {
		fmt.Fprintf(stderr, "open store: %v\n", err)
		return 1
	}
	defer closeFn()

	defaultProject := detectMCPDefaultProject(*project)
	server := mcptransport.NewServerWithConfig(store, mcptransport.Config{DefaultProject: defaultProject}, mcptransport.ResolveTools(*tools))

	fmt.Fprintf(stdout, "kerebrom MCP Streamable HTTP listening on %s%s\n", *addr, normalizeCLIEndpointPath(*path))
	if token != "" {
		fmt.Fprintln(stdout, "auth: bearer token enabled")
	} else {
		fmt.Fprintln(stdout, "auth: disabled")
	}
	if err := server.ServeStreamableHTTP(mcptransport.HTTPConfig{
		Addr:      *addr,
		Path:      *path,
		AuthToken: token,
	}); err != nil {
		fmt.Fprintf(stderr, "mcp-http serve error: %v\n", err)
		return 1
	}

	return 0
}

func detectMCPDefaultProject(project string) string {
	defaultProject := strings.TrimSpace(project)
	if defaultProject == "" {
		defaultProject = os.Getenv(config.ProjectEnv)
	}
	if defaultProject == "" {
		if cwd, err := os.Getwd(); err == nil {
			defaultProject = projectdetect.Detect(cwd)
		}
	}
	return defaultProject
}

func isLoopbackListenAddr(addr string) bool {
	host, _, err := net.SplitHostPort(strings.TrimSpace(addr))
	if err != nil {
		return false
	}
	host = strings.Trim(host, "[]")
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func normalizeCLIEndpointPath(path string) string {
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

func runUpdate(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("update", stderr)
	check := fs.Bool("check", false, "Only check whether a newer release exists; do not install")
	preRelease := fs.Bool("pre-release", false, "Include pre-release tags when picking the latest version")
	yes := fs.Bool("yes", false, "Skip the confirmation prompt and install immediately")
	repo := fs.String("repo", "", "Override the GitHub owner/name (default ulianbass/kerebrom)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	cfg := updater.Config{
		Repo:               strings.TrimSpace(*repo),
		CurrentVersion:     version.Version,
		IncludePreReleases: *preRelease,
		CheckOnly:          *check,
		AssumeYes:          *yes,
		Stdin:              os.Stdin,
		Stdout:             stdout,
		Stderr:             stderr,
	}

	res, err := updater.Run(context.Background(), cfg)
	if err != nil {
		fmt.Fprintf(stderr, "update failed: %v\n", err)
		return 1
	}
	if cfg.CheckOnly && res.UpdateAvailable {
		// Surface a non-zero exit so scripts can detect "update available".
		return 10
	}
	return 0
}

func writeHelp(w io.Writer) {
	defaults := config.LoadDefaults()

	fmt.Fprintf(w, "%s v2\n\n", config.AppName)
	fmt.Fprintln(w, "Local-first persistent memory for coding agents.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Available commands:")
	fmt.Fprintln(w, "  help            Show this help")
	fmt.Fprintln(w, "  version         Print build information")
	fmt.Fprintln(w, "  update          Download and install the latest release from GitHub")
	fmt.Fprintln(w, "  setup           Configure supported local agents")
	fmt.Fprintln(w, "  hook            Run installed lifecycle hooks")
	fmt.Fprintln(w, "  serve           Start the local HTTP API")
	fmt.Fprintln(w, "  mcp             Start the local MCP server over stdio")
	fmt.Fprintln(w, "  mcp-http        Start the MCP server over Streamable HTTP")
	fmt.Fprintln(w, "  session-start   Create or refresh a local session")
	fmt.Fprintln(w, "  session-end     Mark a session as completed")
	fmt.Fprintln(w, "  save            Persist an observation")
	fmt.Fprintln(w, "  search          Search saved observations")
	fmt.Fprintln(w, "  context         Build a local context bundle")
	fmt.Fprintln(w, "  timeline        Show recent observations")
	fmt.Fprintln(w, "  stats           Show memory counts")
	fmt.Fprintln(w, "  doctor          Doctor health checks, repair, reports, and watch mode")
	fmt.Fprintln(w, "  tui             Launch terminal dashboard")
	fmt.Fprintln(w, "  export          Export memory data as JSON")
	fmt.Fprintln(w, "  import          Import memory data from JSON")
	fmt.Fprintln(w, "  sync            Export/import compressed sync chunks")
	fmt.Fprintln(w, "  projects        List, alias, or consolidate project names")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "Default data dir: %s\n", defaults.DataDir)
	fmt.Fprintf(w, "Default db path: %s\n", defaults.DBPath())
	fmt.Fprintf(w, "Default listen addr: %s\n", config.DefaultListenAddr())
	fmt.Fprintf(w, "\nMCP profiles: kerebrom mcp --tools=agent|admin|all [--project NAME]\n")
}

func openStore(ctx context.Context) (*sqlite.Store, func(), error) {
	defaults := config.LoadDefaults()

	store, err := sqlite.Open(sqlite.Config{Path: defaults.DBPath()})
	if err != nil {
		return nil, nil, err
	}

	if err := store.Init(ctx); err != nil {
		_ = store.Close()
		return nil, nil, err
	}

	return store, func() {
		_ = store.Close()
	}, nil
}

func newFlagSet(name string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	return fs
}

func reorderFlagArgs(args []string, boolFlags map[string]bool) []string {
	if len(args) == 0 {
		return args
	}

	flags := make([]string, 0, len(args))
	positionals := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positionals = append(positionals, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(arg, "--") || arg == "--" {
			positionals = append(positionals, arg)
			continue
		}

		flags = append(flags, arg)
		name := strings.TrimPrefix(arg, "--")
		if before, _, found := strings.Cut(name, "="); found {
			name = before
		}
		if strings.Contains(arg, "=") || boolFlags[name] {
			continue
		}
		if i+1 < len(args) {
			flags = append(flags, args[i+1])
			i++
		}
	}

	return append(flags, positionals...)
}

func oneLinePreview(value string, max int) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if max <= 0 || len(value) <= max {
		return value
	}
	return value[:max-3] + "..."
}

func printObservation(w io.Writer, observation sqlite.Observation) {
	fmt.Fprintf(w, "[%d] %s | %s | %s | valid_at=%s\n", observation.ID, observation.Project, displayLabel(observation.Type), observation.Title, observation.ValidAt)
	fmt.Fprintf(w, "    %s\n", oneLinePreview(observation.Content, 120))
}

func printTimelinePayload(w io.Writer, payload map[string]any) {
	if before, ok := payload["before"].([]sqlite.Observation); ok && len(before) > 0 {
		fmt.Fprintln(w, "before:")
		for i := len(before) - 1; i >= 0; i-- {
			printObservation(w, before[i])
		}
	}
	if observation, ok := payload["observation"].(sqlite.Observation); ok {
		fmt.Fprintln(w, "observation:")
		printObservation(w, observation)
	}
	if events, ok := payload["events"].([]sqlite.ObservationEvent); ok && len(events) > 0 {
		fmt.Fprintln(w, "trust ledger:")
		for _, event := range events {
			fmt.Fprintf(w, "[event:%d] observation=%d %s | %s | %s\n", event.ID, event.ObservationID, displayLabel(event.EventType), event.CreatedAt, oneLinePreview(event.Reason, 100))
		}
	}
	if after, ok := payload["after"].([]sqlite.Observation); ok && len(after) > 0 {
		fmt.Fprintln(w, "after:")
		for _, item := range after {
			printObservation(w, item)
		}
	}
}

func printPrompt(w io.Writer, prompt sqlite.Prompt) {
	fmt.Fprintf(w, "[prompt:%d] %s | %s\n", prompt.ID, prompt.Project, prompt.CreatedAt)
	fmt.Fprintf(w, "    %s\n", oneLinePreview(prompt.Content, 120))
}

func printSession(w io.Writer, session sqlite.Session) {
	fmt.Fprintf(w, "[session:%s] %s | %s | %s\n", session.ID, session.Project, displayLabel(session.Status), session.StartedAt)
	if strings.TrimSpace(session.Summary) != "" {
		fmt.Fprintf(w, "    %s\n", oneLinePreview(session.Summary, 120))
	}
}

func printDashboard(ctx context.Context, store *sqlite.Store, stdout io.Writer, project string, limit int) error {
	stats, err := store.Stats(ctx, project)
	if err != nil {
		return err
	}
	recent, err := store.ListObservations(ctx, sqlite.ListObservationOptions{Project: project, Limit: limit})
	if err != nil {
		return err
	}
	prompts, err := store.ListPrompts(ctx, project, 5)
	if err != nil {
		return err
	}

	fmt.Fprintln(stdout, "Kerebrom v2")
	fmt.Fprintf(stdout, "sessions=%d active=%d observations=%d prompts=%d projects=%d\n",
		stats.SessionCount, stats.ActiveSessionCount, stats.ObservationCount, stats.PromptCount, stats.ProjectCount)
	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, "Recent observations:")
	if len(recent) == 0 {
		fmt.Fprintln(stdout, "  no recent observations")
	} else {
		for _, observation := range recent {
			printObservation(stdout, observation)
		}
	}
	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, "Recent prompts:")
	if len(prompts) == 0 {
		fmt.Fprintln(stdout, "  no recent prompts")
	} else {
		for _, prompt := range prompts {
			printPrompt(stdout, prompt)
		}
	}
	return nil
}

func handleTUICommand(ctx context.Context, store *sqlite.Store, stdout io.Writer, line string, project string, limit int) error {
	switch {
	case line == "recent":
		return printDashboard(ctx, store, stdout, project, limit)
	case line == "sessions":
		sessions, err := store.ListSessions(ctx, project, limit)
		if err != nil {
			return err
		}
		if len(sessions) == 0 {
			fmt.Fprintln(stdout, "no sessions")
			return nil
		}
		for _, session := range sessions {
			printSession(stdout, session)
		}
		return nil
	case strings.HasPrefix(line, "session "):
		id := strings.TrimSpace(strings.TrimPrefix(line, "session "))
		session, err := store.GetSession(ctx, id)
		if err != nil {
			return err
		}
		printSession(stdout, session)
		observations, err := store.ListObservations(ctx, sqlite.ListObservationOptions{SessionID: id, Limit: limit})
		if err != nil {
			return err
		}
		for _, observation := range observations {
			printObservation(stdout, observation)
		}
		return nil
	case line == "prompts":
		prompts, err := store.ListPrompts(ctx, project, limit)
		if err != nil {
			return err
		}
		if len(prompts) == 0 {
			fmt.Fprintln(stdout, "no prompts")
			return nil
		}
		for _, prompt := range prompts {
			printPrompt(stdout, prompt)
		}
		return nil
	case strings.HasPrefix(line, "search "):
		query := strings.TrimSpace(strings.TrimPrefix(line, "search "))
		results, err := store.SearchObservations(ctx, sqlite.SearchOptions{Query: query, Project: project, Limit: limit})
		if err != nil {
			return err
		}
		if len(results) == 0 {
			fmt.Fprintln(stdout, "no results")
			return nil
		}
		for _, result := range results {
			printObservation(stdout, result)
		}
		return nil
	case strings.HasPrefix(line, "show "):
		id, err := strconv.ParseInt(strings.TrimSpace(strings.TrimPrefix(line, "show ")), 10, 64)
		if err != nil || id <= 0 {
			return fmt.Errorf("invalid observation id")
		}
		observation, err := store.GetObservation(ctx, id)
		if err != nil {
			return err
		}
		printObservation(stdout, observation)
		return nil
	case strings.HasPrefix(line, "timeline "):
		id, err := strconv.ParseInt(strings.TrimSpace(strings.TrimPrefix(line, "timeline ")), 10, 64)
		if err != nil || id <= 0 {
			return fmt.Errorf("invalid observation id")
		}
		payload, err := store.TimelineAroundObservation(ctx, id, 5, 5)
		if err != nil {
			return err
		}
		printTimelinePayload(stdout, payload)
		return nil
	default:
		return fmt.Errorf("unknown command %q", line)
	}
}

func splitCSV(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			result = append(result, part)
		}
	}
	return result
}

func stableExecutablePath() (string, error) {
	path, err := os.Executable()
	if err != nil {
		return "", err
	}

	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("executable path is empty")
	}

	if strings.Contains(path, "go-build") || strings.Contains(path, string(os.PathSeparator)+"T"+string(os.PathSeparator)) {
		return "", fmt.Errorf("temporary go-run binary detected; pass --binary-path with the installed kerebrom binary")
	}

	return path, nil
}
