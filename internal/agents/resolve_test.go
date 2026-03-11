package agents

import (
	"context"
	"testing"
)

func TestResolveByAlias(t *testing.T) {
	t.Parallel()

	registry, err := DefaultRegistry()
	if err != nil {
		t.Fatalf("DefaultRegistry() error = %v", err)
	}

	profile, ok := registry.Resolve(context.Background(), "codex")
	if !ok {
		t.Fatal("Resolve() ok = false, want true")
	}
	if profile.ID != "codex-cli" {
		t.Fatalf("Resolve() id = %s, want codex-cli", profile.ID)
	}
}
