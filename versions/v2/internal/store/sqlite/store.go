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
const defaultStaleSessionTTL = 24 * time.Hour

var privateTagPattern = regexp.MustCompile(`(?is)<private>.*?</private>`)

type projectResolver interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

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
	ValidAt        string `json:"valid_at"`
	DeletedAt      string `json:"deleted_at"`
}

type ObservationEvent struct {
	ID                   int64  `json:"id"`
	ObservationID        int64  `json:"observation_id"`
	EventType            string `json:"event_type"`
	Actor                string `json:"actor"`
	Reason               string `json:"reason"`
	RelatedObservationID int64  `json:"related_observation_id,omitempty"`
	CreatedAt            string `json:"created_at"`
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

type ProjectAlias struct {
	Alias     string `json:"alias"`
	Target    string `json:"target"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
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

	if err := s.repairSessionLifecycle(ctx); err != nil {
		return err
	}
	if err := s.repairStaleSessions(ctx); err != nil {
		return err
	}
	if err := s.repairObservationDuplicates(ctx); err != nil {
		return err
	}
	if err := s.ensureObservationUniqueIndexes(ctx); err != nil {
		return err
	}
	if err := s.ensureObservationSemanticClock(ctx); err != nil {
		return err
	}
	if err := s.ensureObservationTrustLedger(ctx); err != nil {
		return err
	}

	return nil
}

func (s *Store) repairSessionLifecycle(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE sessions
		SET status = 'completed'
		WHERE status = 'active'
		  AND ended_at IS NOT NULL
		  AND trim(ended_at) != ''
	`)
	if err != nil {
		return fmt.Errorf("repair session lifecycle: %w", err)
	}
	return nil
}

func (s *Store) repairStaleSessions(ctx context.Context) error {
	now := time.Now().UTC().Format(time.RFC3339)
	cutoff := time.Now().UTC().Add(-defaultStaleSessionTTL).Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx, `
		UPDATE sessions
		SET ended_at = ?,
			summary = CASE
				WHEN trim(COALESCE(summary, '')) = '' THEN 'Auto-closed by Kerebrom after 24h without activity.'
				ELSE summary
			END,
			status = 'completed'
		WHERE status = 'active'
		  AND (
			SELECT MAX(activity_at)
			FROM (
				SELECT sessions.started_at AS activity_at
				UNION ALL
				SELECT observations.updated_at AS activity_at
				FROM observations
				WHERE observations.deleted_at IS NULL
				  AND COALESCE(observations.session_id, '') = sessions.id
				UNION ALL
				SELECT user_prompts.created_at AS activity_at
				FROM user_prompts
				WHERE COALESCE(user_prompts.session_id, '') = sessions.id
			)
		  ) < ?
	`, now, cutoff)
	if err != nil {
		return fmt.Errorf("repair stale sessions: %w", err)
	}
	return nil
}

