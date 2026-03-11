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
		Output: "line one\nROMA_FOLLOWUP: delegate gemini | review the result\nline three",
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
	payload, ok := envelope.Payload.(ReportPayload)
	if !ok {
		t.Fatalf("payload type = %T, want ReportPayload", envelope.Payload)
	}
	if len(payload.FollowUpRequests) != 1 || payload.FollowUpRequests[0].AgentID != "gemini" {
		t.Fatalf("follow up requests = %#v, want one gemini delegate", payload.FollowUpRequests)
	}
	if payload.FollowUpRequests[0].Instruction != "review the result" {
		t.Fatalf("instruction = %q, want review the result", payload.FollowUpRequests[0].Instruction)
	}
}

func TestBuildCuriaArtifacts(t *testing.T) {
	t.Parallel()

	svc := NewService()
	proposal, err := svc.BuildProposal(context.Background(), BuildProposalRequest{
		SessionID: "sess_1",
		TaskID:    "task_curia",
		RunID:     "task_curia_codex",
		Agent: domain.AgentProfile{
			ID:          "codex-cli",
			DisplayName: "Codex CLI",
		},
		Output: "Implement the API surface.\ninternal/api/server.go\nrisk: approval flow\ntradeoff: more explicit schema\n",
	})
	if err != nil {
		t.Fatalf("BuildProposal() error = %v", err)
	}
	if proposal.Kind != domain.ArtifactKindProposal {
		t.Fatalf("kind = %s, want %s", proposal.Kind, domain.ArtifactKindProposal)
	}
	ballot, err := svc.BuildBallot(context.Background(), BuildBallotRequest{
		SessionID:        "sess_1",
		TaskID:           "task_curia",
		RunID:            "task_curia_gemini",
		Agent:            domain.AgentProfile{ID: "gemini-cli", DisplayName: "Gemini CLI"},
		TargetProposalID: "prop_task_curia_codex",
		Output:           "prop_task_curia_codex is the best proposal with strong safety",
	})
	if err != nil {
		t.Fatalf("BuildBallot() error = %v", err)
	}
	if ballot.Kind != domain.ArtifactKindBallot {
		t.Fatalf("kind = %s, want %s", ballot.Kind, domain.ArtifactKindBallot)
	}
	plan, err := svc.BuildExecutionPlan(context.Background(), BuildExecutionPlanRequest{
		SessionID: "sess_1",
		TaskID:    "task_curia",
		RunID:     "task_curia_plan",
		Goal:      "Implement the API surface",
		Proposal: ProposalPayload{
			ProposalID:     "prop_task_curia_codex",
			Summary:        "Implement the API surface",
			EstimatedSteps: []string{"Update handlers", "Add tests"},
			AffectedFiles:  []string{"internal/api/server.go"},
		},
		HumanApprovalRequired: true,
	})
	if err != nil {
		t.Fatalf("BuildExecutionPlan() error = %v", err)
	}
	if plan.Kind != domain.ArtifactKindExecutionPlan {
		t.Fatalf("kind = %s, want %s", plan.Kind, domain.ArtifactKindExecutionPlan)
	}
	if got := SummaryFromEnvelope(plan); got == "" {
		t.Fatal("SummaryFromEnvelope(plan) returned empty summary")
	}
}
