package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/liliang/roma/internal/domain"
	"github.com/liliang/roma/internal/events"
	"github.com/liliang/roma/internal/store"
)

// Mode describes the current workspace execution mode.
type Mode string

const (
	ModeSharedRead    Mode = "shared_read"
	ModeIsolatedWrite Mode = "isolated_write"
)

// Prepared captures a task workspace resolution.
type Prepared struct {
	SessionID    string    `json:"session_id"`
	TaskID       string    `json:"task_id"`
	Requested    Mode      `json:"requested_mode"`
	Effective    Mode      `json:"effective_mode"`
	Provider     string    `json:"provider"`
	BaseDir      string    `json:"base_dir"`
	EffectiveDir string    `json:"effective_dir"`
	Fallback     string    `json:"fallback,omitempty"`
	PreparedAt   time.Time `json:"prepared_at"`
	Status       string    `json:"status"`
	ReleasedAt   time.Time `json:"released_at,omitempty"`
	ReclaimedAt  time.Time `json:"reclaimed_at,omitempty"`
	MergedAt     time.Time `json:"merged_at,omitempty"`
	RolledBackAt time.Time `json:"rolled_back_at,omitempty"`
}

type MergePreview struct {
	CanApply        bool              `json:"can_apply"`
	Conflict        bool              `json:"conflict"`
	ConflictDetail  string            `json:"conflict_detail,omitempty"`
	ConflictPaths   []string          `json:"conflict_paths,omitempty"`
	ConflictContext []ConflictSnippet `json:"conflict_context,omitempty"`
	ChangedPaths    []string          `json:"changed_paths,omitempty"`
	PatchBytes      int               `json:"patch_bytes"`
}

type ConflictSnippet struct {
	Path    string `json:"path"`
	Snippet string `json:"snippet"`
}

// Manager resolves per-task workspace directories and persists workspace metadata.
type Manager struct {
	rootDir string
	events  store.EventStore
	now     func() time.Time
	runGit  func(ctx context.Context, dir string, args ...string) error
}

// NewManager constructs a workspace manager rooted in the repository workdir.
func NewManager(rootDir string, eventStore store.EventStore) *Manager {
	return &Manager{
		rootDir: rootDir,
		events:  eventStore,
		now:     func() time.Time { return time.Now().UTC() },
		runGit:  runGit,
	}
}

// Prepare resolves the effective working directory for one task and records the resolution.
func (m *Manager) Prepare(ctx context.Context, sessionID, taskID, baseDir string, strategy domain.TaskStrategy) (Prepared, error) {
	preparedAt := m.now()
	requested := requestedMode(strategy)
	prepared := Prepared{
		SessionID:    sessionID,
		TaskID:       taskID,
		Requested:    requested,
		Effective:    ModeSharedRead,
		Provider:     "shared_read",
		BaseDir:      baseDir,
		EffectiveDir: baseDir,
		PreparedAt:   preparedAt,
		Status:       "prepared",
	}

	if sessionID == "" || taskID == "" || baseDir == "" {
		return prepared, nil
	}

	rootDir := m.rootDir
	if rootDir == "" {
		rootDir = baseDir
	}
	if requested == ModeIsolatedWrite {
		prepared = m.prepareIsolated(ctx, prepared, rootDir)
	}
	metaDir := filepath.Join(rootDir, ".roma", "workspaces", sessionID, taskID)
	if err := os.MkdirAll(metaDir, 0o755); err != nil {
		return Prepared{}, fmt.Errorf("create workspace metadata dir: %w", err)
	}
	if err := writePrepared(filepath.Join(metaDir, "workspace.json"), prepared); err != nil {
		return Prepared{}, fmt.Errorf("write workspace metadata: %w", err)
	}

	if m.events != nil {
		_ = m.events.AppendEvent(ctx, events.Record{
			ID:         fmt.Sprintf("evt_%s_%s_workspace_%d", sessionID, taskID, preparedAt.UnixNano()),
			SessionID:  sessionID,
			TaskID:     taskID,
			Type:       events.TypeWorkspacePrepared,
			ActorType:  events.ActorTypeScheduler,
			OccurredAt: preparedAt,
			ReasonCode: string(prepared.Effective),
			Payload: map[string]any{
				"requested_mode": prepared.Requested,
				"effective_mode": prepared.Effective,
				"provider":       prepared.Provider,
				"base_dir":       prepared.BaseDir,
				"effective_dir":  prepared.EffectiveDir,
				"fallback":       prepared.Fallback,
			},
		})
	}

	return prepared, nil
}

