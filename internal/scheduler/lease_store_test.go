package scheduler

import (
	"context"
	"testing"
)

func TestLeaseStoreLifecycle(t *testing.T) {
	t.Parallel()

	store, err := NewLeaseStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLeaseStore() error = %v", err)
	}
	ctx := context.Background()
	if err := store.Acquire(ctx, "sess_1", "owner_1"); err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	if err := store.Renew(ctx, "sess_1", "owner_1", []string{"task_a"}, []WorkspaceRef{{
		TaskID:        "task_a",
		EffectiveDir:  "/tmp/task_a",
		Provider:      "git_worktree",
		EffectiveMode: "isolated_write",
	}}, []string{"task_gate"}, []string{"task_done"}); err != nil {
		t.Fatalf("Renew() error = %v", err)
	}
	record, err := store.Get(ctx, "sess_1")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if record.Status != LeaseStatusActive || len(record.ReadyTaskIDs) != 1 || len(record.CompletedTaskIDs) != 1 || len(record.WorkspaceRefs) != 1 || len(record.PendingApprovalTaskIDs) != 1 {
		t.Fatalf("record = %#v", record)
	}
	if err := store.Release(ctx, "sess_1", "owner_1", []string{"task_a"}); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	record, err = store.Get(ctx, "sess_1")
	if err != nil {
		t.Fatalf("Get() after release error = %v", err)
	}
	if record.Status != LeaseStatusReleased || len(record.CompletedTaskIDs) != 1 {
		t.Fatalf("released record = %#v", record)
	}
}

func TestLeaseStoreRecoverActive(t *testing.T) {
	t.Parallel()

	store, err := NewLeaseStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLeaseStore() error = %v", err)
	}
	ctx := context.Background()
	if err := store.Acquire(ctx, "sess_2", "owner_2"); err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	if err := store.RecoverActive(ctx); err != nil {
		t.Fatalf("RecoverActive() error = %v", err)
	}
	record, err := store.Get(ctx, "sess_2")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if record.Status != LeaseStatusRecovered {
		t.Fatalf("status = %s, want %s", record.Status, LeaseStatusRecovered)
	}
}
