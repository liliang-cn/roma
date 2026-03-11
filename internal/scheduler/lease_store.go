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
	SessionID        string      `json:"session_id"`
	OwnerID          string      `json:"owner_id"`
	Status           LeaseStatus `json:"status"`
	ReadyTaskIDs     []string    `json:"ready_task_ids,omitempty"`
	CompletedTaskIDs []string    `json:"completed_task_ids,omitempty"`
	CreatedAt        time.Time   `json:"created_at"`
	UpdatedAt        time.Time   `json:"updated_at"`
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
func (s *LeaseStore) Renew(ctx context.Context, sessionID, ownerID string, readyTaskIDs, completedTaskIDs []string) error {
	record, err := s.Get(ctx, sessionID)
	if err != nil {
		return err
	}
	record.OwnerID = ownerID
	record.Status = LeaseStatusActive
	record.ReadyTaskIDs = append([]string(nil), readyTaskIDs...)
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
	record.CompletedTaskIDs = append([]string(nil), completedTaskIDs...)
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
		`SELECT session_id, owner_id, status, ready_task_ids_json, completed_task_ids_json, created_at, updated_at
		 FROM scheduler_leases WHERE session_id = ?`,
		sessionID,
	)
	return scanLease(row)
}

func (s *LeaseStore) save(ctx context.Context, record LeaseRecord) error {
	readyRaw, err := json.Marshal(record.ReadyTaskIDs)
	if err != nil {
		return fmt.Errorf("marshal ready task ids: %w", err)
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
		(session_id, owner_id, status, ready_task_ids_json, completed_task_ids_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		record.SessionID,
		record.OwnerID,
		string(record.Status),
		string(readyRaw),
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
		record       LeaseRecord
		status       string
		readyRaw     string
		completedRaw string
		createdAt    string
		updatedAt    string
	)
	if err := scanner.Scan(
		&record.SessionID,
		&record.OwnerID,
		&status,
		&readyRaw,
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
