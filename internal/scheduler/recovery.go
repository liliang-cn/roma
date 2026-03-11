package scheduler

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/liliang/roma/internal/domain"
	"github.com/liliang/roma/internal/history"
	"github.com/liliang/roma/internal/queue"
	"github.com/liliang/roma/internal/taskstore"
)

// RecoverySnapshot describes one session's recoverable scheduling state.
type RecoverySnapshot struct {
	SessionID  string              `json:"session_id"`
	Status     string              `json:"status"`
	ReadyTasks []domain.TaskRecord `json:"ready_tasks,omitempty"`
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

	sessions, err := sessionStore.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	out := make([]RecoverySnapshot, 0)
	for _, session := range sessions {
		if session.Status == "succeeded" || session.Status == "failed" || session.Status == "rejected" || session.Status == "awaiting_approval" {
			continue
		}
		tasks, err := taskStore.ListTasksBySession(ctx, session.ID)
		if err != nil {
			return nil, fmt.Errorf("list tasks for session %s: %w", session.ID, err)
		}
		ready := make([]domain.TaskRecord, 0)
		for _, task := range tasks {
			if task.State == domain.TaskStateReady || task.State == domain.TaskStatePending {
				ready = append(ready, task)
			}
		}
		if len(ready) == 0 {
			continue
		}
		out = append(out, RecoverySnapshot{
			SessionID:  session.ID,
			Status:     session.Status,
			ReadyTasks: ready,
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

// RecoverInterruptedLeases marks active dispatcher leases as recovered on daemon restart.
func RecoverInterruptedLeases(ctx context.Context, workDir string) error {
	leaseStore, err := NewLeaseStore(workDir)
	if err != nil {
		return err
	}
	return leaseStore.RecoverActive(ctx)
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
	for _, item := range items {
		if _, ok := owned[item.SessionID]; ok {
			continue
		}
		if err := runner.ResumeSession(ctx, workDir, item.SessionID, os.Stdout); err != nil {
			return fmt.Errorf("resume recoverable session %s: %w", item.SessionID, err)
		}
	}
	return nil
}
