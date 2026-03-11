package taskstore

import (
	"context"

	"github.com/liliang/roma/internal/domain"
	"github.com/liliang/roma/internal/store"
)

// MirrorStore writes task records into multiple backends.
type MirrorStore struct {
	stores []store.TaskStore
}

// NewMirrorStore constructs a mirrored task store.
func NewMirrorStore(stores ...store.TaskStore) *MirrorStore {
	out := make([]store.TaskStore, 0, len(stores))
	for _, item := range stores {
		if item != nil {
			out = append(out, item)
		}
	}
	return &MirrorStore{stores: out}
}

// UpsertTask mirrors task persistence.
func (s *MirrorStore) UpsertTask(ctx context.Context, task domain.TaskRecord) error {
	for _, item := range s.stores {
		if err := item.UpsertTask(ctx, task); err != nil {
			return err
		}
	}
	return nil
}

// GetTask loads from the first configured backend.
func (s *MirrorStore) GetTask(ctx context.Context, taskID string) (domain.TaskRecord, error) {
	if len(s.stores) == 0 {
		return domain.TaskRecord{}, store.ErrNotFound
	}
	return s.stores[0].GetTask(ctx, taskID)
}

// ListTasksBySession loads from the first configured backend.
func (s *MirrorStore) ListTasksBySession(ctx context.Context, sessionID string) ([]domain.TaskRecord, error) {
	if len(s.stores) == 0 {
		return nil, nil
	}
	return s.stores[0].ListTasksBySession(ctx, sessionID)
}

// UpdateTaskState mirrors task state changes.
func (s *MirrorStore) UpdateTaskState(ctx context.Context, update store.TaskStateUpdate) error {
	for _, item := range s.stores {
		if err := item.UpdateTaskState(ctx, update); err != nil {
			return err
		}
	}
	return nil
}
