package agents

import (
	"context"
	"testing"
)

func TestDefaultRegistryList(t *testing.T) {
	t.Parallel()

	registry, err := DefaultRegistry()
	if err != nil {
		t.Fatalf("DefaultRegistry() error = %v", err)
	}

	profiles := registry.List(context.Background())
	if len(profiles) != 4 {
		t.Fatalf("profile count = %d, want 4", len(profiles))
	}
	if profiles[0].DisplayName != "Claude Code" {
		t.Fatalf("first profile = %s, want Claude Code", profiles[0].DisplayName)
	}
}
