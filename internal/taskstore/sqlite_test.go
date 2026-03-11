package taskstore

import (
	"context"
	"testing"
	"time"

	"github.com/liliang/roma/internal/domain"
)

func TestSQLiteStoreUpsertAndList(t *testing.T) {
	t.Parallel()

	s, err := NewSQLiteStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	record := domain.TaskRecord{
		ID:           "sess_1__plan",
		SessionID:    "sess_1",
		Title:        "Plan",
		Strategy:     domain.TaskStrategyDirect,
		State:        domain.TaskStateSucceeded,
		AgentID:      "codex-cli",
		Dependencies: []string{"bootstrap"},
		ArtifactID:   "art_plan",
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}
	if err := s.UpsertTask(context.Background(), record); err != nil {
		t.Fatalf("UpsertTask() error = %v", err)
	}
	items, err := s.ListTasksBySession(context.Background(), "sess_1")
	if err != nil {
		t.Fatalf("ListTasksBySession() error = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("task count = %d, want 1", len(items))
	}
	if items[0].ArtifactID != "art_plan" {
		t.Fatalf("artifact id = %s, want art_plan", items[0].ArtifactID)
	}
}
