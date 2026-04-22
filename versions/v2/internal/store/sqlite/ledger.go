package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type observationEventWriter interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func (s *Store) recordObservationEvent(ctx context.Context, observationID int64, eventType string, actor string, reason string, relatedObservationID int64) error {
	return insertObservationEvent(ctx, s.db, ObservationEvent{
		ObservationID:        observationID,
		EventType:            eventType,
		Actor:                actor,
		Reason:               reason,
		RelatedObservationID: relatedObservationID,
		CreatedAt:            time.Now().UTC().Format(time.RFC3339),
	})
}

func insertObservationEvent(ctx context.Context, writer observationEventWriter, event ObservationEvent) error {
	if event.ObservationID <= 0 {
		return fmt.Errorf("observation event observation_id is required")
	}
	event.EventType = strings.TrimSpace(event.EventType)
	if event.EventType == "" {
		return fmt.Errorf("observation event type is required")
	}
	if strings.TrimSpace(event.CreatedAt) == "" {
		event.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}

	_, err := writer.ExecContext(ctx, `
		INSERT INTO observation_events (
			observation_id, event_type, actor, reason, related_observation_id, created_at
		)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT DO NOTHING
	`,
		event.ObservationID,
		event.EventType,
		nullIfBlank(event.Actor),
		nullIfBlank(event.Reason),
		nullInt64IfZero(event.RelatedObservationID),
		event.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("record observation event: %w", err)
	}
	return nil
}

func (s *Store) ListObservationEvents(ctx context.Context, observationID int64, limit int) ([]ObservationEvent, error) {
	if observationID <= 0 {
		return nil, fmt.Errorf("observation id is required")
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > 200 {
		limit = 200
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT
			id, observation_id, event_type, COALESCE(actor, ''), COALESCE(reason, ''),
			COALESCE(related_observation_id, 0), created_at
		FROM observation_events
		WHERE observation_id = ?
		ORDER BY created_at DESC, id DESC
		LIMIT ?
	`, observationID, limit)
	if err != nil {
		return nil, fmt.Errorf("list observation events: %w", err)
	}
	defer rows.Close()

	return scanObservationEvents(rows)
}

func (s *Store) ObservationEventCount(ctx context.Context) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM observation_events`).Scan(&count); err != nil {
		return 0, fmt.Errorf("count observation events: %w", err)
	}
	return count, nil
}

func scanObservationEvents(rows *sql.Rows) ([]ObservationEvent, error) {
	var events []ObservationEvent
	for rows.Next() {
		var event ObservationEvent
		if err := rows.Scan(
			&event.ID,
			&event.ObservationID,
			&event.EventType,
			&event.Actor,
			&event.Reason,
			&event.RelatedObservationID,
			&event.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan observation event: %w", err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate observation events: %w", err)
	}
	return events, nil
}

func nullInt64IfZero(value int64) any {
	if value == 0 {
		return nil
	}
	return value
}
