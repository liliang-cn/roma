package syncdb

import (
	"context"

	"github.com/liliang-cn/roma/internal/artifacts"
	"github.com/liliang-cn/roma/internal/history"
	"github.com/liliang-cn/roma/internal/queue"
	"github.com/liliang-cn/roma/internal/store"
	"github.com/liliang-cn/roma/internal/taskstore"
)

// Workspace imports file-backed metadata into SQLite so newer read paths can use one store.
type Workspace struct {
	workDir string
}

// NewWorkspace constructs a workspace sync helper.
func NewWorkspace(workDir string) *Workspace {
	return &Workspace{workDir: workDir}
}

// Run imports sessions, tasks, events, queue items, and artifact envelopes into SQLite.
func (w *Workspace) Run(ctx context.Context) error {
	if err := w.syncSessions(ctx); err != nil {
		return err
	}
	if err := w.syncTasks(ctx); err != nil {
		return err
	}
	if err := w.syncEvents(ctx); err != nil {
		return err
	}
	if err := w.syncQueue(ctx); err != nil {
		return err
	}
	if err := w.syncArtifacts(ctx); err != nil {
		return err
	}
	return nil
}

func (w *Workspace) syncSessions(ctx context.Context) error {
	fileStore := history.NewStore(w.workDir)
	sqliteStore, err := history.NewSQLiteStore(w.workDir)
	if err != nil {
		return nil
	}
	items, err := fileStore.List(ctx)
	if err != nil {
		return err
	}
	for _, item := range items {
		if err := sqliteStore.Save(ctx, item); err != nil {
			return err
		}
	}
	return nil
}

func (w *Workspace) syncTasks(ctx context.Context) error {
	fileStore := taskstore.NewStore(w.workDir)
	sqliteStore, err := taskstore.NewSQLiteStore(w.workDir)
	if err != nil {
		return nil
	}
	items, err := fileStore.ListTasksBySession(ctx, "")
	if err != nil {
		return err
	}
	for _, item := range items {
		if err := sqliteStore.UpsertTask(ctx, item); err != nil {
			return err
		}
	}
	return nil
}

func (w *Workspace) syncEvents(ctx context.Context) error {
	fileStore := store.NewFileEventStore(w.workDir)
	sqliteStore, err := store.NewSQLiteEventStore(w.workDir)
	if err != nil {
		return nil
	}
	items, err := fileStore.ListEvents(ctx, store.EventFilter{})
	if err != nil {
		return err
	}
	for _, item := range items {
		if err := sqliteStore.AppendEvent(ctx, item); err != nil {
			return err
		}
	}
	return nil
}

func (w *Workspace) syncQueue(ctx context.Context) error {
	fileStore := queue.NewStore(w.workDir)
	sqliteStore, err := queue.NewSQLiteStore(w.workDir)
	if err != nil {
		return nil
	}
	items, err := fileStore.List(ctx)
	if err != nil {
		return err
	}
	for _, item := range items {
		if err := sqliteStore.UpsertExact(ctx, item); err != nil {
			return err
		}
	}
	return nil
}

func (w *Workspace) syncArtifacts(ctx context.Context) error {
	fileStore := artifacts.NewFileStore(w.workDir)
	sqliteStore, err := artifacts.NewSQLiteStore(w.workDir)
	if err != nil {
		return nil
	}
	items, err := fileStore.List(ctx, "")
	if err != nil {
		return err
	}
	for _, item := range items {
		if err := sqliteStore.Save(ctx, item); err != nil {
			return err
		}
	}
	return nil
}
