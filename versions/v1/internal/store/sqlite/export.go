package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type ExportOptions struct {
	Project string
	Since   string
}

type ExportData struct {
	App          string        `json:"app"`
	Schema       int           `json:"schema"`
	ExportedAt   string        `json:"exported_at"`
	Sessions     []Session     `json:"sessions"`
	Observations []Observation `json:"observations"`
	Prompts      []Prompt      `json:"prompts"`
}

type ImportSummary struct {
	Sessions     int `json:"sessions"`
	Observations int `json:"observations"`
	Prompts      int `json:"prompts"`
}

func (s *Store) ExportData(ctx context.Context, opts ExportOptions) (ExportData, error) {
	project := normalizeProject(opts.Project)
	since := strings.TrimSpace(opts.Since)

	sessions, err := s.listSessions(ctx, project, since)
	if err != nil {
		return ExportData{}, err
	}
	observations, err := s.listObservationsForExport(ctx, project, since)
	if err != nil {
		return ExportData{}, err
	}
	prompts, err := s.listPromptsForExport(ctx, project, since)
	if err != nil {
		return ExportData{}, err
	}

	return ExportData{
		App:          "kerebrom",
		Schema:       1,
		ExportedAt:   time.Now().UTC().Format(time.RFC3339),
		Sessions:     sessions,
		Observations: observations,
		Prompts:      prompts,
	}, nil
}

func (s *Store) ListSessions(ctx context.Context, project string, limit int) ([]Session, error) {
	project = normalizeProject(project)
	if limit <= 0 {
		limit = 10
	}
	if limit > 100 {
		limit = 100
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, project, directory, started_at, COALESCE(ended_at, ''), COALESCE(summary, ''), status
		FROM sessions
		WHERE (? = '' OR project = ?)
		ORDER BY started_at DESC, id DESC
		LIMIT ?
	`, project, project, limit)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer rows.Close()

	var sessions []Session
	for rows.Next() {
		var session Session
		if err := rows.Scan(&session.ID, &session.Project, &session.Directory, &session.StartedAt, &session.EndedAt, &session.Summary, &session.Status); err != nil {
			return nil, fmt.Errorf("scan session: %w", err)
		}
		sessions = append(sessions, session)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sessions: %w", err)
	}
	return sessions, nil
}

func (s *Store) ImportData(ctx context.Context, data ExportData) (ImportSummary, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ImportSummary{}, fmt.Errorf("begin import: %w", err)
	}
	defer tx.Rollback()

	var summary ImportSummary
	for _, session := range data.Sessions {
		if strings.TrimSpace(session.ID) == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO sessions (id, project, directory, started_at, ended_at, summary, status)
			VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET
				project = excluded.project,
				directory = excluded.directory,
				started_at = excluded.started_at,
				ended_at = excluded.ended_at,
				summary = excluded.summary,
				status = excluded.status
		`,
			strings.TrimSpace(session.ID),
			defaultString(normalizeProject(session.Project), "default"),
			defaultString(strings.TrimSpace(session.Directory), "."),
			defaultString(strings.TrimSpace(session.StartedAt), time.Now().UTC().Format(time.RFC3339)),
			nullIfBlank(session.EndedAt),
			nullIfBlank(session.Summary),
			defaultString(strings.TrimSpace(session.Status), "completed"),
		); err != nil {
			return ImportSummary{}, fmt.Errorf("import session %q: %w", session.ID, err)
		}
		summary.Sessions++
	}

	for _, observation := range data.Observations {
		if strings.TrimSpace(observation.Title) == "" || strings.TrimSpace(observation.Content) == "" {
			continue
		}
		if _, err := s.importObservation(ctx, tx, observation); err != nil {
			return ImportSummary{}, err
		}
		summary.Observations++
	}

	for _, prompt := range data.Prompts {
		if strings.TrimSpace(prompt.Content) == "" {
			continue
		}
		if err := s.importPrompt(ctx, tx, prompt); err != nil {
			return ImportSummary{}, err
		}
		summary.Prompts++
	}

	if err := tx.Commit(); err != nil {
		return ImportSummary{}, fmt.Errorf("commit import: %w", err)
	}

	return summary, nil
}

