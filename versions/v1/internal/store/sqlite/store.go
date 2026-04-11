package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const defaultBusyTimeout = 5 * time.Second

var privateTagPattern = regexp.MustCompile(`(?is)<private>.*?</private>`)

type Config struct {
	Path string
}

type Store struct {
	db   *sql.DB
	path string
}

type StartSessionInput struct {
	ID        string
	Project   string
	Directory string
	StartedAt time.Time
}

type EndSessionInput struct {
	ID      string
	Summary string
	EndedAt time.Time
}

type ObservationInput struct {
	SessionID string
	Type      string
	Title     string
	Content   string
	ToolName  string
	Project   string
	Scope     string
	TopicKey  string
	CreatedAt time.Time
}

type Session struct {
	ID        string `json:"id"`
	Project   string `json:"project"`
	Directory string `json:"directory"`
	StartedAt string `json:"started_at"`
	EndedAt   string `json:"ended_at"`
	Summary   string `json:"summary"`
	Status    string `json:"status"`
}

type Observation struct {
	ID             int64  `json:"id"`
	SessionID      string `json:"session_id"`
	Type           string `json:"type"`
	Title          string `json:"title"`
	Content        string `json:"content"`
	ToolName       string `json:"tool_name"`
	Project        string `json:"project"`
	Scope          string `json:"scope"`
	TopicKey       string `json:"topic_key"`
	NormalizedHash string `json:"normalized_hash"`
	RevisionCount  int    `json:"revision_count"`
	DuplicateCount int    `json:"duplicate_count"`
	LastSeenAt     string `json:"last_seen_at"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
	DeletedAt      string `json:"deleted_at"`
}

type SearchOptions struct {
	Query   string
	Project string
	Type    string
	Scope   string
	Limit   int
}

type ListObservationOptions struct {
	Project   string
	Scope     string
	SessionID string
	Limit     int
}

type Stats struct {
	SessionCount       int `json:"session_count"`
	ActiveSessionCount int `json:"active_session_count"`
	ObservationCount   int `json:"observation_count"`
	PromptCount        int `json:"prompt_count"`
	ProjectCount       int `json:"project_count"`
}

func Open(cfg Config) (*Store, error) {
	if cfg.Path == "" {
		return nil, fmt.Errorf("sqlite path is required")
	}

	if err := os.MkdirAll(filepath.Dir(cfg.Path), 0o755); err != nil {
		return nil, fmt.Errorf("create sqlite data dir: %w", err)
	}

	db, err := sql.Open("sqlite", cfg.Path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}

	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxIdleTime(30 * time.Second)

	store := &Store{
		db:   db,
		path: cfg.Path,
	}

	if err := store.applyConnectionPragmas(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}

	return store, nil
}

func (s *Store) Init(ctx context.Context) error {
	for _, stmt := range schemaStatements() {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("apply schema: %w", err)
		}
	}

	return nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}

	return s.db.Close()
}

func (s *Store) DB() *sql.DB {
	return s.db
}

func (s *Store) Path() string {
	return s.path
}

func (s *Store) StartSession(ctx context.Context, input StartSessionInput) error {
	if strings.TrimSpace(input.ID) == "" {
		return fmt.Errorf("session id is required")
	}

	project := normalizeProject(input.Project)
	if project == "" {
		project = "default"
	}

	directory := strings.TrimSpace(input.Directory)
	if directory == "" {
		directory = "."
	}

	startedAt := input.StartedAt.UTC()
	if startedAt.IsZero() {
		startedAt = time.Now().UTC()
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sessions (id, project, directory, started_at, status)
		VALUES (?, ?, ?, ?, 'active')
		ON CONFLICT(id) DO UPDATE SET
			project = excluded.project,
			directory = excluded.directory,
			status = 'active'
	`, input.ID, project, directory, startedAt.Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("start session: %w", err)
	}

	return nil
}

func (s *Store) GetSession(ctx context.Context, id string) (Session, error) {
	if strings.TrimSpace(id) == "" {
		return Session{}, fmt.Errorf("session id is required")
	}

	row := s.db.QueryRowContext(ctx, `
		SELECT
			id, project, directory, started_at, COALESCE(ended_at, ''),
			COALESCE(summary, ''), status
		FROM sessions
		WHERE id = ?
	`, strings.TrimSpace(id))

	var session Session
	if err := row.Scan(
		&session.ID,
		&session.Project,
		&session.Directory,
		&session.StartedAt,
		&session.EndedAt,
		&session.Summary,
		&session.Status,
	); err != nil {
		return Session{}, fmt.Errorf("get session %q: %w", id, err)
	}

	return session, nil
}

