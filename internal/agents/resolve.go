package agents

import (
	"context"
	"os/exec"
	"slices"
	"strings"

	"github.com/liliang-cn/roma/internal/domain"
)

// Resolve finds an agent by id, command, or alias and refreshes availability from PATH.
func (r *Registry) Resolve(ctx context.Context, name string) (domain.AgentProfile, bool) {
	_ = ctx
	needle := strings.TrimSpace(strings.ToLower(name))
	for _, profile := range r.List(context.Background()) {
		if profile.ID == needle || strings.ToLower(profile.Command) == needle || slices.Contains(profile.Aliases, needle) {
			profile.Availability = availabilityForCommand(profile.Command)
			return profile, true
		}
	}
	return domain.AgentProfile{}, false
}

// WithResolvedAvailability updates all profiles based on the current PATH.
func (r *Registry) WithResolvedAvailability(ctx context.Context) []domain.AgentProfile {
	profiles := r.List(ctx)
	for i := range profiles {
		profiles[i].Availability = availabilityForCommand(profiles[i].Command)
	}
	return profiles
}

func availabilityForCommand(command string) domain.AgentAvailability {
	if _, err := exec.LookPath(command); err == nil {
		return domain.AgentAvailabilityAvailable
	}
	return domain.AgentAvailabilityPlanned
}
