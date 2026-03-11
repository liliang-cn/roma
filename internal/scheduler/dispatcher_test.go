package scheduler

import (
	"context"
	"errors"
	"os/exec"
	"testing"
	"time"

	"github.com/liliang/roma/internal/domain"
	"github.com/liliang/roma/internal/runtime"
	"github.com/liliang/roma/internal/store"
)

type dispatcherFakeAdapter struct{}

func (dispatcherFakeAdapter) Supports(domain.AgentProfile) bool { return true }

func (dispatcherFakeAdapter) BuildCommand(ctx context.Context, req runtime.StartRequest) (*exec.Cmd, error) {
	return exec.CommandContext(ctx, "python3", "-c", "import sys; print(sys.argv[1])", req.Prompt), nil
}

type dispatcherSlowAdapter struct{}

func (dispatcherSlowAdapter) Supports(domain.AgentProfile) bool { return true }

func (dispatcherSlowAdapter) BuildCommand(ctx context.Context, req runtime.StartRequest) (*exec.Cmd, error) {
	return exec.CommandContext(ctx, "python3", "-c", "import sys,time; time.sleep(float(sys.argv[1])); print(sys.argv[2])", "0.2", req.Prompt), nil
}

func TestDispatcherExecute(t *testing.T) {
	t.Parallel()

	mem := store.NewMemoryStore()
	dispatcher := NewDispatcher("", runtime.NewSupervisor(dispatcherFakeAdapter{}), mem, mem)
	result, err := dispatcher.Execute(context.Background(), "sess_dispatch", ".", "build feature", []NodeAssignment{
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

func TestDispatcherRunsReadyBatchConcurrently(t *testing.T) {
	t.Parallel()

	mem := store.NewMemoryStore()
	dispatcher := NewDispatcher("", runtime.NewSupervisor(dispatcherSlowAdapter{}), mem, mem)
	started := time.Now()
	result, err := dispatcher.Execute(context.Background(), "sess_parallel", ".", "parallel batch", []NodeAssignment{
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
	if elapsed := time.Since(started); elapsed >= 350*time.Millisecond {
		t.Fatalf("elapsed = %v, want concurrent batch under 350ms", elapsed)
	}
}

func TestDispatcherPersistsLease(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	mem := store.NewMemoryStore()
	dispatcher := NewDispatcher(workDir, runtime.NewSupervisor(dispatcherFakeAdapter{}), mem, mem)
	_, err := dispatcher.Execute(context.Background(), "sess_lease", ".", "build feature", []NodeAssignment{
		{
			Node:    domain.TaskNodeSpec{ID: "task_a", Title: "Starter", Strategy: domain.TaskStrategyDirect},
			Profile: domain.AgentProfile{ID: "starter", DisplayName: "Starter", Command: "starter"},
		},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	leaseStore, err := NewLeaseStore(workDir)
	if err != nil {
		t.Fatalf("NewLeaseStore() error = %v", err)
	}
	record, err := leaseStore.Get(context.Background(), "sess_lease")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if record.Status != LeaseStatusReleased {
		t.Fatalf("status = %s, want %s", record.Status, LeaseStatusReleased)
	}
	if len(record.CompletedTaskIDs) != 1 || record.CompletedTaskIDs[0] != "task_a" {
		t.Fatalf("completed = %#v, want [task_a]", record.CompletedTaskIDs)
	}
}

func TestDispatcherReturnsApprovalPendingForRiskyNode(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	mem := store.NewMemoryStore()
	dispatcher := NewDispatcher(workDir, runtime.NewSupervisor(dispatcherFakeAdapter{}), mem, mem)
	_, err := dispatcher.Execute(context.Background(), "sess_gate", workDir, "drop database and rebuild", []NodeAssignment{
		{
			Node:    domain.TaskNodeSpec{ID: "task_a", Title: "Risky", Strategy: domain.TaskStrategyDirect},
			Profile: domain.AgentProfile{ID: "starter", DisplayName: "Starter", Command: "starter"},
		},
	})
	var pending *ApprovalPendingError
	if !errors.As(err, &pending) {
		t.Fatalf("error = %v, want ApprovalPendingError", err)
	}
	task, getErr := mem.GetTask(context.Background(), "sess_gate__task_a")
	if getErr != nil {
		t.Fatalf("GetTask() error = %v", getErr)
	}
	if task.State != domain.TaskStateAwaitingApproval {
		t.Fatalf("state = %s, want %s", task.State, domain.TaskStateAwaitingApproval)
	}
}
