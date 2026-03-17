package run

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liliang-cn/roma/internal/agents"
	"github.com/liliang-cn/roma/internal/artifacts"
	"github.com/liliang-cn/roma/internal/domain"
	"github.com/liliang-cn/roma/internal/history"
	"github.com/liliang-cn/roma/internal/runtime"
	"github.com/liliang-cn/roma/internal/scheduler"
	"github.com/liliang-cn/roma/internal/taskstore"
	workspacepkg "github.com/liliang-cn/roma/internal/workspace"
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

func TestRunWithDelegatesPropagatesExecutionFailure(t *testing.T) {
	t.Parallel()

	registry, err := agents.NewRegistry(
		domain.AgentProfile{
			ID:           "claude",
			DisplayName:  "Claude",
			Command:      "sh",
			Args:         []string{"-c", "printf 'starter ok\\n'"},
			Availability: domain.AgentAvailabilityAvailable,
		},
		domain.AgentProfile{
			ID:           "codex",
			DisplayName:  "Codex",
			Command:      "sh",
			Args:         []string{"-c", "printf 'codex failed\\n' >&2; exit 7"},
			Availability: domain.AgentAvailabilityAvailable,
		},
	)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	workDir := t.TempDir()
	svc := NewService(registry)
	result, err := svc.RunWithResult(context.Background(), Request{
		Prompt:       "refactor the CLI",
		StarterAgent: "claude",
		WorkingDir:   workDir,
		Delegates:    []string{"codex"},
	})
	if err == nil {
		t.Fatal("RunWithResult() error = nil, want delegate failure")
	}
	if result.Status != "failed" {
		t.Fatalf("status = %s, want failed", result.Status)
	}

	sessionStore, err := history.NewSQLiteStore(workDir)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	record, err := sessionStore.Get(context.Background(), result.SessionID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if record.Status != "failed" {
		t.Fatalf("record status = %s, want failed", record.Status)
	}
	if record.FinalArtifactID == "" {
		t.Fatal("final artifact id = empty, want failure final answer artifact")
	}
}

func TestBuildOrchestratedAssignmentsFanOutAfterStarterBootstrap(t *testing.T) {
	t.Parallel()

	starter := domain.AgentProfile{ID: "starter", DisplayName: "Starter"}
	delegates := []domain.AgentProfile{
		{ID: "gemini", DisplayName: "Gemini"},
		{ID: "copilot", DisplayName: "Copilot"},
	}

	assignments := buildOrchestratedAssignments("task_1", starter, delegates, true, 3, nil)
	// clarify + bootstrap + 2 delegates = 4
	if len(assignments) != 4 {
		t.Fatalf("assignment count = %d, want 4", len(assignments))
	}

	clarify := assignments[0]
	bootstrap := assignments[1]

	if clarify.Node.ID != "task_1_starter_clarify" {
		t.Fatalf("clarify node id = %q, want task_1_starter_clarify", clarify.Node.ID)
	}
	if bootstrap.Node.ID != "task_1_starter_bootstrap" {
		t.Fatalf("bootstrap node id = %q, want task_1_starter_bootstrap", bootstrap.Node.ID)
	}
	if bootstrap.Node.Title != "Starter Caesar coordination" {
		t.Fatalf("bootstrap title = %q, want Starter Caesar coordination", bootstrap.Node.Title)
	}
	if bootstrap.SemanticReviewer.ID != "starter" {
		t.Fatalf("bootstrap reviewer = %q, want starter", bootstrap.SemanticReviewer.ID)
	}
	// bootstrap depends on clarify
	if got := bootstrap.Node.Dependencies; len(got) != 1 || got[0] != "task_1_starter_clarify" {
		t.Fatalf("bootstrap dependencies = %#v, want [task_1_starter_clarify]", got)
	}
	// delegates depend on bootstrap
	for _, assignment := range assignments[2:] {
		if got := assignment.Node.Dependencies; len(got) != 1 || got[0] != "task_1_starter_bootstrap" {
			t.Fatalf("delegate %s dependencies = %#v, want [task_1_starter_bootstrap]", assignment.Node.ID, got)
		}
		if assignment.SemanticReviewer.ID != "starter" {
			t.Fatalf("delegate %s reviewer = %q, want starter", assignment.Node.ID, assignment.SemanticReviewer.ID)
		}
	}
	if strings.Contains(strings.ToLower(bootstrap.PromptHint), "inspect the delegate agents") {
		t.Fatalf("bootstrap prompt hint = %q, want no runtime delegate inspection directive", bootstrap.PromptHint)
	}
	if !strings.Contains(bootstrap.PromptHint, "Known delegate profiles") {
		t.Fatalf("bootstrap prompt hint = %q, want embedded delegate summary", bootstrap.PromptHint)
	}
	if !strings.Contains(bootstrap.PromptHint, "You do not implement the task yourself.") {
		t.Fatalf("bootstrap prompt hint = %q, want Caesar-only coordination directive", bootstrap.PromptHint)
	}
	for _, assignment := range assignments[2:] {
		if strings.Contains(strings.ToLower(assignment.PromptHint), "active contributor") {
			t.Fatalf("delegate prompt hint = %q, want no starter worker language", assignment.PromptHint)
		}
	}
}

func TestBuildOrchestratedAssignmentsIncludesClarifyNode(t *testing.T) {
	t.Parallel()

	starter := domain.AgentProfile{ID: "starter", DisplayName: "Starter"}
	delegates := []domain.AgentProfile{
		{ID: "agent-a", DisplayName: "Agent A"},
	}

	assignments := buildOrchestratedAssignments("task_x", starter, delegates, false, 1, nil)
	if len(assignments) != 3 {
		t.Fatalf("assignment count = %d, want 3 (clarify + bootstrap + 1 delegate)", len(assignments))
	}

	clarify := assignments[0]
	bootstrap := assignments[1]
	delegate := assignments[2]

	if clarify.Node.ID != "task_x_starter_clarify" {
		t.Fatalf("clarify node id = %q, want task_x_starter_clarify", clarify.Node.ID)
	}
	if clarify.Node.Title != "Starter prompt clarification" {
		t.Fatalf("clarify node title = %q, want Starter prompt clarification", clarify.Node.Title)
	}
	if len(clarify.Node.Dependencies) != 0 {
		t.Fatalf("clarify dependencies = %#v, want none", clarify.Node.Dependencies)
	}
	if clarify.Profile.ID != "starter" {
		t.Fatalf("clarify profile = %q, want starter", clarify.Profile.ID)
	}

	// bootstrap depends on clarify
	if got := bootstrap.Node.Dependencies; len(got) != 1 || got[0] != "task_x_starter_clarify" {
		t.Fatalf("bootstrap dependencies = %#v, want [task_x_starter_clarify]", got)
	}

	// delegate depends on bootstrap
	if got := delegate.Node.Dependencies; len(got) != 1 || got[0] != "task_x_starter_bootstrap" {
		t.Fatalf("delegate dependencies = %#v, want [task_x_starter_bootstrap]", got)
	}
}

func TestBuildStarterClarifyPromptHintMentionsDelegates(t *testing.T) {
	t.Parallel()

	starter := domain.AgentProfile{ID: "starter", DisplayName: "My Starter"}
	delegates := []domain.AgentProfile{
		{ID: "agent-1", DisplayName: "Agent One", Capabilities: []string{"go", "python"}},
		{ID: "agent-2", DisplayName: "Agent Two", Capabilities: []string{"frontend"}},
	}

	hint := buildStarterClarifyPromptHint(starter, delegates, nil)

	if !strings.Contains(hint, "Agent One") {
		t.Fatalf("prompt hint missing delegate name Agent One: %q", hint)
	}
	if !strings.Contains(hint, "Agent Two") {
		t.Fatalf("prompt hint missing delegate name Agent Two: %q", hint)
	}
	if !strings.Contains(hint, "structured task specification") {
		t.Fatalf("prompt hint missing core instruction: %q", hint)
	}
	if !strings.Contains(hint, "Do not implement the task yourself") {
		t.Fatalf("prompt hint missing no-implementation directive: %q", hint)
	}
	if strings.Contains(hint, "bootstrap") {
		t.Fatalf("clarify prompt hint should not reference bootstrap: %q", hint)
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

func TestMaybePromoteOrchestratedToCuriaIgnoresAvoidanceConstraints(t *testing.T) {
	t.Parallel()

	registry, err := agents.NewRegistry(
		domain.AgentProfile{ID: "my-codex", DisplayName: "My Codex", Command: "sh", HealthcheckArgs: []string{"-c", "exit 0"}, Availability: domain.AgentAvailabilityAvailable},
		domain.AgentProfile{ID: "my-gemini", DisplayName: "My Gemini", Command: "sh", HealthcheckArgs: []string{"-c", "exit 0"}, Availability: domain.AgentAvailabilityAvailable},
		domain.AgentProfile{ID: "my-claude", DisplayName: "My Claude", Command: "sh", HealthcheckArgs: []string{"-c", "exit 0"}, Availability: domain.AgentAvailabilityAvailable},
	)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	svc := NewService(registry)
	assignments, reasons := svc.maybePromoteOrchestratedToCuria(
		context.Background(),
		"Build a TODO app. Do not touch auth, billing, or migrations. Avoid .github/ paths.",
		t.TempDir(),
		"task_1",
		domain.AgentProfile{ID: "my-codex", DisplayName: "My Codex", Command: "sh", Availability: domain.AgentAvailabilityAvailable},
		[]domain.AgentProfile{
			{ID: "my-gemini", DisplayName: "My Gemini", Command: "sh", Availability: domain.AgentAvailabilityAvailable},
			{ID: "my-claude", DisplayName: "My Claude", Command: "sh", Availability: domain.AgentAvailabilityAvailable},
		},
		true,
		4,
	)
	if len(assignments) != 0 {
		t.Fatalf("assignment count = %d, want 0", len(assignments))
	}
	if len(reasons) != 0 {
		t.Fatalf("reasons = %#v, want none", reasons)
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

func TestRunDirectAutoMergeBackRequest(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	initRunGitRepo(t, workDir)
	registry, err := agents.NewRegistry(domain.AgentProfile{
		ID:          "auto-merge",
		DisplayName: "Auto Merge",
		Command:     "sh",
		Args: []string{
			"-c",
			"mkdir -p examples/todo-webapp && printf 'auto merge\\n' > examples/todo-webapp/auto-merge.txt && printf 'ROMA_MERGE_BACK: direct_merge | ready to merge\\nROMA_MERGE_FILE: examples/todo-webapp/auto-merge.txt\\n'",
		},
		Availability: domain.AgentAvailabilityAvailable,
	})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	svc := NewService(registry)
	result, err := svc.RunWithResult(context.Background(), Request{
		Prompt:       "auto merge probe",
		StarterAgent: "auto-merge",
		WorkingDir:   workDir,
	})
	if err != nil {
		t.Fatalf("RunWithResult() error = %v", err)
	}
	if result.Status != "succeeded" {
		t.Fatalf("status = %s, want succeeded", result.Status)
	}

	manager := workspacepkg.NewManager(workDir, nil)
	prepared, err := manager.Get(context.Background(), result.SessionID, result.TaskID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if prepared.Status != "merged" {
		t.Fatalf("workspace status = %q, want merged", prepared.Status)
	}
	content, err := os.ReadFile(filepath.Join(workDir, "examples", "todo-webapp", "auto-merge.txt"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if strings.TrimSpace(string(content)) != "auto merge" {
		t.Fatalf("content = %q, want auto merge", strings.TrimSpace(string(content)))
	}
}

func TestRunDirectAutoMergeBackRequestUsesControlRootWorkspaceMetadata(t *testing.T) {
	workDir := t.TempDir()
	controlDir := t.TempDir()
	t.Setenv("ROMA_HOME", controlDir)
	initRunGitRepo(t, workDir)
	registry, err := agents.NewRegistry(domain.AgentProfile{
		ID:          "auto-merge",
		DisplayName: "Auto Merge",
		Command:     "sh",
		Args: []string{
			"-c",
			"printf 'control root merge\\n' > control-root-merge.txt && printf 'ROMA_MERGE_BACK: direct_merge | ready to merge\\nROMA_MERGE_FILE: control-root-merge.txt\\n'",
		},
		Availability: domain.AgentAvailabilityAvailable,
	})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	svc := NewService(registry)
	svc.SetControlDir(controlDir)
	result, err := svc.RunWithResult(context.Background(), Request{
		Prompt:       "auto merge probe",
		StarterAgent: "auto-merge",
		WorkingDir:   workDir,
	})
	if err != nil {
		t.Fatalf("RunWithResult() error = %v", err)
	}
	if result.Status != "succeeded" {
		t.Fatalf("status = %s, want succeeded", result.Status)
	}

	content, err := os.ReadFile(filepath.Join(workDir, "control-root-merge.txt"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if strings.TrimSpace(string(content)) != "control root merge" {
		t.Fatalf("content = %q, want control root merge", strings.TrimSpace(string(content)))
	}
}

func TestRunDirectMergeBackRequestRequireVoteDoesNotAutoMerge(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	initRunGitRepo(t, workDir)
	registry, err := agents.NewRegistry(domain.AgentProfile{
		ID:          "vote-merge",
		DisplayName: "Vote Merge",
		Command:     "sh",
		Args: []string{
			"-c",
			"mkdir -p examples/todo-webapp && printf 'vote merge\\n' > examples/todo-webapp/vote-merge.txt && printf 'ROMA_MERGE_BACK: require_vote | let Curia decide\\nROMA_MERGE_FILE: examples/todo-webapp/vote-merge.txt\\n'",
		},
		Availability: domain.AgentAvailabilityAvailable,
	})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	svc := NewService(registry)
	result, err := svc.RunWithResult(context.Background(), Request{
		Prompt:       "vote merge probe",
		StarterAgent: "vote-merge",
		WorkingDir:   workDir,
	})
	if err != nil {
		t.Fatalf("RunWithResult() error = %v", err)
	}
	if result.Status != "succeeded" {
		t.Fatalf("status = %s, want succeeded", result.Status)
	}

	manager := workspacepkg.NewManager(workDir, nil)
	prepared, err := manager.Get(context.Background(), result.SessionID, result.TaskID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if prepared.Status != "released" {
		t.Fatalf("workspace status = %q, want released", prepared.Status)
	}
	if _, err := os.Stat(filepath.Join(workDir, "examples", "todo-webapp", "vote-merge.txt")); !os.IsNotExist(err) {
		t.Fatalf("expected base file absent before ROMA merge, stat err = %v", err)
	}
}

func TestRunOrchestratedCaesarCoordinatesFollowUpsAndAutoMerges(t *testing.T) {
	workDir := t.TempDir()
	controlDir := t.TempDir()
	t.Setenv("ROMA_HOME", controlDir)
	initRunGitRepo(t, workDir)

	starterScript := strings.Join([]string{
		`prompt="$1"`,
		`if printf '%s' "$prompt" | grep -Eq "Starter Caesar coordination|Caesar review round"; then`,
		`  if printf '%s' "$prompt" | grep -q "Upstream artifact summaries:"; then`,
		`    if printf '%s' "$prompt" | grep -q "second pass complete"; then`,
		`      printf 'ROMA_DONE: all work is complete\n'`,
		`    else`,
		`      target=$(printf '%s\n' "$prompt" | sed -n 's/^- \([^:]*delegate_1\):.*/\1/p' | head -n1)`,
		`      if [ -z "$target" ]; then target=worker; fi`,
		`      printf 'ROMA_FOLLOWUP: delegate %s | second pass\n' "$target"`,
		`    fi`,
		`  else`,
		`    printf 'bootstrap ready\n'`,
		`  fi`,
		`else`,
		`  printf 'unexpected Caesar prompt\n'`,
		`fi`,
	}, "\n")
	workerScript := strings.Join([]string{
		`prompt="$1"`,
		`if printf '%s' "$prompt" | grep -q "second pass"; then`,
		`  printf 'second\n' > second.txt`,
		`  printf 'second pass complete\nROMA_MERGE_BACK: direct_merge | second pass ready\nROMA_MERGE_FILE: second.txt\n'`,
		`else`,
		`  printf 'first\n' > first.txt`,
		`  printf 'first pass complete\nROMA_MERGE_BACK: direct_merge | first pass ready\nROMA_MERGE_FILE: first.txt\n'`,
		`fi`,
	}, "\n")

	registry, err := agents.NewRegistry(
		domain.AgentProfile{
			ID:           "caesar",
			DisplayName:  "Caesar",
			Command:      "sh",
			Args:         []string{"-c", starterScript, "starter", "{prompt}"},
			Availability: domain.AgentAvailabilityAvailable,
		},
		domain.AgentProfile{
			ID:           "worker",
			DisplayName:  "Worker",
			Command:      "sh",
			Args:         []string{"-c", workerScript, "worker", "{prompt}"},
			Availability: domain.AgentAvailabilityAvailable,
		},
	)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	svc := NewService(registry)
	svc.SetControlDir(controlDir)
	result, err := svc.RunWithResult(context.Background(), Request{
		Prompt:       "coordinate a low-risk sample file update",
		StarterAgent: "caesar",
		Delegates:    []string{"worker"},
		WorkingDir:   workDir,
	})
	if err != nil {
		t.Fatalf("RunWithResult() error = %v", err)
	}
	if result.Status != "succeeded" {
		t.Fatalf("status = %s, want succeeded", result.Status)
	}

	firstContent, err := os.ReadFile(filepath.Join(workDir, "first.txt"))
	if err != nil {
		t.Fatalf("ReadFile(first.txt) error = %v", err)
	}
	if strings.TrimSpace(string(firstContent)) != "first" {
		t.Fatalf("first.txt = %q, want first", strings.TrimSpace(string(firstContent)))
	}
	secondContent, err := os.ReadFile(filepath.Join(workDir, "second.txt"))
	if err != nil {
		t.Fatalf("ReadFile(second.txt) error = %v", err)
	}
	if strings.TrimSpace(string(secondContent)) != "second" {
		t.Fatalf("second.txt = %q, want second", strings.TrimSpace(string(secondContent)))
	}

	taskStore, err := taskstore.NewSQLiteStore(workDir)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	tasks, err := taskStore.ListTasksBySession(context.Background(), result.SessionID)
	if err != nil {
		t.Fatalf("ListTasksBySession() error = %v", err)
	}
	for _, task := range tasks {
		if task.Title == "Starter contribution" {
			t.Fatalf("unexpected starter worker task present: %#v", task)
		}
	}
}

func initRunGitRepo(t *testing.T, dir string) {
	t.Helper()
	runGitCommand(t, dir, "init")
	runGitCommand(t, dir, "config", "user.email", "roma@example.com")
	runGitCommand(t, dir, "config", "user.name", "ROMA")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("roma\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	runGitCommand(t, dir, "add", "README.md")
	runGitCommand(t, dir, "commit", "-m", "init")
}

func runGitCommand(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s error = %v (%s)", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
}
