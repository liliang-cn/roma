package api

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/liliang/roma/internal/artifacts"
	"github.com/liliang/roma/internal/domain"
	"github.com/liliang/roma/internal/events"
	"github.com/liliang/roma/internal/history"
	"github.com/liliang/roma/internal/queue"
	storepkg "github.com/liliang/roma/internal/store"
	"github.com/liliang/roma/internal/taskstore"
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
	if len(resp.Events) != 1 {
		t.Fatalf("event count = %d, want 1", len(resp.Events))
	}
	if len(resp.Tasks) != 0 {
		t.Fatalf("task count = %d, want 0", len(resp.Tasks))
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
