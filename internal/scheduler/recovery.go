package scheduler

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"time"

	"github.com/liliang-cn/roma/internal/domain"
	"github.com/liliang-cn/roma/internal/history"
	"github.com/liliang-cn/roma/internal/queue"
	"github.com/liliang-cn/roma/internal/taskstore"
	"github.com/liliang-cn/roma/internal/workspace"
)

// RecoverySnapshot describes one session's recoverable scheduling state.
type RecoverySnapshot struct {
	SessionID              string              `json:"session_id"`
	Status                 string              `json:"status"`
	Lease                  *LeaseRecord        `json:"lease,omitempty"`
	ReadyTasks             []domain.TaskRecord `json:"ready_tasks,omitempty"`
	PendingApprovalTaskIDs []string            `json:"pending_approval_task_ids,omitempty"`
	ApprovalResumeReady    bool                `json:"approval_resume_ready"`
}

// RecoverableSessions rebuilds ready-to-dispatch task views from authoritative SQLite metadata.
func RecoverableSessions(ctx context.Context, workDir string) ([]RecoverySnapshot, error) {
	sessionStore, err := history.NewSQLiteStore(workDir)
	if err != nil {
		return nil, err
	}
	taskStore, err := taskstore.NewSQLiteStore(workDir)
	if err != nil {
		return nil, err
	}
	var leaseStore *LeaseStore
	if store, err := NewLeaseStore(workDir); err == nil {
		leaseStore = store
	}

	sessions, err := sessionStore.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	out := make([]RecoverySnapshot, 0)
	for _, session := range sessions {
		if session.Status == "succeeded" || session.Status == "failed" || session.Status == "rejected" {
			continue
		}
		tasks, err := taskStore.ListTasksBySession(ctx, session.ID)
		if err != nil {
			return nil, fmt.Errorf("list tasks for session %s: %w", session.ID, err)
		}
		ready := make([]domain.TaskRecord, 0)
		pendingApprovalTaskIDs := make([]string, 0)
		var lease *LeaseRecord
		if leaseStore != nil {
			if item, err := leaseStore.Get(ctx, session.ID); err == nil {
				lease = &item
				pendingApprovalTaskIDs = append(pendingApprovalTaskIDs, item.PendingApprovalTaskIDs...)
			}
		}
		for _, task := range tasks {
			if task.State == domain.TaskStateReady || task.State == domain.TaskStatePending {
				ready = append(ready, task)
			}
		}
		if len(ready) == 0 && len(pendingApprovalTaskIDs) == 0 {
			continue
		}
		out = append(out, RecoverySnapshot{
			SessionID:              session.ID,
			Status:                 session.Status,
			Lease:                  lease,
			ReadyTasks:             ready,
			PendingApprovalTaskIDs: pendingApprovalTaskIDs,
			ApprovalResumeReady:    len(pendingApprovalTaskIDs) == 0,
		})
	}
	return out, nil
}

// NormalizeInterruptedTasks reclassifies in-flight task records so recovery can resume them.
func NormalizeInterruptedTasks(ctx context.Context, workDir string) error {
	taskStore, err := taskstore.NewSQLiteStore(workDir)
	if err != nil {
		return err
	}
	tasks, err := taskStore.ListTasksBySession(ctx, "")
	if err != nil {
		return fmt.Errorf("list tasks: %w", err)
	}
	for _, task := range tasks {
		if task.State != domain.TaskStateRunning {
			continue
		}
		task.State = domain.TaskStateReady
		task.UpdatedAt = time.Now().UTC()
		if err := taskStore.UpsertTask(ctx, task); err != nil {
			return fmt.Errorf("reset interrupted task %s: %w", task.ID, err)
		}
	}
	return nil
}

