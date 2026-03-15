package run

import (
	"context"
	"fmt"
	"strings"

	"github.com/liliang-cn/roma/internal/domain"
	"github.com/liliang-cn/roma/internal/scheduler"
)

func (s *Service) continueCaesarCoordination(
	ctx context.Context,
	req Request,
	sessionID, taskID string,
	starter domain.AgentProfile,
	assignments []scheduler.NodeAssignment,
	result scheduler.DispatchResult,
	dispatcher *scheduler.Dispatcher,
) ([]scheduler.NodeAssignment, scheduler.DispatchResult, error) {
	if len(assignments) <= 1 {
		return assignments, result, nil
	}

	processedArtifacts := map[string]struct{}{}
	s.handleMergeBackRequests(ctx, req.WorkingDir, collectUnprocessedArtifacts(result, processedArtifacts))

	currentAssignments := append([]scheduler.NodeAssignment(nil), assignments...)
	currentResult := result
	currentWave := append([]string(nil), initialDelegateNodeIDs(assignments)...)
	round := 1

	for len(currentWave) > 0 {
		reviewNode := buildCaesarReviewAssignment(taskID, starter, currentWave, req.Continuous, req.MaxRounds, round)
		if s.tasks != nil {
			lifecycle := scheduler.NewGraphLifecycle(s.tasks, s.events)
			if err := lifecycle.RegisterTask(ctx, sessionID, reviewNode.Node, starter.ID); err != nil {
				return currentAssignments, currentResult, fmt.Errorf("register Caesar review task %s: %w", reviewNode.Node.ID, err)
			}
		}
		currentAssignments = append(currentAssignments, reviewNode)

		resumeResult, err := dispatcher.Resume(ctx, sessionID, req.WorkingDir, req.Prompt, currentAssignments, cloneArtifacts(currentResult.Artifacts))
		currentResult = resumeResult
		s.handleMergeBackRequests(ctx, req.WorkingDir, collectUnprocessedArtifacts(currentResult, processedArtifacts))
		if err != nil {
			return currentAssignments, currentResult, err
		}

		reviewArtifact, ok := currentResult.Artifacts[reviewNode.Node.ID]
		if !ok || reviewArtifact.ID == "" {
			return currentAssignments, currentResult, fmt.Errorf("caesar review node %s produced no artifact", reviewNode.Node.ID)
		}

		requests := extractDelegateRequests(reviewArtifact)
		if len(requests) == 0 {
			return currentAssignments, currentResult, nil
		}

		nextWave := make([]string, 0, len(requests))
		for _, request := range requests {
			profile, ok := s.registry.Resolve(ctx, request.AgentID)
			if !ok || profile.Availability != domain.AgentAvailabilityAvailable {
				continue
			}

			nodeID := nextDynamicDelegateNodeID(currentAssignments, reviewNode.Node.ID)
			node := domain.TaskNodeSpec{
				ID:            nodeID,
				Title:         "Caesar delegated execution",
				Strategy:      domain.TaskStrategyRelay,
				Dependencies:  []string{reviewNode.Node.ID},
				SchemaVersion: "v1",
			}
			if s.tasks != nil {
				lifecycle := scheduler.NewGraphLifecycle(s.tasks, s.events)
				if err := lifecycle.RegisterTask(ctx, sessionID, node, profile.ID); err != nil {
					return currentAssignments, currentResult, fmt.Errorf("register Caesar delegate task %s: %w", nodeID, err)
				}
			}
			currentAssignments = append(currentAssignments, scheduler.NodeAssignment{
				Node:             node,
				Profile:          profile,
				SemanticReviewer: starter,
				Continuous:       req.Continuous,
				MaxRounds:        req.MaxRounds,
				PromptHint:       buildCaesarDelegatePromptHint(starter, request.Instruction),
			})
			nextWave = append(nextWave, nodeID)
		}

		if len(nextWave) == 0 {
			return currentAssignments, currentResult, fmt.Errorf("caesar emitted no actionable delegate requests")
		}

		resumeResult, err = dispatcher.Resume(ctx, sessionID, req.WorkingDir, req.Prompt, currentAssignments, cloneArtifacts(currentResult.Artifacts))
		currentResult = resumeResult
		s.handleMergeBackRequests(ctx, req.WorkingDir, collectUnprocessedArtifacts(currentResult, processedArtifacts))
		if err != nil {
			return currentAssignments, currentResult, err
		}

		currentWave = nextWave
		round++
	}

	return currentAssignments, currentResult, nil
}

func buildCaesarReviewAssignment(taskID string, starter domain.AgentProfile, dependencies []string, continuous bool, maxRounds int, round int) scheduler.NodeAssignment {
	return scheduler.NodeAssignment{
		Node: domain.TaskNodeSpec{
			ID:            fmt.Sprintf("%s_starter_caesar_%d", taskID, round),
			Title:         fmt.Sprintf("Caesar review round %d", round),
			Strategy:      domain.TaskStrategyDirect,
			Dependencies:  append([]string(nil), dependencies...),
			SchemaVersion: "v1",
		},
		Profile:          starter,
		SemanticReviewer: starter,
		Continuous:       continuous,
		MaxRounds:        maxRounds,
		PromptHint:       buildCaesarReviewPromptHint(round),
	}
}

func buildCaesarReviewPromptHint(round int) string {
	return strings.Join([]string{
		fmt.Sprintf("You are Caesar review round %d.", round),
		"You are still only the coordinator. Do not edit files or implement the task yourself.",
		"Review the delegate outputs above and ask one question only: is the main task done?",
		"If more implementation work is needed, emit one or more lines in this exact format:",
		"ROMA_FOLLOWUP: delegate <agent_id> | <instruction>",
		"If the task is complete, emit ROMA_DONE: <brief summary> and do not emit any follow-up lines.",
		"Only delegate concrete implementation work to the agents; keep all coordination with Caesar.",
	}, "\n")
}

func buildCaesarDelegatePromptHint(starter domain.AgentProfile, instruction string) string {
	lines := []string{
		fmt.Sprintf("The starter agent %s is Caesar only and will not implement code.", starter.DisplayName),
		"You own the concrete implementation work for this node.",
		"When your workspace is ready to land, emit `ROMA_MERGE_BACK: direct_merge | <reason>` and optionally `ROMA_MERGE_FILE: <path>` lines.",
	}
	if strings.TrimSpace(instruction) != "" {
		lines = append(lines, "Caesar instruction: "+strings.TrimSpace(instruction))
	}
	return strings.Join(lines, "\n")
}

func initialDelegateNodeIDs(assignments []scheduler.NodeAssignment) []string {
	size := 0
	if len(assignments) > 1 {
		size = len(assignments) - 1
	}
	out := make([]string, 0, size)
	for _, assignment := range assignments[1:] {
		out = append(out, assignment.Node.ID)
	}
	return out
}

func collectUnprocessedArtifacts(result scheduler.DispatchResult, seen map[string]struct{}) []domain.ArtifactEnvelope {
	out := make([]domain.ArtifactEnvelope, 0, len(result.Order))
	for _, nodeID := range result.Order {
		if artifact := result.Artifacts[nodeID]; artifact.ID != "" {
			if _, ok := seen[artifact.ID]; !ok {
				seen[artifact.ID] = struct{}{}
				out = append(out, artifact)
			}
		}
		for _, related := range result.RelatedArtifacts[nodeID] {
			if related.ID == "" {
				continue
			}
			if _, ok := seen[related.ID]; ok {
				continue
			}
			seen[related.ID] = struct{}{}
			out = append(out, related)
		}
	}
	return out
}
