package taskstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/liliang/roma/internal/domain"
	"github.com/liliang/roma/internal/sqliteutil"
	"github.com/liliang/roma/internal/store"
)

// SQLiteStore persists task records into the shared workspace SQLite database.
type SQLiteStore struct {
	db *sql.DB
}

// NewSQLiteStore constructs a SQLite-backed task store.
func NewSQLiteStore(workDir string) (*SQLiteStore, error) {
	db, err := sqliteutil.Open(workDir)
	if err != nil {
		return nil, err
	}
	return &SQLiteStore{db: db}, nil
}

// UpsertTask persists a task record.
func (s *SQLiteStore) UpsertTask(ctx context.Context, task domain.TaskRecord) error {
	deps, err := json.Marshal(task.Dependencies)
	if err != nil {
		return fmt.Errorf("marshal dependencies: %w", err)
	}
	_, err = s.db.ExecContext(
		ctx,
		`INSERT OR REPLACE INTO task_records
		(id, session_id, title, strategy, state, agent_id, approval_granted, dependencies_json, artifact_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		task.ID,
		task.SessionID,
		task.Title,
		string(task.Strategy),
		string(task.State),
		task.AgentID,
		boolToInt(task.ApprovalGranted),
		string(deps),
		task.ArtifactID,
		task.CreatedAt.Format(time.RFC3339Nano),
		task.UpdatedAt.Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("insert task record: %w", err)
	}
	return nil
}

// GetTask loads one task record.
func (s *SQLiteStore) GetTask(ctx context.Context, taskID string) (domain.TaskRecord, error) {
	row := s.db.QueryRowContext(
		ctx,
		`SELECT id, session_id, title, strategy, state, agent_id, approval_granted, dependencies_json, artifact_id, created_at, updated_at
		 FROM task_records WHERE id = ?`,
		taskID,
	)
	record, err := scanTaskRecord(row)
	if err == sql.ErrNoRows {
		return domain.TaskRecord{}, store.ErrNotFound
	}
	return record, err
}

// ListTasksBySession returns all task records for a session.
func (s *SQLiteStore) ListTasksBySession(ctx context.Context, sessionID string) ([]domain.TaskRecord, error) {
	query := `SELECT id, session_id, title, strategy, state, agent_id, approval_granted, dependencies_json, artifact_id, created_at, updated_at
		FROM task_records`
	args := make([]any, 0, 1)
	if sessionID != "" {
		query += ` WHERE session_id = ?`
		args = append(args, sessionID)
	}
	query += ` ORDER BY created_at, id`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query task records: %w", err)
	}
	defer rows.Close()

	out := make([]domain.TaskRecord, 0)
	for rows.Next() {
		record, err := scanTaskRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate task records: %w", err)
	}
	return out, nil
}

// UpdateTaskState updates task state while preserving other fields.
func (s *SQLiteStore) UpdateTaskState(ctx context.Context, update store.TaskStateUpdate) error {
	record, err := s.GetTask(ctx, update.TaskID)
	if err != nil {
		return err
	}
	record.State = update.State
	record.UpdatedAt = time.Now().UTC()
	return s.UpsertTask(ctx, record)
}

func scanTaskRecord(scanner interface{ Scan(dest ...any) error }) (domain.TaskRecord, error) {
	var (
		record    domain.TaskRecord
		strategy  string
		state     string
		approval  int
		depsRaw   string
		createdAt string
		updatedAt string
	)
	if err := scanner.Scan(
		&record.ID,
		&record.SessionID,
		&record.Title,
		&strategy,
		&state,
		&record.AgentID,
		&approval,
		&depsRaw,
		&record.ArtifactID,
		&createdAt,
		&updatedAt,
	); err != nil {
		return domain.TaskRecord{}, fmt.Errorf("scan task record: %w", err)
	}
	record.Strategy = domain.TaskStrategy(strategy)
	record.State = domain.TaskState(state)
	record.ApprovalGranted = approval != 0
	if depsRaw != "" && depsRaw != "null" {
		if err := json.Unmarshal([]byte(depsRaw), &record.Dependencies); err != nil {
			return domain.TaskRecord{}, fmt.Errorf("unmarshal dependencies: %w", err)
		}
	}
	if err := record.CreatedAt.UnmarshalText([]byte(createdAt)); err != nil {
		return domain.TaskRecord{}, fmt.Errorf("parse created_at: %w", err)
	}
	if err := record.UpdatedAt.UnmarshalText([]byte(updatedAt)); err != nil {
		return domain.TaskRecord{}, fmt.Errorf("parse updated_at: %w", err)
	}
	return record, nil
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
