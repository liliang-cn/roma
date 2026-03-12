package plans

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liliang-cn/roma/internal/artifacts"
	"github.com/liliang-cn/roma/internal/domain"
	"github.com/liliang-cn/roma/internal/events"
	"github.com/liliang-cn/roma/internal/store"
	workspacepkg "github.com/liliang-cn/roma/internal/workspace"
)

func TestServiceApplyAndRollback(t *testing.T) {
	root := t.TempDir()
	initGitRepo(t, root)

	eventStore := store.NewMemoryStore()
	manager := workspacepkg.NewManager(root, eventStore)
	prepared, err := manager.Prepare(context.Background(), "sess_plan", "task_plan", root, domain.TaskStrategyDirect)
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(prepared.EffectiveDir, "README.md"), []byte("roma changed\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	artifactStore := artifacts.NewFileStore(root)
	artifactSvc := artifacts.NewService()
	envelope, err := artifactSvc.BuildExecutionPlan(context.Background(), artifacts.BuildExecutionPlanRequest{
		SessionID: "sess_plan",
		TaskID:    "task_plan",
		RunID:     "task_plan",
		Goal:      "Apply README change",
		Proposal: artifacts.ProposalPayload{
			ProposalID:     "prop_task_plan",
			Summary:        "Change README",
			EstimatedSteps: []string{"Edit README"},
			AffectedFiles:  []string{"README.md"},
		},
		HumanApprovalRequired: false,
	})
	if err != nil {
		t.Fatalf("BuildExecutionPlan() error = %v", err)
	}
	payload, ok := artifacts.ExecutionPlanFromEnvelope(envelope)
	if !ok {
		t.Fatal("ExecutionPlanFromEnvelope() = false")
	}
	payload.RequiredChecks = nil
	envelope.Payload = payload
	if err := artifactStore.Save(context.Background(), envelope); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	svc := NewService(artifactStore, manager, eventStore)
	dryRun, err := svc.Apply(context.Background(), "sess_plan", "task_plan", envelope.ID, ApplyOptions{DryRun: true})
	if err != nil {
		t.Fatalf("Apply(dry-run) error = %v", err)
	}
	if !dryRun.DryRun || dryRun.Applied {
		t.Fatalf("dryRun result = %#v, want dry-run only", dryRun)
	}
	if len(dryRun.ChangedPaths) != 1 || dryRun.ChangedPaths[0] != "README.md" {
		t.Fatalf("changed paths = %#v, want [README.md]", dryRun.ChangedPaths)
	}

	applied, err := svc.Apply(context.Background(), "sess_plan", "task_plan", envelope.ID, ApplyOptions{})
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if !applied.Applied {
		t.Fatalf("applied result = %#v, want applied=true", applied)
	}
	content, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if strings.TrimSpace(string(content)) != "roma changed" {
		t.Fatalf("base README = %q, want roma changed", strings.TrimSpace(string(content)))
	}

	rolledBack, err := svc.Rollback(context.Background(), "sess_plan", "task_plan", envelope.ID)
	if err != nil {
		t.Fatalf("Rollback() error = %v", err)
	}
	if !rolledBack.RolledBack {
		t.Fatalf("rollback result = %#v, want rolled_back=true", rolledBack)
	}
	content, err = os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if strings.TrimSpace(string(content)) != "roma" {
		t.Fatalf("base README after rollback = %q, want roma", strings.TrimSpace(string(content)))
	}

	items, err := eventStore.ListEvents(context.Background(), store.EventFilter{SessionID: "sess_plan"})
	if err != nil {
		t.Fatalf("ListEvents() error = %v", err)
	}
	var applyEvents, rollbackEvents int
	for _, item := range items {
		switch item.Type {
		case events.TypePlanApplied:
			applyEvents++
		case events.TypePlanRolledBack:
			rollbackEvents++
		}
	}
	if applyEvents != 2 {
		t.Fatalf("plan apply event count = %d, want 2", applyEvents)
	}
	if rollbackEvents != 1 {
		t.Fatalf("plan rollback event count = %d, want 1", rollbackEvents)
	}
}

func TestServiceApplyDryRunReportsMergeConflictPreview(t *testing.T) {
	root := t.TempDir()
	initGitRepo(t, root)

	eventStore := store.NewMemoryStore()
	manager := workspacepkg.NewManager(root, eventStore)
	prepared, err := manager.Prepare(context.Background(), "sess_conflict", "task_conflict", root, domain.TaskStrategyDirect)
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(prepared.EffectiveDir, "README.md"), []byte("worktree version\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(worktree) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("base version\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(base) error = %v", err)
	}

	artifactStore := artifacts.NewFileStore(root)
	artifactSvc := artifacts.NewService()
	envelope, err := artifactSvc.BuildExecutionPlan(context.Background(), artifacts.BuildExecutionPlanRequest{
		SessionID: "sess_conflict",
		TaskID:    "task_conflict",
		RunID:     "task_conflict",
		Goal:      "Apply conflicting README change",
		Proposal: artifacts.ProposalPayload{
			ProposalID:     "prop_task_conflict",
			Summary:        "Change README",
			EstimatedSteps: []string{"Edit README"},
			AffectedFiles:  []string{"README.md"},
		},
		HumanApprovalRequired: false,
	})
	if err != nil {
		t.Fatalf("BuildExecutionPlan() error = %v", err)
	}
	payload, ok := artifacts.ExecutionPlanFromEnvelope(envelope)
	if !ok {
		t.Fatal("ExecutionPlanFromEnvelope() = false")
	}
	payload.RequiredChecks = nil
	envelope.Payload = payload
	if err := artifactStore.Save(context.Background(), envelope); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	svc := NewService(artifactStore, manager, eventStore)
	result, err := svc.Apply(context.Background(), "sess_conflict", "task_conflict", envelope.ID, ApplyOptions{DryRun: true})
	if err != nil {
		t.Fatalf("Apply(dry-run conflict) error = %v", err)
	}
	if !result.DryRun {
		t.Fatalf("result = %#v, want dry run", result)
	}
	if !result.Conflict {
		t.Fatalf("result = %#v, want conflict preview", result)
	}
	if result.ConflictDetail == "" {
		t.Fatalf("result = %#v, want conflict detail", result)
	}
	if result.ConflictKind == "" {
		t.Fatalf("result = %#v, want conflict kind", result)
	}
	if len(result.ConflictPaths) != 1 || result.ConflictPaths[0] != "README.md" {
		t.Fatalf("result = %#v, want README conflict path", result)
	}
	if len(result.ConflictContext) != 1 || result.ConflictContext[0].Path != "README.md" {
		t.Fatalf("result = %#v, want README conflict context", result)
	}
	if !strings.Contains(result.ConflictContext[0].Snippet, "diff --git") {
		t.Fatalf("conflict snippet = %q, want unified diff context", result.ConflictContext[0].Snippet)
	}
	if result.RemediationHint == "" {
		t.Fatalf("result = %#v, want remediation hint", result)
	}
	if result.ConflictSummary == "" {
		t.Fatalf("result = %#v, want conflict summary", result)
	}
	if len(result.ResolutionOptions) == 0 {
		t.Fatalf("result = %#v, want resolution options", result)
	}
	if len(result.ResolutionSteps) == 0 {
		t.Fatalf("result = %#v, want structured resolution steps", result)
	}
}

func TestServicePreviewDoesNotAppendPlanAppliedEvent(t *testing.T) {
	root := t.TempDir()
	initGitRepo(t, root)

	eventStore := store.NewMemoryStore()
	manager := workspacepkg.NewManager(root, eventStore)
	prepared, err := manager.Prepare(context.Background(), "sess_preview", "task_preview", root, domain.TaskStrategyDirect)
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(prepared.EffectiveDir, "README.md"), []byte("preview change\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	artifactStore := artifacts.NewFileStore(root)
	artifactSvc := artifacts.NewService()
	envelope, err := artifactSvc.BuildExecutionPlan(context.Background(), artifacts.BuildExecutionPlanRequest{
		SessionID: "sess_preview",
		TaskID:    "task_preview",
		RunID:     "task_preview",
		Goal:      "Preview README change",
		Proposal: artifacts.ProposalPayload{
			ProposalID:     "prop_task_preview",
			Summary:        "Change README",
			EstimatedSteps: []string{"Edit README"},
			AffectedFiles:  []string{"README.md"},
		},
		HumanApprovalRequired: false,
	})
	if err != nil {
		t.Fatalf("BuildExecutionPlan() error = %v", err)
	}
	payload, ok := artifacts.ExecutionPlanFromEnvelope(envelope)
	if !ok {
		t.Fatal("ExecutionPlanFromEnvelope() = false")
	}
	payload.RequiredChecks = nil
	envelope.Payload = payload
	if err := artifactStore.Save(context.Background(), envelope); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	svc := NewService(artifactStore, manager, eventStore)
	result, err := svc.Preview(context.Background(), "sess_preview", "task_preview", envelope.ID)
	if err != nil {
		t.Fatalf("Preview() error = %v", err)
	}
	if !result.DryRun || result.Applied {
		t.Fatalf("result = %#v, want dry-run preview only", result)
	}

	items, err := eventStore.ListEvents(context.Background(), store.EventFilter{SessionID: "sess_preview"})
	if err != nil {
		t.Fatalf("ListEvents() error = %v", err)
	}
	for _, item := range items {
		if item.Type == events.TypePlanApplied {
			t.Fatalf("Preview() appended unexpected PlanApplied event: %#v", item)
		}
	}
}

func TestServiceInboxSummarizesLatestPlanState(t *testing.T) {
	root := t.TempDir()
	initGitRepo(t, root)

	eventStore := store.NewMemoryStore()
	manager := workspacepkg.NewManager(root, eventStore)
	artifactStore := artifacts.NewFileStore(root)
	artifactSvc := artifacts.NewService()
	envelope, err := artifactSvc.BuildExecutionPlan(context.Background(), artifacts.BuildExecutionPlanRequest{
		SessionID: "sess_inbox",
		TaskID:    "task_inbox",
		RunID:     "task_inbox",
		Goal:      "Inbox goal",
		Proposal: artifacts.ProposalPayload{
			ProposalID:     "prop_task_inbox",
			Summary:        "Change README",
			EstimatedSteps: []string{"Edit README"},
			AffectedFiles:  []string{"README.md"},
		},
		HumanApprovalRequired: true,
	})
	if err != nil {
		t.Fatalf("BuildExecutionPlan() error = %v", err)
	}
	if err := artifactStore.Save(context.Background(), envelope); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := eventStore.AppendEvent(context.Background(), events.Record{
		ID:         "evt_inbox_1",
		SessionID:  "sess_inbox",
		TaskID:     "task_inbox",
		Type:       events.TypePlanApplyRejected,
		ActorType:  events.ActorTypeHuman,
		OccurredAt: time.Now().UTC(),
		ReasonCode: string(ErrorKindApprovalRequired),
		Payload: map[string]any{
			"artifact_id": envelope.ID,
		},
	}); err != nil {
		t.Fatalf("AppendEvent() error = %v", err)
	}

	svc := NewService(artifactStore, manager, eventStore)
	items, err := svc.Inbox(context.Background(), "sess_inbox")
	if err != nil {
		t.Fatalf("Inbox() error = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("inbox count = %d, want 1", len(items))
	}
	if items[0].Status != "pending_approval" {
		t.Fatalf("status = %q, want pending_approval", items[0].Status)
	}
}

func TestServiceApproveAllowsHumanApprovalRequiredApply(t *testing.T) {
	root := t.TempDir()
	initGitRepo(t, root)

	eventStore := store.NewMemoryStore()
	manager := workspacepkg.NewManager(root, eventStore)
	prepared, err := manager.Prepare(context.Background(), "sess_approved", "task_approved", root, domain.TaskStrategyDirect)
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(prepared.EffectiveDir, "README.md"), []byte("approved change\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	artifactStore := artifacts.NewFileStore(root)
	artifactSvc := artifacts.NewService()
	envelope, err := artifactSvc.BuildExecutionPlan(context.Background(), artifacts.BuildExecutionPlanRequest{
		SessionID: "sess_approved",
		TaskID:    "task_approved",
		RunID:     "task_approved",
		Goal:      "Apply README change",
		Proposal: artifacts.ProposalPayload{
			ProposalID:     "prop_task_approved",
			Summary:        "Change README",
			EstimatedSteps: []string{"Edit README"},
			AffectedFiles:  []string{"README.md"},
		},
		HumanApprovalRequired: true,
	})
	if err != nil {
		t.Fatalf("BuildExecutionPlan() error = %v", err)
	}
	payload, _ := artifacts.ExecutionPlanFromEnvelope(envelope)
	payload.RequiredChecks = nil
	envelope.Payload = payload
	if err := artifactStore.Save(context.Background(), envelope); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	svc := NewService(artifactStore, manager, eventStore)
	if err := svc.Approve(context.Background(), envelope.ID, "local_owner"); err != nil {
		t.Fatalf("Approve() error = %v", err)
	}
	result, err := svc.Apply(context.Background(), "sess_approved", "task_approved", envelope.ID, ApplyOptions{})
	if err != nil {
		t.Fatalf("Apply() after approve error = %v", err)
	}
	if !result.Applied {
		t.Fatalf("result = %#v, want applied", result)
	}
}

func initGitRepo(t *testing.T, dir string) {
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
	cmdArgs := append([]string{"-C", dir}, args...)
	cmd := exec.Command("git", cmdArgs...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s error = %v (%s)", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
}
