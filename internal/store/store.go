package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"github.com/ulianbass/kerebrom/internal/embed"
)

const DefaultProject = "default"

// HalfLifeByKind maps memory kind to its half-life in days for FSRS decay.
var HalfLifeByKind = map[string]float64{
	"core":       365 * 100, // ~100 years
	"semantic":   365,
	"procedural": 180,
	"episodic":   30,
}

const (
	ReactivationThreshold = 0.55
	DormantImportance     = 0.05
)

// RecallWeights controls the fusion of search signals.
// All values are relative; they are normalized to sum to 1.0.
type RecallWeights struct {
	Recency    float64
	Importance float64
	Frequency  float64
	Semantic   float64
	Graph      float64
	Keyword    float64
}

var DefaultRecallWeights = RecallWeights{
	Recency: 0.20, Importance: 0.18, Frequency: 0.03,
	Semantic: 0.24, Graph: 0.15, Keyword: 0.20,
}

// RecallOptions configures hybrid search behavior.
type RecallOptions struct {
	Limit           int
	IncludeInactive bool
	Touch           bool
	Reactivate      bool
	Maintain        bool
	Weights         *RecallWeights
}

// MemoryRecord is the fundamental unit of memory.
type MemoryRecord struct {
	ID             int64          `json:"id"`
	Project        string         `json:"project"`
	Content        string         `json:"content"`
	RawContent     string         `json:"raw_content,omitempty"`
	Kind           string         `json:"kind"`
	Source         string         `json:"source"`
	Importance     float64        `json:"importance"`
	Confidence     float64        `json:"confidence"`
	AccessCount    int            `json:"access_count"`
	DuplicateCount int            `json:"duplicate_count"`
	IsRedacted     bool           `json:"is_redacted"`
	PIIHits        []string       `json:"pii_hits"`
	Tags           []string       `json:"tags"`
	Metadata       map[string]any `json:"metadata"`
	CreatedAt      string         `json:"created_at"`
	UpdatedAt      string         `json:"updated_at"`
	ValidAt        string         `json:"valid_at"`
	InvalidAt      *string        `json:"invalid_at"`
	ContentHash    string         `json:"content_hash"`

	// Transient search scores (not persisted).
	Score              float64 `json:"score"`
	SemanticSimilarity float64 `json:"semantic_similarity"`
	GraphScore         float64 `json:"graph_score"`
	KeywordScore       float64 `json:"keyword_score"`
	FusionScore        float64 `json:"fusion_score"`
}

// RememberResult is returned by Store.Remember().
type RememberResult struct {
	Inserted bool          `json:"inserted"`
	Memory   *MemoryRecord `json:"memory"`
}

// Store is the main entry point for all memory operations.
type Store struct {
	db       *sql.DB
	path     string
	embedder embed.Embedder
	cache    *embed.LRUCache
	mu       sync.Mutex
}

// NewStore opens (or creates) the database at dbPath.
func NewStore(dbPath string, opts ...StoreOption) (*Store, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	s := &Store{
		db:       db,
		path:     dbPath,
		embedder: embed.AutoSelect(),
		cache:    embed.NewLRUCache(2048),
	}
	for _, opt := range opts {
		opt(s)
	}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

// StoreOption configures a Store.
type StoreOption func(*Store)

// WithEmbedder overrides the default embedder.
func WithEmbedder(e embed.Embedder) StoreOption {
	return func(s *Store) { s.embedder = e }
}

// Close releases the database connection.
func (s *Store) Close() error { return s.db.Close() }

// migrate runs the schema DDL statements.
func (s *Store) migrate() error {
	// Split by semicolons and execute each statement.
	// We must handle multi-statement execution carefully because
	// modernc.org/sqlite may not support ExecMulti well.
	stmts := splitStatements(schema)
	for _, stmt := range stmts {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		if _, err := s.db.Exec(stmt); err != nil {
			// Skip errors from PRAGMA in some modes.
			if strings.HasPrefix(strings.ToUpper(stmt), "PRAGMA") {
				continue
			}
			return fmt.Errorf("exec %q: %w", truncate(stmt, 80), err)
		}
	}
	return nil
}

// splitStatements splits SQL by semicolons, respecting triggers
// (which contain internal semicolons).
func splitStatements(sql string) []string {
	var result []string
	var current strings.Builder
	inTrigger := false

	for _, line := range strings.Split(sql, "\n") {
		trimmed := strings.TrimSpace(line)
		upper := strings.ToUpper(trimmed)

		if strings.HasPrefix(upper, "CREATE TRIGGER") {
			inTrigger = true
		}

		current.WriteString(line)
		current.WriteString("\n")

		if inTrigger && strings.HasSuffix(trimmed, "END;") {
			result = append(result, current.String())
			current.Reset()
			inTrigger = false
		} else if !inTrigger && strings.HasSuffix(trimmed, ";") {
			result = append(result, current.String())
			current.Reset()
		}
	}
	if s := current.String(); strings.TrimSpace(s) != "" {
		result = append(result, s)
	}
	return result
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// now returns current UTC time as ISO 8601 string.
func now() string {
	return time.Now().UTC().Format(time.RFC3339)
}

// contentHash computes SHA-256 of the scrubbed content.
func contentHash(content string) string {
	h := sha256.Sum256([]byte(content))
	return hex.EncodeToString(h[:])
}

// clamp restricts v to [lo, hi].
func clamp(v, lo, hi float64) float64 {
	return math.Max(lo, math.Min(hi, v))
}

// marshalJSON is a helper for JSON column storage.
func marshalJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
