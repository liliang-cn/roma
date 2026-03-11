package queue

import (
	"context"
	"testing"
)

func TestStoreEnqueueAndGet(t *testing.T) {
	t.Parallel()

	store := NewStore(t.TempDir())
	err := store.Enqueue(context.Background(), Request{
		ID:           "job_1",
		Prompt:       "test",
		StarterAgent: "codex",
		WorkingDir:   ".",
	})
	if err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}

	got, err := store.Get(context.Background(), "job_1")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Status != StatusPending {
		t.Fatalf("status = %s, want %s", got.Status, StatusPending)
	}
}

func TestRecoverInterrupted(t *testing.T) {
	t.Parallel()

	store := NewStore(t.TempDir())
	err := store.Enqueue(context.Background(), Request{
		ID:           "job_1",
		Prompt:       "test",
		StarterAgent: "codex",
		WorkingDir:   ".",
	})
	if err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}

	req, err := store.Get(context.Background(), "job_1")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	req.Status = StatusRunning
	if err := store.Update(context.Background(), req); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	if err := store.RecoverInterrupted(context.Background()); err != nil {
		t.Fatalf("RecoverInterrupted() error = %v", err)
	}

	got, err := store.Get(context.Background(), "job_1")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Status != StatusPending {
		t.Fatalf("status = %s, want %s", got.Status, StatusPending)
	}
}