func (s *Store) SearchPrompts(ctx context.Context, query string, project string, limit int) ([]Prompt, error) {
	ftsQuery := sanitizeFTSQuery(query)
	if ftsQuery == "" {
		return nil, fmt.Errorf("prompt search query is required")
	}
	project = normalizeProject(project)
	if limit <= 0 {
		limit = 10
	}
	if limit > 100 {
		limit = 100
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT p.id, COALESCE(p.session_id, ''), p.content, p.project, p.created_at
		FROM user_prompts p
		JOIN prompts_fts ON prompts_fts.rowid = p.id
		WHERE prompts_fts MATCH ?
		  AND (? = '' OR p.project = ?)
		ORDER BY bm25(prompts_fts), p.created_at DESC
		LIMIT ?
	`, ftsQuery, project, project, limit)
	if err != nil {
		return nil, fmt.Errorf("search prompts: %w", err)
	}
	defer rows.Close()

	return scanPrompts(rows)
}

func (s *Store) SyncChunkImported(ctx context.Context, chunkID string) (bool, error) {
	chunkID = strings.TrimSpace(chunkID)
	if chunkID == "" {
		return false, fmt.Errorf("chunk id is required")
	}

	var imported string
	err := s.db.QueryRowContext(ctx, `SELECT chunk_id FROM sync_chunks WHERE chunk_id = ?`, chunkID).Scan(&imported)
	if err == nil {
		return true, nil
	}
	if err == sql.ErrNoRows {
		return false, nil
	}
	return false, fmt.Errorf("check sync chunk %q: %w", chunkID, err)
}

func (s *Store) MarkSyncChunkImported(ctx context.Context, chunkID string) error {
	chunkID = strings.TrimSpace(chunkID)
	if chunkID == "" {
		return fmt.Errorf("chunk id is required")
	}

	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO sync_chunks (chunk_id, imported_at)
		VALUES (?, ?)
		ON CONFLICT(chunk_id) DO NOTHING
	`, chunkID, time.Now().UTC().Format(time.RFC3339)); err != nil {
		return fmt.Errorf("mark sync chunk imported: %w", err)
	}
	return nil
}

func (s *Store) listSessions(ctx context.Context, project string, since string) ([]Session, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, project, directory, started_at, COALESCE(ended_at, ''), COALESCE(summary, ''), status
		FROM sessions
		WHERE (? = '' OR project = ?)
		  AND (? = '' OR started_at > ? OR COALESCE(ended_at, '') > ?)
		ORDER BY started_at DESC, id DESC
	`, project, project, since, since, since)
	if err != nil {
		return nil, fmt.Errorf("list sessions for export: %w", err)
	}
	defer rows.Close()

	var sessions []Session
	for rows.Next() {
		var session Session
		if err := rows.Scan(&session.ID, &session.Project, &session.Directory, &session.StartedAt, &session.EndedAt, &session.Summary, &session.Status); err != nil {
			return nil, fmt.Errorf("scan export session: %w", err)
		}
		sessions = append(sessions, session)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate export sessions: %w", err)
	}
	return sessions, nil
}

func (s *Store) listObservationsForExport(ctx context.Context, project string, since string) ([]Observation, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, COALESCE(session_id, ''), type, title, content, COALESCE(tool_name, ''),
			project, scope, COALESCE(topic_key, ''), normalized_hash,
			revision_count, duplicate_count, COALESCE(last_seen_at, ''),
			created_at, updated_at, COALESCE(deleted_at, '')
		FROM observations
		WHERE (? = '' OR project = ?)
		  AND (? = '' OR created_at > ? OR updated_at > ? OR COALESCE(last_seen_at, '') > ? OR COALESCE(deleted_at, '') > ?)
		ORDER BY created_at DESC, id DESC
	`, project, project, since, since, since, since, since)
	if err != nil {
		return nil, fmt.Errorf("list observations for export: %w", err)
	}
	defer rows.Close()

	return scanObservations(rows)
}