// Release updates persisted workspace metadata after the task finishes.
func (m *Manager) Release(ctx context.Context, prepared Prepared, outcome string) error {
	if prepared.SessionID == "" || prepared.TaskID == "" || prepared.BaseDir == "" {
		return nil
	}
	prepared.Status = "released"
	prepared.ReleasedAt = m.now()

	rootDir := m.rootDir
	if rootDir == "" {
		rootDir = prepared.BaseDir
	}
	metaPath := filepath.Join(rootDir, ".roma", "workspaces", prepared.SessionID, prepared.TaskID, "workspace.json")
	if err := writePrepared(metaPath, prepared); err != nil {
		return fmt.Errorf("write released workspace metadata: %w", err)
	}

	if m.events != nil {
		_ = m.events.AppendEvent(ctx, events.Record{
			ID:         fmt.Sprintf("evt_%s_%s_workspace_release_%d", prepared.SessionID, prepared.TaskID, prepared.ReleasedAt.UnixNano()),
			SessionID:  prepared.SessionID,
			TaskID:     prepared.TaskID,
			Type:       events.TypeWorkspaceReleased,
			ActorType:  events.ActorTypeScheduler,
			OccurredAt: prepared.ReleasedAt,
			ReasonCode: outcome,
			Payload: map[string]any{
				"effective_mode": prepared.Effective,
				"provider":       prepared.Provider,
				"effective_dir":  prepared.EffectiveDir,
				"outcome":        outcome,
			},
		})
	}
	return nil
}

// CapturePatch exports the current git diff for an isolated worktree.
func (m *Manager) CapturePatch(ctx context.Context, prepared Prepared) ([]byte, error) {
	if prepared.Provider != "git_worktree" || prepared.EffectiveDir == "" {
		return nil, fmt.Errorf("workspace patch capture requires git_worktree provider")
	}
	cmd := exec.CommandContext(ctx, "git", "-C", prepared.EffectiveDir, "diff", "--binary")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git diff --binary: %w", err)
	}
	return output, nil
}

// ChangedPaths returns the currently modified paths inside an isolated worktree.
func (m *Manager) ChangedPaths(ctx context.Context, prepared Prepared) ([]string, error) {
	if prepared.Provider != "git_worktree" || prepared.EffectiveDir == "" {
		return nil, fmt.Errorf("workspace changed paths require git_worktree provider")
	}
	cmd := exec.CommandContext(ctx, "git", "-C", prepared.EffectiveDir, "diff", "--name-only")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git diff --name-only: %w", err)
	}
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, filepath.Clean(strings.ReplaceAll(line, "\\", "/")))
		}
	}
	return out, nil
}

// MergeBack applies a captured worktree patch into the base repository and marks the workspace merged.
func (m *Manager) MergeBack(ctx context.Context, prepared Prepared) error {
	if prepared.Provider != "git_worktree" || prepared.EffectiveDir == "" {
		return fmt.Errorf("workspace merge requires git_worktree provider")
	}
	patch, err := m.CapturePatch(ctx, prepared)
	if err != nil {
		return err
	}
	if len(patch) == 0 {
		return nil
	}
	apply := exec.CommandContext(ctx, "git", "-C", prepared.BaseDir, "apply", "--3way", "-")
	apply.Stdin = strings.NewReader(string(patch))
	output, err := apply.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git apply --3way: %w (%s)", err, strings.TrimSpace(string(output)))
	}

	prepared.Status = "merged"
	prepared.MergedAt = m.now()
	rootDir := m.rootDir
	if rootDir == "" {
		rootDir = prepared.BaseDir
	}
	if err := writePrepared(m.metaPath(rootDir, prepared.SessionID, prepared.TaskID), prepared); err != nil {
		return fmt.Errorf("write merged workspace metadata: %w", err)
	}
	if m.events != nil {
		_ = m.events.AppendEvent(ctx, events.Record{
			ID:         fmt.Sprintf("evt_%s_%s_workspace_merge_%d", prepared.SessionID, prepared.TaskID, prepared.MergedAt.UnixNano()),
			SessionID:  prepared.SessionID,
			TaskID:     prepared.TaskID,
			Type:       events.TypeWorkspaceReleased,
			ActorType:  events.ActorTypeHuman,
			OccurredAt: prepared.MergedAt,
			ReasonCode: "merged",
			Payload: map[string]any{
				"effective_dir": prepared.EffectiveDir,
				"base_dir":      prepared.BaseDir,
				"patch_bytes":   len(patch),
			},
		})
	}
	return nil
}

