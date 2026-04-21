package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type UpdateObservationInput struct {
	ID       int64
	Title    string
	Content  string
	Type     string
	Project  string
	Scope    string
	TopicKey string
	ToolName string
}

type DeleteObservationInput struct {
	ID   int64
	Hard bool
}

type PromptInput struct {
	SessionID string
	Content   string
	Project   string
	CreatedAt time.Time
}

type Prompt struct {
	ID        int64  `json:"id"`
	SessionID string `json:"session_id"`
	Content   string `json:"content"`
	Project   string `json:"project"`
	CreatedAt string `json:"created_at"`
}

type ProjectStats struct {
	Project      string `json:"project"`
	Sessions     int    `json:"sessions"`
	Observations int    `json:"observations"`
	Prompts      int    `json:"prompts"`
}

func (s *Store) UpdateObservation(ctx context.Context, input UpdateObservationInput) (Observation, error) {
	if input.ID <= 0 {
		return Observation{}, fmt.Errorf("observation id is required")
	}

	current, err := s.GetObservation(ctx, input.ID)
	if err != nil {
		return Observation{}, err
	}
	if current.DeletedAt != "" {
		return Observation{}, fmt.Errorf("observation %d is deleted", input.ID)
	}

	title := strings.TrimSpace(input.Title)
	if title == "" {
		title = current.Title
	}
	content := strings.TrimSpace(input.Content)
	if content == "" {
		content = current.Content
	} else {
		content = stripPrivateTags(content)
	}
	observationType := normalizeObservationType(input.Type)
	if strings.TrimSpace(input.Type) == "" {
		observationType = current.Type
	}
	project, err := s.ResolveProject(ctx, input.Project)
	if err != nil {
		return Observation{}, err
	}
	if project == "" {
		project = current.Project
	}
	scope := normalizeScope(input.Scope)
	if strings.TrimSpace(input.Scope) == "" {
		scope = current.Scope
	}
	topicKey := strings.TrimSpace(input.TopicKey)
	if strings.TrimSpace(input.TopicKey) == "" {
		topicKey = current.TopicKey
	}
	toolName := strings.TrimSpace(input.ToolName)
	if toolName == "" {
		toolName = current.ToolName
	}

	now := time.Now().UTC().Format(time.RFC3339)
	hash := normalizedHash(observationType, project, scope, title, content, topicKey)
	_, err = s.db.ExecContext(ctx, `
		UPDATE observations
		SET type = ?, title = ?, content = ?, tool_name = ?, project = ?, scope = ?,
			topic_key = ?, normalized_hash = ?, revision_count = revision_count + 1,
			updated_at = ?
		WHERE id = ? AND deleted_at IS NULL
	`, observationType, title, content, nullIfBlank(toolName), project, scope, nullIfBlank(topicKey), hash, now, input.ID)
	if err != nil {
		return Observation{}, fmt.Errorf("update observation: %w", err)
	}

	return s.GetObservation(ctx, input.ID)
}

func (s *Store) DeleteObservation(ctx context.Context, input DeleteObservationInput) error {
	if input.ID <= 0 {
		return fmt.Errorf("observation id is required")
	}

	var err error
	if input.Hard {
		_, err = s.db.ExecContext(ctx, `DELETE FROM observations WHERE id = ?`, input.ID)
	} else {
		_, err = s.db.ExecContext(ctx, `
			UPDATE observations
			SET deleted_at = ?, updated_at = ?
			WHERE id = ? AND deleted_at IS NULL
		`, time.Now().UTC().Format(time.RFC3339), time.Now().UTC().Format(time.RFC3339), input.ID)
	}
	if err != nil {
		return fmt.Errorf("delete observation: %w", err)
	}

	return nil
}

