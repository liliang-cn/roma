package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/liliang-cn/roma/internal/domain"
	"github.com/liliang-cn/roma/internal/store"
)

func TestListReadyTasks(t *testing.T) {
	t.Parallel()

	mem := store.NewMemoryStore()
	ctx := context.Background()

	if err := mem.CreateSession(ctx, domain.SessionRecord{
		ID:        "sess_1",
		State:     domain.SessionStateRunning,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}

	for _, task := range []domain.TaskRecord{
		{
			ID:        "task_a",
			SessionID: "sess_1",
			Title:     "A",
			Strategy:  domain.TaskStrategyDirect,
			State:     domain.TaskStateReady,
			CreatedAt: time.Now().UTC(),
			UpdatedAt: time.Now().UTC(),
		},
		{
			ID:        "task_b",
			SessionID: "sess_1",
			Title:     "B",
			Strategy:  domain.TaskStrategyRelay,
			State:     domain.TaskStatePending,
			CreatedAt: time.Now().UTC(),
			UpdatedAt: time.Now().UTC(),
		},
	} {
		if err := mem.UpsertTask(ctx, task); err != nil {
			t.Fatalf("UpsertTask() error = %v", err)
		}
	}

	svc := NewService(mem, mem, mem)
	ready, err := svc.ListReadyTasks(ctx, "sess_1")
	if err != nil {
		t.Fatalf("ListReadyTasks() error = %v", err)
	}
	if len(ready) != 1 {
		t.Fatalf("ready task count = %d, want 1", len(ready))
	}
	if ready[0].ID != "task_a" {
		t.Fatalf("ready task id = %s, want task_a", ready[0].ID)
	}
}
