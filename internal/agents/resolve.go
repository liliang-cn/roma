package agents

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/liliang-cn/roma/internal/domain"
)

// Resolve finds an agent by id, command, or alias and refreshes availability from PATH.
func (r *Registry) Resolve(ctx context.Context, name string) (domain.AgentProfile, bool) {
	needle := strings.TrimSpace(strings.ToLower(name))
	for _, profile := range r.List(context.Background()) {
		if matchesProfile(profile, needle) {
			profile.Availability = availabilityForProfile(ctx, profile)
			return profile, true
		}
	}
	return domain.AgentProfile{}, false
}

// WithResolvedAvailability updates all profiles based on the current PATH.
func (r *Registry) WithResolvedAvailability(ctx context.Context) []domain.AgentProfile {
	profiles := r.List(ctx)
	for i := range profiles {
		profiles[i].Availability = availabilityForProfile(ctx, profiles[i])
	}
	return profiles
}

func availabilityForProfile(ctx context.Context, profile domain.AgentProfile) domain.AgentAvailability {
	if _, err := exec.LookPath(profile.Command); err != nil {
		return domain.AgentAvailabilityPlanned
	}

	healthcheckArgs := profile.HealthcheckArgs
	if len(healthcheckArgs) == 0 {
		healthcheckArgs = []string{"--help"}
	}

	checkCtx := ctx
	if checkCtx == nil {
		checkCtx = context.Background()
	}
	checkCtx, cancel := context.WithTimeout(checkCtx, 3*time.Second)
	defer cancel()

	cmd := exec.CommandContext(checkCtx, profile.Command, healthcheckArgs...)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	err := cmd.Run()
	if err == nil {
		return domain.AgentAvailabilityAvailable
	}
	if errors.Is(checkCtx.Err(), context.DeadlineExceeded) {
		return domain.AgentAvailabilityPlanned
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return domain.AgentAvailabilityAvailable
	}
	return domain.AgentAvailabilityPlanned
}
