package scheduler

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liliang-cn/roma/internal/domain"
	"github.com/liliang-cn/roma/internal/history"
	"github.com/liliang-cn/roma/internal/queue"
	"github.com/liliang-cn/roma/internal/taskstore"
	"github.com/liliang-cn/roma/internal/workspace"
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

func TestResumeRecoverableSessionsSkipsActiveLeaseSessions(t *testing.T) {
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
	now := time.Now().UTC()
	for _, sessionID := range []string{"sess_active", "sess_free"} {
		if err := sessionStore.Save(context.Background(), history.SessionRecord{
			ID:         sessionID,
			TaskID:     "task_" + sessionID,
			Prompt:     "test",
			Starter:    "codex-cli",
			WorkingDir: workDir,
			Status:     "running",
			CreatedAt:  now,
			UpdatedAt:  now,
		}); err != nil {
			t.Fatalf("Save session error = %v", err)
		}
		if err := taskStore.UpsertTask(context.Background(), domain.TaskRecord{
			ID:              sessionID + "__plan",
			SessionID:       sessionID,
			Title:           "Plan",
			Strategy:        domain.TaskStrategyRelay,
			State:           domain.TaskStateReady,
			ApprovalGranted: true,
			AgentID:         "codex-cli",
			CreatedAt:       now,
			UpdatedAt:       now,
		}); err != nil {
			t.Fatalf("UpsertTask() error = %v", err)
		}
	}
	leaseStore, err := NewLeaseStore(workDir)
	if err != nil {
		t.Fatalf("NewLeaseStore() error = %v", err)
	}
	if err := leaseStore.Acquire(context.Background(), "sess_active", "owner_1"); err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}

	runner := &fakeResumeRunner{}
	if err := ResumeRecoverableSessions(context.Background(), workDir, nil, runner); err != nil {
		t.Fatalf("ResumeRecoverableSessions() error = %v", err)
	}
	if len(runner.sessions) != 1 || runner.sessions[0] != "sess_free" {
		t.Fatalf("resumed sessions = %v, want [sess_free]", runner.sessions)
	}
}

