package main

import (
	"context"
	"fmt"
	"os/exec"

	"github.com/liliang/roma/internal/artifacts"
	"github.com/liliang/roma/internal/curia"
	"github.com/liliang/roma/internal/domain"
	"github.com/liliang/roma/internal/runtime"
)

type demoAdapter struct{}

func (demoAdapter) Supports(domain.AgentProfile) bool { return true }

func (demoAdapter) BuildCommand(ctx context.Context, req runtime.StartRequest) (*exec.Cmd, error) {
	script := `
import sys
profile = sys.argv[1]
prompt = sys.argv[2]
if "Augustus arbitration phase" in prompt:
    print("winning_mode: replace")
    print("selected_proposals: prop_task_demo_gemini-cli")
    print("rationale: Augustus selected the safer fallback proposal.")
    print("risk_flags:")
    print("- augustus_override")
    print("review_questions:")
    print("- Why did the original leader receive a veto?")
elif "blind review phase" in prompt:
    if profile == "gemini-cli":
        print("prop_task_demo_codex-cli is weak and veto")
    elif profile == "copilot-cli":
        print("prop_task_demo_gemini-cli is the best proposal")
    else:
        print("prop_task_demo_codex-cli is the best proposal")
else:
    if profile == "codex-cli":
        print("Proposal A\ninternal/api/server.go\nrisk: merge conflict")
    elif profile == "gemini-cli":
        print("Proposal B\ninternal/api/server.go\nrisk: safer fallback")
    else:
        print("Proposal C\ninternal/api/server.go\nrisk: scope drift")
`
	return exec.CommandContext(ctx, "python3", "-c", script, req.Profile.ID, req.Prompt), nil
}

func main() {
	root := "."
	executor := curia.NewExecutor(root, runtime.NewSupervisor(demoAdapter{}), artifacts.NewService())
	result, err := executor.Execute(context.Background(), curia.ExecuteRequest{
		SessionID:       "sess_demo",
		TaskID:          "task_demo",
		BasePrompt:      "Resolve conflicting API designs automatically",
		WorkingDir:      root,
		NodeTitle:       "curia augustus demo",
		Senators:        []domain.AgentProfile{{ID: "codex-cli"}, {ID: "gemini-cli"}, {ID: "copilot-cli"}},
		Quorum:          2,
		ArbitrationMode: "augustus",
		Arbitrator:      domain.AgentProfile{ID: "claude-code"},
	})
	if err != nil {
		panic(err)
	}
	decision, _ := artifacts.DecisionPackFromEnvelope(result.RelatedArtifacts[len(result.RelatedArtifacts)-1])
	plan, _ := artifacts.ExecutionPlanFromEnvelope(result.Primary)
	fmt.Printf("arbitrated=%t\n", decision.Arbitrated)
	fmt.Printf("arbitrator=%s\n", decision.ArbitratorID)
	fmt.Printf("winning_mode=%s\n", decision.WinningMode)
	fmt.Printf("selected=%v\n", decision.SelectedProposalIDs)
	fmt.Printf("apply_mode=%s\n", plan.ApplyMode)
}
