package sessions

import (
	"context"
	"testing"

	"github.com/liliang/roma/internal/domain"
	"github.com/liliang/roma/internal/store"
)

func TestServiceCreateAndSubmitTaskGraph(t *testing.T) {
	t.Parallel()

	mem := store.NewMemoryStore()
	svc := NewService(mem, mem, mem)
	ctx := context.Background()

	session, err := svc.Create(ctx, CreateSessionRequest{
		ID:          "sess_1",
		Description: "bootstrap",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	err = svc.SubmitTaskGraph(ctx, session.ID, domain.TaskGraph{
		Nodes: []domain.TaskNodeSpec{
			{ID: "task_a", Title: "A", Strategy: domain.TaskStrategyDirect},
			{ID: "task_b", Title: "B", Strategy: domain.TaskStrategyRelay, Dependencies: []string{"task_a"}},
		},
	})
	if err != nil {
		t.Fatalf("SubmitTaskGraph() error = %v", err)
	}

	snapshot, err := svc.Rebuild(ctx, session.ID)
	if err != nil {
		t.Fatalf("Rebuild() error = %v", err)
	}

	if snapshot.Session.State != domain.SessionStateRunning {
		t.Fatalf("session state = %s, want %s", snapshot.Session.State, domain.SessionStateRunning)
	}
	if len(snapshot.Tasks) != 2 {
		t.Fatalf("task count = %d, want 2", len(snapshot.Tasks))
	}
	if snapshot.Tasks[0].State != domain.TaskStateReady {
		t.Fatalf("first task state = %s, want %s", snapshot.Tasks[0].State, domain.TaskStateReady)
	}
	if snapshot.Tasks[1].State != domain.TaskStatePending {
		t.Fatalf("second task state = %s, want %s", snapshot.Tasks[1].State, domain.TaskStatePending)
	}
}