func (s *Store) EndSession(ctx context.Context, input EndSessionInput) error {
	if strings.TrimSpace(input.ID) == "" {
		return fmt.Errorf("session id is required")
	}

	endedAt := input.EndedAt.UTC()
	if endedAt.IsZero() {
		endedAt = time.Now().UTC()
	}

	result, err := s.db.ExecContext(ctx, `
		UPDATE sessions
		SET ended_at = ?, summary = ?, status = 'completed'
		WHERE id = ?
	`, endedAt.Format(time.RFC3339), strings.TrimSpace(input.Summary), input.ID)
	if err != nil {
		return fmt.Errorf("end session: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("end session rows affected: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("session %q not found", input.ID)
	}

	return nil
}

func (s *Store) SaveObservation(ctx context.Context, input ObservationInput) (Observation, error) {
	if strings.TrimSpace(input.Title) == "" {
		return Observation{}, fmt.Errorf("observation title is required")
	}
	if strings.TrimSpace(input.Content) == "" {
		return Observation{}, fmt.Errorf("observation content is required")
	}

	observationType := normalizeObservationType(input.Type)
	scope := normalizeScope(input.Scope)
	project := normalizeProject(input.Project)
	if project == "" {
		project = "default"
	}

	timestamp := input.CreatedAt.UTC()
	if timestamp.IsZero() {
		timestamp = time.Now().UTC()
	}
	timestampText := timestamp.Format(time.RFC3339)

	title := strings.TrimSpace(input.Title)
	content := stripPrivateTags(input.Content)
	hash := normalizedHash(observationType, project, scope, title, content, input.TopicKey)

	if strings.TrimSpace(input.TopicKey) != "" {
		var existingID int64
		err := s.db.QueryRowContext(ctx, `
			SELECT id
			FROM observations
			WHERE project = ? AND scope = ? AND topic_key = ? AND deleted_at IS NULL
			ORDER BY updated_at DESC, id DESC
			LIMIT 1
		`, project, scope, strings.TrimSpace(input.TopicKey)).Scan(&existingID)
		if err == nil {
			return s.UpdateObservation(ctx, UpdateObservationInput{
				ID:       existingID,
				Title:    title,
				Content:  content,
				Type:     observationType,
				Project:  project,
				Scope:    scope,
				TopicKey: input.TopicKey,
				ToolName: input.ToolName,
			})
		}
		if err != sql.ErrNoRows {
			return Observation{}, fmt.Errorf("find topic observation: %w", err)
		}
	}

	var duplicateID int64
	err := s.db.QueryRowContext(ctx, `
		SELECT id
		FROM observations
		WHERE normalized_hash = ? AND deleted_at IS NULL
		ORDER BY updated_at DESC, id DESC
		LIMIT 1
	`, hash).Scan(&duplicateID)
	if err == nil {
		now := time.Now().UTC().Format(time.RFC3339)
		if _, err := s.db.ExecContext(ctx, `
			UPDATE observations
			SET duplicate_count = duplicate_count + 1, last_seen_at = ?, updated_at = ?
			WHERE id = ?
		`, now, now, duplicateID); err != nil {
			return Observation{}, fmt.Errorf("update duplicate observation: %w", err)
		}
		return s.GetObservation(ctx, duplicateID)
	}
	if err != sql.ErrNoRows {
		return Observation{}, fmt.Errorf("find duplicate observation: %w", err)
	}

	result, err := s.db.ExecContext(ctx, `
		INSERT INTO observations (
			session_id, type, title, content, tool_name, project, scope, topic_key,
			normalized_hash, created_at, updated_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		nullIfBlank(input.SessionID),
		observationType,
		title,
		content,
		nullIfBlank(input.ToolName),
		project,
		scope,
		nullIfBlank(input.TopicKey),
		hash,
		timestampText,
		timestampText,
	)
	if err != nil {
		return Observation{}, fmt.Errorf("save observation: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return Observation{}, fmt.Errorf("save observation last insert id: %w", err)
	}

	observation, err := s.GetObservation(ctx, id)
	if err != nil {
		return Observation{}, err
	}

	return observation, nil
}

func (s *Store) GetObservation(ctx context.Context, id int64) (Observation, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT
			id, COALESCE(session_id, ''), type, title, content, COALESCE(tool_name, ''),
			project, scope, COALESCE(topic_key, ''), normalized_hash,
			revision_count, duplicate_count, COALESCE(last_seen_at, ''),
			created_at, updated_at, COALESCE(deleted_at, '')
		FROM observations
		WHERE id = ?
	`, id)

	var observation Observation
	if err := row.Scan(
		&observation.ID,
		&observation.SessionID,
		&observation.Type,
		&observation.Title,
		&observation.Content,
		&observation.ToolName,
		&observation.Project,
		&observation.Scope,
		&observation.TopicKey,
		&observation.NormalizedHash,
		&observation.RevisionCount,
		&observation.DuplicateCount,
		&observation.LastSeenAt,
		&observation.CreatedAt,
		&observation.UpdatedAt,
		&observation.DeletedAt,
	); err != nil {
		return Observation{}, fmt.Errorf("get observation %d: %w", id, err)
	}

	return observation, nil
}

func (s *Store) SearchObservations(ctx context.Context, opts SearchOptions) ([]Observation, error) {
	query := sanitizeFTSQuery(opts.Query)
	if query == "" {
		return nil, fmt.Errorf("search query is required")
	}

	limit := opts.Limit
	if limit <= 0 {
		limit = 10
	}
	if limit > 100 {
		limit = 100
	}

	project := normalizeProject(opts.Project)
	observationType := optionalNormalizedValue(opts.Type)
	scope := optionalNormalizedValue(opts.Scope)

	rows, err := s.db.QueryContext(ctx, `
		SELECT
			o.id, COALESCE(o.session_id, ''), o.type, o.title, o.content, COALESCE(o.tool_name, ''),
			o.project, o.scope, COALESCE(o.topic_key, ''), o.normalized_hash,
			o.revision_count, o.duplicate_count, COALESCE(o.last_seen_at, ''),
			o.created_at, o.updated_at, COALESCE(o.deleted_at, '')
		FROM observations o
		JOIN observations_fts ON observations_fts.rowid = o.id
		WHERE observations_fts MATCH ?
		  AND o.deleted_at IS NULL
		  AND (? = '' OR o.project = ?)
		  AND (? = '' OR o.type = ?)
		  AND (? = '' OR o.scope = ?)
		ORDER BY bm25(observations_fts), o.created_at DESC
		LIMIT ?
	`, query, project, project, observationType, observationType, scope, scope, limit)
	if err != nil {
		return nil, fmt.Errorf("search observations: %w", err)
	}
	defer rows.Close()

	var observations []Observation
	for rows.Next() {
		var observation Observation
		if err := rows.Scan(
			&observation.ID,
			&observation.SessionID,
			&observation.Type,
			&observation.Title,
			&observation.Content,
			&observation.ToolName,
			&observation.Project,
			&observation.Scope,
			&observation.TopicKey,
			&observation.NormalizedHash,
			&observation.RevisionCount,
			&observation.DuplicateCount,
			&observation.LastSeenAt,
			&observation.CreatedAt,
			&observation.UpdatedAt,
			&observation.DeletedAt,
		); err != nil {
			return nil, fmt.Errorf("scan search observation: %w", err)
		}

		observations = append(observations, observation)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate search observations: %w", err)
	}

	return observations, nil
}

func (s *Store) ListObservations(ctx context.Context, opts ListObservationOptions) ([]Observation, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = 10
	}
	if limit > 100 {
		limit = 100
	}

	project := normalizeProject(opts.Project)
	scope := optionalNormalizedValue(opts.Scope)
	sessionID := strings.TrimSpace(opts.SessionID)

	rows, err := s.db.QueryContext(ctx, `
		SELECT
			id, COALESCE(session_id, ''), type, title, content, COALESCE(tool_name, ''),
			project, scope, COALESCE(topic_key, ''), normalized_hash,
			revision_count, duplicate_count, COALESCE(last_seen_at, ''),
			created_at, updated_at, COALESCE(deleted_at, '')
		FROM observations
		WHERE deleted_at IS NULL
		  AND (? = '' OR project = ?)
		  AND (? = '' OR scope = ?)
		  AND (? = '' OR COALESCE(session_id, '') = ?)
		ORDER BY created_at DESC, id DESC
		LIMIT ?
	`, project, project, scope, scope, sessionID, sessionID, limit)
	if err != nil {
		return nil, fmt.Errorf("list observations: %w", err)
	}
	defer rows.Close()

	var observations []Observation
	for rows.Next() {
		var observation Observation
		if err := rows.Scan(
			&observation.ID,
			&observation.SessionID,
			&observation.Type,
			&observation.Title,
			&observation.Content,
			&observation.ToolName,
			&observation.Project,
			&observation.Scope,
			&observation.TopicKey,
			&observation.NormalizedHash,
			&observation.RevisionCount,
			&observation.DuplicateCount,
			&observation.LastSeenAt,
			&observation.CreatedAt,
			&observation.UpdatedAt,
			&observation.DeletedAt,
		); err != nil {
			return nil, fmt.Errorf("scan listed observation: %w", err)
		}

		observations = append(observations, observation)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate listed observations: %w", err)
	}

	return observations, nil
}

