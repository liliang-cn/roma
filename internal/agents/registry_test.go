package agents

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/liliang-cn/roma/internal/domain"
)

func TestDefaultRegistryList(t *testing.T) {
	t.Parallel()

	registry, err := DefaultRegistry()
	if err != nil {
		t.Fatalf("DefaultRegistry() error = %v", err)
	}

	profiles := registry.List(context.Background())
	if len(profiles) != 0 {
		t.Fatalf("profile count = %d, want 0", len(profiles))
	}
}

func TestRegistryAddRemove(t *testing.T) {
	t.Parallel()

	registry, _ := DefaultRegistry()
	id := "test-agent"
	profile := domain.AgentProfile{
		ID:           id,
		DisplayName:  "Test Agent",
		Command:      "test-cmd",
		Availability: domain.AgentAvailabilityPlanned,
	}

	// Add
	if err := registry.Add(profile); err != nil {
		t.Fatalf("Add() error = %v", err)
	}

	p, ok := registry.Get(id)
	if !ok || p.ID != id {
		t.Fatalf("Get(%s) failed", id)
	}

	// Remove
	if err := registry.Remove(id); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}

	if _, ok := registry.Get(id); ok {
		t.Fatalf("Get(%s) after remove should fail", id)
	}
}

func TestRegistryLoadSave(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "agents.json")

	registry, _ := DefaultRegistry()
	id := "user-agent"
	profile := domain.AgentProfile{
		ID:           id,
		DisplayName:  "User Agent",
		Command:      "user-cmd",
		Availability: domain.AgentAvailabilityPlanned,
	}

	if err := registry.Add(profile); err != nil {
		t.Fatalf("Add() error = %v", err)
	}

	registry.path = configPath
	if err := registry.SaveUserConfig(); err != nil {
		t.Fatalf("SaveUserConfig() error = %v", err)
	}

	// Load in a new registry
	newRegistry, _ := DefaultRegistry()
	if err := newRegistry.LoadUserConfig(configPath); err != nil {
		t.Fatalf("LoadUserConfig() error = %v", err)
	}

	p, ok := newRegistry.Get(id)
	if !ok || p.ID != id {
		t.Fatalf("Get(%s) in new registry failed", id)
	}
}

func TestDefaultUserConfigPath(t *testing.T) {
	t.Setenv("ROMA_HOME", "/tmp/roma-home")
	if got := DefaultUserConfigPath(); got != "/tmp/roma-home/agents.json" {
		t.Fatalf("DefaultUserConfigPath() = %q, want %q", got, "/tmp/roma-home/agents.json")
	}
}

func TestRegistrySetUserConfigPath(t *testing.T) {
	t.Parallel()

	registry, _ := DefaultRegistry()
	path := filepath.Join(t.TempDir(), "agents.json")
	registry.SetUserConfigPath(path)
	if got := registry.UserConfigPath(); got != path {
		t.Fatalf("UserConfigPath() = %q, want %q", got, path)
	}
}

