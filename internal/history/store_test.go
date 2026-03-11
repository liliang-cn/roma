package history

import (
	"context"
	"testing"
	"time"
)

func TestStoreSaveAndGet(t *testing.T) {
	t.Parallel()

	store := NewStore(t.TempDir())
	record := SessionRecord{
		ID:        "sess_1",
		TaskID:    "task_1",
		Prompt:    "test",
		Starter:   "codex-cli",
		Status:    "succeeded",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := store.Save(context.Background(), record); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	got, err := store.Get(context.Background(), "sess_1")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.ID != record.ID {
		t.Fatalf("Get() id = %s, want %s", got.ID, record.ID)
	}
}

func TestRecoverInterrupted(t *testing.T) {
	t.Parallel()

	store := NewStore(t.TempDir())
	record := SessionRecord{
		ID:        "sess_1",
		TaskID:    "task_1",
		Prompt:    "test",
		Starter:   "codex-cli",
		Status:    "running",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := store.Save(context.Background(), record); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := store.RecoverInterrupted(context.Background()); err != nil {
		t.Fatalf("RecoverInterrupted() error = %v", err)
	}
	got, err := store.Get(context.Background(), "sess_1")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Status != "failed_recoverable" {
		t.Fatalf("status = %s, want failed_recoverable", got.Status)
	}
}
