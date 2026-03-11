package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/liliang/roma/internal/artifacts"
	"github.com/liliang/roma/internal/domain"
	"github.com/liliang/roma/internal/plans"
	"github.com/liliang/roma/internal/store"
	workspacepkg "github.com/liliang/roma/internal/workspace"
)

func main() {
	root, err := os.MkdirTemp("", "roma-conflict-demo-*")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(root)

	runGit(root, "init")
	runGit(root, "config", "user.email", "roma@example.com")
	runGit(root, "config", "user.name", "ROMA")
	mustWrite(filepath.Join(root, "README.md"), []byte("base\n"))
	runGit(root, "add", "README.md")
	runGit(root, "commit", "-m", "init")

	eventStore := store.NewMemoryStore()
	manager := workspacepkg.NewManager(root, eventStore)
	prepared, err := manager.Prepare(context.Background(), "sess_conflict_demo", "task_conflict_demo", root, domain.TaskStrategyDirect)
	if err != nil {
		panic(err)
	}
	mustWrite(filepath.Join(prepared.EffectiveDir, "README.md"), []byte("worktree version\n"))
	mustWrite(filepath.Join(root, "README.md"), []byte("base version\n"))

	artifactStore := artifacts.NewFileStore(root)
	svc := artifacts.NewService()
	envelope, err := svc.BuildExecutionPlan(context.Background(), artifacts.BuildExecutionPlanRequest{
		SessionID: "sess_conflict_demo",
		TaskID:    "task_conflict_demo",
		RunID:     "task_conflict_demo",
		Goal:      "Apply conflicting README change",
		Proposal: artifacts.ProposalPayload{
			ProposalID:     "prop_conflict_demo",
			Summary:        "Change README",
			EstimatedSteps: []string{"Edit README"},
			AffectedFiles:  []string{"README.md"},
		},
	})
	if err != nil {
		panic(err)
	}
	payload, _ := artifacts.ExecutionPlanFromEnvelope(envelope)
	payload.RequiredChecks = nil
	envelope.Payload = payload
	if err := artifactStore.Save(context.Background(), envelope); err != nil {
		panic(err)
	}

	planSvc := plans.NewService(artifactStore, manager, eventStore)
	preview, err := planSvc.Preview(context.Background(), "sess_conflict_demo", "task_conflict_demo", envelope.ID)
	if err != nil {
		panic(err)
	}
	raw, err := json.MarshalIndent(preview, "", "  ")
	if err != nil {
		panic(err)
	}
	fmt.Println(string(raw))
}

func runGit(dir string, args ...string) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		panic(fmt.Sprintf("git %v failed: %v (%s)", args, err, string(output)))
	}
}

func mustWrite(path string, data []byte) {
	if err := os.WriteFile(path, data, 0o644); err != nil {
		panic(err)
	}
}
