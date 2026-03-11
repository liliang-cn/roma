package curia

import (
	"testing"

	"github.com/liliang/roma/internal/artifacts"
	"github.com/liliang/roma/internal/domain"
)

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
			"prop_a": 1,
		},
	)

	if !got.Detected {
		t.Fatal("Detected = false, want true")
	}
	if !got.CriticalVeto {
		t.Fatal("CriticalVeto = false, want true")
	}
	if got.WinningMode != "merge" {
		t.Fatalf("WinningMode = %q, want merge", got.WinningMode)
	}
	if len(got.SelectedIDs) != 2 {
		t.Fatalf("SelectedIDs = %#v, want top two proposals", got.SelectedIDs)
	}
	if got.TopScoreGap != 2 {
		t.Fatalf("TopScoreGap = %d, want 2", got.TopScoreGap)
	}
}
