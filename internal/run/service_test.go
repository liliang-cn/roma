package run

import (
	"context"
	"strings"
	"testing"

	"github.com/liliang-cn/roma/internal/agents"
	"github.com/liliang-cn/roma/internal/artifacts"
	"github.com/liliang-cn/roma/internal/domain"
	"github.com/liliang-cn/roma/internal/history"
	"github.com/liliang-cn/roma/internal/runtime"
	"github.com/liliang-cn/roma/internal/scheduler"
	"github.com/liliang-cn/roma/internal/taskstore"
)

func TestRunRejectsUnknownAgent(t *testing.T) {
	t.Parallel()

	registry, err := agents.DefaultRegistry()
	if err != nil {
		t.Fatalf("DefaultRegistry() error = %v", err)
	}

	svc := NewService(registry)
	err = svc.Run(context.Background(), Request{
		Prompt:       "test",
		StarterAgent: "missing",
		WorkingDir:   ".",
	})
	if err == nil {
		t.Fatal("Run() error = nil, want error")
	}
}

func TestRunRejectsUnknownDelegate(t *testing.T) {
	t.Parallel()

	registry, err := agents.NewRegistry(domain.AgentProfile{
		ID:           "starter",
		DisplayName:  "Starter",
		Command:      "starter",
		Aliases:      []string{"codex"},
		Availability: domain.AgentAvailabilityAvailable,
	})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	svc := NewService(registry)
	svc.supervisor = runtime.NewSupervisor()
	err = svc.Run(context.Background(), Request{
		Prompt:       "test",
		StarterAgent: "codex",
		WorkingDir:   ".",
		Delegates:    []string{"missing"},
	})
	if err == nil {
		t.Fatal("Run() error = nil, want error")
	}
}

func TestWriteRelayResult(t *testing.T) {
	t.Parallel()

	var buf strings.Builder
	writeRelayResult(&buf, []scheduler.NodeAssignment{
		{
			Node: domain.TaskNodeSpec{ID: "task_1"},
			Profile: domain.AgentProfile{
				ID:          "codex-cli",
				DisplayName: "Codex CLI",
			},
		},
	}, scheduler.DispatchResult{
		Order: []string{"task_1"},
		Artifacts: map[string]domain.ArtifactEnvelope{
			"task_1": {
				ID: "art_1",
				Payload: artifacts.ReportPayload{
					Summary: "starter output",
				},
				Checksum: "sha256:test",
			},
		},
	})
	if !strings.Contains(buf.String(), "starter output") {
		t.Fatal("writeRelayResult() missing output")
	}
	if !strings.Contains(buf.String(), "artifact=art_1") {
		t.Fatal("writeRelayResult() missing artifact line")
	}
}

