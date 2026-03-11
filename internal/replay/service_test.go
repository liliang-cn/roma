package replay

import (
	"context"
	"testing"
	"time"

	"github.com/liliang/roma/internal/events"
	"github.com/liliang/roma/internal/store"
)

func TestReplaySessionRebuildsTaskAndArtifactState(t *testing.T) {
	t.Parallel()

	mem := store.NewMemoryStore()
	svc := NewService(mem)
	ctx := context.Background()
	now := time.Now().UTC()

	for _, item := range []events.Record{
		{
			ID:         "evt_1",
			SessionID:  "sess_1",
			TaskID:     "task_root",
			Type:       events.TypeSessionCreated,
			ActorType:  events.ActorTypeSystem,
			OccurredAt: now,
		},
		{
			ID:         "evt_2",
			SessionID:  "sess_1",
			TaskID:     "sess_1__plan",
			Type:       events.TypeTaskStateChanged,
			ActorType:  events.ActorTypeScheduler,
			OccurredAt: now.Add(1 * time.Second),
			ReasonCode: "Pending",
			Payload: map[string]any{
				"node_title":   "Plan",
				"strategy":     "direct",
				"agent_id":     "codex-cli",
				"dependencies": []any{},
			},
		},
		{
			ID:         "evt_3",
			SessionID:  "sess_1",
			TaskID:     "sess_1__plan",
			Type:       events.TypeTaskStateChanged,
			ActorType:  events.ActorTypeScheduler,
			OccurredAt: now.Add(2 * time.Second),
			ReasonCode: "Succeeded",
			Payload: map[string]any{
				"node_title":  "Plan",
				"strategy":    "direct",
				"agent_id":    "codex-cli",
				"artifact_id": "art_plan",
			},
		},
		{
			ID:         "evt_4",
			SessionID:  "sess_1",
			TaskID:     "task_root",
			Type:       events.TypeArtifactStored,
			ActorType:  events.ActorTypeSystem,
			OccurredAt: now.Add(3 * time.Second),
			Payload: map[string]any{
				"artifact_id": "art_plan",
			},
		},
		{
			ID:         "evt_5",
			SessionID:  "sess_1",
			TaskID:     "task_root",
			Type:       events.TypeSessionStateChanged,
			ActorType:  events.ActorTypeSystem,
			OccurredAt: now.Add(4 * time.Second),
			ReasonCode: "succeeded",
			Payload: map[string]any{
				"artifact_ids": []any{"art_plan"},
			},
		},
		{
			ID:         "evt_6",
			SessionID:  "sess_1",
			TaskID:     "sess_1__plan",
			Type:       events.TypePlanApplyRejected,
			ActorType:  events.ActorTypeHuman,
			OccurredAt: now.Add(5 * time.Second),
			ReasonCode: "validation_failed",
			Payload: map[string]any{
				"artifact_id":   "art_plan",
				"changed_paths": []any{"README.md"},
				"violations":    []any{"execution plan forbidden path: README.md"},
			},
		},
	} {
		if err := mem.AppendEvent(ctx, item); err != nil {
			t.Fatalf("AppendEvent() error = %v", err)
		}
	}

	snapshot, err := svc.ReplaySession(ctx, "sess_1")
	if err != nil {
		t.Fatalf("ReplaySession() error = %v", err)
	}
	if snapshot.SessionID != "sess_1" {
		t.Fatalf("session id = %s, want sess_1", snapshot.SessionID)
	}
	if snapshot.TaskID != "task_root" {
		t.Fatalf("task id = %s, want task_root", snapshot.TaskID)
	}
	if snapshot.Status != "succeeded" {
		t.Fatalf("status = %s, want succeeded", snapshot.Status)
	}
	if len(snapshot.Tasks) != 1 {
		t.Fatalf("task count = %d, want 1", len(snapshot.Tasks))
	}
	task := snapshot.Tasks[0]
	if task.NodeID != "plan" {
		t.Fatalf("node id = %s, want plan", task.NodeID)
	}
	if task.ArtifactID != "art_plan" {
		t.Fatalf("artifact id = %s, want art_plan", task.ArtifactID)
	}
	if len(snapshot.ArtifactIDs) != 1 || snapshot.ArtifactIDs[0] != "art_plan" {
		t.Fatalf("artifact ids = %#v, want [art_plan]", snapshot.ArtifactIDs)
	}
	if len(snapshot.Plans) != 1 {
		t.Fatalf("plan count = %d, want 1", len(snapshot.Plans))
	}
	if snapshot.Plans[0].Reason != "validation_failed" {
		t.Fatalf("plan reason = %q, want validation_failed", snapshot.Plans[0].Reason)
	}
}
