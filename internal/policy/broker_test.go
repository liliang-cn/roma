package policy

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/liliang-cn/roma/internal/events"
	"github.com/liliang-cn/roma/internal/store"
)

func TestSimpleBrokerBlocksRootWorkingDir(t *testing.T) {
	t.Parallel()

	mem := store.NewMemoryStore()
	broker := NewSimpleBroker(mem)

	decision, err := broker.Evaluate(context.Background(), Request{
		SessionID:  "sess_1",
		TaskID:     "task_1",
		Mode:       "direct",
		Prompt:     "build a feature",
		WorkingDir: "/",
	})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if decision.Kind != DecisionBlock {
		t.Fatalf("decision kind = %s, want block", decision.Kind)
	}

	records, err := mem.ListEvents(context.Background(), store.EventFilter{SessionID: "sess_1", Type: events.TypePolicyDecisionRecorded})
	if err != nil {
		t.Fatalf("ListEvents() error = %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("policy event count = %d, want 1", len(records))
	}
}

func TestSimpleBrokerWarnsOnRiskyPrompt(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	broker := NewSimpleBroker(nil)

	decision, err := broker.Evaluate(context.Background(), Request{
		SessionID:    "sess_1",
		TaskID:       "task_1",
		Mode:         "graph",
		Prompt:       "drop database and rebuild everything",
		WorkingDir:   workDir,
		StarterAgent: "codex-cli",
		NodeCount:    9,
	})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if decision.Kind != DecisionWarn {
		t.Fatalf("decision kind = %s, want warn", decision.Kind)
	}
	if len(decision.Warnings) < 2 {
		t.Fatalf("warnings = %#v, want multiple warnings", decision.Warnings)
	}
}

func TestSimpleBrokerClassifyCommandWarnsOnShell(t *testing.T) {
	t.Parallel()

	broker := NewSimpleBroker(nil)
	decision, err := broker.ClassifyCommand(context.Background(), "sess_1", "task_1", exec.Command("bash", "-lc", "echo ok"))
	if err != nil {
		t.Fatalf("ClassifyCommand() error = %v", err)
	}
	if decision.Kind != DecisionWarn {
		t.Fatalf("decision kind = %s, want warn", decision.Kind)
	}
}

func TestSimpleBrokerBlocksEffectiveDirOutsideWorkspaceBoundary(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "elsewhere")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	broker := NewSimpleBroker(nil)
	decision, err := broker.Evaluate(context.Background(), Request{
		SessionID:    "sess_1",
		TaskID:       "task_1",
		Mode:         "node",
		Prompt:       "build a feature",
		WorkingDir:   workDir,
		EffectiveDir: outside,
	})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if decision.Kind != DecisionBlock || decision.Reason != "effective_dir_outside_workspace_boundary" {
		t.Fatalf("decision = %#v, want block/effective_dir_outside_workspace_boundary", decision)
	}
}

func TestSimpleBrokerAllowsOverrideForApprovedActor(t *testing.T) {
	t.Setenv("ROMA_POLICY_OVERRIDE_ACTORS", "local_owner,admin")
	workDir := t.TempDir()
	broker := NewSimpleBroker(nil)
	decision, err := broker.Evaluate(context.Background(), Request{
		SessionID:      "sess_1",
		TaskID:         "task_1",
		Mode:           "direct",
		Prompt:         "drop database and rebuild everything",
		WorkingDir:     workDir,
		EffectiveDir:   workDir,
		PolicyOverride: true,
		OverrideActor:  "local_owner",
	})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if decision.Kind != DecisionAllow || decision.Reason != "approved_override" {
		t.Fatalf("decision = %#v, want allow/approved_override", decision)
	}
}

func TestSimpleBrokerBlocksOverrideForForbiddenActor(t *testing.T) {
	t.Setenv("ROMA_POLICY_OVERRIDE_ACTORS", "admin")
	workDir := t.TempDir()
	broker := NewSimpleBroker(nil)
	decision, err := broker.Evaluate(context.Background(), Request{
		SessionID:      "sess_1",
		TaskID:         "task_1",
		Mode:           "direct",
		Prompt:         "drop database and rebuild everything",
		WorkingDir:     workDir,
		EffectiveDir:   workDir,
		PolicyOverride: true,
		OverrideActor:  "local_owner",
	})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if decision.Kind != DecisionBlock || decision.Reason != "override_actor_forbidden" {
		t.Fatalf("decision = %#v, want block/override_actor_forbidden", decision)
	}
}

func TestEvaluatePathActionBlocksProtectedPlanApplyWithoutOverride(t *testing.T) {
	t.Parallel()

	decision := EvaluatePathAction(ActionPlanApply, []string{".github/workflows/build.yml"}, false, "")
	if decision.Kind != DecisionBlock || decision.Reason != "protected_path_apply_requires_override" {
		t.Fatalf("decision = %#v, want block/protected_path_apply_requires_override", decision)
	}
}

func TestEvaluatePathActionAllowsProtectedPlanApplyWithApprovedOverride(t *testing.T) {
	t.Setenv("ROMA_POLICY_OVERRIDE_ACTORS", "local_owner")

	decision := EvaluatePathAction(ActionPlanApply, []string{".github/workflows/build.yml"}, true, "local_owner")
	if decision.Kind != DecisionAllow || decision.Reason != "approved_override" {
		t.Fatalf("decision = %#v, want allow/approved_override", decision)
	}
}