func (s *Store) repairObservationDuplicates(ctx context.Context) error {
	now := time.Now().UTC().Format(time.RFC3339)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin duplicate repair: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if _, err := tx.ExecContext(ctx, `
		WITH ranked AS (
			SELECT
				id,
				normalized_hash,
				ROW_NUMBER() OVER (
					PARTITION BY normalized_hash
					ORDER BY updated_at DESC, id DESC
				) AS row_number,
				COUNT(*) OVER (PARTITION BY normalized_hash) AS duplicate_total
			FROM observations
			WHERE deleted_at IS NULL
			  AND normalized_hash != ''
		)
		UPDATE observations
		SET duplicate_count = duplicate_count + (
				SELECT duplicate_total - 1
				FROM ranked
				WHERE ranked.id = observations.id
			),
			last_seen_at = ?,
			updated_at = ?
		WHERE id IN (
			SELECT id
			FROM ranked
			WHERE row_number = 1
			  AND duplicate_total > 1
		)
	`, now, now); err != nil {
		return fmt.Errorf("repair duplicate keeper observations: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		WITH ranked AS (
			SELECT
				id,
				ROW_NUMBER() OVER (
					PARTITION BY normalized_hash
					ORDER BY updated_at DESC, id DESC
				) AS row_number
			FROM observations
			WHERE deleted_at IS NULL
			  AND normalized_hash != ''
		)
		UPDATE observations
		SET deleted_at = ?,
			updated_at = ?
		WHERE id IN (
			SELECT id
			FROM ranked
			WHERE row_number > 1
		)
	`, now, now); err != nil {
		return fmt.Errorf("repair duplicate inactive observations: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit duplicate repair: %w", err)
	}
	return nil
}

func (s *Store) ensureObservationUniqueIndexes(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `
		CREATE UNIQUE INDEX IF NOT EXISTS idx_observations_normalized_hash_unique_active
		ON observations(normalized_hash)
		WHERE deleted_at IS NULL
		  AND normalized_hash != ''
	`); err != nil {
		return fmt.Errorf("create observation hash unique index: %w", err)
	}
	return nil
}

func (s *Store) ensureObservationSemanticClock(ctx context.Context) error {
	hasColumn, err := s.hasColumn(ctx, "observations", "valid_at")
	if err != nil {
		return err
	}
	if !hasColumn {
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE observations ADD COLUMN valid_at TEXT`); err != nil {
			return fmt.Errorf("add observation valid_at column: %w", err)
		}
	}

	if _, err := s.db.ExecContext(ctx, `
		UPDATE observations
		SET valid_at = CASE
			WHEN revision_count > 0 THEN updated_at
			WHEN duplicate_count > 0 AND trim(COALESCE(last_seen_at, '')) != '' THEN last_seen_at
			ELSE created_at
		END
		WHERE trim(COALESCE(valid_at, '')) = ''
	`); err != nil {
		return fmt.Errorf("backfill observation valid_at: %w", err)
	}

	if _, err := s.db.ExecContext(ctx, `
		CREATE INDEX IF NOT EXISTS idx_observations_project_valid_at
		ON observations(project, valid_at DESC)
	`); err != nil {
		return fmt.Errorf("create observation valid_at index: %w", err)
	}

	return nil
}

