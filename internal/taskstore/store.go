package taskstore

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/liliang/roma/internal/domain"
	"github.com/liliang/roma/internal/store"
)

// Store persists task records under .roma/tasks.
type Store struct {
	rootDir string
}

// NewStore constructs a file-backed task store.
func NewStore(workDir string) *Store {
	return &Store{rootDir: filepath.Join(workDir, ".roma", "tasks")}
}

// UpsertTask persists a task record.
func (s *Store) UpsertTask(ctx context.Context, task domain.TaskRecord) error {
	_ = ctx
	if task.ID == "" {
		return fmt.Errorf("task id is required")
	}
	if err := os.MkdirAll(s.rootDir, 0o755); err != nil {
		return fmt.Errorf("create task directory: %w", err)
	}
	raw, err := json.MarshalIndent(task, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal task record: %w", err)
	}
	if err := os.WriteFile(filepath.Join(s.rootDir, task.ID+".json"), raw, 0o644); err != nil {
		return fmt.Errorf("write task record: %w", err)
	}
	return nil
}

// GetTask loads one task record.
func (s *Store) GetTask(ctx context.Context, taskID string) (domain.TaskRecord, error) {
	_ = ctx
	raw, err := os.ReadFile(filepath.Join(s.rootDir, taskID+".json"))
	if err != nil {
		if os.IsNotExist(err) {
			return domain.TaskRecord{}, store.ErrNotFound
		}
		return domain.TaskRecord{}, fmt.Errorf("read task record: %w", err)
	}
	var record domain.TaskRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return domain.TaskRecord{}, fmt.Errorf("unmarshal task record: %w", err)
	}
	return record, nil
}

// ListTasksBySession returns all task records for a session.
func (s *Store) ListTasksBySession(ctx context.Context, sessionID string) ([]domain.TaskRecord, error) {
	_ = ctx
	out := make([]domain.TaskRecord, 0)
	err := filepath.WalkDir(s.rootDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if os.IsNotExist(walkErr) {
				return nil
			}
			return walkErr
		}
		if d.IsDir() || filepath.Ext(d.Name()) != ".json" {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var record domain.TaskRecord
		if err := json.Unmarshal(raw, &record); err != nil {
			return err
		}
		if sessionID != "" && record.SessionID != sessionID {
			return nil
		}
		out = append(out, record)
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("walk task records: %w", err)
	}
	slices.SortFunc(out, func(a, b domain.TaskRecord) int {
		return a.CreatedAt.Compare(b.CreatedAt)
	})
	return out, nil
}

// UpdateTaskState updates task state while preserving other fields.
func (s *Store) UpdateTaskState(ctx context.Context, update store.TaskStateUpdate) error {
	record, err := s.GetTask(ctx, update.TaskID)
	if err != nil {
		return err
	}
	record.State = update.State
	return s.UpsertTask(ctx, record)
}
