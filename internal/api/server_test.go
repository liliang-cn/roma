package api

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liliang/roma/internal/artifacts"
	"github.com/liliang/roma/internal/domain"
	"github.com/liliang/roma/internal/events"
	"github.com/liliang/roma/internal/history"
	"github.com/liliang/roma/internal/queue"
	"github.com/liliang/roma/internal/scheduler"
	storepkg "github.com/liliang/roma/internal/store"
	"github.com/liliang/roma/internal/taskstore"
	workspacepkg "github.com/liliang/roma/internal/workspace"
)

func TestServerSubmitAndQueueList(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	queueStore := queue.NewStore(workDir)
	sessionStore := history.NewStore(workDir)
	server := NewServer(workDir, queueStore, sessionStore)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := server.Start(ctx); err != nil {
		if errors.Is(err, ErrUnavailable) {
			t.Skipf("local listeners unavailable in this environment: %v", err)
		}
		t.Fatalf("Start() error = %v", err)
	}

	client := NewClient(workDir)
	deadline := time.Now().Add(2 * time.Second)
	for !client.Available() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	resp, err := client.Submit(context.Background(), SubmitRequest{
		Prompt:       "test",
		StarterAgent: "codex",
		WorkingDir:   workDir,
	})
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if resp.JobID == "" {
		t.Fatal("Submit() returned empty job id")
	}

	items, err := client.QueueList(context.Background())
	if err != nil {
		t.Fatalf("QueueList() error = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("queue item count = %d, want 1", len(items))
	}
}

func TestServerSubmitInlineGraph(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	queueStore := queue.NewStore(workDir)
	sessionStore := history.NewStore(workDir)
	server := NewServer(workDir, queueStore, sessionStore)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := server.Start(ctx); err != nil {
		if errors.Is(err, ErrUnavailable) {
			t.Skipf("local listeners unavailable in this environment: %v", err)
		}
		t.Fatalf("Start() error = %v", err)
	}

	client := NewClient(workDir)
	deadline := time.Now().Add(2 * time.Second)
	for !client.Available() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	resp, err := client.Submit(context.Background(), SubmitRequest{
		Graph: &GraphSubmitRequest{
			Prompt: "build graph",
			Nodes: []GraphSubmitNode{
				{ID: "plan", Title: "Plan", Agent: "codex", Strategy: "direct"},
			},
		},
		Prompt:     "build graph",
		WorkingDir: workDir,
	})
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}

	item, err := queueStore.Get(context.Background(), resp.JobID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if item.Graph == nil || len(item.Graph.Nodes) != 1 {
		t.Fatalf("graph payload = %#v, want 1 node", item.Graph)
	}
}

func TestServerQueueInspect(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	queueStore := queue.NewStore(workDir)
	sessionStore := history.NewStore(workDir)
	server := NewServer(workDir, queueStore, sessionStore)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := server.Start(ctx); err != nil {
		if errors.Is(err, ErrUnavailable) {
			t.Skipf("local listeners unavailable in this environment: %v", err)
		}
		t.Fatalf("Start() error = %v", err)
	}

	client := NewClient(workDir)
	deadline := time.Now().Add(2 * time.Second)
	for !client.Available() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	job := queue.Request{
		ID:          "job_1",
		GraphFile:   "examples/relay-graph.json",
		WorkingDir:  workDir,
		SessionID:   "sess_1",
		TaskID:      "task_1",
		ArtifactIDs: []string{"art_1"},
		Status:      queue.StatusSucceeded,
	}
	if err := queueStore.Enqueue(context.Background(), job); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}
	session := history.SessionRecord{
		ID:          "sess_1",
		TaskID:      "task_1",
		Prompt:      "test graph",
		Starter:     "codex-cli",
		WorkingDir:  workDir,
		Status:      "succeeded",
		ArtifactIDs: []string{"art_1"},
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}
	if err := sessionStore.Save(context.Background(), session); err != nil {
		t.Fatalf("Save session error = %v", err)
	}
	artifact := domain.ArtifactEnvelope{
		ID:            "art_1",
		Kind:          domain.ArtifactKindReport,
		SchemaVersion: "v1",
		Producer:      domain.Producer{AgentID: "codex-cli", Role: domain.ProducerRoleExecutor},
		SessionID:     "sess_1",
		TaskID:        "task_1",
		CreatedAt:     time.Now().UTC(),
		PayloadSchema: "roma/report/v1",
		Payload:       map[string]any{"summary": "ok"},
		Checksum:      "sha256:test",
	}
	artifactStore, err := artifacts.NewSQLiteStore(workDir)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	if err := artifactStore.Save(context.Background(), artifact); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	eventRecord := events.Record{
		ID:         "evt_1",
		SessionID:  "sess_1",
		TaskID:     "task_1",
		Type:       events.TypeSessionCreated,
		ActorType:  events.ActorTypeSystem,
		OccurredAt: time.Now().UTC(),
	}
	eventStore, err := storepkg.NewSQLiteEventStore(workDir)
	if err != nil {
		t.Fatalf("NewSQLiteEventStore() error = %v", err)
	}
	if err := eventStore.AppendEvent(context.Background(), eventRecord); err != nil {
		t.Fatalf("AppendEvent() error = %v", err)
	}
	if err := eventStore.AppendEvent(context.Background(), events.Record{
		ID:         "evt_plan_1",
		SessionID:  "sess_1",
		TaskID:     "task_1",
		Type:       events.TypePlanApplyRejected,
		ActorType:  events.ActorTypeHuman,
		OccurredAt: time.Now().UTC(),
		ReasonCode: "validation_failed",
		Payload: map[string]any{
			"artifact_id":   "art_1",
			"changed_paths": []string{"README.md"},
			"violations":    []string{"execution plan forbidden path: README.md"},
		},
	}); err != nil {
		t.Fatalf("AppendEvent(plan) error = %v", err)
	}
	leaseStore, err := scheduler.NewLeaseStore(workDir)
	if err != nil {
		t.Fatalf("NewLeaseStore() error = %v", err)
	}
	if err := leaseStore.Acquire(context.Background(), "sess_1", "owner_1"); err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	if err := leaseStore.Renew(context.Background(), "sess_1", "owner_1", []string{"task_1"}, []scheduler.WorkspaceRef{{
		TaskID:        "task_1",
		EffectiveDir:  filepath.Join(workDir, ".roma", "workspaces", "sess_1", "task_1", "root"),
		Provider:      "git_worktree",
		EffectiveMode: "isolated_write",
	}}, []string{"sess_1__task_1"}, []string{}); err != nil {
		t.Fatalf("Renew() error = %v", err)
	}

	resp, err := client.QueueInspect(context.Background(), "job_1")
	if err != nil {
		t.Fatalf("QueueInspect() error = %v", err)
	}
	if resp.Job.ID != "job_1" {
		t.Fatalf("job id = %s, want job_1", resp.Job.ID)
	}
	if resp.Session == nil || resp.Session.ID != "sess_1" {
		t.Fatalf("session = %#v, want sess_1", resp.Session)
	}
	if len(resp.Artifacts) != 1 {
		t.Fatalf("artifact count = %d, want 1", len(resp.Artifacts))
	}
	if len(resp.Events) != 2 {
		t.Fatalf("event count = %d, want 2", len(resp.Events))
	}
	if len(resp.Plans) != 1 || resp.Plans[0].ArtifactID != "art_1" {
		t.Fatalf("plans = %#v, want one summary for art_1", resp.Plans)
	}
	if len(resp.Tasks) != 0 {
		t.Fatalf("task count = %d, want 0", len(resp.Tasks))
	}
	if len(resp.Workspaces) != 0 {
		t.Fatalf("workspace count = %d, want 0", len(resp.Workspaces))
	}
	if resp.Lease == nil || len(resp.Lease.WorkspaceRefs) != 1 {
		t.Fatalf("lease = %#v, want one workspace ref", resp.Lease)
	}
	if resp.ApprovalResumeReady || len(resp.PendingApprovalTaskIDs) != 1 {
		t.Fatalf("approval readiness = %t pending = %#v, want false with one pending task", resp.ApprovalResumeReady, resp.PendingApprovalTaskIDs)
	}
}

