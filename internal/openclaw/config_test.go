package openclaw

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/liliang-cn/tagit/internal/chatbot"
)

func TestLoadMissingFileDisablesTheBridge(t *testing.T) {
	cfg, enabled, err := Load(filepath.Join(t.TempDir(), "absent.json"))
	if err != nil {
		t.Fatalf("Load() error = %v, want nil for a missing file", err)
	}
	if enabled || cfg != nil {
		t.Errorf("Load() = %+v enabled=%v, want disabled", cfg, enabled)
	}
}

func TestLoadBrokenFileIsAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "openclaw.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, enabled, err := Load(path); err == nil {
		t.Fatalf("Load() error = nil enabled=%v, want an error for a broken file", enabled)
	}
}

func TestLoadDefaultsToOpenClawMCPServe(t *testing.T) {
	path := filepath.Join(t.TempDir(), "openclaw.json")
	if err := os.WriteFile(path, []byte(`{"bindings":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, enabled, err := Load(path)
	if err != nil || !enabled {
		t.Fatalf("Load() enabled=%v err=%v", enabled, err)
	}
	if cfg.Command != "openclaw" {
		t.Errorf("Command = %q, want openclaw", cfg.Command)
	}
	if len(cfg.Args) == 0 || cfg.Args[0] != "mcp" {
		t.Errorf("Args = %v, want them to launch the mcp bridge", cfg.Args)
	}
}

func TestLoadKeepsAnExplicitCommand(t *testing.T) {
	path := filepath.Join(t.TempDir(), "openclaw.json")
	if err := os.WriteFile(path, []byte(`{"command":"/opt/bin/openclaw","args":["mcp","serve"]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, _, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Command != "/opt/bin/openclaw" {
		t.Errorf("Command = %q, want the configured path", cfg.Command)
	}
	if len(cfg.Args) != 2 || cfg.Args[1] != "serve" {
		t.Errorf("Args = %v, want the configured args", cfg.Args)
	}
}

func TestBindingForMatchesTheSessionKey(t *testing.T) {
	cfg := &Config{Bindings: chatbot.Bindings{{ChatID: "agent:main:telegram:direct:1", Repo: "/repo"}}}

	if got, ok := cfg.BindingFor("agent:main:telegram:direct:1"); !ok || got.Repo != "/repo" {
		t.Errorf("BindingFor(bound) = %+v ok=%v", got, ok)
	}
	if _, ok := cfg.BindingFor("agent:main:telegram:direct:2"); ok {
		t.Error("BindingFor(unbound) must not match")
	}
}

func TestConfigStoreRoundTripPreservesTheCommand(t *testing.T) {
	path := filepath.Join(t.TempDir(), "openclaw.json")
	seed := Config{Command: "/opt/bin/openclaw", Args: []string{"mcp", "serve", "--verbose"}}
	data, _ := json.MarshalIndent(seed, "", "  ")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	store := NewConfigStore(path)
	if _, ok := store.For("s1"); ok {
		t.Fatal("expected no binding initially")
	}
	if err := store.Set(chatbot.Binding{ChatID: "s1", Repo: "/r", Agent: "codex", Mode: "senate"}); err != nil {
		t.Fatal(err)
	}

	// A fresh store re-reads the file, so external edits and restarts are seen.
	got, ok := NewConfigStore(path).For("s1")
	if !ok || got.Repo != "/r" || got.Agent != "codex" || got.Mode != "senate" {
		t.Fatalf("For(s1) = %+v ok=%v", got, ok)
	}

	cfg, enabled, err := Load(path)
	if err != nil || !enabled {
		t.Fatalf("Load after Set: enabled=%v err=%v", enabled, err)
	}
	if cfg.Command != "/opt/bin/openclaw" || len(cfg.Args) != 3 {
		t.Fatalf("command/args dropped by a binding write: %+v", cfg)
	}

	if err := store.Delete("s1"); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.For("s1"); ok {
		t.Fatal("binding should be deleted")
	}
	if cfg, _, err = Load(path); err != nil {
		t.Fatal(err)
	}
	if cfg.Command != "/opt/bin/openclaw" || len(cfg.Args) != 3 {
		t.Fatalf("command/args dropped by a binding delete: %+v", cfg)
	}
}

func TestConfigStoreOnAMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.json")
	store := NewConfigStore(path)

	if _, ok := store.For("s1"); ok {
		t.Fatal("missing file should yield no bindings")
	}
	if err := store.Set(chatbot.Binding{ChatID: "s1", Repo: "/r"}); err != nil {
		t.Fatal(err)
	}
	if got, ok := store.For("s1"); !ok || got.Repo != "/r" {
		t.Fatalf("For after Set on missing file = %+v ok=%v", got, ok)
	}
}
