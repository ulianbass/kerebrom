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
	App               string             `json:"app"`
	Schema            int                `json:"schema"`
	ExportedAt        string             `json:"exported_at"`
	ProjectAliases    []ProjectAlias     `json:"project_aliases"`
	Sessions          []Session          `json:"sessions"`
	Observations      []Observation      `json:"observations"`
	ObservationEvents []ObservationEvent `json:"observation_events"`
	Prompts           []Prompt           `json:"prompts"`
}

type ImportSummary struct {
	Sessions     int `json:"sessions"`
	Observations int `json:"observations"`
	Prompts      int `json:"prompts"`
	Aliases      int `json:"aliases"`
	Events       int `json:"events"`
}

func (s *Store) ExportData(ctx context.Context, opts ExportOptions) (ExportData, error) {
	project, err := s.ResolveProject(ctx, opts.Project)
	if err != nil {
		return ExportData{}, err
	}
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
	events, err := s.listObservationEventsForExport(ctx, project, since)
	if err != nil {
		return ExportData{}, err
	}
	observations, err = s.appendEventParentObservations(ctx, observations, events)
	if err != nil {
		return ExportData{}, err
	}
	aliases, err := s.listProjectAliases(ctx, project)
	if err != nil {
		return ExportData{}, err
	}

	return ExportData{
		App:               "kerebrom",
		Schema:            2,
		ExportedAt:        time.Now().UTC().Format(time.RFC3339),
		ProjectAliases:    aliases,
		Sessions:          sessions,
		Observations:      observations,
		ObservationEvents: events,
		Prompts:           prompts,
	}, nil
}

func (s *Store) ListSessions(ctx context.Context, project string, limit int) ([]Session, error) {
	resolvedProject, err := s.ResolveProject(ctx, project)
	if err != nil {
		return nil, err
	}
	project = resolvedProject
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
	for _, alias := range data.ProjectAliases {
		aliasName := normalizeProject(alias.Alias)
		targetName, err := resolveProject(ctx, tx, alias.Target)
		if err != nil {
			return ImportSummary{}, err
		}
		if aliasName == "" || targetName == "" || aliasName == targetName {
			continue
		}
		now := time.Now().UTC().Format(time.RFC3339)
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO project_aliases (alias, target, created_at, updated_at)
			VALUES (?, ?, ?, ?)
			ON CONFLICT(alias) DO UPDATE SET
				target = excluded.target,
				updated_at = excluded.updated_at
		`, aliasName, targetName, defaultString(alias.CreatedAt, now), defaultString(alias.UpdatedAt, now)); err != nil {
			return ImportSummary{}, fmt.Errorf("import project alias %q: %w", alias.Alias, err)
		}
		summary.Aliases++
	}

	for _, session := range data.Sessions {
		if strings.TrimSpace(session.ID) == "" {
			continue
		}
		project, err := resolveProject(ctx, tx, session.Project)
		if err != nil {
			return ImportSummary{}, err
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
			defaultString(project, "default"),
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

	importedObservationIDs := map[int64]int64{}
	for _, observation := range data.Observations {
		if strings.TrimSpace(observation.Title) == "" || strings.TrimSpace(observation.Content) == "" {
			continue
		}
		importedID, err := s.importObservation(ctx, tx, observation)
		if err != nil {
			return ImportSummary{}, err
		}
		if observation.ID > 0 && importedID > 0 {
			importedObservationIDs[observation.ID] = importedID
		}
		summary.Observations++
	}

	for _, event := range data.ObservationEvents {
		observationID := event.ObservationID
		if mappedID := importedObservationIDs[event.ObservationID]; mappedID > 0 {
			observationID = mappedID
		} else {
			exists, err := observationExists(ctx, tx, observationID)
			if err != nil {
				return ImportSummary{}, err
			}
			if !exists {
				continue
			}
		}
		if observationID <= 0 {
			continue
		}
		event.ObservationID = observationID
		if event.RelatedObservationID > 0 {
			if mappedID := importedObservationIDs[event.RelatedObservationID]; mappedID > 0 {
				event.RelatedObservationID = mappedID
			} else {
				exists, err := observationExists(ctx, tx, event.RelatedObservationID)
				if err != nil {
					return ImportSummary{}, err
				}
				if !exists {
					event.RelatedObservationID = 0
				}
			}
		}
		if err := insertObservationEvent(ctx, tx, event); err != nil {
			return ImportSummary{}, err
		}
		summary.Events++
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
	resolvedProject, err := s.ResolveProject(ctx, project)
	if err != nil {
		return nil, err
	}
	project = resolvedProject
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
			created_at, updated_at, COALESCE(valid_at, created_at), COALESCE(deleted_at, '')
		FROM observations
		WHERE (? = '' OR project = ?)
		  AND (? = '' OR created_at > ? OR updated_at > ? OR COALESCE(valid_at, created_at) > ? OR COALESCE(last_seen_at, '') > ? OR COALESCE(deleted_at, '') > ?)
		ORDER BY COALESCE(valid_at, created_at) DESC, id DESC
	`, project, project, since, since, since, since, since, since)
	if err != nil {
		return nil, fmt.Errorf("list observations for export: %w", err)
	}
	defer rows.Close()

	return scanObservations(rows)
}