func (s *Store) listPromptsForExport(ctx context.Context, project string, since string) ([]Prompt, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, COALESCE(session_id, ''), content, project, created_at
		FROM user_prompts
		WHERE (? = '' OR project = ?)
		  AND (? = '' OR created_at > ?)
		ORDER BY created_at DESC, id DESC
	`, project, project, since, since)
	if err != nil {
		return nil, fmt.Errorf("list prompts for export: %w", err)
	}
	defer rows.Close()

	return scanPrompts(rows)
}

func (s *Store) importObservation(ctx context.Context, tx *sql.Tx, observation Observation) (int64, error) {
	project := defaultString(normalizeProject(observation.Project), "default")
	scope := normalizeScope(observation.Scope)
	observationType := normalizeObservationType(observation.Type)
	title := strings.TrimSpace(observation.Title)
	content := stripPrivateTags(observation.Content)
	topicKey := strings.TrimSpace(observation.TopicKey)
	hash := strings.TrimSpace(observation.NormalizedHash)
	if hash == "" {
		hash = normalizedHash(observationType, project, scope, title, content, topicKey)
	}

	sessionID := strings.TrimSpace(observation.SessionID)
	if sessionID != "" {
		exists, err := sessionExists(ctx, tx, sessionID)
		if err != nil {
			return 0, err
		}
		if !exists {
			sessionID = ""
		}
	}

	idToUse := observation.ID
	if idToUse > 0 {
		existing, err := observationHashByID(ctx, tx, idToUse)
		if err != nil {
			return 0, err
		}
		if existing != "" && existing != hash {
			idToUse = 0
		}
	}

	if idToUse > 0 {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO observations (
				id, session_id, type, title, content, tool_name, project, scope, topic_key,
				normalized_hash, revision_count, duplicate_count, last_seen_at, created_at, updated_at, deleted_at
			)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET
				session_id = excluded.session_id,
				type = excluded.type,
				title = excluded.title,
				content = excluded.content,
				tool_name = excluded.tool_name,
				project = excluded.project,
				scope = excluded.scope,
				topic_key = excluded.topic_key,
				normalized_hash = excluded.normalized_hash,
				revision_count = excluded.revision_count,
				duplicate_count = excluded.duplicate_count,
				last_seen_at = excluded.last_seen_at,
				created_at = excluded.created_at,
				updated_at = excluded.updated_at,
				deleted_at = excluded.deleted_at
		`, idToUse, nullIfBlank(sessionID), observationType, title, content, nullIfBlank(observation.ToolName), project, scope, nullIfBlank(topicKey), hash,
			observation.RevisionCount, observation.DuplicateCount, nullIfBlank(observation.LastSeenAt),
			defaultString(observation.CreatedAt, time.Now().UTC().Format(time.RFC3339)),
			defaultString(observation.UpdatedAt, time.Now().UTC().Format(time.RFC3339)),
			nullIfBlank(observation.DeletedAt))
		if err != nil {
			return 0, fmt.Errorf("import observation %d: %w", observation.ID, err)
		}
		return idToUse, nil
	}

	result, err := tx.ExecContext(ctx, `
		INSERT INTO observations (
			session_id, type, title, content, tool_name, project, scope, topic_key,
			normalized_hash, revision_count, duplicate_count, last_seen_at, created_at, updated_at, deleted_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, nullIfBlank(sessionID), observationType, title, content, nullIfBlank(observation.ToolName), project, scope, nullIfBlank(topicKey), hash,
		observation.RevisionCount, observation.DuplicateCount, nullIfBlank(observation.LastSeenAt),
		defaultString(observation.CreatedAt, time.Now().UTC().Format(time.RFC3339)),
		defaultString(observation.UpdatedAt, time.Now().UTC().Format(time.RFC3339)),
		nullIfBlank(observation.DeletedAt))
	if err != nil {
		return 0, fmt.Errorf("import observation %d: %w", observation.ID, err)
	}
	return result.LastInsertId()
}

func (s *Store) importPrompt(ctx context.Context, tx *sql.Tx, prompt Prompt) error {
	sessionID := strings.TrimSpace(prompt.SessionID)
	if sessionID != "" {
		exists, err := sessionExists(ctx, tx, sessionID)
		if err != nil {
			return err
		}
		if !exists {
			sessionID = ""
		}
	}
	if prompt.ID > 0 {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO user_prompts (id, session_id, content, project, created_at)
			VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET
				session_id = excluded.session_id,
				content = excluded.content,
				project = excluded.project,
				created_at = excluded.created_at
		`, prompt.ID, nullIfBlank(sessionID), stripPrivateTags(prompt.Content), defaultString(normalizeProject(prompt.Project), "default"), defaultString(prompt.CreatedAt, time.Now().UTC().Format(time.RFC3339)))
		if err != nil {
			return fmt.Errorf("import prompt %d: %w", prompt.ID, err)
		}
		return nil
	}

	_, err := tx.ExecContext(ctx, `
		INSERT INTO user_prompts (session_id, content, project, created_at)
		VALUES (?, ?, ?, ?)
	`, nullIfBlank(sessionID), stripPrivateTags(prompt.Content), defaultString(normalizeProject(prompt.Project), "default"), defaultString(prompt.CreatedAt, time.Now().UTC().Format(time.RFC3339)))
	if err != nil {
		return fmt.Errorf("import prompt: %w", err)
	}
	return nil
}

func sessionExists(ctx context.Context, tx *sql.Tx, sessionID string) (bool, error) {
	var id string
	err := tx.QueryRowContext(ctx, `SELECT id FROM sessions WHERE id = ?`, sessionID).Scan(&id)
	if err == nil {
		return true, nil
	}
	if err == sql.ErrNoRows {
		return false, nil
	}
	return false, fmt.Errorf("check session %q: %w", sessionID, err)
}

func observationHashByID(ctx context.Context, tx *sql.Tx, id int64) (string, error) {
	var hash string
	err := tx.QueryRowContext(ctx, `SELECT normalized_hash FROM observations WHERE id = ?`, id).Scan(&hash)
	if err == nil {
		return hash, nil
	}
	if err == sql.ErrNoRows {
		return "", nil
	}
	return "", fmt.Errorf("check observation %d: %w", id, err)
}

func scanPrompts(rows *sql.Rows) ([]Prompt, error) {
	var prompts []Prompt
	for rows.Next() {
		var prompt Prompt
		if err := rows.Scan(&prompt.ID, &prompt.SessionID, &prompt.Content, &prompt.Project, &prompt.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan prompt: %w", err)
		}
		prompts = append(prompts, prompt)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate prompts: %w", err)
	}
	return prompts, nil
}

func defaultString(value string, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}
