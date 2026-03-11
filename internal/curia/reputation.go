package curia

import (
	"strconv"
	"strings"

	"github.com/liliang-cn/roma/internal/domain"
)

func reviewerWeight(profile domain.AgentProfile) int {
	if profile.Metadata != nil {
		if raw := strings.TrimSpace(profile.Metadata["curia_weight"]); raw != "" {
			if value, err := strconv.Atoi(raw); err == nil && value > 0 {
				return value
			}
		}
	}
	switch profile.ID {
	case "codex-cli":
		return 3
	case "claude-code":
		return 3
	case "gemini-cli":
		return 2
	case "copilot-cli":
		return 1
	default:
		return 1
	}
}

func weightedBallotScore(ballot artifactsBallotView, reviewerWeight int) int {
	base := ballot.Scores.Correctness +
		ballot.Scores.Safety +
		ballot.Scores.Maintainability +
		ballot.Scores.ScopeControl +
		ballot.Scores.Testability
	score := base * reviewerWeight
	if ballot.Veto {
		score -= 10 * reviewerWeight
	}
	return score
}

type artifactsBallotView struct {
	Scores struct {
		Correctness     int
		Safety          int
		Maintainability int
		ScopeControl    int
		Testability     int
	}
	Veto bool
}