func (s *Store) listObservationEventsForExport(ctx context.Context, project string, since string) ([]ObservationEvent, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			e.id, e.observation_id, e.event_type, COALESCE(e.actor, ''), COALESCE(e.reason, ''),
			COALESCE(e.related_observation_id, 0), e.created_at
		FROM observation_events e
		JOIN observations o ON o.id = e.observation_id
		WHERE (? = '' OR o.project = ?)
		  AND (? = '' OR e.created_at > ? OR o.created_at > ? OR o.updated_at > ? OR COALESCE(o.valid_at, o.created_at) > ? OR COALESCE(o.deleted_at, '') > ?)
		ORDER BY e.created_at DESC, e.id DESC
	`, project, project, since, since, since, since, since, since)
	if err != nil {
		return nil, fmt.Errorf("list observation events for export: %w", err)
	}
	defer rows.Close()

	return scanObservationEvents(rows)
}

func (s *Store) appendEventParentObservations(ctx context.Context, observations []Observation, events []ObservationEvent) ([]Observation, error) {
	seen := map[int64]bool{}
	for _, observation := range observations {
		if observation.ID > 0 {
			seen[observation.ID] = true
		}
	}

	for _, event := range events {
		if event.ObservationID <= 0 || seen[event.ObservationID] {
			continue
		}
		observation, err := s.GetObservation(ctx, event.ObservationID)
		if err != nil {
			return nil, fmt.Errorf("load event parent observation %d: %w", event.ObservationID, err)
		}
		observations = append(observations, observation)
		seen[event.ObservationID] = true
	}

	return observations, nil
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
	project, err := resolveProject(ctx, tx, observation.Project)
	if err != nil {
		return 0, err
	}
	project = defaultString(project, "default")
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

	if strings.TrimSpace(observation.DeletedAt) == "" && hash != "" {
		duplicateID, err := activeObservationIDByHash(ctx, tx, hash)
		if err != nil {
			return 0, err
		}
		if duplicateID > 0 && duplicateID != idToUse {
			if err := bumpImportedDuplicateObservation(ctx, tx, duplicateID); err != nil {
				return 0, err
			}
			return duplicateID, nil
		}
	}

	if idToUse > 0 {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO observations (
				id, session_id, type, title, content, tool_name, project, scope, topic_key,
				normalized_hash, revision_count, duplicate_count, last_seen_at, created_at, updated_at, valid_at, deleted_at
			)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
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
				valid_at = excluded.valid_at,
				deleted_at = excluded.deleted_at
		`, idToUse, nullIfBlank(sessionID), observationType, title, content, nullIfBlank(observation.ToolName), project, scope, nullIfBlank(topicKey), hash,
			observation.RevisionCount, observation.DuplicateCount, nullIfBlank(observation.LastSeenAt),
			defaultString(observation.CreatedAt, time.Now().UTC().Format(time.RFC3339)),
			defaultString(observation.UpdatedAt, time.Now().UTC().Format(time.RFC3339)),
			defaultString(observation.ValidAt, defaultString(observation.CreatedAt, time.Now().UTC().Format(time.RFC3339))),
			nullIfBlank(observation.DeletedAt))
		if err != nil {
			return 0, fmt.Errorf("import observation %d: %w", observation.ID, err)
		}
		_ = insertObservationEvent(ctx, tx, ObservationEvent{
			ObservationID: idToUse,
			EventType:     "imported",
			Actor:         "kerebrom_import",
			Reason:        "Observation imported or refreshed from export data.",
			CreatedAt:     time.Now().UTC().Format(time.RFC3339),
		})
		return idToUse, nil
	}

	result, err := tx.ExecContext(ctx, `
		INSERT INTO observations (
			session_id, type, title, content, tool_name, project, scope, topic_key,
			normalized_hash, revision_count, duplicate_count, last_seen_at, created_at, updated_at, valid_at, deleted_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, nullIfBlank(sessionID), observationType, title, content, nullIfBlank(observation.ToolName), project, scope, nullIfBlank(topicKey), hash,
		observation.RevisionCount, observation.DuplicateCount, nullIfBlank(observation.LastSeenAt),
		defaultString(observation.CreatedAt, time.Now().UTC().Format(time.RFC3339)),
		defaultString(observation.UpdatedAt, time.Now().UTC().Format(time.RFC3339)),
		defaultString(observation.ValidAt, defaultString(observation.CreatedAt, time.Now().UTC().Format(time.RFC3339))),
		nullIfBlank(observation.DeletedAt))
	if err != nil {
		return 0, fmt.Errorf("import observation %d: %w", observation.ID, err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	_ = insertObservationEvent(ctx, tx, ObservationEvent{
		ObservationID: id,
		EventType:     "imported",
		Actor:         "kerebrom_import",
		Reason:        "Observation imported from export data.",
		CreatedAt:     time.Now().UTC().Format(time.RFC3339),
	})
	return id, nil
}

func activeObservationIDByHash(ctx context.Context, tx *sql.Tx, hash string) (int64, error) {
	hash = strings.TrimSpace(hash)
	if hash == "" {
		return 0, nil
	}
	var id int64
	err := tx.QueryRowContext(ctx, `
		SELECT id
		FROM observations
		WHERE normalized_hash = ? AND deleted_at IS NULL
		ORDER BY updated_at DESC, id DESC
		LIMIT 1
	`, hash).Scan(&id)
	if err == nil {
		return id, nil
	}
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return 0, fmt.Errorf("find duplicate import observation: %w", err)
}

func bumpImportedDuplicateObservation(ctx context.Context, tx *sql.Tx, id int64) error {
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := tx.ExecContext(ctx, `
		UPDATE observations
		SET duplicate_count = duplicate_count + 1, last_seen_at = ?, updated_at = ?, valid_at = ?
		WHERE id = ? AND deleted_at IS NULL
	`, now, now, now, id); err != nil {
		return fmt.Errorf("update duplicate import observation: %w", err)
	}
	return insertObservationEvent(ctx, tx, ObservationEvent{
		ObservationID: id,
		EventType:     "reasserted",
		Actor:         "kerebrom_import",
		Reason:        "Imported durable memory matched an existing active normalized_hash; duplicate_count and valid_at refreshed.",
		CreatedAt:     now,
	})
}

func (s *Store) importPrompt(ctx context.Context, tx *sql.Tx, prompt Prompt) error {
	project, err := resolveProject(ctx, tx, prompt.Project)
	if err != nil {
		return err
	}
	project = defaultString(project, "default")

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
		`, prompt.ID, nullIfBlank(sessionID), stripPrivateTags(prompt.Content), project, defaultString(prompt.CreatedAt, time.Now().UTC().Format(time.RFC3339)))
		if err != nil {
			return fmt.Errorf("import prompt %d: %w", prompt.ID, err)
		}
		return nil
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO user_prompts (session_id, content, project, created_at)
		VALUES (?, ?, ?, ?)
	`, nullIfBlank(sessionID), stripPrivateTags(prompt.Content), project, defaultString(prompt.CreatedAt, time.Now().UTC().Format(time.RFC3339)))
	if err != nil {
		return fmt.Errorf("import prompt: %w", err)
	}
	return nil
}

func observationExists(ctx context.Context, tx *sql.Tx, observationID int64) (bool, error) {
	if observationID <= 0 {
		return false, nil
	}
	var id int64
	err := tx.QueryRowContext(ctx, `SELECT id FROM observations WHERE id = ?`, observationID).Scan(&id)
	if err == nil {
		return true, nil
	}
	if err == sql.ErrNoRows {
		return false, nil
	}
	return false, fmt.Errorf("check observation %d: %w", observationID, err)
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
