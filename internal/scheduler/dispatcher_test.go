package scheduler

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/liliang-cn/roma/internal/domain"
	"github.com/liliang-cn/roma/internal/runtime"
	"github.com/liliang-cn/roma/internal/store"
	workspacepkg "github.com/liliang-cn/roma/internal/workspace"
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

type dispatcherFailFastAdapter struct{}

func (dispatcherFailFastAdapter) Supports(domain.AgentProfile) bool { return true }

func (dispatcherFailFastAdapter) BuildCommand(ctx context.Context, req runtime.StartRequest) (*exec.Cmd, error) {
	if req.Profile.ID == "fail-fast" {
		return exec.CommandContext(ctx, "python3", "-c", "import sys; print('boom', file=sys.stderr); sys.exit(7)"), nil
	}
	return exec.CommandContext(ctx, "python3", "-c", "import time; time.sleep(10); print('slow ok')"), nil
}

type dispatcherCuriaAdapter struct{}

func (dispatcherCuriaAdapter) Supports(domain.AgentProfile) bool { return true }

func (dispatcherCuriaAdapter) BuildCommand(ctx context.Context, req runtime.StartRequest) (*exec.Cmd, error) {
	script := `
import re, sys
prompt = sys.argv[1]
if "blind review phase" in prompt:
    match = re.search(r"(prop_[A-Za-z0-9_]+)", prompt)
    if match:
        print(match.group(1) + " is the best proposal with strong safety")
    else:
        print("best proposal")
else:
    print("Implement the plan\\ninternal/api/server.go\\nrisk: approval flow\\ntradeoff: more explicit schema")
`
	return exec.CommandContext(ctx, "python3", "-c", script, req.Prompt), nil
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

func TestDispatcherCancelsSiblingNodesAfterFirstFailure(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	initDispatcherGitRepo(t, workDir)

	mem := store.NewMemoryStore()
	dispatcher := NewDispatcher(workDir, runtime.NewSupervisor(dispatcherFailFastAdapter{}), mem, mem)
	started := time.Now()
	_, err := dispatcher.Execute(context.Background(), "sess_failfast", workDir, "parallel batch", []NodeAssignment{
		{
			Node:    domain.TaskNodeSpec{ID: "task_fail", Title: "Fail", Strategy: domain.TaskStrategyDirect},
			Profile: domain.AgentProfile{ID: "fail-fast", DisplayName: "Fail Fast", Command: "python3"},
		},
		{
			Node:    domain.TaskNodeSpec{ID: "task_slow", Title: "Slow", Strategy: domain.TaskStrategyDirect},
			Profile: domain.AgentProfile{ID: "slow", DisplayName: "Slow", Command: "python3"},
		},
	})
	if err == nil {
		t.Fatal("Execute() error = nil, want failure")
	}
	if elapsed := time.Since(started); elapsed >= 4*time.Second {
		t.Fatalf("elapsed = %v, want sibling cancellation before 4s", elapsed)
	}

	failTask, getErr := mem.GetTask(context.Background(), "sess_failfast__task_fail")
	if getErr != nil {
		t.Fatalf("GetTask(task_fail) error = %v", getErr)
	}
	if failTask.State != domain.TaskStateFailedTerminal {
		t.Fatalf("task_fail state = %s, want %s", failTask.State, domain.TaskStateFailedTerminal)
	}

	slowTask, getErr := mem.GetTask(context.Background(), "sess_failfast__task_slow")
	if getErr != nil {
		t.Fatalf("GetTask(task_slow) error = %v", getErr)
	}
	if slowTask.State == domain.TaskStateRunning {
		t.Fatalf("task_slow state = %s, want non-running terminal state", slowTask.State)
	}
}

func TestDispatcherPersistsLease(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	mem := store.NewMemoryStore()
	dispatcher := NewDispatcher(workDir, runtime.NewSupervisor(dispatcherFakeAdapter{}), mem, mem)
	_, err := dispatcher.Execute(context.Background(), "sess_lease", workDir, "build feature", []NodeAssignment{
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
	leaseStore, err := NewLeaseStore(workDir)
	if err != nil {
		t.Fatalf("NewLeaseStore() error = %v", err)
	}
	lease, err := leaseStore.Get(context.Background(), "sess_gate")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if len(lease.PendingApprovalTaskIDs) != 1 || lease.PendingApprovalTaskIDs[0] != "sess_gate__task_a" {
		t.Fatalf("pending approvals = %#v, want [sess_gate__task_a]", lease.PendingApprovalTaskIDs)
	}
	records, err := mem.ListEvents(context.Background(), store.EventFilter{SessionID: "sess_gate", Type: "SchedulerLeaseRecorded"})
	if err != nil {
		t.Fatalf("ListEvents() error = %v", err)
	}
	if len(records) == 0 {
		t.Fatal("missing scheduler lease events")
	}
	last := records[len(records)-1]
	items, ok := last.Payload["pending_approval_task_ids"].([]string)
	if !ok || len(items) != 1 || items[0] != "sess_gate__task_a" {
		t.Fatalf("event pending approvals = %#v, want [sess_gate__task_a]", last.Payload["pending_approval_task_ids"])
	}
}

func TestDispatcherExecutesCuriaNode(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	mem := store.NewMemoryStore()
	dispatcher := NewDispatcher(workDir, runtime.NewSupervisor(dispatcherCuriaAdapter{}), mem, mem)
	result, err := dispatcher.Execute(context.Background(), "sess_curia", workDir, "design a safer API", []NodeAssignment{
		{
			Node: domain.TaskNodeSpec{
				ID:       "task_curia",
				Title:    "Curia Review",
				Strategy: domain.TaskStrategyCuria,
				Quorum:   2,
			},
			Profile: domain.AgentProfile{ID: "codex-cli", DisplayName: "Codex CLI", Command: "codex"},
			CuriaProfiles: []domain.AgentProfile{
				{ID: "codex-cli", DisplayName: "Codex CLI", Command: "codex"},
				{ID: "gemini-cli", DisplayName: "Gemini CLI", Command: "gemini"},
			},
			CuriaQuorum: 2,
		},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	artifact, ok := result.Artifacts["task_curia"]
	if !ok {
		t.Fatal("missing curia primary artifact")
	}
	if artifact.Kind != domain.ArtifactKindExecutionPlan {
		t.Fatalf("kind = %s, want %s", artifact.Kind, domain.ArtifactKindExecutionPlan)
	}
	if len(result.RelatedArtifacts["task_curia"]) < 4 {
		t.Fatalf("related artifact count = %d, want at least 4", len(result.RelatedArtifacts["task_curia"]))
	}
}

func TestDispatcherConcurrentGraphSoakBaseline(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	initDispatcherGitRepo(t, workDir)

	mem := store.NewMemoryStore()
	dispatcher := NewDispatcher(workDir, runtime.NewSupervisor(dispatcherSlowAdapter{}), mem, mem)

	assignments := make([]NodeAssignment, 0, 12)
	for i := 0; i < 12; i++ {
		id := "task_" + strconv.Itoa(i)
		assignments = append(assignments, NodeAssignment{
			Node:    domain.TaskNodeSpec{ID: id, Title: "Node " + id, Strategy: domain.TaskStrategyDirect},
			Profile: domain.AgentProfile{ID: "agent-" + id, DisplayName: "Agent " + id, Command: "agent"},
		})
	}

	result, err := dispatcher.Execute(context.Background(), "sess_soak", workDir, "parallel soak", assignments)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(result.Order) != len(assignments) {
		t.Fatalf("order len = %d, want %d", len(result.Order), len(assignments))
	}

	manager := workspacepkg.NewManager(workDir, mem)
	workspaces, err := manager.List(context.Background())
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	var sessionCount int
	for _, item := range workspaces {
		if item.SessionID != "sess_soak" {
			continue
		}
		sessionCount++
		if item.Status != "released" && item.Status != "merged" {
			t.Fatalf("workspace status = %q, want released/merged", item.Status)
		}
		if item.Provider != "git_worktree" {
			t.Fatalf("workspace provider = %q, want git_worktree", item.Provider)
		}
	}
	if sessionCount != len(assignments) {
		t.Fatalf("workspace count = %d, want %d", sessionCount, len(assignments))
	}

	if err := manager.ReclaimStale(context.Background()); err != nil {
		t.Fatalf("ReclaimStale() error = %v", err)
	}
	for i := 0; i < 12; i++ {
		taskID := "task_" + strconv.Itoa(i)
		item, err := manager.Get(context.Background(), "sess_soak", taskID)
		if err != nil {
			t.Fatalf("Get(%s) error = %v", taskID, err)
		}
		if item.Status != "reclaimed" {
			t.Fatalf("workspace %s status = %q, want reclaimed", taskID, item.Status)
		}
	}
}

func TestDispatcherRepeatedConcurrentSoakMaintainsLeaseAndWorkspaceInvariants(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	initDispatcherGitRepo(t, workDir)

	mem := store.NewMemoryStore()
	dispatcher := NewDispatcher(workDir, runtime.NewSupervisor(dispatcherSlowAdapter{}), mem, mem)
	manager := workspacepkg.NewManager(workDir, mem)

	const (
		rounds      = 4
		nodesPerRun = 6
	)

	for round := 0; round < rounds; round++ {
		sessionID := "sess_repeat_" + strconv.Itoa(round)
		assignments := make([]NodeAssignment, 0, nodesPerRun)
		for i := 0; i < nodesPerRun; i++ {
			id := "task_" + strconv.Itoa(round) + "_" + strconv.Itoa(i)
			assignments = append(assignments, NodeAssignment{
				Node:    domain.TaskNodeSpec{ID: id, Title: "Node " + id, Strategy: domain.TaskStrategyDirect},
				Profile: domain.AgentProfile{ID: "agent-" + id, DisplayName: "Agent " + id, Command: "agent"},
			})
		}

		result, err := dispatcher.Execute(context.Background(), sessionID, workDir, "parallel repeated soak", assignments)
		if err != nil {
			t.Fatalf("Execute(round=%d) error = %v", round, err)
		}
		if len(result.Order) != nodesPerRun {
			t.Fatalf("round %d order len = %d, want %d", round, len(result.Order), nodesPerRun)
		}
	}

	leaseStore, err := NewLeaseStore(workDir)
	if err != nil {
		t.Fatalf("NewLeaseStore() error = %v", err)
	}
	active, err := leaseStore.ListByStatus(context.Background(), LeaseStatusActive)
	if err != nil {
		t.Fatalf("ListByStatus(active) error = %v", err)
	}
	if len(active) != 0 {
		t.Fatalf("active lease count = %d, want 0", len(active))
	}
	released, err := leaseStore.ListByStatus(context.Background(), LeaseStatusReleased)
	if err != nil {
		t.Fatalf("ListByStatus(released) error = %v", err)
	}
	if len(released) != rounds {
		t.Fatalf("released lease count = %d, want %d", len(released), rounds)
	}

	workspaces, err := manager.List(context.Background())
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	var relevant int
	for _, item := range workspaces {
		if !strings.HasPrefix(item.SessionID, "sess_repeat_") {
			continue
		}
		relevant++
		if item.Status != "released" && item.Status != "merged" {
			t.Fatalf("workspace %s/%s status = %q, want released/merged", item.SessionID, item.TaskID, item.Status)
		}
	}
	if relevant != rounds*nodesPerRun {
		t.Fatalf("workspace count = %d, want %d", relevant, rounds*nodesPerRun)
	}

	if err := manager.ReclaimStale(context.Background()); err != nil {
		t.Fatalf("ReclaimStale() error = %v", err)
	}
	for round := 0; round < rounds; round++ {
		sessionID := "sess_repeat_" + strconv.Itoa(round)
		for i := 0; i < nodesPerRun; i++ {
			taskID := "task_" + strconv.Itoa(round) + "_" + strconv.Itoa(i)
			item, err := manager.Get(context.Background(), sessionID, taskID)
			if err != nil {
				t.Fatalf("Get(%s,%s) error = %v", sessionID, taskID, err)
			}
			if item.Status != "reclaimed" {
				t.Fatalf("workspace %s/%s status = %q, want reclaimed", sessionID, taskID, item.Status)
			}
		}
	}
}

func TestDispatcherParallelSessionsSoakMaintainsLeaseAndWorkspaceInvariants(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	initDispatcherGitRepo(t, workDir)

	mem := store.NewMemoryStore()
	dispatcher := NewDispatcher(workDir, runtime.NewSupervisor(dispatcherSlowAdapter{}), mem, mem)
	manager := workspacepkg.NewManager(workDir, mem)

	const (
		sessionCount = 3
		nodesPerRun  = 4
	)

	var wg sync.WaitGroup
	errCh := make(chan error, sessionCount)
	for session := 0; session < sessionCount; session++ {
		wg.Add(1)
		go func(session int) {
			defer wg.Done()
			sessionID := "sess_parallel_" + strconv.Itoa(session)
			assignments := make([]NodeAssignment, 0, nodesPerRun)
			for node := 0; node < nodesPerRun; node++ {
				id := "task_" + strconv.Itoa(session) + "_" + strconv.Itoa(node)
				assignments = append(assignments, NodeAssignment{
					Node:    domain.TaskNodeSpec{ID: id, Title: "Node " + id, Strategy: domain.TaskStrategyDirect},
					Profile: domain.AgentProfile{ID: "agent-" + id, DisplayName: "Agent " + id, Command: "agent"},
				})
			}
			result, err := dispatcher.Execute(context.Background(), sessionID, workDir, "parallel session soak", assignments)
			if err != nil {
				errCh <- err
				return
			}
			if len(result.Order) != nodesPerRun {
				errCh <- fmt.Errorf("session %s order len = %d, want %d", sessionID, len(result.Order), nodesPerRun)
			}
		}(session)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("parallel session soak error = %v", err)
		}
	}

	leaseStore, err := NewLeaseStore(workDir)
	if err != nil {
		t.Fatalf("NewLeaseStore() error = %v", err)
	}
	active, err := leaseStore.ListByStatus(context.Background(), LeaseStatusActive)
	if err != nil {
		t.Fatalf("ListByStatus(active) error = %v", err)
	}
	if len(active) != 0 {
		t.Fatalf("active lease count = %d, want 0", len(active))
	}

	workspaces, err := manager.List(context.Background())
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	var relevant int
	for _, item := range workspaces {
		if !strings.HasPrefix(item.SessionID, "sess_parallel_") {
			continue
		}
		relevant++
		if item.Status != "released" && item.Status != "merged" {
			t.Fatalf("workspace %s/%s status = %q, want released/merged", item.SessionID, item.TaskID, item.Status)
		}
	}
	if relevant != sessionCount*nodesPerRun {
		t.Fatalf("workspace count = %d, want %d", relevant, sessionCount*nodesPerRun)
	}

	if err := manager.ReclaimStale(context.Background()); err != nil {
		t.Fatalf("ReclaimStale() error = %v", err)
	}
	for session := 0; session < sessionCount; session++ {
		sessionID := "sess_parallel_" + strconv.Itoa(session)
		for node := 0; node < nodesPerRun; node++ {
			taskID := "task_" + strconv.Itoa(session) + "_" + strconv.Itoa(node)
			item, err := manager.Get(context.Background(), sessionID, taskID)
			if err != nil {
				t.Fatalf("Get(%s,%s) error = %v", sessionID, taskID, err)
			}
			if item.Status != "reclaimed" {
				t.Fatalf("workspace %s/%s status = %q, want reclaimed", sessionID, taskID, item.Status)
			}
		}
	}
}

func initDispatcherGitRepo(t *testing.T, dir string) {
	t.Helper()
	runDispatcherGit(t, dir, "init")
	runDispatcherGit(t, dir, "config", "user.email", "roma@example.com")
	runDispatcherGit(t, dir, "config", "user.name", "ROMA")
	if err := os.WriteFile(dir+"/README.md", []byte("roma\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	runDispatcherGit(t, dir, "add", "README.md")
	runDispatcherGit(t, dir, "commit", "-m", "init")
}

func runDispatcherGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmdArgs := append([]string{"-C", dir}, args...)
	cmd := exec.Command("git", cmdArgs...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v error = %v (%s)", args, err, output)
	}
}
