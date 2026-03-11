package agents

import (
	"context"
	"fmt"
	"slices"

	"github.com/liliang/roma/internal/domain"
)

// Registry provides discoverable agent profiles.
type Registry struct {
	profiles map[string]domain.AgentProfile
}

// NewRegistry constructs a registry from agent profiles.
func NewRegistry(profiles ...domain.AgentProfile) (*Registry, error) {
	registry := &Registry{
		profiles: make(map[string]domain.AgentProfile, len(profiles)),
	}

	for _, profile := range profiles {
		if err := registry.Register(profile); err != nil {
			return nil, err
		}
	}

	return registry, nil
}

// DefaultRegistry returns the built-in coding agents known by ROMA.
func DefaultRegistry() (*Registry, error) {
	return NewRegistry(
		domain.AgentProfile{
			ID:                 "claude-code",
			DisplayName:        "Claude Code",
			Command:            "claude",
			Args:               []string{"code"},
			Aliases:            []string{"claude"},
			SupportsMCP:        true,
			SupportsJSONOutput: false,
			Capabilities:       []string{"interactive", "patch_generation", "tool_use"},
			Availability:       domain.AgentAvailabilityPlanned,
		},
		domain.AgentProfile{
			ID:                 "codex-cli",
			DisplayName:        "Codex CLI",
			Command:            "codex",
			Aliases:            []string{"codex"},
			SupportsMCP:        true,
			SupportsJSONOutput: false,
			Capabilities:       []string{"interactive", "exec_mode", "tool_use"},
			Availability:       domain.AgentAvailabilityPlanned,
		},
		domain.AgentProfile{
			ID:                 "gemini-cli",
			DisplayName:        "Gemini CLI",
			Command:            "gemini",
			Aliases:            []string{"gemini"},
			SupportsMCP:        false,
			SupportsJSONOutput: true,
			Capabilities:       []string{"interactive", "structured_output"},
			Availability:       domain.AgentAvailabilityPlanned,
		},
		domain.AgentProfile{
			ID:                 "copilot-cli",
			DisplayName:        "Copilot CLI",
			Command:            "copilot",
			Aliases:            []string{"copilot"},
			SupportsMCP:        false,
			SupportsJSONOutput: false,
			Capabilities:       []string{"interactive", "code_generation"},
			Availability:       domain.AgentAvailabilityPlanned,
		},
	)
}

// Register adds a profile to the registry.
func (r *Registry) Register(profile domain.AgentProfile) error {
	if err := domain.ValidateAgentProfile(profile); err != nil {
		return fmt.Errorf("validate agent profile: %w", err)
	}
	if _, exists := r.profiles[profile.ID]; exists {
		return fmt.Errorf("agent profile %s already exists", profile.ID)
	}
	r.profiles[profile.ID] = profile
	return nil
}

// List returns sorted agent profiles.
func (r *Registry) List(_ context.Context) []domain.AgentProfile {
	out := make([]domain.AgentProfile, 0, len(r.profiles))
	for _, profile := range r.profiles {
		out = append(out, profile)
	}
	slices.SortFunc(out, func(a, b domain.AgentProfile) int {
		if a.DisplayName == b.DisplayName {
			switch {
			case a.ID < b.ID:
				return -1
			case a.ID > b.ID:
				return 1
			default:
				return 0
			}
		}
		switch {
		case a.DisplayName < b.DisplayName:
			return -1
		case a.DisplayName > b.DisplayName:
			return 1
		default:
			return 0
		}
	})
	return out
}
