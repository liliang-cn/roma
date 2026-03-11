package orchestrator

import (
	"context"
	"os/exec"
	"testing"

	"github.com/liliang-cn/roma/internal/domain"
	"github.com/liliang-cn/roma/internal/runtime"
)

func TestRunSequentialBuildsDelegateChain(t *testing.T) {
	t.Parallel()

	supervisor := runtime.NewSupervisor(fakePromptAdapter{})
	svc := NewService(supervisor)

	result, err := svc.RunSequential(context.Background(), Request{
		Prompt:     "build a platform",
		WorkingDir: ".",
		SessionID:  "sess_1",
		TaskID:     "task_1",
		Starter: domain.AgentProfile{
			ID:          "starter",
			DisplayName: "Starter",
			Command:     "starter",
		},
		Delegates: []domain.AgentProfile{
			{
				ID:          "delegate",
				DisplayName: "Delegate",
				Command:     "delegate",
			},
		},
	})
	if err != nil {
		t.Fatalf("RunSequential() error = %v", err)
	}
	if len(result.Steps) != 2 {
		t.Fatalf("step count = %d, want 2", len(result.Steps))
	}
	if result.Steps[1].Prompt == "" {
		t.Fatal("delegate prompt is empty")
	}
	if result.Steps[0].Artifact.ID == "" || result.Steps[1].Artifact.ID == "" {
		t.Fatal("artifact envelope was not created")
	}
}

type fakePromptAdapter struct{}

func (fakePromptAdapter) Supports(profile domain.AgentProfile) bool {
	return true
}

func (fakePromptAdapter) BuildCommand(ctx context.Context, req runtime.StartRequest) (*exec.Cmd, error) {
	return exec.CommandContext(ctx, "python3", "-c", "import sys; print(sys.argv[1])", req.Prompt), nil
}