func (s *Store) CountSessionObservations(ctx context.Context, sessionID string) (int, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return 0, fmt.Errorf("session id is required")
	}

	var count int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM observations
		WHERE deleted_at IS NULL
		  AND COALESCE(session_id, '') = ?
	`, sessionID).Scan(&count); err != nil {
		return 0, fmt.Errorf("count session observations: %w", err)
	}

	return count, nil
}

func (s *Store) Stats(ctx context.Context, project string) (Stats, error) {
	project = normalizeProject(project)

	var stats Stats
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM sessions
		WHERE (? = '' OR project = ?)
	`, project, project).Scan(&stats.SessionCount); err != nil {
		return Stats{}, fmt.Errorf("count sessions: %w", err)
	}

	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM sessions
		WHERE status = 'active'
		  AND (? = '' OR project = ?)
	`, project, project).Scan(&stats.ActiveSessionCount); err != nil {
		return Stats{}, fmt.Errorf("count active sessions: %w", err)
	}

	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM observations
		WHERE deleted_at IS NULL
		  AND (? = '' OR project = ?)
	`, project, project).Scan(&stats.ObservationCount); err != nil {
		return Stats{}, fmt.Errorf("count observations: %w", err)
	}

	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM user_prompts
		WHERE (? = '' OR project = ?)
	`, project, project).Scan(&stats.PromptCount); err != nil {
		return Stats{}, fmt.Errorf("count prompts: %w", err)
	}

	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM (
			SELECT project FROM sessions WHERE (? = '' OR project = ?)
			UNION
			SELECT project FROM observations WHERE deleted_at IS NULL AND (? = '' OR project = ?)
			UNION
			SELECT project FROM user_prompts WHERE (? = '' OR project = ?)
		)
	`, project, project, project, project, project, project).Scan(&stats.ProjectCount); err != nil {
		return Stats{}, fmt.Errorf("count projects: %w", err)
	}

	return stats, nil
}

