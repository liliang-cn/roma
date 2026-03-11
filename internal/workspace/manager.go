package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	raw, err := json.MarshalIndent(prepared, "", "  ")
	if err != nil {
		return Prepared{}, fmt.Errorf("marshal workspace metadata: %w", err)
	}
	if err := os.WriteFile(filepath.Join(metaDir, "workspace.json"), raw, 0o644); err != nil {
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
	raw, err := json.MarshalIndent(prepared, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal released workspace metadata: %w", err)
	}
	if err := os.WriteFile(metaPath, raw, 0o644); err != nil {
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
		if err := m.runGit(ctx, prepared.BaseDir, "worktree", "add", "--detach", worktreeRoot); err == nil {
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

func sanitizeFallback(err error) string {
	text := strings.TrimSpace(err.Error())
	text = strings.ReplaceAll(text, " ", "_")
	text = strings.ReplaceAll(text, "\n", "_")
	if text == "" {
		return "git_worktree_failed"
	}
	return text
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
