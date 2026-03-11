package scheduler

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/liliang/roma/internal/sqliteutil"
)

// LeaseStatus identifies scheduler lease state.
type LeaseStatus string

const (
	LeaseStatusActive    LeaseStatus = "active"
	LeaseStatusReleased  LeaseStatus = "released"
	LeaseStatusRecovered LeaseStatus = "recovered"
)

// LeaseRecord persists scheduler dispatch ownership for a session.
type LeaseRecord struct {
	SessionID              string         `json:"session_id"`
	OwnerID                string         `json:"owner_id"`
	Status                 LeaseStatus    `json:"status"`
	ReadyTaskIDs           []string       `json:"ready_task_ids,omitempty"`
	WorkspaceRefs          []WorkspaceRef `json:"workspace_refs,omitempty"`
	PendingApprovalTaskIDs []string       `json:"pending_approval_task_ids,omitempty"`
	CompletedTaskIDs       []string       `json:"completed_task_ids,omitempty"`
	CreatedAt              time.Time      `json:"created_at"`
	UpdatedAt              time.Time      `json:"updated_at"`
}

// WorkspaceRef captures scheduler-owned workspace linkage for an active lease.
type WorkspaceRef struct {
	TaskID        string `json:"task_id"`
	EffectiveDir  string `json:"effective_dir"`
	Provider      string `json:"provider"`
	EffectiveMode string `json:"effective_mode"`
}

// LeaseStore persists scheduler ownership in the workspace SQLite database.
type LeaseStore struct {
	db *sql.DB
}

// NewLeaseStore constructs a SQLite-backed lease store.
func NewLeaseStore(workDir string) (*LeaseStore, error) {
	db, err := sqliteutil.Open(workDir)
	if err != nil {
		return nil, err
	}
	return &LeaseStore{db: db}, nil
}

// Acquire creates or replaces the active lease for a session.
func (s *LeaseStore) Acquire(ctx context.Context, sessionID, ownerID string) error {
	now := time.Now().UTC()
	record := LeaseRecord{
		SessionID: sessionID,
		OwnerID:   ownerID,
		Status:    LeaseStatusActive,
		CreatedAt: now,
		UpdatedAt: now,
	}
	return s.save(ctx, record)
}

// Renew updates ready/completed checkpoint information for the active owner.
func (s *LeaseStore) Renew(ctx context.Context, sessionID, ownerID string, readyTaskIDs []string, workspaceRefs []WorkspaceRef, pendingApprovalTaskIDs []string, completedTaskIDs []string) error {
	record, err := s.Get(ctx, sessionID)
	if err != nil {
		return err
	}
	record.OwnerID = ownerID
	record.Status = LeaseStatusActive
	record.ReadyTaskIDs = append([]string(nil), readyTaskIDs...)
	record.WorkspaceRefs = append([]WorkspaceRef(nil), workspaceRefs...)
	record.PendingApprovalTaskIDs = append([]string(nil), pendingApprovalTaskIDs...)
	record.CompletedTaskIDs = append([]string(nil), completedTaskIDs...)
	record.UpdatedAt = time.Now().UTC()
	return s.save(ctx, record)
}

// Release marks a lease released while keeping the latest checkpoint metadata.
func (s *LeaseStore) Release(ctx context.Context, sessionID, ownerID string, completedTaskIDs []string) error {
	record, err := s.Get(ctx, sessionID)
	if err != nil {
		return err
	}
	record.OwnerID = ownerID
	record.Status = LeaseStatusReleased
	record.ReadyTaskIDs = nil
	record.WorkspaceRefs = nil
	record.PendingApprovalTaskIDs = nil
	record.CompletedTaskIDs = append([]string(nil), completedTaskIDs...)
	record.UpdatedAt = time.Now().UTC()
	return s.save(ctx, record)
}

// UpdatePendingApprovalTaskIDs rewrites the active pending-approval checkpoint while preserving other lease fields.
func (s *LeaseStore) UpdatePendingApprovalTaskIDs(ctx context.Context, sessionID string, pendingApprovalTaskIDs []string) error {
	record, err := s.Get(ctx, sessionID)
	if err != nil {
		return err
	}
	record.PendingApprovalTaskIDs = append([]string(nil), pendingApprovalTaskIDs...)
	record.UpdatedAt = time.Now().UTC()
	return s.save(ctx, record)
}