func (s *Store) applyConnectionPragmas(ctx context.Context) error {
	pragmas := []string{
		fmt.Sprintf("PRAGMA busy_timeout = %d;", defaultBusyTimeout/time.Millisecond),
		"PRAGMA foreign_keys = ON;",
		"PRAGMA synchronous = NORMAL;",
		"PRAGMA journal_mode = WAL;",
	}

	for _, stmt := range pragmas {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("apply sqlite pragma %q: %w", stmt, err)
		}
	}

	return nil
}

func schemaStatements() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS sessions (
			id TEXT PRIMARY KEY,
			project TEXT NOT NULL,
			directory TEXT NOT NULL,
			started_at TEXT NOT NULL,
			ended_at TEXT,
			summary TEXT,
			status TEXT NOT NULL DEFAULT 'active'
		);`,
		`CREATE TABLE IF NOT EXISTS observations (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT REFERENCES sessions(id) ON DELETE SET NULL,
			type TEXT NOT NULL,
			title TEXT NOT NULL,
			content TEXT NOT NULL,
			tool_name TEXT,
			project TEXT NOT NULL,
			scope TEXT NOT NULL DEFAULT 'project',
			topic_key TEXT,
			normalized_hash TEXT NOT NULL DEFAULT '',
			revision_count INTEGER NOT NULL DEFAULT 0,
			duplicate_count INTEGER NOT NULL DEFAULT 0,
			last_seen_at TEXT,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			deleted_at TEXT
		);`,
		`CREATE INDEX IF NOT EXISTS idx_observations_project_created_at
			ON observations(project, created_at DESC);`,
		`CREATE INDEX IF NOT EXISTS idx_observations_topic_key
			ON observations(project, scope, topic_key);`,
		`CREATE VIRTUAL TABLE IF NOT EXISTS observations_fts USING fts5(
			title,
			content,
			tool_name,
			type,
			project,
			content='observations',
			content_rowid='id'
		);`,
		`CREATE TRIGGER IF NOT EXISTS observations_ai AFTER INSERT ON observations BEGIN
			INSERT INTO observations_fts(rowid, title, content, tool_name, type, project)
			VALUES (new.id, new.title, new.content, coalesce(new.tool_name, ''), new.type, new.project);
		END;`,
		`CREATE TRIGGER IF NOT EXISTS observations_ad AFTER DELETE ON observations BEGIN
			INSERT INTO observations_fts(observations_fts, rowid, title, content, tool_name, type, project)
			VALUES('delete', old.id, old.title, old.content, coalesce(old.tool_name, ''), old.type, old.project);
		END;`,
		`CREATE TRIGGER IF NOT EXISTS observations_au AFTER UPDATE ON observations BEGIN
			INSERT INTO observations_fts(observations_fts, rowid, title, content, tool_name, type, project)
			VALUES('delete', old.id, old.title, old.content, coalesce(old.tool_name, ''), old.type, old.project);
			INSERT INTO observations_fts(rowid, title, content, tool_name, type, project)
			VALUES (new.id, new.title, new.content, coalesce(new.tool_name, ''), new.type, new.project);
		END;`,
		`CREATE TABLE IF NOT EXISTS user_prompts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT REFERENCES sessions(id) ON DELETE SET NULL,
			content TEXT NOT NULL,
			project TEXT NOT NULL,
			created_at TEXT NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_user_prompts_project_created_at
			ON user_prompts(project, created_at DESC);`,
		`CREATE VIRTUAL TABLE IF NOT EXISTS prompts_fts USING fts5(
			content,
			project,
			content='user_prompts',
			content_rowid='id'
		);`,
		`CREATE TRIGGER IF NOT EXISTS prompts_ai AFTER INSERT ON user_prompts BEGIN
			INSERT INTO prompts_fts(rowid, content, project)
			VALUES (new.id, new.content, new.project);
		END;`,
		`CREATE TRIGGER IF NOT EXISTS prompts_ad AFTER DELETE ON user_prompts BEGIN
			INSERT INTO prompts_fts(prompts_fts, rowid, content, project)
			VALUES('delete', old.id, old.content, old.project);
		END;`,
		`CREATE TRIGGER IF NOT EXISTS prompts_au AFTER UPDATE ON user_prompts BEGIN
			INSERT INTO prompts_fts(prompts_fts, rowid, content, project)
			VALUES('delete', old.id, old.content, old.project);
			INSERT INTO prompts_fts(rowid, content, project)
			VALUES (new.id, new.content, new.project);
		END;`,
		`CREATE TABLE IF NOT EXISTS sync_chunks (
			chunk_id TEXT PRIMARY KEY,
			imported_at TEXT NOT NULL
		);`,
	}
}

func normalizeProject(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	value = strings.ReplaceAll(value, "_", "-")
	value = strings.Join(strings.Fields(value), "-")
	return value
}

func normalizeObservationType(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return "discovery"
	}
	return value
}

func normalizeScope(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return "project"
	}
	return value
}

func optionalNormalizedValue(value string) string {
	return strings.TrimSpace(strings.ToLower(value))
}

func sanitizeFTSQuery(value string) string {
	parts := strings.Fields(strings.TrimSpace(value))
	if len(parts) == 0 {
		return ""
	}

	quoted := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.ReplaceAll(part, `"`, `""`)
		quoted = append(quoted, fmt.Sprintf(`"%s"`, part))
	}

	return strings.Join(quoted, " AND ")
}

func stripPrivateTags(value string) string {
	return strings.TrimSpace(privateTagPattern.ReplaceAllString(value, "[REDACTED]"))
}

func normalizedHash(parts ...string) string {
	hash := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(hash[:])
}

func nullIfBlank(value string) any {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return value
}
