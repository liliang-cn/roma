package run

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/liliang-cn/roma/internal/artifacts"
	"github.com/liliang-cn/roma/internal/domain"
	"github.com/liliang-cn/roma/internal/scheduler"
)

const maxDynamicDelegationPasses = 3

type delegateRequest struct {
	SourceNodeID string
	AgentID      string
	Instruction  string
}

func (s *Service) extendDynamicDelegations(
	ctx context.Context,
	sessionID, workDir, basePrompt string,
	assignments []scheduler.NodeAssignment,
	result scheduler.DispatchResult,
) ([]scheduler.NodeAssignment, scheduler.DispatchResult, []string, error) {
	currentAssignments := append([]scheduler.NodeAssignment(nil), assignments...)
	currentResult := result
	addedDelegates := make([]string, 0)
	seen := make(map[string]struct{})
	dispatcher := scheduler.NewDispatcher(workDir, s.supervisor, s.events, s.tasks)

	for pass := 0; pass < maxDynamicDelegationPasses; pass++ {
		requests := collectDelegateRequests(currentResult, currentAssignments, seen)
		if len(requests) == 0 {
			break
		}

		existing := cloneArtifacts(currentResult.Artifacts)
		beforeCount := len(currentAssignments)
		for _, req := range requests {
			profile, ok := s.registry.Resolve(ctx, req.AgentID)
			if !ok || profile.Availability != domain.AgentAvailabilityAvailable {
				continue
			}
			key := req.SourceNodeID + "::" + profile.ID
			if _, ok := seen[key]; ok {
				continue
			}
			if hasDelegateAssignment(currentAssignments, req.SourceNodeID, profile.ID) {
				seen[key] = struct{}{}
				continue
			}

			nodeID := nextDynamicDelegateNodeID(currentAssignments, req.SourceNodeID)
			reviewer := semanticReviewerForDynamicDelegate(currentAssignments, req.SourceNodeID)
			node := domain.TaskNodeSpec{
				ID:            nodeID,
				Title:         "Dynamic delegated follow-up",
				Strategy:      domain.TaskStrategyRelay,
				Dependencies:  []string{req.SourceNodeID},
				SchemaVersion: "v1",
			}
			if s.tasks != nil {
				lifecycle := scheduler.NewGraphLifecycle(s.tasks, s.events)
				if err := lifecycle.RegisterTask(ctx, sessionID, node, profile.ID); err != nil {
					return currentAssignments, currentResult, addedDelegates, fmt.Errorf("register dynamic delegate task %s: %w", nodeID, err)
				}
			}
			currentAssignments = append(currentAssignments, scheduler.NodeAssignment{
				Node:             node,
				Profile:          profile,
				SemanticReviewer: reviewer,
				PromptHint:       req.Instruction,
			})
			addedDelegates = append(addedDelegates, profile.ID)
			seen[key] = struct{}{}
		}

		if len(currentAssignments) == beforeCount {
			break
		}

		resumeResult, err := dispatcher.Resume(ctx, sessionID, workDir, basePrompt, currentAssignments, existing)
		currentResult = resumeResult
		if err != nil {
			return currentAssignments, currentResult, addedDelegates, err
		}
	}

	return currentAssignments, currentResult, addedDelegates, nil
}

func collectDelegateRequests(result scheduler.DispatchResult, assignments []scheduler.NodeAssignment, seen map[string]struct{}) []delegateRequest {
	requests := make([]delegateRequest, 0)
	for _, nodeID := range result.Order {
		artifact := result.Artifacts[nodeID]
		for _, item := range extractDelegateRequests(artifact) {
			key := nodeID + "::" + item.AgentID + "::" + item.Instruction
			if _, ok := seen[key]; ok {
				continue
			}
			if hasDelegateAssignment(assignments, nodeID, item.AgentID) {
				seen[key] = struct{}{}
				continue
			}
			requests = append(requests, delegateRequest{
				SourceNodeID: nodeID,
				AgentID:      item.AgentID,
				Instruction:  item.Instruction,
			})
		}
	}
	return requests
}

func extractDelegateRequests(envelope domain.ArtifactEnvelope) []artifacts.FollowUpRequest {
	raw, err := json.Marshal(envelope.Payload)
	if err != nil {
		return nil
	}
	var payload artifacts.ReportPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil
	}
	out := make([]artifacts.FollowUpRequest, 0, len(payload.FollowUpRequests))
	for _, item := range payload.FollowUpRequests {
		if item.Kind != "delegate" || strings.TrimSpace(item.AgentID) == "" {
			continue
		}
		out = append(out, item)
	}
	return out
}

func hasDelegateAssignment(assignments []scheduler.NodeAssignment, sourceNodeID, agentID string) bool {
	for _, assignment := range assignments {
		if assignment.Profile.ID != agentID {
			continue
		}
		for _, dep := range assignment.Node.Dependencies {
			if dep == sourceNodeID {
				return true
			}
		}
	}
	return false
}

func nextDynamicDelegateNodeID(assignments []scheduler.NodeAssignment, sourceNodeID string) string {
	prefix := sourceNodeID + "__delegate_"
	maxIndex := 0
	for _, assignment := range assignments {
		if !strings.HasPrefix(assignment.Node.ID, prefix) {
			continue
		}
		suffix := strings.TrimPrefix(assignment.Node.ID, prefix)
		index, err := strconv.Atoi(suffix)
		if err != nil {
			continue
		}
		if index > maxIndex {
			maxIndex = index
		}
	}
	return fmt.Sprintf("%s__delegate_%d", sourceNodeID, maxIndex+1)
}

func semanticReviewerForDynamicDelegate(assignments []scheduler.NodeAssignment, sourceNodeID string) domain.AgentProfile {
	for _, assignment := range assignments {
		if assignment.Node.ID != sourceNodeID {
			continue
		}
		if strings.TrimSpace(assignment.SemanticReviewer.ID) != "" {
			return assignment.SemanticReviewer
		}
		return assignment.Profile
	}
	return domain.AgentProfile{}
}

func cloneArtifacts(in map[string]domain.ArtifactEnvelope) map[string]domain.ArtifactEnvelope {
	out := make(map[string]domain.ArtifactEnvelope, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