func (s *Store) SavePrompt(ctx context.Context, input PromptInput) (Prompt, error) {
	if strings.TrimSpace(input.Content) == "" {
		return Prompt{}, fmt.Errorf("prompt content is required")
	}

	project, err := s.ResolveProject(ctx, input.Project)
	if err != nil {
		return Prompt{}, err
	}
	if project == "" {
		project = "default"
	}
	timestamp := input.CreatedAt.UTC()
	if timestamp.IsZero() {
		timestamp = time.Now().UTC()
	}

	result, err := s.db.ExecContext(ctx, `
		INSERT INTO user_prompts (session_id, content, project, created_at)
		VALUES (?, ?, ?, ?)
	`, nullIfBlank(input.SessionID), stripPrivateTags(input.Content), project, timestamp.Format(time.RFC3339))
	if err != nil {
		return Prompt{}, fmt.Errorf("save prompt: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return Prompt{}, fmt.Errorf("save prompt last insert id: %w", err)
	}

	return s.GetPrompt(ctx, id)
}

func (s *Store) GetPrompt(ctx context.Context, id int64) (Prompt, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, COALESCE(session_id, ''), content, project, created_at
		FROM user_prompts
		WHERE id = ?
	`, id)

	var prompt Prompt
	if err := row.Scan(&prompt.ID, &prompt.SessionID, &prompt.Content, &prompt.Project, &prompt.CreatedAt); err != nil {
		return Prompt{}, fmt.Errorf("get prompt %d: %w", id, err)
	}

	return prompt, nil
}

func (s *Store) ListPrompts(ctx context.Context, project string, limit int) ([]Prompt, error) {
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
		SELECT id, COALESCE(session_id, ''), content, project, created_at
		FROM user_prompts
		WHERE (? = '' OR project = ?)
		ORDER BY created_at DESC, id DESC
		LIMIT ?
	`, project, project, limit)
	if err != nil {
		return nil, fmt.Errorf("list prompts: %w", err)
	}
	defer rows.Close()

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

func (s *Store) CountSessionPrompts(ctx context.Context, sessionID string) (int, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return 0, fmt.Errorf("session id is required")
	}

	var count int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM user_prompts
		WHERE COALESCE(session_id, '') = ?
	`, sessionID).Scan(&count); err != nil {
		return 0, fmt.Errorf("count session prompts: %w", err)
	}

	return count, nil
}

func (s *Store) TimelineAroundObservation(ctx context.Context, observationID int64, before int, after int) (map[string]any, error) {
	center, err := s.GetObservation(ctx, observationID)
	if err != nil {
		return nil, err
	}
	if center.DeletedAt != "" {
		return nil, fmt.Errorf("observation %d is deleted", observationID)
	}
	if before <= 0 {
		before = 5
	}
	if after <= 0 {
		after = 5
	}

	prevRows, err := s.db.QueryContext(ctx, `
		SELECT id, COALESCE(session_id, ''), type, title, content, COALESCE(tool_name, ''),
			project, scope, COALESCE(topic_key, ''), normalized_hash,
			revision_count, duplicate_count, COALESCE(last_seen_at, ''),
			created_at, updated_at, COALESCE(deleted_at, '')
		FROM observations
		WHERE deleted_at IS NULL
		  AND COALESCE(session_id, '') = ?
		  AND (created_at < ? OR (created_at = ? AND id < ?))
		ORDER BY created_at DESC, id DESC
		LIMIT ?
	`, center.SessionID, center.CreatedAt, center.CreatedAt, center.ID, before)
	if err != nil {
		return nil, fmt.Errorf("timeline before: %w", err)
	}
	defer prevRows.Close()
	beforeItems, err := scanObservations(prevRows)
	if err != nil {
		return nil, err
	}

	nextRows, err := s.db.QueryContext(ctx, `
		SELECT id, COALESCE(session_id, ''), type, title, content, COALESCE(tool_name, ''),
			project, scope, COALESCE(topic_key, ''), normalized_hash,
			revision_count, duplicate_count, COALESCE(last_seen_at, ''),
			created_at, updated_at, COALESCE(deleted_at, '')
		FROM observations
		WHERE deleted_at IS NULL
		  AND COALESCE(session_id, '') = ?
		  AND (created_at > ? OR (created_at = ? AND id > ?))
		ORDER BY created_at ASC, id ASC
		LIMIT ?
	`, center.SessionID, center.CreatedAt, center.CreatedAt, center.ID, after)
	if err != nil {
		return nil, fmt.Errorf("timeline after: %w", err)
	}
	defer nextRows.Close()
	afterItems, err := scanObservations(nextRows)
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"observation": center,
		"before":      beforeItems,
		"after":       afterItems,
	}, nil
}

func (s *Store) MergeProjects(ctx context.Context, sources []string, target string) (map[string]any, error) {
	target, err := s.ResolveProject(ctx, target)
	if err != nil {
		return nil, err
	}
	if target == "" {
		return nil, fmt.Errorf("target project is required")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin merge projects: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	updated := map[string]int64{}
	for _, source := range sources {
		source = normalizeProject(source)
		if source == "" || source == target {
			continue
		}
		now := time.Now().UTC().Format(time.RFC3339)

		sessionResult, err := tx.ExecContext(ctx, `UPDATE sessions SET project = ? WHERE project = ?`, target, source)
		if err != nil {
			return nil, fmt.Errorf("merge sessions for %s: %w", source, err)
		}
		observationResult, err := tx.ExecContext(ctx, `UPDATE observations SET project = ?, updated_at = ? WHERE project = ?`, target, now, source)
		if err != nil {
			return nil, fmt.Errorf("merge observations for %s: %w", source, err)
		}
		promptResult, err := tx.ExecContext(ctx, `UPDATE user_prompts SET project = ? WHERE project = ?`, target, source)
		if err != nil {
			return nil, fmt.Errorf("merge prompts for %s: %w", source, err)
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE project_aliases
			SET target = ?, updated_at = ?
			WHERE target = ?
		`, target, now, source); err != nil {
			return nil, fmt.Errorf("retarget aliases for %s: %w", source, err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO project_aliases (alias, target, created_at, updated_at)
			VALUES (?, ?, ?, ?)
			ON CONFLICT(alias) DO UPDATE SET
				target = excluded.target,
				updated_at = excluded.updated_at
		`, source, target, now, now); err != nil {
			return nil, fmt.Errorf("set project alias for %s: %w", source, err)
		}

		count := int64(0)
		if rows, err := sessionResult.RowsAffected(); err == nil {
			count += rows
		}
		if rows, err := observationResult.RowsAffected(); err == nil {
			count += rows
		}
		if rows, err := promptResult.RowsAffected(); err == nil {
			count += rows
		}
		updated[source] = count
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit merge projects: %w", err)
	}

	return map[string]any{"target": target, "updated": updated}, nil
}

func (s *Store) SetProjectAliases(ctx context.Context, aliases []string, target string) (map[string]any, error) {
	target, err := s.ResolveProject(ctx, target)
	if err != nil {
		return nil, err
	}
	if target == "" {
		return nil, fmt.Errorf("target project is required")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin set project aliases: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	now := time.Now().UTC().Format(time.RFC3339)
	updated := map[string]string{}
	for _, alias := range aliases {
		alias = normalizeProject(alias)
		if alias == "" || alias == target {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO project_aliases (alias, target, created_at, updated_at)
			VALUES (?, ?, ?, ?)
			ON CONFLICT(alias) DO UPDATE SET
				target = excluded.target,
				updated_at = excluded.updated_at
		`, alias, target, now, now); err != nil {
			return nil, fmt.Errorf("set project alias %s: %w", alias, err)
		}
		updated[alias] = target
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit set project aliases: %w", err)
	}

	return map[string]any{"target": target, "aliases": updated}, nil
}

