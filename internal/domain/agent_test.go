package domain

import "testing"

func TestValidateAgentProfile(t *testing.T) {
	t.Parallel()

	err := ValidateAgentProfile(AgentProfile{
		ID:           "claude-code",
		DisplayName:  "Claude Code",
		Command:      "claude",
		Availability: AgentAvailabilityPlanned,
	})
	if err != nil {
		t.Fatalf("ValidateAgentProfile() error = %v", err)
	}
}

func TestValidateAgentProfileRejectsUnknownAvailability(t *testing.T) {
	t.Parallel()

	err := ValidateAgentProfile(AgentProfile{
		ID:           "claude-code",
		DisplayName:  "Claude Code",
		Command:      "claude",
		Availability: AgentAvailability("bad"),
	})
	if err == nil {
		t.Fatal("ValidateAgentProfile() error = nil, want error")
	}
}