func TestServerTaskListAndShow(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	queueStore := queue.NewStore(workDir)
	sessionStore := history.NewStore(workDir)
	server := NewServer(workDir, queueStore, sessionStore)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := server.Start(ctx); err != nil {
		if errors.Is(err, ErrUnavailable) {
			t.Skipf("local listeners unavailable in this environment: %v", err)
		}
		t.Fatalf("Start() error = %v", err)
	}

	client := NewClient(workDir)
	deadline := time.Now().Add(2 * time.Second)
	for !client.Available() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	taskStore, err := taskstore.NewSQLiteStore(workDir)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	task := domain.TaskRecord{
		ID:         "sess_1__plan",
		SessionID:  "sess_1",
		Title:      "Plan",
		Strategy:   domain.TaskStrategyDirect,
		State:      domain.TaskStateSucceeded,
		AgentID:    "codex-cli",
		ArtifactID: "art_plan",
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}
	if err := taskStore.UpsertTask(context.Background(), task); err != nil {
		t.Fatalf("UpsertTask() error = %v", err)
	}

	items, err := client.TaskList(context.Background(), "sess_1")
	if err != nil {
		t.Fatalf("TaskList() error = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("task count = %d, want 1", len(items))
	}

	got, err := client.TaskGet(context.Background(), "sess_1__plan")
	if err != nil {
		t.Fatalf("TaskGet() error = %v", err)
	}
	if got.ArtifactID != "art_plan" {
		t.Fatalf("artifact id = %s, want art_plan", got.ArtifactID)
	}
}

func TestServerWorkspaceListShowAndCleanup(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	initAPIGitRepo(t, workDir)
	queueStore := queue.NewStore(workDir)
	sessionStore := history.NewStore(workDir)
	server := NewServer(workDir, queueStore, sessionStore)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := server.Start(ctx); err != nil {
		if errors.Is(err, ErrUnavailable) {
			t.Skipf("local listeners unavailable in this environment: %v", err)
		}
		t.Fatalf("Start() error = %v", err)
	}

	client := NewClient(workDir)
	deadline := time.Now().Add(2 * time.Second)
	for !client.Available() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	manager := workspacepkg.NewManager(workDir, nil)
	prepared, err := manager.Prepare(context.Background(), "sess_1", "task_1", workDir, domain.TaskStrategyDirect)
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}

	items, err := client.WorkspaceList(context.Background())
	if err != nil {
		t.Fatalf("WorkspaceList() error = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("workspace count = %d, want 1", len(items))
	}

	got, err := client.WorkspaceGet(context.Background(), "sess_1", "task_1")
	if err != nil {
		t.Fatalf("WorkspaceGet() error = %v", err)
	}
	if got.Provider != "git_worktree" {
		t.Fatalf("provider = %q, want git_worktree", got.Provider)
	}

	cleaned, err := client.WorkspaceCleanup(context.Background())
	if err != nil {
		t.Fatalf("WorkspaceCleanup() error = %v", err)
	}
	if len(cleaned) != 1 || cleaned[0].Status != "reclaimed" {
		t.Fatalf("cleanup result = %#v, want reclaimed workspace", cleaned)
	}
	if _, err := os.Stat(prepared.EffectiveDir); !os.IsNotExist(err) {
		t.Fatalf("expected cleaned worktree removed, stat err = %v", err)
	}
}

func TestServerWorkspaceMerge(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	initAPIGitRepo(t, workDir)
	queueStore := queue.NewStore(workDir)
	sessionStore := history.NewStore(workDir)
	server := NewServer(workDir, queueStore, sessionStore)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := server.Start(ctx); err != nil {
		if errors.Is(err, ErrUnavailable) {
			t.Skipf("local listeners unavailable in this environment: %v", err)
		}
		t.Fatalf("Start() error = %v", err)
	}

	client := NewClient(workDir)
	deadline := time.Now().Add(2 * time.Second)
	for !client.Available() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	manager := workspacepkg.NewManager(workDir, nil)
	prepared, err := manager.Prepare(context.Background(), "sess_merge", "task_merge", workDir, domain.TaskStrategyDirect)
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(prepared.EffectiveDir, "README.md"), []byte("roma via api\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	merged, err := client.WorkspaceMerge(context.Background(), "sess_merge", "task_merge")
	if err != nil {
		t.Fatalf("WorkspaceMerge() error = %v", err)
	}
	if merged.Status != "merged" {
		t.Fatalf("status = %q, want merged", merged.Status)
	}
	content, err := os.ReadFile(filepath.Join(workDir, "README.md"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if strings.TrimSpace(string(content)) != "roma via api" {
		t.Fatalf("base README = %q, want roma via api", strings.TrimSpace(string(content)))
	}
}

func TestServerSessionInspectIncludesWorkspaces(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	initAPIGitRepo(t, workDir)
	queueStore := queue.NewStore(workDir)
	sessionStore := history.NewStore(workDir)
	server := NewServer(workDir, queueStore, sessionStore)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := server.Start(ctx); err != nil {
		if errors.Is(err, ErrUnavailable) {
			t.Skipf("local listeners unavailable in this environment: %v", err)
		}
		t.Fatalf("Start() error = %v", err)
	}

	client := NewClient(workDir)
	deadline := time.Now().Add(2 * time.Second)
	for !client.Available() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	session := history.SessionRecord{
		ID:         "sess_1",
		TaskID:     "task_1",
		Prompt:     "test",
		Starter:    "codex-cli",
		WorkingDir: workDir,
		Status:     "running",
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}
	if err := sessionStore.Save(context.Background(), session); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	manager := workspacepkg.NewManager(workDir, nil)
	if _, err := manager.Prepare(context.Background(), "sess_1", "task_1", workDir, domain.TaskStrategyDirect); err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	leaseStore, err := scheduler.NewLeaseStore(workDir)
	if err != nil {
		t.Fatalf("NewLeaseStore() error = %v", err)
	}
	if err := leaseStore.Acquire(context.Background(), "sess_1", "owner_1"); err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	if err := leaseStore.Renew(context.Background(), "sess_1", "owner_1", nil, nil, []string{"sess_1__task_1"}, nil); err != nil {
		t.Fatalf("Renew() error = %v", err)
	}

	resp, err := client.SessionInspect(context.Background(), "sess_1")
	if err != nil {
		t.Fatalf("SessionInspect() error = %v", err)
	}
	if resp.Session.ID != "sess_1" {
		t.Fatalf("session id = %s, want sess_1", resp.Session.ID)
	}
	if len(resp.Workspaces) != 1 {
		t.Fatalf("workspace count = %d, want 1", len(resp.Workspaces))
	}
	if resp.Lease == nil || resp.Lease.SessionID != "sess_1" {
		t.Fatalf("lease = %#v, want sess_1 lease", resp.Lease)
	}
	if resp.ApprovalResumeReady || len(resp.PendingApprovalTaskIDs) != 1 {
		t.Fatalf("approval readiness = %t pending = %#v, want false with one pending task", resp.ApprovalResumeReady, resp.PendingApprovalTaskIDs)
	}
}

func TestServerRecoveryListIncludesLeaseState(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	queueStore := queue.NewStore(workDir)
	sessionStore := history.NewStore(workDir)
	server := NewServer(workDir, queueStore, sessionStore)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := server.Start(ctx); err != nil {
		if errors.Is(err, ErrUnavailable) {
			t.Skipf("local listeners unavailable in this environment: %v", err)
		}
		t.Fatalf("Start() error = %v", err)
	}

	client := NewClient(workDir)
	deadline := time.Now().Add(2 * time.Second)
	for !client.Available() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	session := history.SessionRecord{
		ID:         "sess_recover",
		TaskID:     "task_1",
		Prompt:     "recover me",
		Starter:    "codex-cli",
		WorkingDir: workDir,
		Status:     "running",
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}
	if err := sessionStore.Save(context.Background(), session); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	taskStore, err := taskstore.NewSQLiteStore(workDir)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	task := domain.TaskRecord{
		ID:        "sess_recover__task_1",
		SessionID: "sess_recover",
		Title:     "Task 1",
		Strategy:  domain.TaskStrategyDirect,
		State:     domain.TaskStateReady,
		AgentID:   "codex-cli",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := taskStore.UpsertTask(context.Background(), task); err != nil {
		t.Fatalf("UpsertTask() error = %v", err)
	}
	leaseStore, err := scheduler.NewLeaseStore(workDir)
	if err != nil {
		t.Fatalf("NewLeaseStore() error = %v", err)
	}
	if err := leaseStore.Acquire(context.Background(), "sess_recover", "owner_1"); err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	if err := leaseStore.Renew(context.Background(), "sess_recover", "owner_1", []string{"task_1"}, nil, []string{"sess_recover__task_1"}, nil); err != nil {
		t.Fatalf("Renew() error = %v", err)
	}

	items, err := client.RecoveryList(context.Background())
	if err != nil {
		t.Fatalf("RecoveryList() error = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("recovery count = %d, want 1", len(items))
	}
	if items[0].SessionID != "sess_recover" {
		t.Fatalf("session id = %s, want sess_recover", items[0].SessionID)
	}
	if items[0].Lease == nil || items[0].Lease.SessionID != "sess_recover" {
		t.Fatalf("lease = %#v, want sess_recover lease", items[0].Lease)
	}
	if items[0].ApprovalResumeReady || len(items[0].PendingApprovalTaskIDs) != 1 {
		t.Fatalf("approval readiness = %t pending = %#v, want false with one pending task", items[0].ApprovalResumeReady, items[0].PendingApprovalTaskIDs)
	}
}

func TestServerStatus(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	queueStore := queue.NewStore(workDir)
	sessionStore := history.NewStore(workDir)
	server := NewServer(workDir, queueStore, sessionStore)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := server.Start(ctx); err != nil {
		if errors.Is(err, ErrUnavailable) {
			t.Skipf("local listeners unavailable in this environment: %v", err)
		}
		t.Fatalf("Start() error = %v", err)
	}

	client := NewClient(workDir)
	deadline := time.Now().Add(2 * time.Second)
	for !client.Available() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	if err := queueStore.Enqueue(context.Background(), queue.Request{ID: "job_1", Prompt: "test", StarterAgent: "codex", WorkingDir: workDir}); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}
	if err := sessionStore.Save(context.Background(), history.SessionRecord{
		ID:         "sess_1",
		TaskID:     "task_1",
		Prompt:     "test",
		Starter:    "codex-cli",
		WorkingDir: workDir,
		Status:     "running",
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	status, err := client.Status(context.Background())
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if status.QueueItems != 1 {
		t.Fatalf("queue items = %d, want 1", status.QueueItems)
	}
	if status.Sessions != 1 {
		t.Fatalf("sessions = %d, want 1", status.Sessions)
	}
	if !status.SQLiteEnabled {
		t.Fatal("sqlite should be enabled")
	}
}

func TestServerQueueApproveDelegatesToPendingTasks(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	queueStore := queue.NewStore(workDir)
	sessionStore := history.NewStore(workDir)
	server := NewServer(workDir, queueStore, sessionStore)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := server.Start(ctx); err != nil {
		if errors.Is(err, ErrUnavailable) {
			t.Skipf("local listeners unavailable in this environment: %v", err)
		}
		t.Fatalf("Start() error = %v", err)
	}

	client := NewClient(workDir)
	deadline := time.Now().Add(2 * time.Second)
	for !client.Available() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	session := history.SessionRecord{
		ID:         "sess_gate",
		TaskID:     "task_gate",
		Prompt:     "risky task",
		Starter:    "codex-cli",
		WorkingDir: workDir,
		Status:     "awaiting_approval",
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}
	if err := sessionStore.Save(context.Background(), session); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	taskStore, err := taskstore.NewSQLiteStore(workDir)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	task := domain.TaskRecord{
		ID:        "sess_gate__task_1",
		SessionID: "sess_gate",
		Title:     "Gate",
		Strategy:  domain.TaskStrategyDirect,
		State:     domain.TaskStateAwaitingApproval,
		AgentID:   "codex-cli",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := taskStore.UpsertTask(context.Background(), task); err != nil {
		t.Fatalf("UpsertTask() error = %v", err)
	}
	job := queue.Request{
		ID:        "job_gate",
		Prompt:    "risky task",
		SessionID: "sess_gate",
		TaskID:    "task_gate",
		Status:    queue.StatusAwaitingApproval,
	}
	if err := queueStore.Enqueue(context.Background(), job); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}
	queued, err := queueStore.Get(context.Background(), "job_gate")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	queued.Status = queue.StatusAwaitingApproval
	if err := queueStore.Update(context.Background(), queued); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	leaseStore, err := scheduler.NewLeaseStore(workDir)
	if err != nil {
		t.Fatalf("NewLeaseStore() error = %v", err)
	}
	if err := leaseStore.Acquire(context.Background(), "sess_gate", "owner_1"); err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	if err := leaseStore.Renew(context.Background(), "sess_gate", "owner_1", nil, nil, []string{"sess_gate__task_1"}, nil); err != nil {
		t.Fatalf("Renew() error = %v", err)
	}

	updated, err := client.QueueApprove(context.Background(), "job_gate")
	if err != nil {
		t.Fatalf("QueueApprove() error = %v", err)
	}
	if updated.Status != queue.StatusPending {
		t.Fatalf("queue status = %s, want pending", updated.Status)
	}
	task, err = taskStore.GetTask(context.Background(), "sess_gate__task_1")
	if err != nil {
		t.Fatalf("GetTask() error = %v", err)
	}
	if task.State != domain.TaskStateReady || !task.ApprovalGranted {
		t.Fatalf("task = %#v, want ready with approval granted", task)
	}
	lease, err := leaseStore.Get(context.Background(), "sess_gate")
	if err != nil {
		t.Fatalf("Get lease error = %v", err)
	}
	if len(lease.PendingApprovalTaskIDs) != 0 {
		t.Fatalf("pending approvals = %#v, want empty", lease.PendingApprovalTaskIDs)
	}
	session, err = sessionStore.Get(context.Background(), "sess_gate")
	if err != nil {
		t.Fatalf("Get session error = %v", err)
	}
	if session.Status != "running" {
		t.Fatalf("session status = %s, want running", session.Status)
	}
}

func TestServerPlanApplyDryRunAndApprovalGate(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	initAPIGitRepo(t, workDir)
	queueStore := queue.NewStore(workDir)
	sessionStore := history.NewStore(workDir)
	server := NewServer(workDir, queueStore, sessionStore)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := server.Start(ctx); err != nil {
		if errors.Is(err, ErrUnavailable) {
			t.Skipf("local listeners unavailable in this environment: %v", err)
		}
		t.Fatalf("Start() error = %v", err)
	}

	client := NewClient(workDir)
	deadline := time.Now().Add(2 * time.Second)
	for !client.Available() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	manager := workspacepkg.NewManager(workDir, storepkg.NewMemoryStore())
	prepared, err := manager.Prepare(context.Background(), "sess_plan", "task_plan", workDir, domain.TaskStrategyDirect)
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(prepared.EffectiveDir, "README.md"), []byte("roma changed\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	artifactStore, err := artifacts.NewSQLiteStore(workDir)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	svc := artifacts.NewService()
	envelope, err := svc.BuildExecutionPlan(context.Background(), artifacts.BuildExecutionPlanRequest{
		SessionID: "sess_plan",
		TaskID:    "task_plan",
		RunID:     "task_plan",
		Goal:      "Apply README change",
		Proposal: artifacts.ProposalPayload{
			ProposalID:     "prop_task_plan",
			Summary:        "Change README",
			EstimatedSteps: []string{"Edit README"},
			AffectedFiles:  []string{"README.md"},
		},
		HumanApprovalRequired: true,
	})
	if err != nil {
		t.Fatalf("BuildExecutionPlan() error = %v", err)
	}
	payload, ok := artifacts.ExecutionPlanFromEnvelope(envelope)
	if !ok {
		t.Fatal("ExecutionPlanFromEnvelope() = false")
	}
	payload.RequiredChecks = nil
	envelope.Payload = payload
	if err := artifactStore.Save(context.Background(), envelope); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	dryRun, err := client.PlanApply(context.Background(), PlanApplyRequest{
		SessionID:  "sess_plan",
		TaskID:     "task_plan",
		ArtifactID: envelope.ID,
		DryRun:     true,
	})
	if err != nil {
		t.Fatalf("PlanApply(dry-run) error = %v", err)
	}
	if !dryRun.DryRun || dryRun.Applied {
		t.Fatalf("dryRun = %#v, want dry-run only", dryRun)
	}

	if _, err := client.PlanApply(context.Background(), PlanApplyRequest{
		SessionID:  "sess_plan",
		TaskID:     "task_plan",
		ArtifactID: envelope.ID,
	}); err == nil {
		t.Fatal("PlanApply() error = nil, want approval conflict")
	}

	applied, err := client.PlanApply(context.Background(), PlanApplyRequest{
		SessionID:           "sess_plan",
		TaskID:              "task_plan",
		ArtifactID:          envelope.ID,
		PolicyOverride:      true,
		PolicyOverrideActor: "local_owner",
	})
	if err != nil {
		t.Fatalf("PlanApply() with override error = %v", err)
	}
	if !applied.Applied {
		t.Fatalf("applied = %#v, want applied=true", applied)
	}

	eventStore, err := storepkg.NewSQLiteEventStore(workDir)
	if err != nil {
		t.Fatalf("NewSQLiteEventStore() error = %v", err)
	}
	records, err := eventStore.ListEvents(context.Background(), storepkg.EventFilter{SessionID: "sess_plan"})
	if err != nil {
		t.Fatalf("ListEvents() error = %v", err)
	}
	var rejected, appliedEvents int
	for _, record := range records {
		switch record.Type {
		case events.TypePlanApplyRejected:
			rejected++
		case events.TypePlanApplied:
			appliedEvents++
		}
	}
	if rejected == 0 {
		t.Fatal("expected at least one PlanApplyRejected event")
	}
	if appliedEvents != 2 {
		t.Fatalf("plan applied event count = %d, want 2", appliedEvents)
	}
}

func TestServerPlanInbox(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	initAPIGitRepo(t, workDir)
	queueStore := queue.NewStore(workDir)
	sessionStore := history.NewStore(workDir)
	server := NewServer(workDir, queueStore, sessionStore)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := server.Start(ctx); err != nil {
		if errors.Is(err, ErrUnavailable) {
			t.Skipf("local listeners unavailable in this environment: %v", err)
		}
		t.Fatalf("Start() error = %v", err)
	}

	client := NewClient(workDir)
	deadline := time.Now().Add(2 * time.Second)
	for !client.Available() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	artifactStore, err := artifacts.NewSQLiteStore(workDir)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	svc := artifacts.NewService()
	envelope, err := svc.BuildExecutionPlan(context.Background(), artifacts.BuildExecutionPlanRequest{
		SessionID: "sess_plan_inbox",
		TaskID:    "task_plan_inbox",
		RunID:     "task_plan_inbox",
		Goal:      "Approve README change",
		Proposal: artifacts.ProposalPayload{
			ProposalID:     "prop_task_plan_inbox",
			Summary:        "Change README",
			EstimatedSteps: []string{"Edit README"},
			AffectedFiles:  []string{"README.md"},
		},
		HumanApprovalRequired: true,
	})
	if err != nil {
		t.Fatalf("BuildExecutionPlan() error = %v", err)
	}
	if err := artifactStore.Save(context.Background(), envelope); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	eventStore, err := storepkg.NewSQLiteEventStore(workDir)
	if err != nil {
		t.Fatalf("NewSQLiteEventStore() error = %v", err)
	}
	if err := eventStore.AppendEvent(context.Background(), events.Record{
		ID:         "evt_plan_inbox_1",
		SessionID:  "sess_plan_inbox",
		TaskID:     "task_plan_inbox",
		Type:       events.TypePlanApplyRejected,
		ActorType:  events.ActorTypeHuman,
		OccurredAt: time.Now().UTC(),
		ReasonCode: "approval_required",
		Payload: map[string]any{
			"artifact_id": envelope.ID,
		},
	}); err != nil {
		t.Fatalf("AppendEvent() error = %v", err)
	}

	items, err := client.PlanInbox(context.Background(), "sess_plan_inbox")
	if err != nil {
		t.Fatalf("PlanInbox() error = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("plan inbox count = %d, want 1", len(items))
	}
	if items[0].Status != "pending_approval" {
		t.Fatalf("status = %q, want pending_approval", items[0].Status)
	}

	if err := client.PlanApprove(context.Background(), envelope.ID, "local_owner"); err != nil {
		t.Fatalf("PlanApprove() error = %v", err)
	}
	items, err = client.PlanInbox(context.Background(), "sess_plan_inbox")
	if err != nil {
		t.Fatalf("PlanInbox() after approve error = %v", err)
	}
	if items[0].Status != "approved" {
		t.Fatalf("status after approve = %q, want approved", items[0].Status)
	}
}

func initAPIGitRepo(t *testing.T, dir string) {
	t.Helper()
	runAPIGit(t, dir, "init")
	runAPIGit(t, dir, "config", "user.email", "roma@example.com")
	runAPIGit(t, dir, "config", "user.name", "ROMA")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("roma\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	runAPIGit(t, dir, "add", "README.md")
	runAPIGit(t, dir, "commit", "-m", "init")
}

func runAPIGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmdArgs := append([]string{"-C", dir}, args...)
	cmd := exec.Command("git", cmdArgs...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s error = %v (%s)", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
}
