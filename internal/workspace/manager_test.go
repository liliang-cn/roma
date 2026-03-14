package workspace

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liliang-cn/roma/internal/domain"
	"github.com/liliang-cn/roma/internal/events"
	"github.com/liliang-cn/roma/internal/store"
)

func TestManagerPreparePersistsMetadataAndEvent(t *testing.T) {
	root := t.TempDir()
	eventStore := store.NewMemoryStore()
	manager := NewManager(root, eventStore)

	prepared, err := manager.Prepare(context.Background(), "sess_1", "task_1", root, domain.TaskStrategyDirect)
	if err != nil {
		t.Fatalf("Prepare returned error: %v", err)
	}
	if prepared.EffectiveDir != root {
		t.Fatalf("expected effective dir %q, got %q", root, prepared.EffectiveDir)
	}
	if prepared.Requested != ModeIsolatedWrite {
		t.Fatalf("expected requested mode %q, got %q", ModeIsolatedWrite, prepared.Requested)
	}
	if prepared.Effective != ModeSharedRead {
		t.Fatalf("expected effective mode %q, got %q", ModeSharedRead, prepared.Effective)
	}
	if prepared.Provider != "shared_read" {
		t.Fatalf("expected provider shared_read, got %q", prepared.Provider)
	}
	if prepared.Fallback == "" {
		t.Fatal("expected fallback reason for non-git workspace")
	}

	path := filepath.Join(root, ".roma", "workspaces", "sess_1", "task_1", "workspace.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected workspace metadata file: %v", err)
	}

	items, err := eventStore.ListEvents(context.Background(), store.EventFilter{SessionID: "sess_1", Type: events.TypeWorkspacePrepared})
	if err != nil {
		t.Fatalf("ListEvents returned error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 workspace event, got %d", len(items))
	}

	if err := manager.Release(context.Background(), prepared, "succeeded"); err != nil {
		t.Fatalf("Release returned error: %v", err)
	}
	items, err = eventStore.ListEvents(context.Background(), store.EventFilter{SessionID: "sess_1", Type: events.TypeWorkspaceReleased})
	if err != nil {
		t.Fatalf("ListEvents after release returned error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 workspace release event, got %d", len(items))
	}
}

func TestManagerPrepareCreatesGitWorktreeForIsolatedWrite(t *testing.T) {
	root := t.TempDir()
	initGitRepo(t, root)

	eventStore := store.NewMemoryStore()
	manager := NewManager(root, eventStore)

	prepared, err := manager.Prepare(context.Background(), "sess_git", "task_git", root, domain.TaskStrategyDirect)
	if err != nil {
		t.Fatalf("Prepare returned error: %v", err)
	}
	if prepared.Effective != ModeIsolatedWrite {
		t.Fatalf("expected effective mode %q, got %q", ModeIsolatedWrite, prepared.Effective)
	}
	if prepared.Provider != "git_worktree" {
		t.Fatalf("expected provider git_worktree, got %q", prepared.Provider)
	}
	if prepared.Fallback != "" {
		t.Fatalf("expected empty fallback, got %q", prepared.Fallback)
	}
	if prepared.EffectiveDir == root {
		t.Fatal("expected isolated effective dir, got base dir")
	}
	if _, err := os.Stat(filepath.Join(prepared.EffectiveDir, ".git")); err != nil {
		t.Fatalf("expected git worktree checkout: %v", err)
	}

	if err := manager.Release(context.Background(), prepared, "succeeded"); err != nil {
		t.Fatalf("Release returned error: %v", err)
	}
	if err := manager.ReclaimStale(context.Background()); err != nil {
		t.Fatalf("ReclaimStale returned error: %v", err)
	}

	reclaimed, err := loadPrepared(filepath.Join(root, ".roma", "workspaces", "sess_git", "task_git", "workspace.json"))
	if err != nil {
		t.Fatalf("loadPrepared() error = %v", err)
	}
	if reclaimed.Status != "reclaimed" {
		t.Fatalf("status = %q, want reclaimed", reclaimed.Status)
	}
	if reclaimed.ReclaimedAt.IsZero() {
		t.Fatal("expected reclaimed timestamp")
	}
	if _, err := os.Stat(reclaimed.EffectiveDir); !os.IsNotExist(err) {
		t.Fatalf("expected worktree dir removed, stat err = %v", err)
	}
}

