package runtime

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/liliang-cn/roma/internal/domain"
)

func TestBuildCommandForCodex(t *testing.T) {
	t.Parallel()

	supervisor := DefaultSupervisor()
	cmd, err := supervisor.BuildCommand(context.Background(), StartRequest{
		Profile: domain.AgentProfile{
			ID:      "codex-cli",
			Command: "codex",
		},
		Prompt:     "test",
		WorkingDir: "/tmp",
	})
	if err != nil {
		t.Fatalf("BuildCommand() error = %v", err)
	}
	if got := cmd.Args[0]; got != "codex" {
		t.Fatalf("command = %s, want codex", got)
	}
}

func TestBuildDelegationPrompt(t *testing.T) {
	t.Parallel()

	got := BuildDelegationPrompt("do work", []domain.AgentProfile{
		{ID: "gemini-cli", DisplayName: "Gemini CLI"},
	})
	if got == "do work" {
		t.Fatal("BuildDelegationPrompt() did not append delegation guidance")
	}
}

type continuousFakeAdapter struct{}

func (continuousFakeAdapter) Supports(domain.AgentProfile) bool { return true }

func (continuousFakeAdapter) BuildCommand(ctx context.Context, req StartRequest) (*exec.Cmd, error) {
	script := `import sys
prompt = sys.argv[1]
if "Current round: 2" in prompt:
    print("ROMA_DONE: completed on second round")
else:
    print("still working")`
	return exec.CommandContext(ctx, "python3", "-c", script, req.Prompt), nil
}

func TestRunCapturedContinuous(t *testing.T) {
	t.Parallel()

	supervisor := NewSupervisor(continuousFakeAdapter{})
	result, err := supervisor.RunCaptured(context.Background(), StartRequest{
		Profile: domain.AgentProfile{
			ID:      "fake",
			Command: "python3",
		},
		Prompt:     "build feature",
		WorkingDir: ".",
		Continuous: true,
		MaxRounds:  3,
	})
	if err != nil {
		t.Fatalf("RunCaptured() error = %v", err)
	}
	if !strings.Contains(result.Stdout, "== round 1 ==") || !strings.Contains(result.Stdout, "== round 2 ==") {
		t.Fatalf("continuous output missing rounds: %s", result.Stdout)
	}
	if !strings.Contains(result.Stdout, "ROMA_DONE:") {
		t.Fatalf("continuous output missing completion marker: %s", result.Stdout)
	}
}
