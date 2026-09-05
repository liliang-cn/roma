package gateway

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/liliang-cn/tagit/internal/domain"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gateway.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadMissingFileIsNotAnError(t *testing.T) {
	t.Parallel()

	cfg, enabled, err := Load(filepath.Join(t.TempDir(), "absent.json"))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if enabled || len(cfg.Endpoints) != 0 {
		t.Fatalf("missing config should be disabled and empty, got enabled=%v cfg=%+v", enabled, cfg)
	}
}

func TestLoadAndRegistrations(t *testing.T) {
	t.Parallel()

	path := writeConfig(t, `{
	  "endpoints": [
	    {
	      "id": "ci",
	      "target": "https://example.com/hook",
	      "secret": "env:HOOK_SECRET",
	      "events": ["task_succeeded", "task_failed"],
	      "severity": "medium",
	      "actions": ["approve", "reject"]
	    }
	  ]
	}`)

	cfg, enabled, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !enabled {
		t.Fatal("a config with one endpoint should be enabled")
	}

	regs, err := cfg.Registrations()
	if err != nil {
		t.Fatalf("Registrations() error = %v", err)
	}
	if len(regs) != 1 {
		t.Fatalf("got %d registrations, want 1", len(regs))
	}
	ep := regs[0].Endpoint
	// type defaults to webhook so the common case needs no "type" key.
	if ep.Type != domain.GatewayEndpointTypeWebhook || !ep.Enabled || ep.Target != "https://example.com/hook" {
		t.Fatalf("endpoint = %+v", ep)
	}
	if !slices.Equal(ep.AllowedActions, []domain.RemoteCommandAction{domain.RemoteCommandActionApprove, domain.RemoteCommandActionReject}) {
		t.Fatalf("actions = %v", ep.AllowedActions)
	}
	sub := regs[0].Subscription
	if sub.EndpointID != "ci" || sub.SeverityThreshold != domain.NotificationSeverityMedium {
		t.Fatalf("subscription = %+v", sub)
	}
	if !slices.Equal(sub.EventTypes, []string{"task_succeeded", "task_failed"}) {
		t.Fatalf("event types = %v", sub.EventTypes)
	}
}

func TestRegistrationsDefaultsAndDisabled(t *testing.T) {
	t.Parallel()

	cfg := Config{Endpoints: []EndpointConfig{{
		ID: "quiet", Target: "https://example.com/x", Disabled: true,
	}}}
	regs, err := cfg.Registrations()
	if err != nil {
		t.Fatalf("Registrations() error = %v", err)
	}
	if regs[0].Endpoint.Enabled {
		t.Fatal("disabled:true must produce a disabled endpoint")
	}
	// No events listed means every event type, and severity defaults to low so
	// nothing is filtered out by accident.
	if regs[0].Subscription.EventTypes != nil {
		t.Fatalf("event types = %v, want nil (all)", regs[0].Subscription.EventTypes)
	}
	if regs[0].Subscription.SeverityThreshold != domain.NotificationSeverityLow {
		t.Fatalf("severity = %q, want low", regs[0].Subscription.SeverityThreshold)
	}
}

func TestRegistrationsRejectsBadEntries(t *testing.T) {
	t.Parallel()

	cases := map[string]Config{
		"missing id":     {Endpoints: []EndpointConfig{{Target: "https://x"}}},
		"missing target": {Endpoints: []EndpointConfig{{ID: "a"}}},
		"bad severity":   {Endpoints: []EndpointConfig{{ID: "a", Target: "https://x", Severity: "urgent"}}},
		"bad action":     {Endpoints: []EndpointConfig{{ID: "a", Target: "https://x", Actions: []string{"detonate"}}}},
		"duplicate id": {Endpoints: []EndpointConfig{
			{ID: "a", Target: "https://x"},
			{ID: "a", Target: "https://y"},
		}},
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			// A bad entry fails the load instead of being skipped: an endpoint
			// silently dropped is a page silently not sent.
			if _, err := cfg.Registrations(); err == nil {
				t.Fatalf("Registrations() error = nil for %s", name)
			}
		})
	}
}

func TestLoadRejectsMalformedJSON(t *testing.T) {
	t.Parallel()

	if _, _, err := Load(writeConfig(t, "{not json")); err == nil {
		t.Fatal("Load() error = nil for malformed JSON")
	}
}
