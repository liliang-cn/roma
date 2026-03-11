package run

import (
	"testing"

	"github.com/liliang/roma/internal/artifacts"
	"github.com/liliang/roma/internal/domain"
	"github.com/liliang/roma/internal/scheduler"
)

func TestExtractDelegateRequests(t *testing.T) {
	t.Parallel()

	envelope := domain.ArtifactEnvelope{
		Payload: artifacts.ReportPayload{
			FollowUpRequests: []artifacts.FollowUpRequest{
				{Kind: "delegate", AgentID: "gemini"},
				{Kind: "delegate", AgentID: "copilot", Instruction: "review the plan"},
			},
		},
	}
	got := extractDelegateRequests(envelope)
	if len(got) != 2 || got[0].AgentID != "gemini" || got[1].Instruction != "review the plan" {
		t.Fatalf("extractDelegateRequests() = %#v", got)
	}
}

func TestCollectDelegateRequestsSkipsExistingAssignments(t *testing.T) {
	t.Parallel()

	result := scheduler.DispatchResult{
		Order: []string{"task_1"},
		Artifacts: map[string]domain.ArtifactEnvelope{
			"task_1": {
				Payload: artifacts.ReportPayload{
					FollowUpRequests: []artifacts.FollowUpRequest{
						{Kind: "delegate", AgentID: "gemini"},
						{Kind: "delegate", AgentID: "copilot", Instruction: "review"},
					},
				},
			},
		},
	}
	assignments := []scheduler.NodeAssignment{
		{
			Node: domain.TaskNodeSpec{ID: "task_1"},
			Profile: domain.AgentProfile{
				ID: "codex-cli",
			},
		},
		{
			Node: domain.TaskNodeSpec{
				ID:           "task_1__delegate_1",
				Dependencies: []string{"task_1"},
			},
			Profile: domain.AgentProfile{
				ID: "gemini",
			},
		},
	}

	got := collectDelegateRequests(result, assignments, map[string]struct{}{
		"task_1::gemini": {},
	})
	if len(got) != 1 || got[0].SourceNodeID != "task_1" || got[0].AgentID != "copilot" || got[0].Instruction != "review" {
		t.Fatalf("collectDelegateRequests() = %#v, want one copilot request", got)
	}
}

func TestNextDynamicDelegateNodeID(t *testing.T) {
	t.Parallel()

	assignments := []scheduler.NodeAssignment{
		{Node: domain.TaskNodeSpec{ID: "task_1"}},
		{Node: domain.TaskNodeSpec{ID: "task_1__delegate_1"}},
		{Node: domain.TaskNodeSpec{ID: "task_1__delegate_2"}},
	}
	if got := nextDynamicDelegateNodeID(assignments, "task_1"); got != "task_1__delegate_3" {
		t.Fatalf("nextDynamicDelegateNodeID() = %q, want task_1__delegate_3", got)
	}
}