func TestRunReturnsAwaitingApprovalOnPolicyWarn(t *testing.T) {
	t.Parallel()

	registry, err := agents.NewRegistry(domain.AgentProfile{
		ID:           "codex-cli",
		DisplayName:  "Codex CLI",
		Command:      "codex",
		Aliases:      []string{"codex"},
		Availability: domain.AgentAvailabilityAvailable,
	})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	workDir := t.TempDir()
	svc := NewService(registry)
	result, err := svc.RunWithResult(context.Background(), Request{
		Prompt:       "drop database and then summarize the risk",
		StarterAgent: "codex",
		WorkingDir:   workDir,
	})
	if err != nil {
		t.Fatalf("RunWithResult() error = %v", err)
	}
	if result.Status != "awaiting_approval" {
		t.Fatalf("status = %s, want awaiting_approval", result.Status)
	}

	sessionStore, err := history.NewSQLiteStore(workDir)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	record, err := sessionStore.Get(context.Background(), result.SessionID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if record.Status != "awaiting_approval" {
		t.Fatalf("record status = %s, want awaiting_approval", record.Status)
	}
	if record.FinalArtifactID == "" {
		t.Fatal("final artifact id = empty, want final answer artifact")
	}
	taskStore, err := taskstore.NewSQLiteStore(workDir)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	tasks, err := taskStore.ListTasksBySession(context.Background(), result.SessionID)
	if err != nil {
		t.Fatalf("ListTasksBySession() error = %v", err)
	}
	if len(tasks) != 1 || tasks[0].State != domain.TaskStateAwaitingApproval {
		t.Fatalf("tasks = %#v, want one awaiting approval task", tasks)
	}
	leaseStore, err := scheduler.NewLeaseStore(workDir)
	if err != nil {
		t.Fatalf("NewLeaseStore() error = %v", err)
	}
	lease, err := leaseStore.Get(context.Background(), result.SessionID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if len(lease.PendingApprovalTaskIDs) != 1 || lease.PendingApprovalTaskIDs[0] != tasks[0].ID {
		t.Fatalf("pending approvals = %#v, want [%s]", lease.PendingApprovalTaskIDs, tasks[0].ID)
	}
	artifactStore, err := artifacts.NewSQLiteStore(workDir)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	envelope, err := artifactStore.Get(context.Background(), record.FinalArtifactID)
	if err != nil {
		t.Fatalf("Get(final artifact) error = %v", err)
	}
	if envelope.Kind != domain.ArtifactKindFinalAnswer {
		t.Fatalf("final artifact kind = %s, want %s", envelope.Kind, domain.ArtifactKindFinalAnswer)
	}
}

func TestBuildOrchestratedAssignmentsFanOutAfterStarterBootstrap(t *testing.T) {
	t.Parallel()

	starter := domain.AgentProfile{ID: "starter", DisplayName: "Starter"}
	delegates := []domain.AgentProfile{
		{ID: "gemini", DisplayName: "Gemini"},
		{ID: "copilot", DisplayName: "Copilot"},
	}

	assignments := buildOrchestratedAssignments("task_1", starter, delegates, true, 3)
	if len(assignments) != 4 {
		t.Fatalf("assignment count = %d, want 4", len(assignments))
	}
	if assignments[0].Node.ID != "task_1_starter_bootstrap" {
		t.Fatalf("bootstrap node id = %q, want task_1_starter_bootstrap", assignments[0].Node.ID)
	}
	if assignments[1].Node.ID != "task_1_starter" {
		t.Fatalf("starter worker node id = %q, want task_1_starter", assignments[1].Node.ID)
	}
	if assignments[0].SemanticReviewer.ID != "starter" {
		t.Fatalf("bootstrap reviewer = %q, want starter", assignments[0].SemanticReviewer.ID)
	}
	if assignments[1].SemanticReviewer.ID != "starter" {
		t.Fatalf("starter reviewer = %q, want starter", assignments[1].SemanticReviewer.ID)
	}
	if got := assignments[1].Node.Dependencies; len(got) != 1 || got[0] != "task_1_starter_bootstrap" {
		t.Fatalf("starter worker dependencies = %#v, want [task_1_starter_bootstrap]", got)
	}
	for _, assignment := range assignments[2:] {
		if got := assignment.Node.Dependencies; len(got) != 1 || got[0] != "task_1_starter_bootstrap" {
			t.Fatalf("delegate %s dependencies = %#v, want [task_1_starter_bootstrap]", assignment.Node.ID, got)
		}
		if assignment.SemanticReviewer.ID != "starter" {
			t.Fatalf("delegate %s reviewer = %q, want starter", assignment.Node.ID, assignment.SemanticReviewer.ID)
		}
	}
	if !strings.Contains(assignments[0].PromptHint, "YOLO") && !strings.Contains(assignments[0].PromptHint, "auto-run") {
		t.Fatalf("bootstrap prompt hint = %q, want automation guidance", assignments[0].PromptHint)
	}
}

func TestMaybePromoteOrchestratedToCuriaForProtectedScope(t *testing.T) {
	t.Parallel()

	registry, err := agents.NewRegistry(
		domain.AgentProfile{ID: "my-codex", DisplayName: "My Codex", Command: "sh", HealthcheckArgs: []string{"-c", "exit 0"}, Availability: domain.AgentAvailabilityAvailable},
		domain.AgentProfile{ID: "my-gemini", DisplayName: "My Gemini", Command: "sh", HealthcheckArgs: []string{"-c", "exit 0"}, Availability: domain.AgentAvailabilityAvailable},
		domain.AgentProfile{ID: "my-copilot", DisplayName: "My Copilot", Command: "sh", HealthcheckArgs: []string{"-c", "exit 0"}, Availability: domain.AgentAvailabilityAvailable},
		domain.AgentProfile{ID: "my-claude", DisplayName: "My Claude", Command: "sh", HealthcheckArgs: []string{"-c", "exit 0"}, Availability: domain.AgentAvailabilityAvailable},
	)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	svc := NewService(registry)
	assignments, reasons := svc.maybePromoteOrchestratedToCuria(
		context.Background(),
		"Refactor auth and billing flows with a breaking change",
		t.TempDir(),
		"task_1",
		domain.AgentProfile{ID: "my-codex", DisplayName: "My Codex", Command: "sh", Availability: domain.AgentAvailabilityAvailable},
		[]domain.AgentProfile{
			{ID: "my-gemini", DisplayName: "My Gemini", Command: "sh", Availability: domain.AgentAvailabilityAvailable},
			{ID: "my-copilot", DisplayName: "My Copilot", Command: "sh", Availability: domain.AgentAvailabilityAvailable},
		},
		true,
		4,
	)
	if len(assignments) != 1 {
		t.Fatalf("assignment count = %d, want 1", len(assignments))
	}
	if assignments[0].Node.Strategy != domain.TaskStrategyCuria {
		t.Fatalf("strategy = %s, want curia", assignments[0].Node.Strategy)
	}
	if len(assignments[0].CuriaProfiles) != 3 {
		t.Fatalf("curia profiles = %d, want 3", len(assignments[0].CuriaProfiles))
	}
	if assignments[0].CuriaArbitrationMode != "augustus" {
		t.Fatalf("arbitration mode = %q, want augustus", assignments[0].CuriaArbitrationMode)
	}
	if len(reasons) == 0 {
		t.Fatal("reasons = empty, want auto-curia reasons")
	}
}

func TestMaybePromoteGraphAssignmentsToCuria(t *testing.T) {
	t.Parallel()

	registry, err := agents.NewRegistry(
		domain.AgentProfile{ID: "my-codex", DisplayName: "My Codex", Command: "sh", HealthcheckArgs: []string{"-c", "exit 0"}, Availability: domain.AgentAvailabilityAvailable},
		domain.AgentProfile{ID: "my-gemini", DisplayName: "My Gemini", Command: "sh", HealthcheckArgs: []string{"-c", "exit 0"}, Availability: domain.AgentAvailabilityAvailable},
		domain.AgentProfile{ID: "my-copilot", DisplayName: "My Copilot", Command: "sh", HealthcheckArgs: []string{"-c", "exit 0"}, Availability: domain.AgentAvailabilityAvailable},
		domain.AgentProfile{ID: "my-claude", DisplayName: "My Claude", Command: "sh", HealthcheckArgs: []string{"-c", "exit 0"}, Availability: domain.AgentAvailabilityAvailable},
	)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	svc := NewService(registry)
	assignments, reasons := svc.maybePromoteGraphAssignmentsToCuria(context.Background(), "Apply a database migration for auth", t.TempDir(), []scheduler.NodeAssignment{{
		Node: domain.TaskNodeSpec{
			ID:       "node_1",
			Title:    "Auth migration",
			Strategy: domain.TaskStrategyDirect,
		},
		Profile: domain.AgentProfile{ID: "my-codex", DisplayName: "My Codex", Command: "sh", Availability: domain.AgentAvailabilityAvailable},
	}})
	if len(assignments) != 1 {
		t.Fatalf("assignment count = %d, want 1", len(assignments))
	}
	if assignments[0].Node.Strategy != domain.TaskStrategyCuria {
		t.Fatalf("strategy = %s, want curia", assignments[0].Node.Strategy)
	}
	if assignments[0].CuriaQuorum != 2 {
		t.Fatalf("curia quorum = %d, want 2", assignments[0].CuriaQuorum)
	}
	if !strings.Contains(assignments[0].Node.Title, "[auto-curia]") {
		t.Fatalf("title = %q, want [auto-curia] suffix", assignments[0].Node.Title)
	}
	if len(reasons) != 1 {
		t.Fatalf("reasons = %#v, want one promotion reason", reasons)
	}
}