// PreviewMerge checks whether the isolated worktree patch can merge cleanly into base.
func (m *Manager) PreviewMerge(ctx context.Context, prepared Prepared) (MergePreview, error) {
	if prepared.Provider != "git_worktree" || prepared.EffectiveDir == "" {
		return MergePreview{}, fmt.Errorf("workspace merge preview requires git_worktree provider")
	}
	patch, err := m.CapturePatch(ctx, prepared)
	if err != nil {
		return MergePreview{}, err
	}
	changedPaths, err := m.ChangedPaths(ctx, prepared)
	if err != nil {
		return MergePreview{}, err
	}
	preview := MergePreview{
		CanApply:     true,
		ChangedPaths: changedPaths,
		PatchBytes:   len(patch),
	}
	if len(patch) == 0 {
		return preview, nil
	}
	apply := exec.CommandContext(ctx, "git", "-C", prepared.BaseDir, "apply", "--check", "--3way", "-")
	apply.Stdin = strings.NewReader(string(patch))
	output, err := apply.CombinedOutput()
	if err != nil {
		preview.CanApply = false
		preview.Conflict = true
		preview.ConflictDetail = strings.TrimSpace(string(output))
		preview.ConflictPaths = append([]string(nil), changedPaths...)
		preview.ConflictContext = buildConflictContext(string(patch), preview.ConflictPaths)
		if preview.ConflictDetail == "" {
			preview.ConflictDetail = err.Error()
		}
		return preview, nil
	}
	return preview, nil
}

// RollbackMerge reverse-applies the isolated worktree patch into the base repository.
func (m *Manager) RollbackMerge(ctx context.Context, prepared Prepared) error {
	if prepared.Provider != "git_worktree" || prepared.EffectiveDir == "" {
		return fmt.Errorf("workspace rollback requires git_worktree provider")
	}
	patch, err := m.CapturePatch(ctx, prepared)
	if err != nil {
		return err
	}
	if len(patch) == 0 {
		return nil
	}
	apply := exec.CommandContext(ctx, "git", "-C", prepared.BaseDir, "apply", "-R", "--3way", "-")
	apply.Stdin = strings.NewReader(string(patch))
	output, err := apply.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git apply -R --3way: %w (%s)", err, strings.TrimSpace(string(output)))
	}

	prepared.Status = "rolled_back"
	prepared.RolledBackAt = m.now()
	rootDir := m.rootDir
	if rootDir == "" {
		rootDir = prepared.BaseDir
	}
	if err := writePrepared(m.metaPath(rootDir, prepared.SessionID, prepared.TaskID), prepared); err != nil {
		return fmt.Errorf("write rolled back workspace metadata: %w", err)
	}
	if m.events != nil {
		_ = m.events.AppendEvent(ctx, events.Record{
			ID:         fmt.Sprintf("evt_%s_%s_workspace_rollback_%d", prepared.SessionID, prepared.TaskID, prepared.RolledBackAt.UnixNano()),
			SessionID:  prepared.SessionID,
			TaskID:     prepared.TaskID,
			Type:       events.TypeWorkspaceReleased,
			ActorType:  events.ActorTypeHuman,
			OccurredAt: prepared.RolledBackAt,
			ReasonCode: "rolled_back",
			Payload: map[string]any{
				"effective_dir": prepared.EffectiveDir,
				"base_dir":      prepared.BaseDir,
				"patch_bytes":   len(patch),
			},
		})
	}
	return nil
}

// List returns all persisted task workspaces.
func (m *Manager) List(_ context.Context) ([]Prepared, error) {
	rootDir := m.rootDir
	if rootDir == "" {
		return nil, nil
	}
	items, err := m.loadAll(rootDir)
	if err != nil {
		return nil, err
	}
	slices.SortFunc(items, func(a, b Prepared) int {
		if cmp := strings.Compare(a.SessionID, b.SessionID); cmp != 0 {
			return cmp
		}
		return strings.Compare(a.TaskID, b.TaskID)
	})
	return items, nil
}