func (s *Store) ListProjectAliases(ctx context.Context) ([]ProjectAlias, error) {
	return s.listProjectAliases(ctx, "")
}

func (s *Store) listProjectAliases(ctx context.Context, project string) ([]ProjectAlias, error) {
	project, err := s.ResolveProject(ctx, project)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT alias, target, created_at, updated_at
		FROM project_aliases
		WHERE (? = '' OR alias = ? OR target = ?)
		ORDER BY alias
	`, project, project, project)
	if err != nil {
		return nil, fmt.Errorf("list project aliases: %w", err)
	}
	defer rows.Close()

	var aliases []ProjectAlias
	for rows.Next() {
		var alias ProjectAlias
		if err := rows.Scan(&alias.Alias, &alias.Target, &alias.CreatedAt, &alias.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan project alias: %w", err)
		}
		aliases = append(aliases, alias)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate project aliases: %w", err)
	}

	return aliases, nil
}

func (s *Store) ListProjects(ctx context.Context) ([]ProjectStats, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT project,
			SUM(sessions) AS sessions,
			SUM(observations) AS observations,
			SUM(prompts) AS prompts
		FROM (
			SELECT project, COUNT(*) AS sessions, 0 AS observations, 0 AS prompts FROM sessions GROUP BY project
			UNION ALL
			SELECT project, 0, COUNT(*), 0 FROM observations WHERE deleted_at IS NULL GROUP BY project
			UNION ALL
			SELECT project, 0, 0, COUNT(*) FROM user_prompts GROUP BY project
		)
		GROUP BY project
		ORDER BY project
	`)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	defer rows.Close()

	var projects []ProjectStats
	for rows.Next() {
		var project ProjectStats
		if err := rows.Scan(&project.Project, &project.Sessions, &project.Observations, &project.Prompts); err != nil {
			return nil, fmt.Errorf("scan project stats: %w", err)
		}
		projects = append(projects, project)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate project stats: %w", err)
	}

	return projects, nil
}

func scanObservations(rows *sql.Rows) ([]Observation, error) {
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
			return nil, fmt.Errorf("scan observation: %w", err)
		}
		observations = append(observations, observation)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate observations: %w", err)
	}

	return observations, nil
}
