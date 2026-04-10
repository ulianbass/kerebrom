// Package tokens provides honest token tracking for memory operations.
// tokens_saved = tokens_output for reads, 0 for writes.
package tokens

import (
	"database/sql"
	"encoding/json"
	"sync"
	"time"
)

// Tracker records token usage per operation.
type Tracker struct {
	db      *sql.DB
	enabled bool
	mu      sync.Mutex
	batch   []event
}

type event struct {
	timestamp     string
	operation     string
	project       string
	tokensInput   int
	tokensOutput  int
	tokensServed  int
	memoriesCount int
	metadata      string
}

// NewTracker creates a tracker backed by the given database.
func NewTracker(db *sql.DB) *Tracker {
	return &Tracker{db: db, enabled: true}
}

// Disable stops tracking (useful for benchmarks).
func (t *Tracker) Disable() { t.enabled = false }

// Enable resumes tracking.
func (t *Tracker) Enable() { t.enabled = true }

// writesOnly returns true for operations that don't serve context to the model.
var writesOnly = map[string]bool{
	"remember": true,
	"forget":   true,
}

// Track records a single operation. For write-only ops, tokens_saved = 0.
func (t *Tracker) Track(operation, inputText, outputText, project string, memoriesCount int, metadata map[string]any) {
	if !t.enabled || t.db == nil {
		return
	}
	tokIn := countTokens(inputText)
	tokOut := countTokens(outputText)
	tokServed := tokOut
	if writesOnly[operation] {
		tokServed = 0
	}
	if project == "" {
		project = "default"
	}
	metaJSON := "{}"
	if metadata != nil {
		if b, err := json.Marshal(metadata); err == nil {
			metaJSON = string(b)
		}
	}

	// Write immediately (no batching — MCP servers handle 1-2 ops at a time).
	t.mu.Lock()
	defer t.mu.Unlock()
	t.db.Exec(`INSERT INTO token_stats
		(timestamp, operation, project, tokens_input, tokens_output, tokens_saved, memories_count, metadata)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		time.Now().UTC().Format(time.RFC3339), operation, project,
		tokIn, tokOut, tokServed, memoriesCount, metaJSON)
}

// Flush writes pending events to the database.
func (t *Tracker) Flush() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.flushLocked()
}

func (t *Tracker) flushLocked() {
	if len(t.batch) == 0 || t.db == nil {
		return
	}
	tx, err := t.db.Begin()
	if err != nil {
		return
	}
	stmt, err := tx.Prepare(`INSERT INTO token_stats
		(timestamp, operation, project, tokens_input, tokens_output, tokens_saved, memories_count, metadata)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		tx.Rollback()
		return
	}
	for _, e := range t.batch {
		stmt.Exec(e.timestamp, e.operation, e.project, e.tokensInput, e.tokensOutput, e.tokensServed, e.memoriesCount, e.metadata)
	}
	stmt.Close()
	tx.Commit()
	t.batch = t.batch[:0]
}

// TokenSummary matches the format expected by the dashboard JS.
// The JS expects: summary.total.operations, summary.total.tokens_served,
// summary.last_hour.ops, summary.last_hour.served, etc.
type TokenSummary struct {
	Total   TotalPeriod `json:"total"`
	LastHour  Period    `json:"last_hour"`
	Last24h   Period    `json:"last_24h"`
	Last7d    Period    `json:"last_7d"`
	Last30d   Period    `json:"last_30d"`
}

type TotalPeriod struct {
	Operations   int `json:"operations"`
	TokensServed int `json:"tokens_served"`
	TokensInput  int `json:"tokens_input"`
}

// Period is a rolling window aggregate.
type Period struct {
	Ops    int `json:"ops"`
	Served int `json:"served"`
}

// Summary returns aggregated token statistics in the format the dashboard JS expects.
func (t *Tracker) Summary(project string) (*TokenSummary, error) {
	s := &TokenSummary{}
	if t.db == nil {
		return s, nil
	}

	projectFilter := ""
	args := []any{}
	if project != "" {
		projectFilter = "WHERE project = ?"
		args = append(args, project)
	}

	// Total.
	t.db.QueryRow("SELECT COALESCE(COUNT(*),0), COALESCE(SUM(tokens_saved),0), COALESCE(SUM(tokens_input),0) FROM token_stats "+projectFilter, args...).
		Scan(&s.Total.Operations, &s.Total.TokensServed, &s.Total.TokensInput)

	// Rolling windows.
	type windowDef struct {
		dur  time.Duration
		dest *Period
	}
	windows := []windowDef{
		{time.Hour, &s.LastHour},
		{24 * time.Hour, &s.Last24h},
		{7 * 24 * time.Hour, &s.Last7d},
		{30 * 24 * time.Hour, &s.Last30d},
	}
	for _, w := range windows {
		cutoff := time.Now().UTC().Add(-w.dur).Format(time.RFC3339)
		q := "SELECT COALESCE(COUNT(*),0), COALESCE(SUM(tokens_saved),0) FROM token_stats WHERE timestamp >= ?"
		qArgs := []any{cutoff}
		if project != "" {
			q += " AND project = ?"
			qArgs = append(qArgs, project)
		}
		t.db.QueryRow(q, qArgs...).Scan(&w.dest.Ops, &w.dest.Served)
	}

	return s, nil
}

// Reset clears all token stats.
func (t *Tracker) Reset(project string) error {
	t.mu.Lock()
	t.batch = t.batch[:0]
	t.mu.Unlock()

	if project == "" {
		_, err := t.db.Exec("DELETE FROM token_stats")
		return err
	}
	_, err := t.db.Exec("DELETE FROM token_stats WHERE project = ?", project)
	return err
}

// countTokens estimates token count. Fallback: chars / 4.
func countTokens(text string) int {
	if text == "" {
		return 0
	}
	return len(text) / 4
}
