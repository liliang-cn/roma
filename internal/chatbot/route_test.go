package chatbot

import (
	"slices"
	"testing"
)

func TestSplitLeadingMention(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		text        string
		wantMention string
		wantRest    string
		wantOK      bool
	}{
		{"plain", "@dev-agent fix the login bug", "dev-agent", "fix the login bug", true},
		{"extra spaces", "  @qa-agent   run the suite", "qa-agent", "run the suite", true},
		{"dots and underscores", "@qa_agent.v2 go", "qa_agent.v2", "go", true},
		{"mention only", "@dev-agent", "dev-agent", "", true},
		{"no mention", "fix the login bug", "", "fix the login bug", false},
		{"multiline task survives", "@dev-agent do this\nand that", "dev-agent", "do this\nand that", true},
		// An "@" that is not leading is prose, not a route.
		{"mid-text at sign", "ask @bob about the bug", "", "ask @bob about the bug", false},
		{"email", "mail ops@example.com", "", "mail ops@example.com", false},
		{"bare at", "@ something", "", "@ something", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mention, rest, ok := SplitLeadingMention(tc.text)
			if ok != tc.wantOK || mention != tc.wantMention || rest != tc.wantRest {
				t.Fatalf("SplitLeadingMention(%q) = (%q, %q, %v), want (%q, %q, %v)",
					tc.text, mention, rest, ok, tc.wantMention, tc.wantRest, tc.wantOK)
			}
		})
	}
}

func TestRoutesSetLookupDelete(t *testing.T) {
	t.Parallel()

	var r Routes // zero value must be usable
	r = r.Set("@Dev-Agent", "claude")
	r = r.Set("qa-agent", "codex")

	// Lookup is case-insensitive and "@"-insensitive.
	for _, probe := range []string{"dev-agent", "Dev-Agent", "@DEV-AGENT"} {
		if agent, ok := r.Lookup(probe); !ok || agent != "claude" {
			t.Fatalf("Lookup(%q) = (%q, %v)", probe, agent, ok)
		}
	}
	if _, ok := r.Lookup("nobody"); ok {
		t.Fatal("Lookup of an unknown mention should miss")
	}
	if got := r.Mentions(); !slices.Equal(got, []string{"dev-agent", "qa-agent"}) {
		t.Fatalf("Mentions() = %v", got)
	}
	if !r.Delete("@dev-agent") {
		t.Fatal("Delete of an existing route should report true")
	}
	if r.Delete("@dev-agent") {
		t.Fatal("Delete of a missing route should report false")
	}
}

func TestResolveAgent(t *testing.T) {
	t.Parallel()

	b := Binding{
		ChatID: "c1",
		Repo:   "/repo",
		Agent:  "claude",
		Routes: Routes{"dev-agent": "codex", "qa-agent": "gemini"},
	}

	t.Run("routed mention picks its agent and is stripped", func(t *testing.T) {
		agent, prompt := ResolveAgent(b, "@qa-agent run the suite")
		if agent != "gemini" || prompt != "run the suite" {
			t.Fatalf("= (%q, %q)", agent, prompt)
		}
	})

	t.Run("no mention uses the default agent", func(t *testing.T) {
		agent, prompt := ResolveAgent(b, "fix the bug")
		if agent != "claude" || prompt != "fix the bug" {
			t.Fatalf("= (%q, %q)", agent, prompt)
		}
	})

	t.Run("unrouted mention stays in the prompt", func(t *testing.T) {
		// "@bob" is far more likely a person than a typo'd route, so deleting
		// it from someone's request would be the worse guess.
		agent, prompt := ResolveAgent(b, "@bob please review this")
		if agent != "claude" || prompt != "@bob please review this" {
			t.Fatalf("= (%q, %q)", agent, prompt)
		}
	})

	t.Run("no routes configured", func(t *testing.T) {
		plain := Binding{Agent: "claude"}
		agent, prompt := ResolveAgent(plain, "@dev-agent do it")
		if agent != "claude" || prompt != "@dev-agent do it" {
			t.Fatalf("= (%q, %q)", agent, prompt)
		}
	})
}