func TestRegistrySaveUserConfigUsesConfiguredPath(t *testing.T) {
	t.Parallel()

	registry, _ := DefaultRegistry()
	path := filepath.Join(t.TempDir(), "config", "agents.json")
	registry.SetUserConfigPath(path)
	if err := registry.Add(domain.AgentProfile{
		ID:           "user-agent-2",
		DisplayName:  "User Agent Two",
		Command:      "user-two",
		Availability: domain.AgentAvailabilityPlanned,
	}); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if err := registry.SaveUserConfig(); err != nil {
		t.Fatalf("SaveUserConfig() error = %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("stat saved config: %v", err)
	}
}

func TestRegistryGetAlias(t *testing.T) {
	t.Parallel()

	registry, _ := DefaultRegistry()
	if err := registry.Add(domain.AgentProfile{
		ID:           "custom-claude",
		DisplayName:  "Custom Claude",
		Command:      "claude",
		Aliases:      []string{"claude"},
		Availability: domain.AgentAvailabilityPlanned,
	}); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	p, ok := registry.Get("claude")
	if !ok || p.ID != "custom-claude" {
		t.Fatalf("Get(claude) failed, got %v", p.ID)
	}
}

func TestRegistryDefaultProfileUsesFirstConfiguredAgent(t *testing.T) {
	t.Parallel()

	registry, _ := DefaultRegistry()
	if err := registry.Add(domain.AgentProfile{
		ID:           "agent-a",
		DisplayName:  "Agent A",
		Command:      "agent-a",
		Availability: domain.AgentAvailabilityPlanned,
	}); err != nil {
		t.Fatalf("Add(agent-a) error = %v", err)
	}
	if err := registry.Add(domain.AgentProfile{
		ID:           "agent-b",
		DisplayName:  "Agent B",
		Command:      "agent-b",
		Availability: domain.AgentAvailabilityPlanned,
	}); err != nil {
		t.Fatalf("Add(agent-b) error = %v", err)
	}
	profile, err := registry.DefaultProfile(context.Background())
	if err != nil {
		t.Fatalf("DefaultProfile() error = %v", err)
	}
	if profile.ID != "agent-a" {
		t.Fatalf("DefaultProfile() = %s, want agent-a", profile.ID)
	}
}

func TestRegistryAddInfersKnownRuntimeDefaults(t *testing.T) {
	t.Parallel()

	registry, _ := DefaultRegistry()
	if err := registry.Add(domain.AgentProfile{
		ID:           "my-codex",
		DisplayName:  "My Codex",
		Command:      "codex",
		Availability: domain.AgentAvailabilityPlanned,
	}); err != nil {
		t.Fatalf("Add() error = %v", err)
	}

	profile, ok := registry.Get("my-codex")
	if !ok {
		t.Fatal("Get(my-codex) failed")
	}
	if !profile.UsePTY {
		t.Fatal("UsePTY = false, want true")
	}
	if len(profile.Args) == 0 {
		t.Fatal("Args empty, want inferred defaults")
	}
	if got := profile.Args[0]; got != "exec" {
		t.Fatalf("first arg = %q, want exec", got)
	}
}

func TestRegistryLoadUserConfigInfersKnownRuntimeDefaults(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "agents.json")
	data := `[
  {
    "id": "my-gemini",
    "display_name": "My Gemini",
    "command": "gemini",
    "availability": "planned"
  }
]`
	if err := os.WriteFile(configPath, []byte(data), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	registry, _ := DefaultRegistry()
	if err := registry.LoadUserConfig(configPath); err != nil {
		t.Fatalf("LoadUserConfig() error = %v", err)
	}
	profile, ok := registry.Get("my-gemini")
	if !ok {
		t.Fatal("Get(my-gemini) failed")
	}
	if len(profile.Args) == 0 {
		t.Fatal("Args empty after load, want inferred defaults")
	}
	if !profile.UsePTY {
		t.Fatal("UsePTY = false, want true")
	}
}

func TestRegistryAddUserProfileSerializesConcurrentWriters(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "agents.json")
	registryA, _ := DefaultRegistry()
	registryB, _ := DefaultRegistry()
	registryA.SetUserConfigPath(path)
	registryB.SetUserConfigPath(path)

	var wg sync.WaitGroup
	errCh := make(chan error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		errCh <- registryA.AddUserProfile(domain.AgentProfile{
			ID:           "agent-a",
			DisplayName:  "Agent A",
			Command:      "agent-a",
			Availability: domain.AgentAvailabilityPlanned,
		})
	}()
	go func() {
		defer wg.Done()
		errCh <- registryB.AddUserProfile(domain.AgentProfile{
			ID:           "agent-b",
			DisplayName:  "Agent B",
			Command:      "agent-b",
			Availability: domain.AgentAvailabilityPlanned,
		})
	}()
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("AddUserProfile() error = %v", err)
		}
	}

	verify, _ := DefaultRegistry()
	verify.SetUserConfigPath(path)
	if err := verify.LoadUserConfig(path); err != nil {
		t.Fatalf("LoadUserConfig() error = %v", err)
	}
	if _, ok := verify.Get("agent-a"); !ok {
		t.Fatal("agent-a missing after concurrent add")
	}
	if _, ok := verify.Get("agent-b"); !ok {
		t.Fatal("agent-b missing after concurrent add")
	}
}