// RecoverActive marks all active leases as recovered during daemon restart recovery.
func (s *LeaseStore) RecoverActive(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT session_id FROM scheduler_leases WHERE status = ?`, string(LeaseStatusActive))
	if err != nil {
		return fmt.Errorf("query active leases: %w", err)
	}
	defer rows.Close()

	var sessionIDs []string
	for rows.Next() {
		var sessionID string
		if err := rows.Scan(&sessionID); err != nil {
			return fmt.Errorf("scan active lease: %w", err)
		}
		sessionIDs = append(sessionIDs, sessionID)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate active leases: %w", err)
	}
	for _, sessionID := range sessionIDs {
		record, err := s.Get(ctx, sessionID)
		if err != nil {
			return err
		}
		record.Status = LeaseStatusRecovered
		record.UpdatedAt = time.Now().UTC()
		if err := s.save(ctx, record); err != nil {
			return err
		}
	}
	return nil
}

// Get returns one persisted lease.
func (s *LeaseStore) Get(ctx context.Context, sessionID string) (LeaseRecord, error) {
	row := s.db.QueryRowContext(
		ctx,
		`SELECT session_id, owner_id, status, ready_task_ids_json, workspace_refs_json, pending_approval_task_ids_json, completed_task_ids_json, created_at, updated_at
		 FROM scheduler_leases WHERE session_id = ?`,
		sessionID,
	)
	return scanLease(row)
}

// ListByStatus returns all leases in one status bucket.
func (s *LeaseStore) ListByStatus(ctx context.Context, status LeaseStatus) ([]LeaseRecord, error) {
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT session_id, owner_id, status, ready_task_ids_json, workspace_refs_json, pending_approval_task_ids_json, completed_task_ids_json, created_at, updated_at
		 FROM scheduler_leases WHERE status = ? ORDER BY updated_at`,
		string(status),
	)
	if err != nil {
		return nil, fmt.Errorf("query scheduler leases: %w", err)
	}
	defer rows.Close()

	out := make([]LeaseRecord, 0)
	for rows.Next() {
		record, err := scanLease(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate scheduler leases: %w", err)
	}
	return out, nil
}

func (s *LeaseStore) save(ctx context.Context, record LeaseRecord) error {
	readyRaw, err := json.Marshal(record.ReadyTaskIDs)
	if err != nil {
		return fmt.Errorf("marshal ready task ids: %w", err)
	}
	workspaceRaw, err := json.Marshal(record.WorkspaceRefs)
	if err != nil {
		return fmt.Errorf("marshal workspace refs: %w", err)
	}
	pendingApprovalRaw, err := json.Marshal(record.PendingApprovalTaskIDs)
	if err != nil {
		return fmt.Errorf("marshal pending approval task ids: %w", err)
	}
	completedRaw, err := json.Marshal(record.CompletedTaskIDs)
	if err != nil {
		return fmt.Errorf("marshal completed task ids: %w", err)
	}
	if record.CreatedAt.IsZero() {
		record.CreatedAt = time.Now().UTC()
	}
	if record.UpdatedAt.IsZero() {
		record.UpdatedAt = record.CreatedAt
	}
	_, err = s.db.ExecContext(
		ctx,
		`INSERT OR REPLACE INTO scheduler_leases
		(session_id, owner_id, status, ready_task_ids_json, workspace_refs_json, pending_approval_task_ids_json, completed_task_ids_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		record.SessionID,
		record.OwnerID,
		string(record.Status),
		string(readyRaw),
		string(workspaceRaw),
		string(pendingApprovalRaw),
		string(completedRaw),
		record.CreatedAt.Format(time.RFC3339Nano),
		record.UpdatedAt.Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("insert scheduler lease: %w", err)
	}
	return nil
}

func scanLease(scanner interface{ Scan(dest ...any) error }) (LeaseRecord, error) {
	var (
		record             LeaseRecord
		status             string
		readyRaw           string
		workspaceRaw       string
		pendingApprovalRaw string
		completedRaw       string
		createdAt          string
		updatedAt          string
	)
	if err := scanner.Scan(
		&record.SessionID,
		&record.OwnerID,
		&status,
		&readyRaw,
		&workspaceRaw,
		&pendingApprovalRaw,
		&completedRaw,
		&createdAt,
		&updatedAt,
	); err != nil {
		return LeaseRecord{}, err
	}
	record.Status = LeaseStatus(status)
	if readyRaw != "" && readyRaw != "null" {
		if err := json.Unmarshal([]byte(readyRaw), &record.ReadyTaskIDs); err != nil {
			return LeaseRecord{}, fmt.Errorf("unmarshal ready task ids: %w", err)
		}
	}
	if workspaceRaw != "" && workspaceRaw != "null" {
		if err := json.Unmarshal([]byte(workspaceRaw), &record.WorkspaceRefs); err != nil {
			return LeaseRecord{}, fmt.Errorf("unmarshal workspace refs: %w", err)
		}
	}
	if pendingApprovalRaw != "" && pendingApprovalRaw != "null" {
		if err := json.Unmarshal([]byte(pendingApprovalRaw), &record.PendingApprovalTaskIDs); err != nil {
			return LeaseRecord{}, fmt.Errorf("unmarshal pending approval task ids: %w", err)
		}
	}
	if completedRaw != "" && completedRaw != "null" {
		if err := json.Unmarshal([]byte(completedRaw), &record.CompletedTaskIDs); err != nil {
			return LeaseRecord{}, fmt.Errorf("unmarshal completed task ids: %w", err)
		}
	}
	if err := record.CreatedAt.UnmarshalText([]byte(createdAt)); err != nil {
		return LeaseRecord{}, fmt.Errorf("parse created_at: %w", err)
	}
	if err := record.UpdatedAt.UnmarshalText([]byte(updatedAt)); err != nil {
		return LeaseRecord{}, fmt.Errorf("parse updated_at: %w", err)
	}
	return record, nil
}
