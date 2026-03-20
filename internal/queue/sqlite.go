package queue

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/liliang-cn/roma/internal/sqliteutil"
)

// SQLiteStore persists queue requests into the shared workspace SQLite database.
type SQLiteStore struct {
	db *sql.DB
}

// NewSQLiteStore constructs a SQLite-backed queue store.
func NewSQLiteStore(workDir string) (*SQLiteStore, error) {
	db, err := sqliteutil.Open(workDir)
	if err != nil {
		return nil, err
	}
	return &SQLiteStore{db: db}, nil
}

// Enqueue creates a pending request.
func (s *SQLiteStore) Enqueue(ctx context.Context, req Request) error {
	req.Status = StatusPending
	req.CreatedAt = time.Now().UTC()
	req.UpdatedAt = req.CreatedAt
	return s.save(ctx, req)
}

// Update overwrites a request state.
func (s *SQLiteStore) Update(ctx context.Context, req Request) error {
	req.UpdatedAt = time.Now().UTC()
	return s.save(ctx, req)
}

// UpsertExact persists the request without mutating timestamps or status.
func (s *SQLiteStore) UpsertExact(ctx context.Context, req Request) error {
	return s.save(ctx, req)
}

// Get loads one request by id.
func (s *SQLiteStore) Get(ctx context.Context, id string) (Request, error) {
	row := s.db.QueryRowContext(
		ctx,
		`SELECT id, graph_file, graph_json, prompt, mode, starter_agent, delegates_json, working_dir, continuous, max_rounds, session_id, task_id, artifact_ids_json, policy_override, policy_override_actor, status, created_at, updated_at, error
		 FROM queue_requests WHERE id = ?`,
		id,
	)
	return scanRequest(row)
}

// List returns all queue requests.
func (s *SQLiteStore) List(ctx context.Context) ([]Request, error) {
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT id, graph_file, graph_json, prompt, mode, starter_agent, delegates_json, working_dir, continuous, max_rounds, session_id, task_id, artifact_ids_json, policy_override, policy_override_actor, status, created_at, updated_at, error
		 FROM queue_requests ORDER BY created_at`,
	)
	if err != nil {
		return nil, fmt.Errorf("query queue requests: %w", err)
	}
	defer rows.Close()

	out := make([]Request, 0)
	for rows.Next() {
		record, err := scanRequest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate queue requests: %w", err)
	}
	return out, nil
}

// NextPending returns the earliest pending request.
func (s *SQLiteStore) NextPending(ctx context.Context) (Request, bool, error) {
	row := s.db.QueryRowContext(
		ctx,
		`SELECT id, graph_file, graph_json, prompt, mode, starter_agent, delegates_json, working_dir, continuous, max_rounds, session_id, task_id, artifact_ids_json, policy_override, policy_override_actor, status, created_at, updated_at, error
		 FROM queue_requests WHERE status = ? ORDER BY created_at LIMIT 1`,
		string(StatusPending),
	)
	record, err := scanRequest(row)
	if err == sql.ErrNoRows {
		return Request{}, false, nil
	}
	if err != nil {
		return Request{}, false, err
	}
	return record, true, nil
}

// RecoverInterrupted requeues requests that were left in running state.
func (s *SQLiteStore) RecoverInterrupted(ctx context.Context) error {
	_, err := s.db.ExecContext(
		ctx,
		`UPDATE queue_requests SET status = ?, error = ?, updated_at = ? WHERE status = ?`,
		string(StatusPending),
		"recovered after daemon restart",
		time.Now().UTC().Format(time.RFC3339Nano),
		string(StatusRunning),
	)
	if err != nil {
		return fmt.Errorf("recover interrupted queue requests: %w", err)
	}
	return nil
}

func (s *SQLiteStore) save(ctx context.Context, req Request) error {
	graphRaw, err := json.Marshal(req.Graph)
	if err != nil {
		return fmt.Errorf("marshal graph: %w", err)
	}
	delegatesRaw, err := json.Marshal(req.Delegates)
	if err != nil {
		return fmt.Errorf("marshal delegates: %w", err)
	}
	artifactIDsRaw, err := json.Marshal(req.ArtifactIDs)
	if err != nil {
		return fmt.Errorf("marshal artifact ids: %w", err)
	}
	_, err = s.db.ExecContext(
		ctx,
		`INSERT OR REPLACE INTO queue_requests
		(id, graph_file, graph_json, prompt, mode, starter_agent, delegates_json, working_dir, continuous, max_rounds, session_id, task_id, artifact_ids_json, policy_override, policy_override_actor, status, created_at, updated_at, error)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		req.ID,
		req.GraphFile,
		string(graphRaw),
		req.Prompt,
		req.Mode,
		req.StarterAgent,
		string(delegatesRaw),
		req.WorkingDir,
		boolToInt(req.Continuous),
		req.MaxRounds,
		req.SessionID,
		req.TaskID,
		string(artifactIDsRaw),
		boolToInt(req.PolicyOverride),
		req.PolicyOverrideActor,
		string(req.Status),
		req.CreatedAt.Format(time.RFC3339Nano),
		req.UpdatedAt.Format(time.RFC3339Nano),
		req.Error,
	)
	if err != nil {
		return fmt.Errorf("insert queue request: %w", err)
	}
	return nil
}

type requestScanner interface {
	Scan(dest ...any) error
}

func scanRequest(scanner requestScanner) (Request, error) {
	var (
		record              Request
		graphRaw            string
		delegatesRaw        string
		artifactIDsRaw      string
		continuous          int
		maxRounds           int
		policyOverride      int
		policyOverrideActor string
		status              string
		createdAt           string
		updatedAt           string
	)
	if err := scanner.Scan(
		&record.ID,
		&record.GraphFile,
		&graphRaw,
		&record.Prompt,
		&record.Mode,
		&record.StarterAgent,
		&delegatesRaw,
		&record.WorkingDir,
		&continuous,
		&maxRounds,
		&record.SessionID,
		&record.TaskID,
		&artifactIDsRaw,
		&policyOverride,
		&policyOverrideActor,
		&status,
		&createdAt,
		&updatedAt,
		&record.Error,
	); err != nil {
		return Request{}, err
	}
	record.Status = Status(status)
	record.Continuous = continuous != 0
	record.MaxRounds = maxRounds
	record.PolicyOverride = policyOverride != 0
	record.PolicyOverrideActor = policyOverrideActor
	if graphRaw != "" && graphRaw != "null" {
		var graph GraphSpec
		if err := json.Unmarshal([]byte(graphRaw), &graph); err != nil {
			return Request{}, fmt.Errorf("unmarshal graph: %w", err)
		}
		record.Graph = &graph
	}
	if delegatesRaw != "" && delegatesRaw != "null" {
		if err := json.Unmarshal([]byte(delegatesRaw), &record.Delegates); err != nil {
			return Request{}, fmt.Errorf("unmarshal delegates: %w", err)
		}
	}
	if artifactIDsRaw != "" && artifactIDsRaw != "null" {
		if err := json.Unmarshal([]byte(artifactIDsRaw), &record.ArtifactIDs); err != nil {
			return Request{}, fmt.Errorf("unmarshal artifact ids: %w", err)
		}
	}
	if err := record.CreatedAt.UnmarshalText([]byte(createdAt)); err != nil {
		return Request{}, fmt.Errorf("parse created_at: %w", err)
	}
	if err := record.UpdatedAt.UnmarshalText([]byte(updatedAt)); err != nil {
		return Request{}, fmt.Errorf("parse updated_at: %w", err)
	}
	return record, nil
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
