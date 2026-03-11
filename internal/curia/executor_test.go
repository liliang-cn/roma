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
}
