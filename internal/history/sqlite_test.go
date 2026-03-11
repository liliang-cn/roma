package history

import (
	"context"
	"testing"
	"time"
)

func TestSQLiteStoreSaveAndGet(t *testing.T) {
	t.Parallel()

	s, err := NewSQLiteStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	record := SessionRecord{
		ID:          "sess_1",
		TaskID:      "task_1",
		Prompt:      "test",
		Starter:     "codex-cli",
		Delegates:   []string{"gemini"},
		WorkingDir:  ".",
		Status:      "succeeded",
		ArtifactIDs: []string{"art_1"},
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}
	if err := s.Save(context.Background(), record); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	got, err := s.Get(context.Background(), "sess_1")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.ID != "sess_1" || len(got.Delegates) != 1 || len(got.ArtifactIDs) != 1 {
		t.Fatalf("got = %#v", got)
	}
}
