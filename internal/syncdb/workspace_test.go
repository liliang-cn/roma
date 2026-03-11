package syncdb

import (
	"context"
	"testing"
	"time"

	"github.com/liliang/roma/internal/artifacts"
	"github.com/liliang/roma/internal/domain"
	"github.com/liliang/roma/internal/events"
	"github.com/liliang/roma/internal/history"
	"github.com/liliang/roma/internal/queue"
	"github.com/liliang/roma/internal/store"
	"github.com/liliang/roma/internal/taskstore"
)

func TestWorkspaceRunBackfillsFileMetadataIntoSQLite(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	ctx := context.Background()

	if err := history.NewStore(workDir).Save(ctx, history.SessionRecord{
		ID:         "sess_1",
		TaskID:     "task_1",
		Prompt:     "test",
		Starter:    "codex-cli",
		WorkingDir: ".",
		Status:     "succeeded",
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}); err != nil {
		t.Fatalf("save session error = %v", err)
	}
	if err := taskstore.NewStore(workDir).UpsertTask(ctx, domain.TaskRecord{
		ID:        "sess_1__plan",
		SessionID: "sess_1",
		Title:     "Plan",
		Strategy:  domain.TaskStrategyDirect,
		State:     domain.TaskStateSucceeded,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("save task error = %v", err)
	}
	if err := store.NewFileEventStore(workDir).AppendEvent(ctx, events.Record{
		ID:         "evt_1",
		SessionID:  "sess_1",
		TaskID:     "task_1",
		Type:       events.TypeSessionCreated,
		ActorType:  events.ActorTypeSystem,
		OccurredAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("save event error = %v", err)
	}
	if err := queue.NewStore(workDir).Enqueue(ctx, queue.Request{
		ID:           "job_1",
		Prompt:       "test",
		StarterAgent: "codex",
		WorkingDir:   ".",
	}); err != nil {
		t.Fatalf("save queue error = %v", err)
	}
	if err := artifacts.NewFileStore(workDir).Save(ctx, domain.ArtifactEnvelope{
		ID:            "art_1",
		Kind:          domain.ArtifactKindReport,
		SchemaVersion: "v1",
		SessionID:     "sess_1",
		TaskID:        "task_1",
		CreatedAt:     time.Now().UTC(),
		PayloadSchema: artifacts.ReportPayloadSchema,
		Payload: map[string]any{
			"summary": "ok",
		},
		Checksum: "sha256:test",
	}); err != nil {
		t.Fatalf("save artifact error = %v", err)
	}

	if err := NewWorkspace(workDir).Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if _, err := history.NewSQLiteStore(workDir); err != nil {
		t.Fatalf("sqlite history init error = %v", err)
	}
	if items, err := queue.NewSQLiteStore(workDir); err != nil {
		t.Fatalf("sqlite queue init error = %v", err)
	} else if _, err := items.Get(ctx, "job_1"); err != nil {
		t.Fatalf("sqlite queue get error = %v", err)
	}
	if items, err := artifacts.NewSQLiteStore(workDir); err != nil {
		t.Fatalf("sqlite artifacts init error = %v", err)
	} else if _, err := items.Get(ctx, "art_1"); err != nil {
		t.Fatalf("sqlite artifact get error = %v", err)
	}
}