func TestResumeRecoverableSessionsSkipsPendingApprovalLeaseSessions(t *testing.T) {
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
	now := time.Now().UTC()
	if err := sessionStore.Save(context.Background(), history.SessionRecord{
		ID:         "sess_waiting",
		TaskID:     "task_waiting",
		Prompt:     "test",
		Starter:    "codex-cli",
		WorkingDir: workDir,
		Status:     "running",
		CreatedAt:  now,
		UpdatedAt:  now,
	}); err != nil {
		t.Fatalf("Save session error = %v", err)
	}
	if err := taskStore.UpsertTask(context.Background(), domain.TaskRecord{
		ID:        "sess_waiting__plan",
		SessionID: "sess_waiting",
		Title:     "Plan",
		Strategy:  domain.TaskStrategyRelay,
		State:     domain.TaskStateReady,
		AgentID:   "codex-cli",
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("UpsertTask() error = %v", err)
	}
	leaseStore, err := NewLeaseStore(workDir)
	if err != nil {
		t.Fatalf("NewLeaseStore() error = %v", err)
	}
	if err := leaseStore.Acquire(context.Background(), "sess_waiting", "owner_1"); err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	if err := leaseStore.Renew(context.Background(), "sess_waiting", "owner_1", nil, nil, []string{"sess_waiting__plan"}, nil); err != nil {
		t.Fatalf("Renew() error = %v", err)
	}

	runner := &fakeResumeRunner{}
	if err := ResumeRecoverableSessions(context.Background(), workDir, nil, runner); err != nil {
		t.Fatalf("ResumeRecoverableSessions() error = %v", err)
	}
	if len(runner.sessions) != 0 {
		t.Fatalf("resumed sessions = %v, want []", runner.sessions)
	}
	snapshots, err := RecoverableSessions(context.Background(), workDir)
	if err != nil {
		t.Fatalf("RecoverableSessions() error = %v", err)
	}
	if len(snapshots) != 1 || snapshots[0].ApprovalResumeReady {
		t.Fatalf("snapshots = %#v, want pending approval snapshot", snapshots)
	}
}

func TestNormalizeInterruptedTasksForSession(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	taskStore, err := taskstore.NewSQLiteStore(workDir)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	now := time.Now().UTC()
	for _, record := range []domain.TaskRecord{
		{
			ID:        "sess_1__running",
			SessionID: "sess_1",
			Title:     "Running task",
			Strategy:  domain.TaskStrategyRelay,
			State:     domain.TaskStateRunning,
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:        "sess_1__done",
			SessionID: "sess_1",
			Title:     "Done task",
			Strategy:  domain.TaskStrategyRelay,
			State:     domain.TaskStateSucceeded,
			CreatedAt: now,
			UpdatedAt: now,
		},
	} {
		if err := taskStore.UpsertTask(context.Background(), record); err != nil {
			t.Fatalf("UpsertTask() error = %v", err)
		}
	}

	if err := NormalizeInterruptedTasksForSession(context.Background(), workDir, "sess_1"); err != nil {
		t.Fatalf("NormalizeInterruptedTasksForSession() error = %v", err)
	}

	running, err := taskStore.GetTask(context.Background(), "sess_1__running")
	if err != nil {
		t.Fatalf("GetTask(running) error = %v", err)
	}
	if running.State != domain.TaskStateReady {
		t.Fatalf("running state = %s, want %s", running.State, domain.TaskStateReady)
	}
	done, err := taskStore.GetTask(context.Background(), "sess_1__done")
	if err != nil {
		t.Fatalf("GetTask(done) error = %v", err)
	}
	if done.State != domain.TaskStateSucceeded {
		t.Fatalf("done state = %s, want %s", done.State, domain.TaskStateSucceeded)
	}
}

func TestReclaimStaleWorkspaces(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	initRecoveryGitRepo(t, workDir)
	sessionStore, err := history.NewSQLiteStore(workDir)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	now := time.Now().UTC()
	if err := sessionStore.Save(context.Background(), history.SessionRecord{
		ID:         "sess_git",
		TaskID:     "task_git",
		Prompt:     "test",
		Starter:    "codex-cli",
		WorkingDir: workDir,
		Status:     "running",
		CreatedAt:  now,
		UpdatedAt:  now,
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	manager := workspace.NewManager(workDir, nil)
	prepared, err := manager.Prepare(context.Background(), "sess_git", "task_git", workDir, domain.TaskStrategyDirect)
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if err := manager.Release(context.Background(), prepared, "succeeded"); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if err := ReclaimStaleWorkspaces(context.Background(), workDir); err != nil {
		t.Fatalf("ReclaimStaleWorkspaces() error = %v", err)
	}
	if _, err := os.Stat(prepared.EffectiveDir); !os.IsNotExist(err) {
		t.Fatalf("expected reclaimed worktree removed, stat err = %v", err)
	}
}

type fakeResumeRunner struct {
	sessions []string
}

func (f *fakeResumeRunner) ResumeSession(_ context.Context, _ string, sessionID string, _ io.Writer) error {
	f.sessions = append(f.sessions, sessionID)
	return nil
}

func initRecoveryGitRepo(t *testing.T, dir string) {
	t.Helper()
	runRecoveryGit(t, dir, "init")
	runRecoveryGit(t, dir, "config", "user.email", "roma@example.com")
	runRecoveryGit(t, dir, "config", "user.name", "ROMA")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("roma\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	runRecoveryGit(t, dir, "add", "README.md")
	runRecoveryGit(t, dir, "commit", "-m", "init")
}

func runRecoveryGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmdArgs := append([]string{"-C", dir}, args...)
	cmd := exec.Command("git", cmdArgs...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s error = %v (%s)", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
}
