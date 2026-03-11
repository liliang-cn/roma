package sessions

import (
	"context"
	"fmt"
	"time"

	"github.com/liliang-cn/roma/internal/domain"
	"github.com/liliang-cn/roma/internal/events"
	"github.com/liliang-cn/roma/internal/store"
)

// Service manages session and task graph registration.
type Service struct {
	sessions store.SessionStore
	tasks    store.TaskStore
	events   store.EventStore
	now      func() time.Time
}

// NewService constructs a session service.
func NewService(sessionStore store.SessionStore, taskStore store.TaskStore, eventStore store.EventStore) *Service {
	return &Service{
		sessions: sessionStore,
		tasks:    taskStore,
		events:   eventStore,
		now: func() time.Time {
			return time.Now().UTC()
		},
	}
}

// CreateSessionRequest contains creation inputs.
type CreateSessionRequest struct {
	ID          string
	Description string
}

// Snapshot is the reconstructed session view.
type Snapshot struct {
	Session domain.SessionRecord
	Tasks   []domain.TaskRecord
}

// Create creates a new session.
func (s *Service) Create(ctx context.Context, req CreateSessionRequest) (domain.SessionRecord, error) {
	record := domain.SessionRecord{
		ID:          req.ID,
		State:       domain.SessionStatePending,
		CreatedAt:   s.now(),
		UpdatedAt:   s.now(),
		Description: req.Description,
	}
	if err := s.sessions.CreateSession(ctx, record); err != nil {
		return domain.SessionRecord{}, fmt.Errorf("create session: %w", err)
	}

	if err := s.events.AppendEvent(ctx, events.Record{
		ID:         "evt_" + req.ID + "_created",
		SessionID:  record.ID,
		Type:       events.TypeSessionCreated,
		ActorType:  events.ActorTypeSystem,
		OccurredAt: s.now(),
		Payload: map[string]any{
			"state":       record.State,
			"description": record.Description,
		},
	}); err != nil {
		return domain.SessionRecord{}, fmt.Errorf("append session created event: %w", err)
	}

	return record, nil
}

// SubmitTaskGraph registers tasks for a session.
func (s *Service) SubmitTaskGraph(ctx context.Context, sessionID string, graph domain.TaskGraph) error {
	if err := graph.Validate(); err != nil {
		return fmt.Errorf("validate graph: %w", err)
	}

	for _, node := range graph.Nodes {
		state := domain.TaskStateReady
		if len(node.Dependencies) > 0 {
			state = domain.TaskStatePending
		}

		record := domain.TaskRecord{
			ID:        node.ID,
			SessionID: sessionID,
			Title:     node.Title,
			Strategy:  node.Strategy,
			State:     state,
			CreatedAt: s.now(),
			UpdatedAt: s.now(),
		}
		if err := s.tasks.UpsertTask(ctx, record); err != nil {
			return fmt.Errorf("upsert task %s: %w", node.ID, err)
		}
	}

	if err := s.events.AppendEvent(ctx, events.Record{
		ID:         "evt_" + sessionID + "_graph_submitted",
		SessionID:  sessionID,
		Type:       events.TypeTaskGraphSubmitted,
		ActorType:  events.ActorTypeHuman,
		OccurredAt: s.now(),
		Payload: map[string]any{
			"task_count": len(graph.Nodes),
		},
	}); err != nil {
		return fmt.Errorf("append graph submitted event: %w", err)
	}

	if err := s.sessions.UpdateSessionState(ctx, store.SessionStateUpdate{
		SessionID: sessionID,
		State:     domain.SessionStateRunning,
	}); err != nil {
		return fmt.Errorf("update session state: %w", err)
	}

	return nil
}

// Rebuild returns the persisted session snapshot.
func (s *Service) Rebuild(ctx context.Context, sessionID string) (Snapshot, error) {
	session, err := s.sessions.GetSession(ctx, sessionID)
	if err != nil {
		return Snapshot{}, fmt.Errorf("get session: %w", err)
	}
	tasks, err := s.tasks.ListTasksBySession(ctx, sessionID)
	if err != nil {
		return Snapshot{}, fmt.Errorf("list tasks: %w", err)
	}
	return Snapshot{
		Session: session,
		Tasks:   tasks,
	}, nil
}
