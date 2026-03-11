package artifacts

import (
	"context"
	"testing"
	"time"

	"github.com/liliang-cn/roma/internal/domain"
)

func TestFileStoreSaveAndGet(t *testing.T) {
	t.Parallel()

	store := NewFileStore(t.TempDir())
	envelope := domain.ArtifactEnvelope{
		ID:            "art_1",
		Kind:          domain.ArtifactKindReport,
		SchemaVersion: "v1",
		SessionID:     "sess_1",
		TaskID:        "task_1",
		CreatedAt:     time.Now().UTC(),
		PayloadSchema: ReportPayloadSchema,
		Payload: ReportPayload{
			ReportID:        "report_1",
			Summary:         "ok",
			Result:          "success",
			SourceAgentID:   "codex-cli",
			SourceAgentName: "Codex CLI",
		},
		Checksum: "sha256:test",
	}

	if err := store.Save(context.Background(), envelope); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	got, err := store.Get(context.Background(), "art_1")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.ID != envelope.ID {
		t.Fatalf("Get() id = %s, want %s", got.ID, envelope.ID)
	}
}
