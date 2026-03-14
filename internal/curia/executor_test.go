package curia

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liliang-cn/roma/internal/artifacts"
	"github.com/liliang-cn/roma/internal/domain"
	"github.com/liliang-cn/roma/internal/runtime"
)

type augustusTestAdapter struct{}

func (augustusTestAdapter) Supports(domain.AgentProfile) bool { return true }

func (augustusTestAdapter) BuildCommand(ctx context.Context, req runtime.StartRequest) (*exec.Cmd, error) {
	script := `
import sys
profile = sys.argv[1]
prompt = sys.argv[2]
if "Augustus arbitration phase" in prompt:
    print("winning_mode: replace")
    print("selected_proposals: prop_task_augustus_gemini-cli")
    print("competing_proposals: prop_task_augustus_codex-cli,prop_task_augustus_gemini-cli")
    print("confidence: high")
    print("consensus_strength: augustus_resolved")
    print("arbitration_strategy: replace_with_runner_up")
    print("approval_required: false")
    print("approval_reason: Augustus resolved the dispute with high confidence.")
    print("rationale: Augustus selected the safer fallback proposal.")
    print("escalation_reasons:")
    print("- critical_veto")
    print("risk_flags:")
    print("- augustus_override")
    print("review_questions:")
    print("- Why did the original leader receive a veto?")
    print("dissent_summary:")
    print("- prop_task_augustus_codex-cli was rejected after veto.")
elif "blind review phase" in prompt:
    if profile == "gemini-cli":
        print("prop_task_augustus_codex-cli is weak and veto")
    elif profile == "copilot-cli":
        print("prop_task_augustus_gemini-cli is the best proposal")
    else:
        print("prop_task_augustus_codex-cli is the best proposal")
else:
    if profile == "codex-cli":
        print("Proposal A\ninternal/api/server.go\nrisk: merge conflict")
    elif profile == "gemini-cli":
        print("Proposal B\ninternal/api/server.go\nrisk: safer fallback")
    else:
        print("Proposal C\ninternal/api/server.go\nrisk: scope drift")
`
	return exec.CommandContext(ctx, "python3", "-c", script, req.Profile.ID, req.Prompt), nil
}

