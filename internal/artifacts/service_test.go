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
		ReviewerWeight:   2,
		WeightedScore:    40,
		Output:           "prop_task_curia_codex is the best proposal with strong safety",
	})
	if err != nil {
		t.Fatalf("BuildBallot() error = %v", err)
	}
	if ballot.Kind != domain.ArtifactKindBallot {
		t.Fatalf("kind = %s, want %s", ballot.Kind, domain.ArtifactKindBallot)
	}
	ballotPayload, ok := BallotFromEnvelope(ballot)
	if !ok {
		t.Fatal("BallotFromEnvelope(ballot) = false")
	}
	if ballotPayload.ReviewerWeight != 2 || ballotPayload.WeightedScore != 40 {
		t.Fatalf("ballot payload = %#v, want reviewer weight 2 and weighted score 40", ballotPayload)
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
	planPayload, ok := ExecutionPlanFromEnvelope(plan)
	if !ok {
		t.Fatal("ExecutionPlanFromEnvelope(plan) = false")
	}
	if planPayload.ApplyMode != "proposal_accept" {
		t.Fatalf("apply mode = %q, want proposal_accept", planPayload.ApplyMode)
	}
	if got := SummaryFromEnvelope(plan); got == "" {
		t.Fatal("SummaryFromEnvelope(plan) returned empty summary")
	}
}

func TestBuildExecutionPlanTracksReplaceWinningMode(t *testing.T) {
	t.Parallel()

	svc := NewService()
	plan, err := svc.BuildExecutionPlan(context.Background(), BuildExecutionPlanRequest{
		SessionID: "sess_1",
		TaskID:    "task_curia",
		RunID:     "task_curia_replace",
		Goal:      "Implement fallback",
		Proposal: ProposalPayload{
			ProposalID:     "prop_task_curia_replace",
			Summary:        "Fallback proposal",
			EstimatedSteps: []string{"Replace prior plan"},
			AffectedFiles:  []string{"internal/api/server.go"},
		},
		WinningMode:           "replace",
		SelectedProposalIDs:   []string{"prop_task_curia_replace"},
		HumanApprovalRequired: true,
	})
	if err != nil {
		t.Fatalf("BuildExecutionPlan() error = %v", err)
	}
	payload, ok := ExecutionPlanFromEnvelope(plan)
	if !ok {
		t.Fatal("ExecutionPlanFromEnvelope() = false")
	}
	if payload.ApplyMode != "proposal_replace" {
		t.Fatalf("apply mode = %q, want proposal_replace", payload.ApplyMode)
	}
	if len(payload.Steps) == 0 || payload.Steps[0] != "Replace the prior dominant proposal with the arbitrated fallback plan." {
		t.Fatalf("steps = %#v, want replace preface", payload.Steps)
	}
}

func TestBuildCuriaDecisionArtifactsCarryScoreboard(t *testing.T) {
	t.Parallel()

	svc := NewService()
	scoreboard := []CuriaScoreEntry{
		{ProposalID: "prop_a", RawScore: 20, WeightedScore: 54, VetoCount: 0, ReviewerCount: 2},
		{ProposalID: "prop_b", RawScore: 18, WeightedScore: 36, VetoCount: 1, ReviewerCount: 2},
	}
	debate, err := svc.BuildDebateLog(context.Background(), BuildDebateLogRequest{
		SessionID:           "sess_1",
		TaskID:              "task_curia",
		RunID:               "task_curia_debate",
		ProposalIDs:         []string{"prop_a", "prop_b"},
		BallotIDs:           []string{"ballot_1", "ballot_2"},
		WinningProposalID:   "prop_a",
		DisputeReasons:      []string{"close vote"},
		DisputeDetected:     true,
		CriticalVeto:        false,
		TopScoreGap:         2,
		Scoreboard:          scoreboard,
		ArbitrationRequired: true,
	})
	if err != nil {
		t.Fatalf("BuildDebateLog() error = %v", err)
	}
	debatePayload, ok := DebateLogFromEnvelope(debate)
	if !ok {
		t.Fatal("DebateLogFromEnvelope() = false")
	}
	if len(debatePayload.Scoreboard) != 2 || debatePayload.Scoreboard[0].WeightedScore != 54 {
		t.Fatalf("debate scoreboard = %#v, want weighted scoreboard entries", debatePayload.Scoreboard)
	}

	decision, err := svc.BuildDecisionPack(context.Background(), BuildDecisionPackRequest{
		SessionID:           "sess_1",
		TaskID:              "task_curia",
		RunID:               "task_curia_decision",
		WinningMode:         "merge",
		SelectedProposalIDs: []string{"prop_a", "prop_b"},
		ExecutionPlanID:     "plan_1",
		ApprovalRequired:    true,
		MergedRationale:     "merge due to close vote",
		RejectedReasons:     []string{"prop_c scored lower"},
		Scoreboard:          scoreboard,
	})
	if err != nil {
		t.Fatalf("BuildDecisionPack() error = %v", err)
	}
	decisionPayload, ok := DecisionPackFromEnvelope(decision)
	if !ok {
		t.Fatal("DecisionPackFromEnvelope() = false")
	}
	if len(decisionPayload.Scoreboard) != 2 || decisionPayload.Scoreboard[1].VetoCount != 1 {
		t.Fatalf("decision scoreboard = %#v, want veto count carried through", decisionPayload.Scoreboard)
	}
}