// NormalizeInterruptedTasksForSession reclassifies in-flight task records for one session so recovery can resume them.
func NormalizeInterruptedTasksForSession(ctx context.Context, workDir, sessionID string) error {
	taskStore, err := taskstore.NewSQLiteStore(workDir)
	if err != nil {
		return err
	}
	tasks, err := taskStore.ListTasksBySession(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("list tasks for session %s: %w", sessionID, err)
	}
	for _, task := range tasks {
		if task.State != domain.TaskStateRunning {
			continue
		}
		task.State = domain.TaskStateReady
		task.UpdatedAt = time.Now().UTC()
		if err := taskStore.UpsertTask(ctx, task); err != nil {
			return fmt.Errorf("reset interrupted task %s: %w", task.ID, err)
		}
	}
	return nil
}

// RecoverInterruptedLeases marks active dispatcher leases as recovered on daemon restart.
func RecoverInterruptedLeases(ctx context.Context, workDir string) error {
	leaseStore, err := NewLeaseStore(workDir)
	if err != nil {
		return err
	}
	return leaseStore.RecoverActive(ctx)
}

// ReclaimStaleWorkspaces removes stale workspaces on daemon startup.
func ReclaimStaleWorkspaces(ctx context.Context, workDir string) error {
	activeSessions := make(map[string]struct{})
	if leaseStore, err := NewLeaseStore(workDir); err == nil {
		items, err := leaseStore.ListByStatus(ctx, LeaseStatusActive)
		if err == nil {
			for _, item := range items {
				activeSessions[item.SessionID] = struct{}{}
			}
		}
	}
	sessionStore, err := history.NewSQLiteStore(workDir)
	if err != nil {
		return err
	}
	sessions, err := sessionStore.List(ctx)
	if err != nil {
		return fmt.Errorf("list sessions for workspace reclaim: %w", err)
	}
	roots := make(map[string]struct{})
	for _, session := range sessions {
		if session.WorkingDir == "" {
			continue
		}
		roots[session.WorkingDir] = struct{}{}
	}
	for root := range roots {
		if err := workspace.NewManager(root, nil).ReclaimStaleExcept(ctx, activeSessions); err != nil {
			return err
		}
	}
	return nil
}

// ResumeRunner captures the recovery execution contract needed by the daemon.
type ResumeRunner interface {
	ResumeSession(ctx context.Context, workDir, sessionID string, stdout io.Writer) error
}

// ResumeRecoverableSessions resumes SQLite-backed runnable sessions that are not already owned by a queued job.
func ResumeRecoverableSessions(ctx context.Context, workDir string, queueStore queue.Backend, runner ResumeRunner) error {
	if runner == nil {
		return nil
	}
	var sessionStore history.Backend
	if store, err := history.NewSQLiteStore(workDir); err == nil {
		sessionStore = store
	}
	items, err := RecoverableSessions(ctx, workDir)
	if err != nil {
		return err
	}
	owned := make(map[string]struct{})
	if queueStore != nil {
		requests, err := queueStore.List(ctx)
		if err != nil {
			return fmt.Errorf("list queue for recovery ownership: %w", err)
		}
		for _, req := range requests {
			if req.SessionID == "" {
				continue
			}
			if req.Status == queue.StatusPending || req.Status == queue.StatusRunning || req.Status == queue.StatusAwaitingApproval {
				owned[req.SessionID] = struct{}{}
			}
		}
	}
	if leaseStore, err := NewLeaseStore(workDir); err == nil {
		for _, item := range items {
			record, err := leaseStore.Get(ctx, item.SessionID)
			if err != nil {
				continue
			}
			if record.Status == LeaseStatusActive {
				owned[item.SessionID] = struct{}{}
			}
		}
	}
	for _, item := range items {
		if _, ok := owned[item.SessionID]; ok {
			continue
		}
		if len(item.PendingApprovalTaskIDs) > 0 {
			continue
		}
		if err := runner.ResumeSession(ctx, workDir, item.SessionID, os.Stdout); err != nil {
			log.Printf("romad skipping recoverable session=%s: %v", item.SessionID, err)
			if sessionStore != nil {
				if record, getErr := sessionStore.Get(ctx, item.SessionID); getErr == nil {
					record.Status = "failed"
					record.UpdatedAt = time.Now().UTC()
					_ = sessionStore.Save(ctx, record)
				}
			}
			continue
		}
	}
	return nil
}