func TestDetectDisputeFlagsCloseVoteAndVeto(t *testing.T) {
	t.Parallel()

	proposals := []proposalEnvelope{
		{proposal: artifacts.ProposalPayload{ProposalID: "prop_a"}, author: domain.AgentProfile{ID: "codex-cli"}},
		{proposal: artifacts.ProposalPayload{ProposalID: "prop_b"}, author: domain.AgentProfile{ID: "gemini-cli"}},
		{proposal: artifacts.ProposalPayload{ProposalID: "prop_c"}, author: domain.AgentProfile{ID: "copilot-cli"}},
	}

	got := detectDispute(
		proposals,
		map[string]int{
			"prop_a": 20,
			"prop_b": 18,
			"prop_c": 7,
		},
		map[string]int{
			"prop_a": 20,
			"prop_b": 18,
			"prop_c": 7,
		},
		map[string]int{
			"prop_a": 1,
		},
		map[string]int{
			"prop_a": 2,
			"prop_b": 2,
			"prop_c": 1,
		},
	)

	if !got.Detected {
		t.Fatal("Detected = false, want true")
	}
	if !got.CriticalVeto {
		t.Fatal("CriticalVeto = false, want true")
	}
	if got.WinningMode != "replace" {
		t.Fatalf("WinningMode = %q, want replace", got.WinningMode)
	}
	if len(got.SelectedIDs) != 1 || got.SelectedIDs[0] != "prop_b" {
		t.Fatalf("SelectedIDs = %#v, want [prop_b]", got.SelectedIDs)
	}
	if got.TopScoreGap != 2 {
		t.Fatalf("TopScoreGap = %d, want 2", got.TopScoreGap)
	}
	if len(got.Scoreboard) != 3 {
		t.Fatalf("scoreboard len = %d, want 3", len(got.Scoreboard))
	}
	if got.Scoreboard[0].ProposalID != "prop_a" || got.Scoreboard[0].WeightedScore != 20 {
		t.Fatalf("top scoreboard entry = %#v, want prop_a weighted 20", got.Scoreboard[0])
	}
	if got.Class != "close_score+critical_veto" {
		t.Fatalf("Class = %q, want close_score+critical_veto", got.Class)
	}
	if got.Confidence != domain.ConfidenceLow {
		t.Fatalf("Confidence = %q, want low", got.Confidence)
	}
	if got.ConsensusStrength != "veto_replacement" {
		t.Fatalf("ConsensusStrength = %q, want veto_replacement", got.ConsensusStrength)
	}
	if got.ArbitrationStrategy != "replace_with_runner_up" {
		t.Fatalf("ArbitrationStrategy = %q, want replace_with_runner_up", got.ArbitrationStrategy)
	}
	if len(got.CompetingProposalIDs) < 2 {
		t.Fatalf("CompetingProposalIDs = %#v, want top competing proposals", got.CompetingProposalIDs)
	}
	if len(got.EscalationReasons) == 0 {
		t.Fatal("EscalationReasons = empty, want escalation metadata")
	}
	if len(got.DissentSummary) == 0 {
		t.Fatal("DissentSummary = empty, want dissent entries")
	}
	summaries := buildCandidateSummaries(proposals, got.Scoreboard)
	if len(summaries) != 3 || summaries[0].ProposalID != "prop_a" {
		t.Fatalf("candidate summaries = %#v, want proposal summaries", summaries)
	}
	questions := buildReviewQuestions(artifacts.ProposalPayload{DesignRisks: []string{"migration risk"}}, got)
	if len(questions) == 0 {
		t.Fatal("review questions = empty, want arbitration prompts")
	}
	flags := buildRiskFlags(artifacts.ProposalPayload{DesignRisks: []string{"migration risk"}}, got)
	if len(flags) < 2 {
		t.Fatalf("risk flags = %#v, want dispute and design risk flags", flags)
	}
	ballots := []ballotEnvelope{
		{
			envelope: domain.ArtifactEnvelope{Producer: domain.Producer{AgentID: "codex-cli"}},
			ballot: artifacts.BallotPayload{
				TargetProposalID: "prop_a",
				ReviewerWeight:   3,
				WeightedScore:    54,
				Veto:             false,
				Scores: artifacts.BallotScores{
					Correctness: 4, Safety: 4, Maintainability: 4, ScopeControl: 4, Testability: 4,
				},
			},
		},
	}
	breakdown := buildReviewerBreakdown(ballots)
	if len(breakdown) != 1 || breakdown[0].ReviewerWeight != 3 || breakdown[0].RawScore != 20 {
		t.Fatalf("reviewer breakdown = %#v, want weighted reviewer contribution", breakdown)
	}
}

