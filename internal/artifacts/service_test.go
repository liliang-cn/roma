package artifacts

import (
	"context"
	"strings"
	"testing"

	"github.com/liliang-cn/roma/internal/domain"
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
		DecisionConfidence:    domain.ConfidenceHigh,
		ConsensusStrength:     "augustus_resolved",
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
	if payload.DecisionConfidence != domain.ConfidenceHigh {
		t.Fatalf("decision confidence = %q, want high", payload.DecisionConfidence)
	}
	if payload.ConsensusStrength != "augustus_resolved" {
		t.Fatalf("consensus strength = %q, want augustus_resolved", payload.ConsensusStrength)
	}
	if len(payload.SelectedProposalIDs) != 1 || payload.SelectedProposalIDs[0] != "prop_task_curia_replace" {
		t.Fatalf("selected proposals = %#v, want replace proposal id", payload.SelectedProposalIDs)
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
		SessionID:             "sess_1",
		TaskID:                "task_curia",
		RunID:                 "task_curia_debate",
		ProposalIDs:           []string{"prop_a", "prop_b"},
		BallotIDs:             []string{"ballot_1", "ballot_2"},
		WinningProposalID:     "prop_a",
		DisputeClass:          "close_score",
		ArbitrationConfidence: domain.ConfidenceMedium,
		ConsensusStrength:     "disputed_consensus",
		DisputeReasons:        []string{"close vote"},
		DisputeDetected:       true,
		CriticalVeto:          false,
		TopScoreGap:           2,
		Scoreboard:            scoreboard,
		ArbitrationRequired:   true,
	})
	if err != nil {
		t.Fatalf("BuildDebateLog() error = %v", err)
	}
	debatePayload, ok := DebateLogFromEnvelope(debate)
	if !ok {
		t.Fatal("DebateLogFromEnvelope() = false")
	}
	if debatePayload.DisputeClass != "close_score" {
		t.Fatalf("dispute class = %q, want close_score", debatePayload.DisputeClass)
	}
	if debatePayload.ArbitrationConfidence != domain.ConfidenceMedium {
		t.Fatalf("debate confidence = %q, want medium", debatePayload.ArbitrationConfidence)
	}
	if debatePayload.ConsensusStrength != "disputed_consensus" {
		t.Fatalf("debate consensus strength = %q, want disputed_consensus", debatePayload.ConsensusStrength)
	}
	if len(debatePayload.Scoreboard) != 2 || debatePayload.Scoreboard[0].WeightedScore != 54 {
		t.Fatalf("debate scoreboard = %#v, want weighted scoreboard entries", debatePayload.Scoreboard)
	}

	decision, err := svc.BuildDecisionPack(context.Background(), BuildDecisionPackRequest{
		SessionID:             "sess_1",
		TaskID:                "task_curia",
		RunID:                 "task_curia_decision",
		WinningMode:           "merge",
		DisputeClass:          "close_score",
		ArbitrationConfidence: domain.ConfidenceMedium,
		ConsensusStrength:     "disputed_consensus",
		SelectedProposalIDs:   []string{"prop_a", "prop_b"},
		ExecutionPlanID:       "plan_1",
		ApprovalRequired:      true,
		MergedRationale:       "merge due to close vote",
		RejectedReasons:       []string{"prop_c scored lower"},
		RiskFlags:             []string{"close_score", "needs_review"},
		ReviewQuestions:       []string{"Which tradeoff separates prop_a and prop_b?"},
		DissentSummary:        []string{"prop_c was not selected."},
		CandidateSummaries: []CuriaCandidateSummary{
			{ProposalID: "prop_a", Summary: "A", RawScore: 20, WeightedScore: 54, VetoCount: 0},
			{ProposalID: "prop_b", Summary: "B", RawScore: 18, WeightedScore: 36, VetoCount: 1},
		},
		ReviewerBreakdown: []CuriaReviewContribution{
			{ReviewerID: "codex-cli", TargetProposalID: "prop_a", RawScore: 20, ReviewerWeight: 3, WeightedScore: 54, Veto: false},
		},
		Scoreboard: scoreboard,
	})
	if err != nil {
		t.Fatalf("BuildDecisionPack() error = %v", err)
	}
	decisionPayload, ok := DecisionPackFromEnvelope(decision)
	if !ok {
		t.Fatal("DecisionPackFromEnvelope() = false")
	}
	if decisionPayload.DisputeClass != "close_score" {
		t.Fatalf("decision dispute class = %q, want close_score", decisionPayload.DisputeClass)
	}
	if decisionPayload.ArbitrationConfidence != domain.ConfidenceMedium {
		t.Fatalf("decision confidence = %q, want medium", decisionPayload.ArbitrationConfidence)
	}
	if decisionPayload.ConsensusStrength != "disputed_consensus" {
		t.Fatalf("decision consensus strength = %q, want disputed_consensus", decisionPayload.ConsensusStrength)
	}
	if len(decisionPayload.Scoreboard) != 2 || decisionPayload.Scoreboard[1].VetoCount != 1 {
		t.Fatalf("decision scoreboard = %#v, want veto count carried through", decisionPayload.Scoreboard)
	}
	if len(decisionPayload.CandidateSummaries) != 2 || decisionPayload.CandidateSummaries[0].ProposalID != "prop_a" {
		t.Fatalf("candidate summaries = %#v, want prop_a/prop_b summaries", decisionPayload.CandidateSummaries)
	}
	if len(decisionPayload.ReviewQuestions) != 1 || len(decisionPayload.RiskFlags) != 2 {
		t.Fatalf("decision refinement = %#v, want risk flags and review questions", decisionPayload)
	}
	if len(decisionPayload.ReviewerBreakdown) != 1 || decisionPayload.ReviewerBreakdown[0].ReviewerID != "codex-cli" {
		t.Fatalf("reviewer breakdown = %#v, want reviewer contribution", decisionPayload.ReviewerBreakdown)
	}
	if len(decisionPayload.DissentSummary) != 1 {
		t.Fatalf("dissent summary = %#v, want one dissent note", decisionPayload.DissentSummary)
	}
}

