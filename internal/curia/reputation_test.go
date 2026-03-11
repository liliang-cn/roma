package curia

import (
	"testing"

	"github.com/liliang-cn/roma/internal/domain"
)

func TestReviewerWeightUsesMetadataOverride(t *testing.T) {
	t.Parallel()

	got := reviewerWeight(domain.AgentProfile{
		ID:       "custom",
		Metadata: map[string]string{"curia_weight": "5"},
	})
	if got != 5 {
		t.Fatalf("reviewerWeight() = %d, want 5", got)
	}
}

func TestWeightedBallotScoreUsesReviewerReputation(t *testing.T) {
	t.Parallel()

	ballot := artifactsBallotView{
		Scores: struct {
			Correctness     int
			Safety          int
			Maintainability int
			ScopeControl    int
			Testability     int
		}{
			Correctness:     3,
			Safety:          3,
			Maintainability: 3,
			ScopeControl:    3,
			Testability:     3,
		},
	}

	high := weightedBallotScore(ballot, reviewerWeight(domain.AgentProfile{ID: "codex-cli"}))
	low := weightedBallotScore(ballot, reviewerWeight(domain.AgentProfile{ID: "copilot-cli"}))
	if high <= low {
		t.Fatalf("weighted scores = high:%d low:%d, want high > low", high, low)
	}
}
