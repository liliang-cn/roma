package main

import (
	"testing"

	"github.com/liliang/roma/internal/artifacts"
	"github.com/liliang/roma/internal/domain"
)

func TestQueueCuriaSuffix(t *testing.T) {
	t.Parallel()

	items := []domain.ArtifactEnvelope{
		{
			Kind: domain.ArtifactKindDebateLog,
			Payload: artifacts.DebateLogPayload{
				DisputeClass: "close_score",
			},
		},
		{
			Kind: domain.ArtifactKindDecisionPack,
			Payload: artifacts.DecisionPackPayload{
				WinningMode: "merge",
			},
		},
	}

	got := queueCuriaSuffix(items)
	want := "curia mode=merge dispute=close_score"
	if got != want {
		t.Fatalf("queueCuriaSuffix() = %q, want %q", got, want)
	}
}
