package scheduler

import (
	"context"
	"fmt"
	"time"

	"github.com/liliang-cn/roma/internal/domain"
	"github.com/liliang-cn/roma/internal/events"
	"github.com/liliang-cn/roma/internal/store"
)

// Service implements a minimal scheduler bootstrap.
type Service struct {
	sessions store.SessionStore
	tasks    store.TaskStore
	events   store.EventStore
}

// NewService constructs a scheduler service.
func NewService(sessionStore store.SessionStore, taskStore store.TaskStore, eventStore store.EventStore) *Service {
	return &Service{
		sessions: sessionStore,
		tasks:    taskStore,
		events:   eventStore,
	}
}

// StartSession validates the session exists and emits a scheduler event.
func (s *Service) StartSession(ctx context.Context, sessionID string) error {
	if _, err := s.sessions.GetSession(ctx, sessionID); err != nil {
		return fmt.Errorf("get session: %w", err)
	}

	return s.events.AppendEvent(ctx, events.Record{
		ID:         "evt_" + sessionID + "_scheduler_started",
		SessionID:  sessionID,
		Type:       events.TypeSessionStateChanged,
		ActorType:  events.ActorTypeScheduler,
		OccurredAt: time.Now().UTC(),
		Payload: map[string]any{
			"state": domain.SessionStateRunning,
		},
	})
}

// ListReadyTasks returns tasks eligible for dispatch.
func (s *Service) ListReadyTasks(ctx context.Context, sessionID string) ([]domain.TaskRecord, error) {
	tasks, err := s.tasks.ListTasksBySession(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}

	ready := make([]domain.TaskRecord, 0, len(tasks))
	for _, task := range tasks {
		if task.State == domain.TaskStateReady {
			ready = append(ready, task)
		}
	}
	return ready, nil
}