func TestManagerReclaimStaleRemovesPreparedWorktree(t *testing.T) {
	root := t.TempDir()
	initGitRepo(t, root)

	manager := NewManager(root, nil)
	prepared, err := manager.Prepare(context.Background(), "sess_prepared", "task_prepared", root, domain.TaskStrategyDirect)
	if err != nil {
		t.Fatalf("Prepare returned error: %v", err)
	}
	if err := manager.ReclaimStale(context.Background()); err != nil {
		t.Fatalf("ReclaimStale returned error: %v", err)
	}
	reclaimed, err := loadPrepared(filepath.Join(root, ".roma", "workspaces", "sess_prepared", "task_prepared", "workspace.json"))
	if err != nil {
		t.Fatalf("loadPrepared() error = %v", err)
	}
	if reclaimed.Status != "reclaimed" {
		t.Fatalf("status = %q, want reclaimed", reclaimed.Status)
	}
	if _, err := os.Stat(prepared.EffectiveDir); !os.IsNotExist(err) {
		t.Fatalf("expected prepared worktree dir removed, stat err = %v", err)
	}
}

func TestManagerCapturePatchAndMergeBack(t *testing.T) {
	root := t.TempDir()
	initGitRepo(t, root)

	manager := NewManager(root, store.NewMemoryStore())
	prepared, err := manager.Prepare(context.Background(), "sess_merge", "task_merge", root, domain.TaskStrategyDirect)
	if err != nil {
		t.Fatalf("Prepare returned error: %v", err)
	}
	target := filepath.Join(prepared.EffectiveDir, "README.md")
	if err := os.WriteFile(target, []byte("roma merged\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	patch, err := manager.CapturePatch(context.Background(), prepared)
	if err != nil {
		t.Fatalf("CapturePatch() error = %v", err)
	}
	if len(patch) == 0 {
		t.Fatal("expected non-empty patch")
	}
	if err := manager.MergeBack(context.Background(), prepared); err != nil {
		t.Fatalf("MergeBack() error = %v", err)
	}

	mergedPath := filepath.Join(root, ".roma", "workspaces", "sess_merge", "task_merge", "workspace.json")
	merged, err := loadPrepared(mergedPath)
	if err != nil {
		t.Fatalf("loadPrepared() error = %v", err)
	}
	if merged.Status != "merged" {
		t.Fatalf("status = %q, want merged", merged.Status)
	}
	content, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if strings.TrimSpace(string(content)) != "roma merged" {
		t.Fatalf("base README = %q, want roma merged", strings.TrimSpace(string(content)))
	}

	if err := manager.RollbackMerge(context.Background(), prepared); err != nil {
		t.Fatalf("RollbackMerge() error = %v", err)
	}
	rolledBack, err := loadPrepared(mergedPath)
	if err != nil {
		t.Fatalf("loadPrepared() error = %v", err)
	}
	if rolledBack.Status != "rolled_back" {
		t.Fatalf("status = %q, want rolled_back", rolledBack.Status)
	}
	content, err = os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if strings.TrimSpace(string(content)) != "roma" {
		t.Fatalf("base README after rollback = %q, want roma", strings.TrimSpace(string(content)))
	}
}

func TestManagerCapturePatchAndMergeBackIncludesUntrackedFiles(t *testing.T) {
	root := t.TempDir()
	initGitRepo(t, root)

	manager := NewManager(root, store.NewMemoryStore())
	prepared, err := manager.Prepare(context.Background(), "sess_merge_new", "task_merge_new", root, domain.TaskStrategyDirect)
	if err != nil {
		t.Fatalf("Prepare returned error: %v", err)
	}

	newPath := filepath.Join(prepared.EffectiveDir, "docs", "note.txt")
	if err := os.MkdirAll(filepath.Dir(newPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(newPath, []byte("new file\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	patch, err := manager.CapturePatch(context.Background(), prepared)
	if err != nil {
		t.Fatalf("CapturePatch() error = %v", err)
	}
	if !strings.Contains(string(patch), "note.txt") {
		t.Fatalf("patch = %q, want new file path", string(patch))
	}

	paths, err := manager.ChangedPaths(context.Background(), prepared)
	if err != nil {
		t.Fatalf("ChangedPaths() error = %v", err)
	}
	if len(paths) != 1 || paths[0] != filepath.Clean("docs/note.txt") {
		t.Fatalf("paths = %#v, want [docs/note.txt]", paths)
	}

	if err := manager.MergeBack(context.Background(), prepared); err != nil {
		t.Fatalf("MergeBack() error = %v", err)
	}
	content, err := os.ReadFile(filepath.Join(root, "docs", "note.txt"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if strings.TrimSpace(string(content)) != "new file" {
		t.Fatalf("merged content = %q, want new file", strings.TrimSpace(string(content)))
	}

	if err := manager.RollbackMerge(context.Background(), prepared); err != nil {
		t.Fatalf("RollbackMerge() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "docs", "note.txt")); !os.IsNotExist(err) {
		t.Fatalf("expected merged new file removed on rollback, stat err = %v", err)
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