// Get returns one persisted task workspace.
func (m *Manager) Get(_ context.Context, sessionID, taskID string) (Prepared, error) {
	rootDir := m.rootDir
	if rootDir == "" {
		return Prepared{}, os.ErrNotExist
	}
	return loadPrepared(filepath.Join(rootDir, ".roma", "workspaces", sessionID, taskID, "workspace.json"))
}

// ReclaimStale removes prepared or released git worktrees and marks them as reclaimed.
func (m *Manager) ReclaimStale(ctx context.Context) error {
	return m.ReclaimStaleExcept(ctx, nil)
}

// ReclaimStaleExcept removes stale git worktrees except for sessions explicitly marked active.
func (m *Manager) ReclaimStaleExcept(ctx context.Context, activeSessions map[string]struct{}) error {
	rootDir := m.rootDir
	if rootDir == "" {
		return nil
	}
	items, err := m.loadAll(rootDir)
	if err != nil {
		return err
	}
	for _, prepared := range items {
		if _, ok := activeSessions[prepared.SessionID]; ok {
			continue
		}
		if (prepared.Status != "released" && prepared.Status != "prepared") || prepared.Provider != "git_worktree" || prepared.EffectiveDir == "" {
			continue
		}
		if err := m.runGitWithRetry(ctx, prepared.BaseDir, "worktree", "remove", "--force", prepared.EffectiveDir); err != nil {
			return err
		}
		prepared.Status = "reclaimed"
		prepared.ReclaimedAt = m.now()
		if err := writePrepared(m.metaPath(rootDir, prepared.SessionID, prepared.TaskID), prepared); err != nil {
			return err
		}
		if m.events != nil {
			_ = m.events.AppendEvent(ctx, events.Record{
				ID:         fmt.Sprintf("evt_%s_%s_workspace_reclaim_%d", prepared.SessionID, prepared.TaskID, prepared.ReclaimedAt.UnixNano()),
				SessionID:  prepared.SessionID,
				TaskID:     prepared.TaskID,
				Type:       events.TypeWorkspaceReclaimed,
				ActorType:  events.ActorTypeScheduler,
				OccurredAt: prepared.ReclaimedAt,
				ReasonCode: prepared.Status,
				Payload: map[string]any{
					"effective_dir": prepared.EffectiveDir,
					"provider":      prepared.Provider,
				},
			})
		}
	}
	return nil
}

func (m *Manager) prepareIsolated(ctx context.Context, prepared Prepared, rootDir string) Prepared {
	worktreeRoot := filepath.Join(rootDir, ".roma", "workspaces", prepared.SessionID, prepared.TaskID, "root")
	if isGitWorktree(prepared.BaseDir) {
		if stat, err := os.Stat(filepath.Join(worktreeRoot, ".git")); err == nil && !stat.IsDir() {
			prepared.Effective = ModeIsolatedWrite
			prepared.Provider = "git_worktree"
			prepared.EffectiveDir = worktreeRoot
			return prepared
		}
		if err := os.MkdirAll(filepath.Dir(worktreeRoot), 0o755); err != nil {
			prepared.Fallback = "workspace_metadata_dir_failed"
			return prepared
		}
		if err := m.runGitWithRetry(ctx, prepared.BaseDir, "worktree", "add", "--detach", worktreeRoot); err == nil {
			prepared.Effective = ModeIsolatedWrite
			prepared.Provider = "git_worktree"
			prepared.EffectiveDir = worktreeRoot
			return prepared
		} else {
			prepared.Fallback = sanitizeFallback(err)
			return prepared
		}
	}
	prepared.Fallback = "git_worktree_unavailable"
	return prepared
}

func isGitWorktree(dir string) bool {
	cmd := exec.Command("git", "-C", dir, "rev-parse", "--is-inside-work-tree")
	output, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(output)) == "true"
}

func runGit(ctx context.Context, dir string, args ...string) error {
	cmdArgs := append([]string{"-C", dir}, args...)
	cmd := exec.CommandContext(ctx, "git", cmdArgs...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s: %w (%s)", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return nil
}

func (m *Manager) runGitWithRetry(ctx context.Context, dir string, args ...string) error {
	if m.runGit == nil {
		return runGit(ctx, dir, args...)
	}
	var lastErr error
	for attempt := 0; attempt < 6; attempt++ {
		lastErr = m.runGit(ctx, dir, args...)
		if lastErr == nil {
			return nil
		}
		if !isGitLockError(lastErr) || ctx.Err() != nil {
			return lastErr
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(attempt+1) * 25 * time.Millisecond):
		}
	}
	return lastErr
}

