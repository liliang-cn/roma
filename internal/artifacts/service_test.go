package artifacts

import (
	"context"
	"strings"
	"testing"

	"github.com/liliang/roma/internal/domain"
)

func TestBuildReport(t *testing.T) {
	t.Parallel()

	svc := NewService()
	envelope, err := svc.BuildReport(context.Background(), BuildReportRequest{
		SessionID: "sess_1",
		TaskID:    "task_1",
		RunID:     "run_1",
		Agent: domain.AgentProfile{
			ID:          "codex-cli",
			DisplayName: "Codex CLI",
		},
		Result: "success",
		Output: "line one\nline two\nline three",
	})
	if err != nil {
		t.Fatalf("BuildReport() error = %v", err)
	}
	if envelope.Kind != domain.ArtifactKindReport {
		t.Fatalf("kind = %s, want %s", envelope.Kind, domain.ArtifactKindReport)
	}
	if !strings.HasPrefix(envelope.Checksum, "sha256:") {
		t.Fatalf("checksum = %q, want sha256 prefix", envelope.Checksum)
	}
	if got := SummaryFromEnvelope(envelope); got == "" {
		t.Fatal("SummaryFromEnvelope() returned empty summary")
	}
}
