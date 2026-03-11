package agents

import (
	"context"
	"testing"

	"github.com/liliang-cn/roma/internal/domain"
)

func TestResolveByAlias(t *testing.T) {
	t.Parallel()

	registry, err := DefaultRegistry()
	if err != nil {
		t.Fatalf("DefaultRegistry() error = %v", err)
	}
	if err := registry.Add(domain.AgentProfile{
		ID:           "my-codex",
		DisplayName:  "My Codex",
		Command:      "codex",
		Aliases:      []string{"codex"},
		Availability: domain.AgentAvailabilityPlanned,
	}); err != nil {
		t.Fatalf("Add() error = %v", err)
	}

	profile, ok := registry.Resolve(context.Background(), "codex")
	if !ok {
		t.Fatal("Resolve() ok = false, want true")
	}
	if profile.ID != "my-codex" {
		t.Fatalf("Resolve() id = %s, want my-codex", profile.ID)
	}
}
