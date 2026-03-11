package history

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/liliang/roma/internal/sqliteutil"
)

// SQLiteStore persists session history into the shared workspace SQLite database.
type SQLiteStore struct {
	db *sql.DB
}

// NewSQLiteStore constructs a SQLite-backed session history store.
func NewSQLiteStore(workDir string) (*SQLiteStore, error) {
	db, err := sqliteutil.Open(workDir)
	if err != nil {
		return nil, err
	}
	return &SQLiteStore{db: db}, nil
}

// Save persists the session record.
func (s *SQLiteStore) Save(ctx context.Context, record SessionRecord) error {
	delegates, err := json.Marshal(record.Delegates)
	if err != nil {
		return fmt.Errorf("marshal delegates: %w", err)
	}
	artifacts, err := json.Marshal(record.ArtifactIDs)
	if err != nil {
		return fmt.Errorf("marshal artifact ids: %w", err)
	}
	_, err = s.db.ExecContext(
		ctx,
		`INSERT OR REPLACE INTO session_history
		(id, task_id, prompt, starter, delegates_json, working_dir, status, artifact_ids_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		record.ID,
		record.TaskID,
		record.Prompt,
		record.Starter,
		string(delegates),
		record.WorkingDir,
		record.Status,
		string(artifacts),
		record.CreatedAt.Format(time.RFC3339Nano),
		record.UpdatedAt.Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("insert session history: %w", err)
	}
	return nil
}

// Get loads one session record.
func (s *SQLiteStore) Get(ctx context.Context, sessionID string) (SessionRecord, error) {
	row := s.db.QueryRowContext(
		ctx,
		`SELECT id, task_id, prompt, starter, delegates_json, working_dir, status, artifact_ids_json, created_at, updated_at
		 FROM session_history WHERE id = ?`,
		sessionID,
	)
	return scanSessionRecord(row)
}

// List returns all persisted session records.
func (s *SQLiteStore) List(ctx context.Context) ([]SessionRecord, error) {
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT id, task_id, prompt, starter, delegates_json, working_dir, status, artifact_ids_json, created_at, updated_at
		 FROM session_history ORDER BY created_at`,
	)
	if err != nil {
		return nil, fmt.Errorf("query session history: %w", err)
	}
	defer rows.Close()

	out := make([]SessionRecord, 0)
	for rows.Next() {
		record, err := scanSessionRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate session history: %w", err)
	}
	return out, nil
}

// RecoverInterrupted marks stale running sessions as failed-recoverable.
func (s *SQLiteStore) RecoverInterrupted(ctx context.Context) error {
	_, err := s.db.ExecContext(
		ctx,
		`UPDATE session_history SET status = ?, updated_at = ? WHERE status = ?`,
		"failed_recoverable",
		time.Now().UTC().Format(time.RFC3339Nano),
		"running",
	)
	if err != nil {
		return fmt.Errorf("recover interrupted sessions: %w", err)
	}
	return nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanSessionRecord(scanner rowScanner) (SessionRecord, error) {
	var (
		record       SessionRecord
		delegatesRaw string
		artifactsRaw string
		createdAt    string
		updatedAt    string
	)
	if err := scanner.Scan(
		&record.ID,
		&record.TaskID,
		&record.Prompt,
		&record.Starter,
		&delegatesRaw,
		&record.WorkingDir,
		&record.Status,
		&artifactsRaw,
		&createdAt,
		&updatedAt,
	); err != nil {
		return SessionRecord{}, fmt.Errorf("scan session history: %w", err)
	}
	if delegatesRaw != "" && delegatesRaw != "null" {
		if err := json.Unmarshal([]byte(delegatesRaw), &record.Delegates); err != nil {
			return SessionRecord{}, fmt.Errorf("unmarshal delegates: %w", err)
		}
	}
	if artifactsRaw != "" && artifactsRaw != "null" {
		if err := json.Unmarshal([]byte(artifactsRaw), &record.ArtifactIDs); err != nil {
			return SessionRecord{}, fmt.Errorf("unmarshal artifact ids: %w", err)
		}
	}
	if err := record.CreatedAt.UnmarshalText([]byte(createdAt)); err != nil {
		return SessionRecord{}, fmt.Errorf("parse created_at: %w", err)
	}
	if err := record.UpdatedAt.UnmarshalText([]byte(updatedAt)); err != nil {
		return SessionRecord{}, fmt.Errorf("parse updated_at: %w", err)
	}
	return record, nil
}