func isGitLockError(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "index.lock") ||
		strings.Contains(text, "could not lock") ||
		strings.Contains(text, "another git process") ||
		strings.Contains(text, "unable to create")
}

func sanitizeFallback(err error) string {
	text := strings.TrimSpace(err.Error())
	text = strings.ReplaceAll(text, " ", "_")
	text = strings.ReplaceAll(text, "\n", "_")
	if text == "" {
		return "git_worktree_failed"
	}
	return text
}

func buildConflictContext(patch string, conflictPaths []string) []ConflictSnippet {
	if strings.TrimSpace(patch) == "" || len(conflictPaths) == 0 {
		return nil
	}
	sections := splitPatchByPath(patch)
	out := make([]ConflictSnippet, 0, len(conflictPaths))
	for _, path := range conflictPaths {
		snippet, ok := sections[normalizePatchPath(path)]
		if !ok || strings.TrimSpace(snippet) == "" {
			continue
		}
		lines := strings.Split(strings.TrimSpace(snippet), "\n")
		if len(lines) > 24 {
			lines = append(lines[:24], "...(truncated)")
		}
		out = append(out, ConflictSnippet{
			Path:    path,
			Snippet: strings.Join(lines, "\n"),
		})
	}
	return out
}

func splitPatchByPath(patch string) map[string]string {
	out := map[string]string{}
	var currentPath string
	var current []string
	flush := func() {
		if currentPath == "" || len(current) == 0 {
			return
		}
		out[currentPath] = strings.Join(current, "\n")
	}
	for _, line := range strings.Split(patch, "\n") {
		if strings.HasPrefix(line, "diff --git ") {
			flush()
			current = current[:0]
			currentPath = ""
			parts := strings.Fields(line)
			if len(parts) >= 4 && strings.HasPrefix(parts[3], "b/") {
				currentPath = normalizePatchPath(strings.TrimPrefix(parts[3], "b/"))
			}
		}
		if currentPath != "" {
			current = append(current, line)
		}
	}
	flush()
	return out
}

func normalizePatchPath(path string) string {
	return strings.ReplaceAll(filepath.Clean(path), "\\", "/")
}

func loadPrepared(path string) (Prepared, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Prepared{}, fmt.Errorf("read workspace metadata: %w", err)
	}
	var prepared Prepared
	if err := json.Unmarshal(raw, &prepared); err != nil {
		return Prepared{}, fmt.Errorf("decode workspace metadata: %w", err)
	}
	return prepared, nil
}

func writePrepared(path string, prepared Prepared) error {
	raw, err := json.MarshalIndent(prepared, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal workspace metadata: %w", err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		return err
	}
	return nil
}

func requestedMode(strategy domain.TaskStrategy) Mode {
	switch strategy {
	case domain.TaskStrategyDirect:
		return ModeIsolatedWrite
	case domain.TaskStrategyRelay, domain.TaskStrategyCuria:
		return ModeSharedRead
	default:
		return ModeSharedRead
	}
}

func (m *Manager) loadAll(rootDir string) ([]Prepared, error) {
	workspaceRoot := filepath.Join(rootDir, ".roma", "workspaces")
	sessionEntries, err := os.ReadDir(workspaceRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read workspace root: %w", err)
	}
	items := make([]Prepared, 0)
	for _, sessionEntry := range sessionEntries {
		if !sessionEntry.IsDir() {
			continue
		}
		taskEntries, err := os.ReadDir(filepath.Join(workspaceRoot, sessionEntry.Name()))
		if err != nil {
			return nil, fmt.Errorf("read session workspace dir: %w", err)
		}
		for _, taskEntry := range taskEntries {
			if !taskEntry.IsDir() {
				continue
			}
			prepared, err := loadPrepared(m.metaPath(rootDir, sessionEntry.Name(), taskEntry.Name()))
			if err != nil {
				return nil, err
			}
			items = append(items, prepared)
		}
	}
	return items, nil
}

func (m *Manager) metaPath(rootDir, sessionID, taskID string) string {
	return filepath.Join(rootDir, ".roma", "workspaces", sessionID, taskID, "workspace.json")
}
