package run

import (
	"context"
	"fmt"
	"strings"

	"github.com/liliang-cn/roma/internal/domain"
	"github.com/liliang-cn/roma/internal/policy"
	"github.com/liliang-cn/roma/internal/scheduler"
)

func (s *Service) maybePromoteOrchestratedToCuria(ctx context.Context, prompt, workingDir, taskID string, starter domain.AgentProfile, delegates []domain.AgentProfile, continuous bool, maxRounds int) ([]scheduler.NodeAssignment, []string) {
	participants := append([]domain.AgentProfile{starter}, delegates...)
	rec := policy.RecommendCuria(policy.Request{
		Prompt:       prompt,
		WorkingDir:   workingDir,
		EffectiveDir: workingDir,
		PathHints:    []string{workingDir},
		StarterAgent: starter.ID,
		Delegates:    profileIDs(delegates),
		NodeCount:    1 + len(delegates),
	}, len(participants))
	if !rec.Upgrade {
		return nil, nil
	}
	return buildCuriaAssignments(taskID, starter, delegates, s.pickCuriaArbitrator(ctx, participants), continuous, maxRounds, rec.Reasons), rec.Reasons
}

func (s *Service) maybePromoteGraphAssignmentsToCuria(ctx context.Context, prompt, workingDir string, assignments []scheduler.NodeAssignment) ([]scheduler.NodeAssignment, []string) {
	if len(assignments) == 0 {
		return assignments, nil
	}
	out := append([]scheduler.NodeAssignment(nil), assignments...)
	reasons := make([]string, 0)
	for i := range out {
		if out[i].Node.Strategy == domain.TaskStrategyCuria {
			continue
		}
		participants := out[i].CuriaProfiles
		if len(participants) == 0 {
			participants = s.defaultCuriaParticipants(ctx, out[i].Profile)
		}
		rec := policy.RecommendCuria(policy.Request{
			Prompt:       prompt + "\n" + out[i].Node.Title,
			WorkingDir:   workingDir,
			EffectiveDir: workingDir,
			PathHints:    []string{workingDir},
			StarterAgent: out[i].Profile.ID,
			NodeCount:    len(assignments),
		}, len(participants))
		if !rec.Upgrade {
			continue
		}
		out[i].Node.Strategy = domain.TaskStrategyCuria
		out[i].CuriaProfiles = participants
		out[i].CuriaQuorum = min(2, len(participants))
		out[i].CuriaArbitrationMode = "augustus"
		out[i].CuriaArbitrator = s.pickCuriaArbitrator(ctx, participants)
		out[i].PromptHint = mergePromptHints(out[i].PromptHint, "This node was automatically promoted to Curia due to: "+strings.Join(rec.Reasons, ", ")+".")
		out[i].Node.Title = out[i].Node.Title + " [auto-curia]"
		reasons = append(reasons, fmt.Sprintf("%s:%s", out[i].Node.ID, strings.Join(rec.Reasons, ",")))
	}
	return out, reasons
}

func buildCuriaAssignments(taskID string, starter domain.AgentProfile, delegates []domain.AgentProfile, arbitrator domain.AgentProfile, continuous bool, maxRounds int, reasons []string) []scheduler.NodeAssignment {
	participants := append([]domain.AgentProfile{starter}, delegates...)
	node := domain.TaskNodeSpec{
		ID:            taskID + "_curia",
		Title:         "Curia consensus review",
		Strategy:      domain.TaskStrategyCuria,
		SchemaVersion: "v1",
	}
	hint := "This task was automatically promoted to Curia. Produce competing proposals, review them anonymously, and derive an execution-ready consensus plan."
	if len(reasons) > 0 {
		hint += "\nPromotion reasons: " + strings.Join(reasons, ", ")
	}
	return []scheduler.NodeAssignment{{
		Node:                 node,
		Profile:              starter,
		SemanticReviewer:     starter,
		CuriaProfiles:        participants,
		CuriaQuorum:          min(2, len(participants)),
		CuriaArbitrator:      arbitrator,
		CuriaArbitrationMode: curiaArbitrationMode(arbitrator),
		PromptHint:           hint,
		Continuous:           continuous,
		MaxRounds:            maxRounds,
	}}
}

func (s *Service) defaultCuriaParticipants(ctx context.Context, preferred domain.AgentProfile) []domain.AgentProfile {
	profiles := s.registry.WithResolvedAvailability(ctx)
	out := make([]domain.AgentProfile, 0, 3)
	added := map[string]struct{}{}
	add := func(profile domain.AgentProfile) {
		if profile.ID == "" || profile.Availability != domain.AgentAvailabilityAvailable {
			return
		}
		if _, ok := added[profile.ID]; ok {
			return
		}
		added[profile.ID] = struct{}{}
		out = append(out, profile)
	}
	add(preferred)
	for _, profile := range profiles {
		add(profile)
		if len(out) >= 3 {
			break
		}
	}
	return out
}

func (s *Service) pickCuriaArbitrator(ctx context.Context, participants []domain.AgentProfile) domain.AgentProfile {
	participantIDs := make(map[string]struct{}, len(participants))
	for _, participant := range participants {
		participantIDs[participant.ID] = struct{}{}
	}
	for _, profile := range s.registry.WithResolvedAvailability(ctx) {
		if profile.Availability != domain.AgentAvailabilityAvailable {
			continue
		}
		if _, ok := participantIDs[profile.ID]; ok {
			continue
		}
		return profile
	}
	if len(participants) > 0 {
		return participants[0]
	}
	return domain.AgentProfile{}
}

func curiaArbitrationMode(arbitrator domain.AgentProfile) string {
	if arbitrator.ID != "" {
		return "augustus"
	}
	return ""
}

func profileIDs(items []domain.AgentProfile) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		if item.ID != "" {
			out = append(out, item.ID)
		}
	}
	return out
}

func mergePromptHints(base, extra string) string {
	base = strings.TrimSpace(base)
	extra = strings.TrimSpace(extra)
	switch {
	case base == "":
		return extra
	case extra == "":
		return base
	default:
		return base + "\n" + extra
	}
}

func countAutoCuriaAssignments(assignments []scheduler.NodeAssignment) int {
	count := 0
	for _, assignment := range assignments {
		if assignment.Node.Strategy == domain.TaskStrategyCuria && strings.Contains(assignment.Node.Title, "[auto-curia]") {
			count++
		}
	}
	return count
}
