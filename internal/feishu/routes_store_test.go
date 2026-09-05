package feishu

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/liliang-cn/tagit/internal/chatbot"
)

// Routes are per-channel config, so they have to survive the same write-read
// cycle the repo and agent do. The store replaces a binding wholesale, which
// means a field added to Binding persists for free — this pins that, since the
// failure mode is silent: /route replies with a tick and the route is gone
// after the next daemon restart.
func TestConfigStorePersistsRoutes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "feishu.json")
	seed, _ := json.MarshalIndent(Config{AppID: "app123", AppSecret: "secret456"}, "", "  ")
	if err := os.WriteFile(path, seed, 0o600); err != nil {
		t.Fatal(err)
	}

	store := NewConfigStore(path)
	err := store.Set(chatbot.Binding{
		ChatID: "c1", Repo: "/r", Agent: "claude",
		Routes: chatbot.Routes{"dev-agent": "codex", "qa-agent": "gemini"},
	})
	if err != nil {
		t.Fatal(err)
	}

	got, ok := NewConfigStore(path).For("c1")
	if !ok {
		t.Fatal("binding did not survive the reload")
	}
	if agent, found := got.Routes.Lookup("@DEV-AGENT"); !found || agent != "codex" {
		t.Fatalf("routes after reload = %+v", got.Routes)
	}
	if agent, found := got.Routes.Lookup("qa-agent"); !found || agent != "gemini" {
		t.Fatalf("routes after reload = %+v", got.Routes)
	}
	if got.Agent != "claude" {
		t.Fatalf("default agent = %q", got.Agent)
	}

	// A binding without routes must not grow an empty "routes" key.
	if err := NewConfigStore(path).Set(chatbot.Binding{ChatID: "c2", Repo: "/r2"}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var probe struct {
		Bindings []map[string]any `json:"bindings"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatal(err)
	}
	for _, b := range probe.Bindings {
		if b["chat_id"] != "c2" {
			continue
		}
		if _, present := b["routes"]; present {
			t.Fatalf("unrouted binding serialized a routes key: %v", b)
		}
	}
}