func TestExecutorUsesAugustusArbitration(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	executor := NewExecutor(root, runtime.NewSupervisor(augustusTestAdapter{}), artifacts.NewService())
	result, err := executor.Execute(context.Background(), ExecuteRequest{
		SessionID:       "sess_augustus",
		TaskID:          "task_augustus",
		BasePrompt:      "Resolve conflicting API designs automatically",
		WorkingDir:      root,
		NodeTitle:       "curia arbitration demo",
		Senators:        []domain.AgentProfile{{ID: "codex-cli"}, {ID: "gemini-cli"}, {ID: "copilot-cli"}},
		Quorum:          2,
		ArbitrationMode: "augustus",
		Arbitrator:      domain.AgentProfile{ID: "claude-code"},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	plan, ok := artifacts.ExecutionPlanFromEnvelope(result.Primary)
	if !ok {
		t.Fatal("ExecutionPlanFromEnvelope() = false")
	}
	if plan.ApplyMode != "proposal_replace" {
		t.Fatalf("apply mode = %q, want proposal_replace", plan.ApplyMode)
	}
	if plan.DecisionConfidence != domain.ConfidenceHigh {
		t.Fatalf("decision confidence = %q, want high", plan.DecisionConfidence)
	}
	if plan.ConsensusStrength != "augustus_resolved" {
		t.Fatalf("consensus strength = %q, want augustus_resolved", plan.ConsensusStrength)
	}
	if plan.ArbitrationStrategy != "replace_with_runner_up" {
		t.Fatalf("arbitration strategy = %q, want replace_with_runner_up", plan.ArbitrationStrategy)
	}
	if plan.HumanApprovalRequired {
		t.Fatalf("human approval required = true, want false")
	}
	if plan.ApprovalReason != "Augustus resolved the dispute with high confidence." {
		t.Fatalf("approval reason = %q, want augustus override reason", plan.ApprovalReason)
	}
	var decision artifacts.DecisionPackPayload
	found := false
	for _, item := range result.RelatedArtifacts {
		if payload, ok := artifacts.DecisionPackFromEnvelope(item); ok {
			decision = payload
			found = true
		}
	}
	if !found {
		t.Fatal("missing decision pack")
	}
	if !decision.Arbitrated || decision.ArbitratorID != "claude-code" {
		t.Fatalf("decision pack = %#v, want augustus arbitration metadata", decision)
	}
	if decision.WinningMode != "replace" {
		t.Fatalf("winning mode = %q, want replace", decision.WinningMode)
	}
	if decision.ArbitrationConfidence != domain.ConfidenceHigh {
		t.Fatalf("arbitration confidence = %q, want high", decision.ArbitrationConfidence)
	}
	if decision.ConsensusStrength != "augustus_resolved" {
		t.Fatalf("consensus strength = %q, want augustus_resolved", decision.ConsensusStrength)
	}
	if decision.ArbitrationStrategy != "replace_with_runner_up" {
		t.Fatalf("arbitration strategy = %q, want replace_with_runner_up", decision.ArbitrationStrategy)
	}
	if decision.ApprovalReason != "Augustus resolved the dispute with high confidence." {
		t.Fatalf("approval reason = %q, want augustus override reason", decision.ApprovalReason)
	}
	if len(decision.DissentSummary) == 0 {
		t.Fatal("dissent summary = empty, want arbitration dissent notes")
	}
	if len(decision.SelectedProposalIDs) != 1 || decision.SelectedProposalIDs[0] != "prop_task_augustus_gemini-cli" {
		t.Fatalf("selected proposals = %#v, want gemini replacement proposal", decision.SelectedProposalIDs)
	}
}

func TestExecutorDefaultsToAugustusWhenArbitratorIsPresent(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	executor := NewExecutor(root, runtime.NewSupervisor(augustusTestAdapter{}), artifacts.NewService())
	result, err := executor.Execute(context.Background(), ExecuteRequest{
		SessionID:  "sess_augustus",
		TaskID:     "task_augustus",
		BasePrompt: "Resolve conflicting API designs automatically",
		WorkingDir: root,
		NodeTitle:  "curia arbitration demo",
		Senators:   []domain.AgentProfile{{ID: "codex-cli"}, {ID: "gemini-cli"}, {ID: "copilot-cli"}},
		Quorum:     2,
		Arbitrator: domain.AgentProfile{ID: "claude-code"},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	var decision artifacts.DecisionPackPayload
	found := false
	for _, item := range result.RelatedArtifacts {
		if payload, ok := artifacts.DecisionPackFromEnvelope(item); ok {
			decision = payload
			found = true
		}
	}
	if !found {
		t.Fatal("missing decision pack")
	}
	if !decision.Arbitrated || decision.ArbitratorID != "claude-code" {
		t.Fatalf("decision pack = %#v, want automatic augustus arbitration", decision)
	}
}

func TestExecutorKeepsHumanArbitrationWhenExplicitlyRequested(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	executor := NewExecutor(root, runtime.NewSupervisor(augustusTestAdapter{}), artifacts.NewService())
	result, err := executor.Execute(context.Background(), ExecuteRequest{
		SessionID:       "sess_human_curia",
		TaskID:          "task_human_curia",
		BasePrompt:      "Resolve conflicting API designs automatically",
		WorkingDir:      root,
		NodeTitle:       "curia arbitration demo",
		Senators:        []domain.AgentProfile{{ID: "codex-cli"}, {ID: "gemini-cli"}, {ID: "copilot-cli"}},
		Quorum:          2,
		ArbitrationMode: "human",
		Arbitrator:      domain.AgentProfile{ID: "claude-code"},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	plan, ok := artifacts.ExecutionPlanFromEnvelope(result.Primary)
	if !ok {
		t.Fatal("ExecutionPlanFromEnvelope() = false")
	}
	if !plan.HumanApprovalRequired {
		t.Fatalf("human approval required = false, want true for explicit human arbitration")
	}
	for _, item := range result.RelatedArtifacts {
		if payload, ok := artifacts.DecisionPackFromEnvelope(item); ok {
			if payload.Arbitrated {
				t.Fatalf("decision pack = %#v, want non-automatic arbitration", payload)
			}
			return
		}
	}
	t.Fatal("missing decision pack")
}

func TestReputationStorePersistsReviewerWeight(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := NewReputationStore(root)
	err := store.RecordOutcome(context.Background(),
		[]domain.AgentProfile{{
			ID:       "codex-cli",
			Metadata: map[string]string{"curia_weight": "3"},
		}},
		[]ballotEnvelope{{
			envelope: domain.ArtifactEnvelope{Producer: domain.Producer{AgentID: "codex-cli"}},
			ballot: artifacts.BallotPayload{
				TargetProposalID: "prop_a",
				ReviewerWeight:   3,
				WeightedScore:    54,
				Scores: artifacts.BallotScores{
					Correctness: 4, Safety: 4, Maintainability: 4, ScopeControl: 4, Testability: 4,
				},
			},
		}},
		[]string{"prop_a"},
		false,
	)
	if err != nil {
		t.Fatalf("RecordOutcome() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".roma", "curia-reputation.json")); err != nil {
		t.Fatalf("reputation file missing: %v", err)
	}
	weight := store.EffectiveWeight(context.Background(), domain.AgentProfile{
		ID:       "codex-cli",
		Metadata: map[string]string{"curia_weight": "3"},
	})
	if weight < 3 {
		t.Fatalf("effective weight = %d, want >= 3", weight)
	}
}

func TestReviewPromptUsesAnonymousProposalIDs(t *testing.T) {
	t.Parallel()

	req := ExecuteRequest{
		TaskID:     "test_task",
		BasePrompt: "Test prompt",
	}
	proposals := []proposalEnvelope{
		{
			proposal: artifacts.ProposalPayload{
				ProposalID: "prop_task_senator_1",
				Summary:    "First proposal",
			},
			author: domain.AgentProfile{ID: "senator_1"},
		},
		{
			proposal: artifacts.ProposalPayload{
				ProposalID: "prop_task_senator_2",
				Summary:    "Second proposal",
			},
			author: domain.AgentProfile{ID: "senator_2"},
		},
	}
	senator := domain.AgentProfile{ID: "senator_3"}

	prompt := reviewPrompt(req, proposals, senator)

	// The prompt should NOT contain the author IDs
	if strings.Contains(prompt, "senator_1") || strings.Contains(prompt, "senator_2") {
		t.Error("reviewPrompt should use anonymous IDs, not author IDs")
	}

	// The prompt should contain anonymous IDs
	if !strings.Contains(prompt, "proposal_1") || !strings.Contains(prompt, "proposal_2") {
		t.Error("reviewPrompt should contain anonymous proposal IDs")
	}

	// The prompt should mention it's anonymous
	if !strings.Contains(prompt, "anonymous") {
		t.Error("reviewPrompt should mention anonymous proposals")
	}
}