func TestBuildSemanticReport(t *testing.T) {
	t.Parallel()

	svc := NewService()
	envelope, err := svc.BuildSemanticReport(context.Background(), BuildSemanticReportRequest{
		SessionID:        "sess_1",
		TaskID:           "task_1",
		RunID:            "run_semantic",
		Agent:            domain.AgentProfile{ID: "semantic-reviewer", DisplayName: "Semantic Reviewer"},
		SignalKind:       "dangerous_command_detected",
		SignalReason:     "approval phrase",
		SignalConfidence: domain.ConfidenceHigh,
		SignalText:       "rm -rf /",
		Output:           "intent: destructive_write\nrisk: high\nneeds_approval: true\nrecommend_curia: true\nsummary: The agent is attempting a dangerous destructive command.",
	})
	if err != nil {
		t.Fatalf("BuildSemanticReport() error = %v", err)
	}
	if envelope.Kind != domain.ArtifactKindSemanticReport {
		t.Fatalf("kind = %s, want %s", envelope.Kind, domain.ArtifactKindSemanticReport)
	}
	payload, ok := SemanticReportFromEnvelope(envelope)
	if !ok {
		t.Fatal("SemanticReportFromEnvelope() = false")
	}
	if payload.Intent != "destructive_write" {
		t.Fatalf("intent = %q, want destructive_write", payload.Intent)
	}
	if payload.Risk != domain.ConfidenceHigh {
		t.Fatalf("risk = %s, want %s", payload.Risk, domain.ConfidenceHigh)
	}
	if !payload.NeedsApproval || !payload.RecommendCuria {
		t.Fatalf("payload = %#v, want approval and curia recommendations", payload)
	}
}

