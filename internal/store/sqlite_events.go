package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/liliang/roma/internal/events"
	"github.com/liliang/roma/internal/sqliteutil"
)

// SQLiteEventStore persists events into the shared workspace SQLite database.
type SQLiteEventStore struct {
	db *sql.DB
}

// NewSQLiteEventStore constructs a SQLite-backed event store.
func NewSQLiteEventStore(workDir string) (*SQLiteEventStore, error) {
	db, err := sqliteutil.Open(workDir)
	if err != nil {
		return nil, err
	}
	return &SQLiteEventStore{db: db}, nil
}

// AppendEvent appends an immutable event into SQLite.
func (s *SQLiteEventStore) AppendEvent(ctx context.Context, event events.Record) error {
	payload, err := json.Marshal(event.Payload)
	if err != nil {
		return fmt.Errorf("marshal event payload: %w", err)
	}
	_, err = s.db.ExecContext(
		ctx,
		`INSERT OR REPLACE INTO event_records
		(id, session_id, task_id, type, actor_type, occurred_at, reason_code, payload_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		event.ID,
		event.SessionID,
		event.TaskID,
		string(event.Type),
		string(event.ActorType),
		event.OccurredAt.Format("2006-01-02T15:04:05.999999999Z07:00"),
		event.ReasonCode,
		string(payload),
	)
	if err != nil {
		return fmt.Errorf("insert event: %w", err)
	}
	return nil
}

// ListEvents scans and filters persisted events from SQLite.
func (s *SQLiteEventStore) ListEvents(ctx context.Context, filter EventFilter) ([]events.Record, error) {
	query := `SELECT id, session_id, task_id, type, actor_type, occurred_at, reason_code, payload_json
		FROM event_records WHERE 1=1`
	args := make([]any, 0, 3)
	if filter.SessionID != "" {
		query += ` AND session_id = ?`
		args = append(args, filter.SessionID)
	}
	if filter.TaskID != "" {
		query += ` AND task_id = ?`
		args = append(args, filter.TaskID)
	}
	if filter.Type != "" {
		query += ` AND type = ?`
		args = append(args, string(filter.Type))
	}
	query += ` ORDER BY occurred_at, id`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query events: %w", err)
	}
	defer rows.Close()

	out := make([]events.Record, 0)
	for rows.Next() {
		var (
			item       events.Record
			eventType  string
			actorType  string
			occurredAt string
			payloadRaw string
		)
		if err := rows.Scan(&item.ID, &item.SessionID, &item.TaskID, &eventType, &actorType, &occurredAt, &item.ReasonCode, &payloadRaw); err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}
		item.Type = events.Type(eventType)
		item.ActorType = events.ActorType(actorType)
		if err := item.OccurredAt.UnmarshalText([]byte(occurredAt)); err != nil {
			return nil, fmt.Errorf("parse event timestamp: %w", err)
		}
		if payloadRaw != "" && payloadRaw != "null" {
			if err := json.Unmarshal([]byte(payloadRaw), &item.Payload); err != nil {
				return nil, fmt.Errorf("unmarshal event payload: %w", err)
			}
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate events: %w", err)
	}
	return out, nil
}
