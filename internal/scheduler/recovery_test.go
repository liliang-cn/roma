package scheduler

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/liliang/roma/internal/domain"
	"github.com/liliang/roma/internal/history"
	"github.com/liliang/roma/internal/queue"
	"github.com/liliang/roma/internal/taskstore"
)

func TestRecoverableSessions(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	sessionStore, err := history.NewSQLiteStore(workDir)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	taskStore, err := taskstore.NewSQLiteStore(workDir)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	if err := sessionStore.Save(context.Background(), history.SessionRecord{
		ID:         "sess_1",
		TaskID:     "task_1",
		Prompt:     "test",
		Starter:    "codex-cli",
		WorkingDir: ".",
		Status:     "running",
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}); err != nil {
		t.Fatalf("Save session error = %v", err)
	}
	if err := taskStore.UpsertTask(context.Background(), domain.TaskRecord{
		ID:        "sess_1__plan",
		SessionID: "sess_1",
		Title:     "Plan",
		Strategy:  domain.TaskStrategyDirect,
		State:     domain.TaskStateReady,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("UpsertTask() error = %v", err)
	}

	items, err := RecoverableSessions(context.Background(), workDir)
	if err != nil {
		t.Fatalf("RecoverableSessions() error = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("snapshot count = %d, want 1", len(items))
	}
	if len(items[0].ReadyTasks) != 1 {
		t.Fatalf("ready task count = %d, want 1", len(items[0].ReadyTasks))
	}
}

func TestResumeRecoverableSessionsSkipsOwnedQueueSessions(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	sessionStore, err := history.NewSQLiteStore(workDir)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	taskStore, err := taskstore.NewSQLiteStore(workDir)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	queueStore, err := queue.NewSQLiteStore(workDir)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	now := time.Now().UTC()
	for _, sessionID := range []string{"sess_owned", "sess_free"} {
		if err := sessionStore.Save(context.Background(), history.SessionRecord{
			ID:         sessionID,
			TaskID:     "task_" + sessionID,
			Prompt:     "test",
			Starter:    "codex-cli",
			WorkingDir: workDir,
			Status:     "failed_recoverable",
			CreatedAt:  now,
			UpdatedAt:  now,
		}); err != nil {
			t.Fatalf("Save session error = %v", err)
		}
		if err := taskStore.UpsertTask(context.Background(), domain.TaskRecord{
			ID:        sessionID + "__plan",
			SessionID: sessionID,
			Title:     "Plan",
			Strategy:  domain.TaskStrategyRelay,
			State:     domain.TaskStateReady,
			AgentID:   "codex-cli",
			CreatedAt: now,
			UpdatedAt: now,
		}); err != nil {
			t.Fatalf("UpsertTask() error = %v", err)
		}
	}
	if err := queueStore.Enqueue(context.Background(), queue.Request{
		ID:           "job_owned",
		Prompt:       "test",
		StarterAgent: "codex",
		WorkingDir:   workDir,
		SessionID:    "sess_owned",
	}); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}

	runner := &fakeResumeRunner{}
	if err := ResumeRecoverableSessions(context.Background(), workDir, queueStore, runner); err != nil {
		t.Fatalf("ResumeRecoverableSessions() error = %v", err)
	}
	if len(runner.sessions) != 1 || runner.sessions[0] != "sess_free" {
		t.Fatalf("resumed sessions = %v, want [sess_free]", runner.sessions)
	}
}

type fakeResumeRunner struct {
	sessions []string
}

func (f *fakeResumeRunner) ResumeSession(_ context.Context, _ string, sessionID string, _ io.Writer) error {
	f.sessions = append(f.sessions, sessionID)
	return nil
}
