package scheduler

import (
	"context"
	"testing"

	"github.com/liliang/roma/internal/domain"
	"github.com/liliang/roma/internal/events"
	"github.com/liliang/roma/internal/store"
)

func TestGraphLifecycleTransitions(t *testing.T) {
	t.Parallel()

	mem := store.NewMemoryStore()
	lifecycle := NewGraphLifecycle(mem, mem)
	node := domain.TaskNodeSpec{
		ID:            "plan",
		Title:         "Plan",
		Strategy:      domain.TaskStrategyDirect,
		Dependencies:  []string{"input"},
		SchemaVersion: "v1",
	}

	if err := lifecycle.RegisterTask(context.Background(), "sess_1", node, "codex-cli"); err != nil {
		t.Fatalf("RegisterTask() error = %v", err)
	}
	if err := lifecycle.MarkRunning(context.Background(), "sess_1", "plan"); err != nil {
		t.Fatalf("MarkRunning() error = %v", err)
	}
	if err := lifecycle.MarkFinished(context.Background(), "sess_1", "plan", "art_plan", nil); err != nil {
		t.Fatalf("MarkFinished() error = %v", err)
	}

	record, err := mem.GetTask(context.Background(), "sess_1__plan")
	if err != nil {
		t.Fatalf("GetTask() error = %v", err)
	}
	if record.State != domain.TaskStateSucceeded {
		t.Fatalf("state = %s, want %s", record.State, domain.TaskStateSucceeded)
	}
	if record.ArtifactID != "art_plan" {
		t.Fatalf("artifact_id = %s, want art_plan", record.ArtifactID)
	}

	events, err := mem.ListEvents(context.Background(), store.EventFilter{SessionID: "sess_1"})
	if err != nil {
		t.Fatalf("ListEvents() error = %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("event count = %d, want 3", len(events))
	}
}

func TestGraphLifecycleReadyNodeIDs(t *testing.T) {
	t.Parallel()

	mem := store.NewMemoryStore()
	lifecycle := NewGraphLifecycle(mem, mem)
	if err := lifecycle.RegisterTask(context.Background(), "sess_2", domain.TaskNodeSpec{
		ID:            "plan",
		Title:         "Plan",
		Strategy:      domain.TaskStrategyDirect,
		SchemaVersion: "v1",
	}, "codex-cli"); err != nil {
		t.Fatalf("RegisterTask(plan) error = %v", err)
	}
	if err := lifecycle.RegisterTask(context.Background(), "sess_2", domain.TaskNodeSpec{
		ID:            "refine",
		Title:         "Refine",
		Strategy:      domain.TaskStrategyRelay,
		Dependencies:  []string{"plan"},
		SchemaVersion: "v1",
	}, "codex-cli"); err != nil {
		t.Fatalf("RegisterTask(refine) error = %v", err)
	}

	ready, err := lifecycle.ReadyTasks(context.Background(), "sess_2")
	if err != nil {
		t.Fatalf("ReadyTasks() error = %v", err)
	}
	if len(ready) != 1 || ready[0].ID != "sess_2__plan" {
		t.Fatalf("ready = %#v, want [sess_2__plan]", ready)
	}

	record, err := mem.GetTask(context.Background(), "sess_2__plan")
	if err != nil {
		t.Fatalf("GetTask() error = %v", err)
	}
	if record.State != domain.TaskStateReady {
		t.Fatalf("state = %s, want %s", record.State, domain.TaskStateReady)
	}

	events, err := mem.ListEvents(context.Background(), store.EventFilter{SessionID: "sess_2", Type: events.TypeSchedulerCheckpointRecorded})
	if err != nil {
		t.Fatalf("ListEvents() error = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("checkpoint event count = %d, want 1", len(events))
	}
}
