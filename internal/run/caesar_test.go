package run

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liliang-cn/roma/internal/agents"
	"github.com/liliang-cn/roma/internal/domain"
	"github.com/liliang-cn/roma/internal/scheduler"
)

func TestInspectRepoConflictsDetectsUnmergedPaths(t *testing.T) {
	root := t.TempDir()
	initRunGitRepo(t, root)
	baseBranch := currentGitBranch(t, root)

	runGitCommand(t, root, "checkout", "-b", "feature")
	writeAndCommit(t, root, "README.md", "feature\n", "feature change")
	runGitCommand(t, root, "checkout", baseBranch)
	writeAndCommit(t, root, "README.md", "master\n", "master change")

	if err := runGitCommandAllowFailure(root, "merge", "feature"); err == nil {
		t.Fatal("merge error = nil, want conflict")
	}

	summary, err := inspectRepoConflicts(context.Background(), root)
	if err != nil {
		t.Fatalf("inspectRepoConflicts() error = %v", err)
	}
	if !summary.HasConflicts() {
		t.Fatal("HasConflicts() = false, want true")
	}
	if len(summary.Paths) != 1 || summary.Paths[0] != "README.md" {
		t.Fatalf("paths = %#v, want [README.md]", summary.Paths)
	}
	if len(summary.StatusLines) != 1 || !strings.Contains(summary.StatusLines[0], "README.md") {
		t.Fatalf("status lines = %#v, want README.md entry", summary.StatusLines)
	}
}

func TestEnsureConflictFreeConclusionFailsForUnmergedRepo(t *testing.T) {
	root := t.TempDir()
	initRunGitRepo(t, root)
	baseBranch := currentGitBranch(t, root)

	runGitCommand(t, root, "checkout", "-b", "feature")
	writeAndCommit(t, root, "README.md", "feature\n", "feature change")
	runGitCommand(t, root, "checkout", baseBranch)
	writeAndCommit(t, root, "README.md", "master\n", "master change")

	if err := runGitCommandAllowFailure(root, "merge", "feature"); err == nil {
		t.Fatal("merge error = nil, want conflict")
	}

	err := ensureConflictFreeConclusion(context.Background(), root)
	if err == nil {
		t.Fatal("ensureConflictFreeConclusion() error = nil, want conflict error")
	}
	if !strings.Contains(err.Error(), "README.md") {
		t.Fatalf("error = %q, want README.md mentioned", err.Error())
	}
}

func TestResolveCaesarDelegateTargetRequiresKnownNodeTarget(t *testing.T) {
	t.Parallel()

	registry, err := agents.NewRegistry(
		domain.AgentProfile{ID: "starter", DisplayName: "Starter", Command: "sh", Args: []string{"-c", "true"}, Availability: domain.AgentAvailabilityAvailable},
		domain.AgentProfile{ID: "worker", DisplayName: "Worker", Command: "sh", Args: []string{"-c", "true"}, Availability: domain.AgentAvailabilityAvailable},
	)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	svc := NewService(registry)
	assignments := []scheduler.NodeAssignment{
		{
			Node: domain.TaskNodeSpec{ID: "task_1_starter_caesar_1"},
			Profile: domain.AgentProfile{
				ID:           "starter",
				Availability: domain.AgentAvailabilityAvailable,
			},
		},
		{
			Node: domain.TaskNodeSpec{ID: "task_1_delegate_1"},
			Profile: domain.AgentProfile{
				ID:           "worker",
				Availability: domain.AgentAvailabilityAvailable,
			},
		},
	}

	if _, ok := svc.resolveCaesarDelegateTarget(context.Background(), assignments, "task_1_delegate_1"); !ok {
		t.Fatal("resolveCaesarDelegateTarget(node id) = false, want true")
	}
	if _, ok := svc.resolveCaesarDelegateTarget(context.Background(), assignments, "delegate_1"); !ok {
		t.Fatal("resolveCaesarDelegateTarget(suffix) = false, want true")
	}
	if _, ok := svc.resolveCaesarDelegateTarget(context.Background(), assignments, "starter"); ok {
		t.Fatal("resolveCaesarDelegateTarget(starter agent id) = true, want false")
	}
	if _, ok := svc.resolveCaesarDelegateTarget(context.Background(), assignments, "worker"); ok {
		t.Fatal("resolveCaesarDelegateTarget(worker agent id) = true, want false")
	}
}

func writeAndCommit(t *testing.T, dir, path, content, message string) {
	t.Helper()
	writeFileForRunTest(t, dir, path, content)
	runGitCommand(t, dir, "add", path)
	runGitCommand(t, dir, "commit", "-m", message)
}

func writeFileForRunTest(t *testing.T, dir, path, content string) {
	t.Helper()
	fullPath := dir + "/" + path
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}

func runGitCommandAllowFailure(dir string, args ...string) error {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	_, err := cmd.CombinedOutput()
	return err
}

func currentGitBranch(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command("git", "-C", dir, "branch", "--show-current")
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("git branch --show-current error = %v", err)
	}
	return strings.TrimSpace(string(output))
}
