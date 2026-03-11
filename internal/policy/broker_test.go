package policy

import (
	"context"
	"os/exec"
	"testing"

	"github.com/liliang/roma/internal/events"
	"github.com/liliang/roma/internal/store"
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
