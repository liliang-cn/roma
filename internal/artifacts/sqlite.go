package artifacts

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/liliang/roma/internal/domain"
	"github.com/liliang/roma/internal/sqliteutil"
)

// SQLiteStore persists artifact envelopes into the shared workspace SQLite database.
type SQLiteStore struct {
	db *sql.DB
}

// NewSQLiteStore constructs a SQLite-backed artifact store.
func NewSQLiteStore(workDir string) (*SQLiteStore, error) {
	db, err := sqliteutil.Open(workDir)
	if err != nil {
		return nil, err
	}
	return &SQLiteStore{db: db}, nil
}

// Save persists an artifact envelope as JSON in SQLite.
func (s *SQLiteStore) Save(ctx context.Context, envelope domain.ArtifactEnvelope) error {
	raw, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("marshal artifact: %w", err)
	}
	_, err = s.db.ExecContext(
		ctx,
		`INSERT OR REPLACE INTO artifact_records
		(id, session_id, task_id, kind, created_at, envelope_json)
		VALUES (?, ?, ?, ?, ?, ?)`,
		envelope.ID,
		envelope.SessionID,
		envelope.TaskID,
		string(envelope.Kind),
		envelope.CreatedAt.Format(time.RFC3339Nano),
		string(raw),
	)
	if err != nil {
		return fmt.Errorf("insert artifact: %w", err)
	}
	return nil
}

// Get loads one artifact envelope.
func (s *SQLiteStore) Get(ctx context.Context, artifactID string) (domain.ArtifactEnvelope, error) {
	row := s.db.QueryRowContext(ctx, `SELECT envelope_json FROM artifact_records WHERE id = ?`, artifactID)
	var raw string
	if err := row.Scan(&raw); err != nil {
		return domain.ArtifactEnvelope{}, err
	}
	return decodeEnvelope(raw)
}

// List returns artifact envelopes, optionally filtered by session id.
func (s *SQLiteStore) List(ctx context.Context, sessionID string) ([]domain.ArtifactEnvelope, error) {
	query := `SELECT envelope_json FROM artifact_records`
	args := make([]any, 0, 1)
	if sessionID != "" {
		query += ` WHERE session_id = ?`
		args = append(args, sessionID)
	}
	query += ` ORDER BY created_at, id`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query artifacts: %w", err)
	}
	defer rows.Close()

	out := make([]domain.ArtifactEnvelope, 0)
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("scan artifact: %w", err)
		}
		envelope, err := decodeEnvelope(raw)
		if err != nil {
			return nil, err
		}
		out = append(out, envelope)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate artifacts: %w", err)
	}
	return out, nil
}

func decodeEnvelope(raw string) (domain.ArtifactEnvelope, error) {
	var envelope domain.ArtifactEnvelope
	if err := json.Unmarshal([]byte(raw), &envelope); err != nil {
		return domain.ArtifactEnvelope{}, fmt.Errorf("unmarshal artifact: %w", err)
	}
	return envelope, nil
}
