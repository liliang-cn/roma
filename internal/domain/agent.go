package domain

import "fmt"

// AgentAvailability indicates whether an agent is wired and usable.
type AgentAvailability string

const (
	AgentAvailabilityPlanned   AgentAvailability = "planned"
	AgentAvailabilityAvailable AgentAvailability = "available"
)

// AgentProfile describes an external coding agent runtime.
type AgentProfile struct {
	ID                 string            `json:"id"`
	DisplayName        string            `json:"display_name"`
	Command            string            `json:"command"`
	Args               []string          `json:"args,omitempty"`
	HealthcheckArgs    []string          `json:"healthcheck_args,omitempty"`
	Aliases            []string          `json:"aliases,omitempty"`
	UsePTY             bool              `json:"use_pty,omitempty"`
	SupportsMCP        bool              `json:"supports_mcp"`
	SupportsJSONOutput bool              `json:"supports_json_output"`
	Capabilities       []string          `json:"capabilities,omitempty"`
	Metadata           map[string]string `json:"metadata,omitempty"`
	Availability       AgentAvailability `json:"availability"`
}

// ValidateAgentProfile checks minimal agent profile invariants.
func ValidateAgentProfile(profile AgentProfile) error {
	if profile.ID == "" {
		return fmt.Errorf("agent id is required")
	}
	if profile.DisplayName == "" {
		return fmt.Errorf("agent display name is required")
	}
	if profile.Command == "" {
		return fmt.Errorf("agent command is required")
	}
	switch profile.Availability {
	case AgentAvailabilityPlanned, AgentAvailabilityAvailable:
		return nil
	default:
		return fmt.Errorf("unknown agent availability %q", profile.Availability)
	}
}