func TestBuildFinalAnswer(t *testing.T) {
	t.Parallel()

	svc := NewService()
	plan, err := svc.BuildExecutionPlan(context.Background(), BuildExecutionPlanRequest{
		SessionID: "sess_1",
		TaskID:    "task_1",
		RunID:     "run_plan",
		Goal:      "Implement the API surface",
		Proposal: ProposalPayload{
			ProposalID:     "prop_1",
			Summary:        "Implement the API surface",
			EstimatedSteps: []string{"Update handlers", "Add tests"},
			AffectedFiles:  []string{"internal/api/server.go"},
		},
		HumanApprovalRequired: true,
	})
	if err != nil {
		t.Fatalf("BuildExecutionPlan() error = %v", err)
	}

	envelope, err := svc.BuildFinalAnswer(context.Background(), BuildFinalAnswerRequest{
		SessionID:    "sess_1",
		TaskID:       "task_1",
		RunID:        "run_final",
		Status:       "awaiting_approval",
		Prompt:       "Implement the API surface",
		StarterAgent: "my-codex",
		Artifacts:    []domain.ArtifactEnvelope{plan},
	})
	if err != nil {
		t.Fatalf("BuildFinalAnswer() error = %v", err)
	}
	if envelope.Kind != domain.ArtifactKindFinalAnswer {
		t.Fatalf("kind = %s, want %s", envelope.Kind, domain.ArtifactKindFinalAnswer)
	}
	payload, ok := FinalAnswerFromEnvelope(envelope)
	if !ok {
		t.Fatal("FinalAnswerFromEnvelope() = false")
	}
	if payload.OutcomeType != "pending_approval" {
		t.Fatalf("outcome_type = %q, want pending_approval", payload.OutcomeType)
	}
	if !payload.ApprovalRequired {
		t.Fatal("approval_required = false, want true")
	}
	if len(payload.ChangedFiles) != 1 || payload.ChangedFiles[0] != "internal/api/server.go" {
		t.Fatalf("changed_files = %#v, want internal/api/server.go", payload.ChangedFiles)
	}
	if len(payload.ArtifactRefs) != 1 || payload.ArtifactRefs[0] != plan.ID {
		t.Fatalf("artifact_refs = %#v, want [%s]", payload.ArtifactRefs, plan.ID)
	}
	if got := SummaryFromEnvelope(envelope); got == "" {
		t.Fatal("SummaryFromEnvelope(final answer) returned empty summary")
	}
}

func TestBuildFinalAnswerPrefersMeaningfulReportLine(t *testing.T) {
	t.Parallel()

	svc := NewService()
	report, err := svc.BuildReport(context.Background(), BuildReportRequest{
		SessionID: "sess_1",
		TaskID:    "task_1",
		RunID:     "run_1",
		Agent: domain.AgentProfile{
			ID:          "my-codex",
			DisplayName: "My Codex",
		},
		Result: "success",
		Output: strings.Join([]string{
			"OpenAI Codex v0.114.0 (research preview)",
			"--------",
			"workdir: /tmp/work",
			"model: gpt-5.4",
			"codex",
			"ROMA is a daemon-first local orchestrator for multi-agent AI coding sessions.",
			"tokens used",
			"7,671",
		}, "\n"),
	})
	if err != nil {
		t.Fatalf("BuildReport() error = %v", err)
	}
	finalAnswer, err := svc.BuildFinalAnswer(context.Background(), BuildFinalAnswerRequest{
		SessionID:    "sess_1",
		TaskID:       "task_1",
		RunID:        "run_final",
		Status:       "succeeded",
		Prompt:       "Describe the repository.",
		StarterAgent: "my-codex",
		Artifacts:    []domain.ArtifactEnvelope{report},
	})
	if err != nil {
		t.Fatalf("BuildFinalAnswer() error = %v", err)
	}
	payload, ok := FinalAnswerFromEnvelope(finalAnswer)
	if !ok {
		t.Fatal("FinalAnswerFromEnvelope() = false")
	}
	want := "ROMA is a daemon-first local orchestrator for multi-agent AI coding sessions."
	if payload.Summary != want {
		t.Fatalf("summary = %q, want %q", payload.Summary, want)
	}
}