func (s *Store) ensureObservationTrustLedger(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO observation_events (observation_id, event_type, actor, reason, created_at)
		SELECT id, 'created', 'kerebrom_migration', 'Backfilled trust ledger event for an existing observation.', created_at
		FROM observations
		WHERE NOT EXISTS (
			SELECT 1
			FROM observation_events
			WHERE observation_events.observation_id = observations.id
		)
	`); err != nil {
		return fmt.Errorf("backfill observation trust ledger: %w", err)
	}

	return nil
}

func (s *Store) hasColumn(ctx context.Context, table string, column string) (bool, error) {
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return false, fmt.Errorf("inspect %s schema: %w", table, err)
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull int
		var defaultValue any
		var primaryKey int
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return false, fmt.Errorf("scan %s schema: %w", table, err)
		}
		if strings.EqualFold(name, column) {
			return true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("iterate %s schema: %w", table, err)
	}

	return false, nil
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

	project, err := s.ResolveProject(ctx, input.Project)
	if err != nil {
		return err
	}
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

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO sessions (id, project, directory, started_at, status)
		VALUES (?, ?, ?, ?, 'active')
		ON CONFLICT(id) DO UPDATE SET
			project = excluded.project,
			directory = excluded.directory
		WHERE sessions.status = 'active'
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

// LatestActiveSession returns the most recently active non-MCP session for a
// project after cutoff. This lets MCP tools join native client hook sessions
// when the host client does not pass a session_id through the MCP call.
func (s *Store) LatestActiveSession(ctx context.Context, project string, cutoff time.Time) (Session, bool, error) {
	project, err := s.ResolveProject(ctx, project)
	if err != nil {
		return Session{}, false, err
	}
	if project == "" {
		project = "default"
	}
	if cutoff.IsZero() {
		cutoff = time.Now().UTC().Add(-10 * time.Minute)
	}
	cutoffText := cutoff.UTC().Format(time.RFC3339)

	row := s.db.QueryRowContext(ctx, `
		SELECT
			id, project, directory, started_at, COALESCE(ended_at, ''),
			COALESCE(summary, ''), status
		FROM sessions
		WHERE status = 'active'
		  AND project = ?
		  AND id NOT LIKE 'mcp:%'
		  AND (
			started_at >= ?
			OR EXISTS (
				SELECT 1
				FROM observations
				WHERE observations.deleted_at IS NULL
				  AND COALESCE(observations.session_id, '') = sessions.id
				  AND observations.updated_at >= ?
			)
			OR EXISTS (
				SELECT 1
				FROM user_prompts
				WHERE COALESCE(user_prompts.session_id, '') = sessions.id
				  AND user_prompts.created_at >= ?
			)
		  )
		ORDER BY (
			SELECT MAX(activity_at)
			FROM (
				SELECT sessions.started_at AS activity_at
				UNION ALL
				SELECT observations.updated_at AS activity_at
				FROM observations
				WHERE observations.deleted_at IS NULL
				  AND COALESCE(observations.session_id, '') = sessions.id
				UNION ALL
				SELECT user_prompts.created_at AS activity_at
				FROM user_prompts
				WHERE COALESCE(user_prompts.session_id, '') = sessions.id
			)
		) DESC, started_at DESC
		LIMIT 1
	`, project, cutoffText, cutoffText, cutoffText)

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
		if err == sql.ErrNoRows {
			return Session{}, false, nil
		}
		return Session{}, false, fmt.Errorf("latest active session: %w", err)
	}

	return session, true, nil
}

func (s *Store) SessionExists(ctx context.Context, id string) (bool, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return false, fmt.Errorf("session id is required")
	}

	var exists bool
	if err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1
			FROM sessions
			WHERE id = ?
		)
	`, id).Scan(&exists); err != nil {
		return false, fmt.Errorf("check session exists: %w", err)
	}

	return exists, nil
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
		return nil
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
	project, err := s.ResolveProject(ctx, input.Project)
	if err != nil {
		return Observation{}, err
	}
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
			ORDER BY COALESCE(valid_at, created_at) DESC, id DESC
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
	err = s.db.QueryRowContext(ctx, `
		SELECT id
		FROM observations
		WHERE normalized_hash = ? AND deleted_at IS NULL
		ORDER BY updated_at DESC, id DESC
		LIMIT 1
	`, hash).Scan(&duplicateID)
	if err == nil {
		return s.bumpDuplicateObservation(ctx, duplicateID)
	}
	if err != sql.ErrNoRows {
		return Observation{}, fmt.Errorf("find duplicate observation: %w", err)
	}

	result, err := s.db.ExecContext(ctx, `
		INSERT INTO observations (
			session_id, type, title, content, tool_name, project, scope, topic_key,
			normalized_hash, created_at, updated_at, valid_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
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
		timestampText,
	)
	if err != nil {
		var duplicateID int64
		if findErr := s.db.QueryRowContext(ctx, `
			SELECT id
			FROM observations
			WHERE normalized_hash = ? AND deleted_at IS NULL
			ORDER BY updated_at DESC, id DESC
			LIMIT 1
		`, hash).Scan(&duplicateID); findErr == nil {
			return s.bumpDuplicateObservation(ctx, duplicateID)
		}
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
	if err := s.recordObservationEvent(ctx, id, "created", defaultString(input.ToolName, "kerebrom"), "Observation created from distilled durable memory.", 0); err != nil {
		return Observation{}, err
	}

	return observation, nil
}

func (s *Store) bumpDuplicateObservation(ctx context.Context, id int64) (Observation, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := s.db.ExecContext(ctx, `
		UPDATE observations
		SET duplicate_count = duplicate_count + 1, last_seen_at = ?, updated_at = ?, valid_at = ?
		WHERE id = ?
	`, now, now, now, id); err != nil {
		return Observation{}, fmt.Errorf("update duplicate observation: %w", err)
	}
	if err := s.recordObservationEvent(ctx, id, "reasserted", "kerebrom", "Duplicate durable memory seen; duplicate_count and valid_at refreshed.", 0); err != nil {
		return Observation{}, err
	}
	return s.GetObservation(ctx, id)
}

func (s *Store) GetObservation(ctx context.Context, id int64) (Observation, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT
			id, COALESCE(session_id, ''), type, title, content, COALESCE(tool_name, ''),
			project, scope, COALESCE(topic_key, ''), normalized_hash,
			revision_count, duplicate_count, COALESCE(last_seen_at, ''),
			created_at, updated_at, COALESCE(valid_at, created_at), COALESCE(deleted_at, '')
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
		&observation.ValidAt,
		&observation.DeletedAt,
	); err != nil {
		return Observation{}, fmt.Errorf("get observation %d: %w", id, err)
	}

	return observation, nil
}

func (s *Store) SearchObservations(ctx context.Context, opts SearchOptions) ([]Observation, error) {
	rawQuery := strings.TrimSpace(opts.Query)
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

	project, err := s.ResolveProject(ctx, opts.Project)
	if err != nil {
		return nil, err
	}
	observationType := optionalNormalizedValue(opts.Type)
	scope := optionalNormalizedValue(opts.Scope)

	if rawQuery != "" {
		topicRows, err := s.db.QueryContext(ctx, `
			SELECT
				id, COALESCE(session_id, ''), type, title, content, COALESCE(tool_name, ''),
				project, scope, COALESCE(topic_key, ''), normalized_hash,
				revision_count, duplicate_count, COALESCE(last_seen_at, ''),
				created_at, updated_at, COALESCE(valid_at, created_at), COALESCE(deleted_at, '')
			FROM observations
			WHERE deleted_at IS NULL
			  AND topic_key = ?
			  AND (? = '' OR project = ? OR scope = 'global')
			  AND (? = '' OR type = ?)
			  AND (? = '' OR scope = ?)
			ORDER BY COALESCE(valid_at, created_at) DESC, id DESC
			LIMIT ?
		`, rawQuery, project, project, observationType, observationType, scope, scope, limit)
		if err != nil {
			return nil, fmt.Errorf("search topic key: %w", err)
		}
		defer topicRows.Close()
		topicResults, err := scanObservations(topicRows)
		if err != nil {
			return nil, err
		}
		if len(topicResults) > 0 {
			return topicResults, nil
		}
	}

	observations, err := s.searchObservationsFTS(ctx, query, project, observationType, scope, limit)
	if err != nil {
		return nil, err
	}
	if len(observations) > 0 {
		return observations, nil
	}

	relaxedQuery := sanitizeFTSAnyQuery(rawQuery)
	if relaxedQuery == "" || relaxedQuery == query {
		return observations, nil
	}

	return s.searchObservationsFTS(ctx, relaxedQuery, project, observationType, scope, limit)
}

func (s *Store) searchObservationsFTS(ctx context.Context, query string, project string, observationType string, scope string, limit int) ([]Observation, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			o.id, COALESCE(o.session_id, ''), o.type, o.title, o.content, COALESCE(o.tool_name, ''),
			o.project, o.scope, COALESCE(o.topic_key, ''), o.normalized_hash,
			o.revision_count, o.duplicate_count, COALESCE(o.last_seen_at, ''),
			o.created_at, o.updated_at, COALESCE(o.valid_at, o.created_at), COALESCE(o.deleted_at, '')
		FROM observations o
		JOIN observations_fts ON observations_fts.rowid = o.id
		WHERE observations_fts MATCH ?
		  AND o.deleted_at IS NULL
		  AND (? = '' OR o.project = ? OR o.scope = 'global')
		  AND (? = '' OR o.type = ?)
		  AND (? = '' OR o.scope = ?)
		ORDER BY COALESCE(o.valid_at, o.created_at) DESC, o.id DESC, bm25(observations_fts)
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
			&observation.ValidAt,
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

	project, err := s.ResolveProject(ctx, opts.Project)
	if err != nil {
		return nil, err
	}
	scope := optionalNormalizedValue(opts.Scope)
	sessionID := strings.TrimSpace(opts.SessionID)

	rows, err := s.db.QueryContext(ctx, `
		SELECT
			id, COALESCE(session_id, ''), type, title, content, COALESCE(tool_name, ''),
			project, scope, COALESCE(topic_key, ''), normalized_hash,
			revision_count, duplicate_count, COALESCE(last_seen_at, ''),
			created_at, updated_at, COALESCE(valid_at, created_at), COALESCE(deleted_at, '')
		FROM observations
		WHERE deleted_at IS NULL
		  AND (? = '' OR project = ? OR scope = 'global')
		  AND (? = '' OR scope = ?)
		  AND (? = '' OR COALESCE(session_id, '') = ?)
		ORDER BY COALESCE(valid_at, created_at) DESC, id DESC
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
			&observation.ValidAt,
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
	resolvedProject, err := s.ResolveProject(ctx, project)
	if err != nil {
		return Stats{}, err
	}
	project = resolvedProject

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
			valid_at TEXT,
			deleted_at TEXT
		);`,
		`CREATE TABLE IF NOT EXISTS observation_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			observation_id INTEGER NOT NULL REFERENCES observations(id) ON DELETE CASCADE,
			event_type TEXT NOT NULL,
			actor TEXT,
			reason TEXT,
			related_observation_id INTEGER REFERENCES observations(id) ON DELETE SET NULL,
			created_at TEXT NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_observation_events_observation_id_created_at
			ON observation_events(observation_id, created_at DESC, id DESC);`,
		`CREATE INDEX IF NOT EXISTS idx_observation_events_created_at
			ON observation_events(created_at DESC, id DESC);`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_observation_events_dedup
			ON observation_events(
				observation_id,
				event_type,
				COALESCE(actor, ''),
				COALESCE(reason, ''),
				COALESCE(related_observation_id, 0),
				created_at
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
		`CREATE TABLE IF NOT EXISTS project_aliases (
			alias TEXT PRIMARY KEY,
			target TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);`,
	}
}

func (s *Store) ResolveProject(ctx context.Context, value string) (string, error) {
	return resolveProject(ctx, s.db, value)
}

func resolveProject(ctx context.Context, resolver projectResolver, value string) (string, error) {
	project := normalizeProject(value)
	if project == "" {
		return "", nil
	}

	seen := map[string]bool{}
	for depth := 0; depth < 10; depth++ {
		if seen[project] {
			return "", fmt.Errorf("project alias cycle detected at %q", project)
		}
		seen[project] = true

		var target string
		err := resolver.QueryRowContext(ctx, `
			SELECT target
			FROM project_aliases
			WHERE alias = ?
		`, project).Scan(&target)
		if err == sql.ErrNoRows {
			return project, nil
		}
		if err != nil {
			return "", fmt.Errorf("resolve project alias %q: %w", project, err)
		}

		target = normalizeProject(target)
		if target == "" || target == project {
			return project, nil
		}
		project = target
	}

	return "", fmt.Errorf("project alias chain too deep for %q", value)
}

func normalizeProject(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	value = strings.ReplaceAll(value, "_", "-")
	value = strings.Join(strings.Fields(value), "-")
	return value
}

func NormalizeProject(value string) string {
	return normalizeProject(value)
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
	return sanitizeFTSQueryWithOperator(value, "AND")
}

func sanitizeFTSAnyQuery(value string) string {
	return sanitizeFTSQueryWithOperator(value, "OR")
}

func sanitizeFTSQueryWithOperator(value string, operator string) string {
	parts := strings.Fields(strings.TrimSpace(value))
	if len(parts) == 0 {
		return ""
	}
	if operator != "AND" && operator != "OR" {
		operator = "AND"
	}

	quoted := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.ReplaceAll(part, `"`, `""`)
		quoted = append(quoted, fmt.Sprintf(`"%s"`, part))
	}

	return strings.Join(quoted, " "+operator+" ")
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
