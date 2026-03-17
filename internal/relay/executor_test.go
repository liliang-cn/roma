package relay

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/liliang-cn/roma/internal/domain"
	"github.com/liliang-cn/roma/internal/runtime"
	"github.com/liliang-cn/roma/internal/store"
)

type fakeAdapter struct{}

func (fakeAdapter) Supports(domain.AgentProfile) bool { return true }

func (fakeAdapter) BuildCommand(ctx context.Context, req runtime.StartRequest) (*exec.Cmd, error) {
	return exec.CommandContext(ctx, "python3", "-c", "import sys; print(sys.argv[1])", req.Prompt), nil
}

func TestExecuteRelayGraph(t *testing.T) {
	t.Parallel()

	executor := NewExecutor(runtime.NewSupervisor(fakeAdapter{}), store.NewMemoryStore())
	result, err := executor.Execute(context.Background(), "sess_1", ".", "build feature", []NodeAssignment{
		{
			Node:    domain.TaskNodeSpec{ID: "task_a", Title: "Starter", Strategy: domain.TaskStrategyDirect},
			Profile: domain.AgentProfile{ID: "starter", DisplayName: "Starter", Command: "starter"},
		},
		{
			Node:    domain.TaskNodeSpec{ID: "task_b", Title: "Relay", Strategy: domain.TaskStrategyRelay, Dependencies: []string{"task_a"}},
			Profile: domain.AgentProfile{ID: "delegate", DisplayName: "Delegate", Command: "delegate"},
		},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(result.Order) != 2 {
		t.Fatalf("order len = %d, want 2", len(result.Order))
	}
	if _, ok := result.Artifacts["task_b"]; !ok {
		t.Fatal("missing relay artifact")
	}
}

type slowFakeAdapter struct{}

func (slowFakeAdapter) Supports(domain.AgentProfile) bool { return true }

func (slowFakeAdapter) BuildCommand(ctx context.Context, req runtime.StartRequest) (*exec.Cmd, error) {
	return exec.CommandContext(ctx, "python3", "-c", "import sys,time; time.sleep(float(sys.argv[1])); print(sys.argv[2])", "0.2", req.Prompt), nil
}

func TestExecuteRelayGraphRunsReadyNodesConcurrently(t *testing.T) {
	t.Parallel()

	executor := NewExecutor(runtime.NewSupervisor(slowFakeAdapter{}), store.NewMemoryStore())
	started := time.Now()
	result, err := executor.Execute(context.Background(), "sess_2", ".", "parallel batch", []NodeAssignment{
		{
			Node:    domain.TaskNodeSpec{ID: "task_a", Title: "A", Strategy: domain.TaskStrategyDirect},
			Profile: domain.AgentProfile{ID: "starter-a", DisplayName: "Starter A", Command: "starter-a"},
		},
		{
			Node:    domain.TaskNodeSpec{ID: "task_b", Title: "B", Strategy: domain.TaskStrategyDirect},
			Profile: domain.AgentProfile{ID: "starter-b", DisplayName: "Starter B", Command: "starter-b"},
		},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(result.Order) != 2 {
		t.Fatalf("order len = %d, want 2", len(result.Order))
	}
	elapsed := time.Since(started)
	if elapsed >= 1000*time.Millisecond {
		t.Fatalf("elapsed = %v, want concurrent batch under 1000ms", elapsed)
	}
}
