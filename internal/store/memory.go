package store

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"slices"
	"sync"
	"time"

	"github.com/liliang/roma/internal/domain"
	"github.com/liliang/roma/internal/events"
)

// MemoryStore is a bootstrap in-memory implementation for early wiring and tests.
type MemoryStore struct {
	mu        sync.RWMutex
	sessions  map[string]domain.SessionRecord
	tasks     map[string]domain.TaskRecord
	events    []events.Record
	artifacts map[string]ArtifactBlob
	blobs     map[string][]byte
}

// NewMemoryStore constructs an empty in-memory store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		sessions:  make(map[string]domain.SessionRecord),
		tasks:     make(map[string]domain.TaskRecord),
		artifacts: make(map[string]ArtifactBlob),
		blobs:     make(map[string][]byte),
	}
}

// AppendEvent stores an immutable event.
func (s *MemoryStore) AppendEvent(_ context.Context, event events.Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.events = append(s.events, event)
	return nil
}

// ListEvents returns filtered events.
func (s *MemoryStore) ListEvents(_ context.Context, filter EventFilter) ([]events.Record, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]events.Record, 0, len(s.events))
	for _, evt := range s.events {
		if filter.SessionID != "" && evt.SessionID != filter.SessionID {
			continue
		}
		if filter.TaskID != "" && evt.TaskID != filter.TaskID {
			continue
		}
		if filter.Type != "" && evt.Type != filter.Type {
			continue
		}
		out = append(out, evt)
	}
	return out, nil
}

// CreateSession stores a session.
func (s *MemoryStore) CreateSession(_ context.Context, sess domain.SessionRecord) error {
	if err := domain.ValidateSessionRecord(sess); err != nil {
		return fmt.Errorf("validate session: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.sessions[sess.ID]; exists {
		return fmt.Errorf("session %s already exists", sess.ID)
	}
	s.sessions[sess.ID] = sess
	return nil
}

// GetSession loads a session by id.
func (s *MemoryStore) GetSession(_ context.Context, sessionID string) (domain.SessionRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	sess, ok := s.sessions[sessionID]
	if !ok {
		return domain.SessionRecord{}, ErrNotFound
	}
	return sess, nil
}

// ListSessions returns all sessions.
func (s *MemoryStore) ListSessions(_ context.Context, _ SessionFilter) ([]domain.SessionRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]domain.SessionRecord, 0, len(s.sessions))
	for _, sess := range s.sessions {
		out = append(out, sess)
	}
	slices.SortFunc(out, func(a, b domain.SessionRecord) int {
		return a.CreatedAt.Compare(b.CreatedAt)
	})
	return out, nil
}

// UpdateSessionState updates a session state.
func (s *MemoryStore) UpdateSessionState(_ context.Context, update SessionStateUpdate) error {
	if err := domain.ValidateSessionState(update.State); err != nil {
		return fmt.Errorf("validate session state: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	sess, ok := s.sessions[update.SessionID]
	if !ok {
		return ErrNotFound
	}
	sess.State = update.State
	sess.UpdatedAt = time.Now().UTC()
	s.sessions[update.SessionID] = sess
	return nil
}

// UpsertTask stores a task record.
func (s *MemoryStore) UpsertTask(_ context.Context, task domain.TaskRecord) error {
	if err := domain.ValidateTaskRecord(task); err != nil {
		return fmt.Errorf("validate task: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.tasks[task.ID] = task
	return nil
}

// GetTask loads a task by id.
func (s *MemoryStore) GetTask(_ context.Context, taskID string) (domain.TaskRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	task, ok := s.tasks[taskID]
	if !ok {
		return domain.TaskRecord{}, ErrNotFound
	}
	return task, nil
}

// ListTasksBySession lists tasks for a session.
func (s *MemoryStore) ListTasksBySession(_ context.Context, sessionID string) ([]domain.TaskRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]domain.TaskRecord, 0, len(s.tasks))
	for _, task := range s.tasks {
		if task.SessionID == sessionID {
			out = append(out, task)
		}
	}
	slices.SortFunc(out, func(a, b domain.TaskRecord) int {
		return a.CreatedAt.Compare(b.CreatedAt)
	})
	return out, nil
}

// UpdateTaskState updates a task state.
func (s *MemoryStore) UpdateTaskState(_ context.Context, update TaskStateUpdate) error {
	if err := domain.ValidateTaskState(update.State); err != nil {
		return fmt.Errorf("validate task state: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	task, ok := s.tasks[update.TaskID]
	if !ok {
		return ErrNotFound
	}
	task.State = update.State
	task.UpdatedAt = time.Now().UTC()
	s.tasks[update.TaskID] = task
	return nil
}

// PutArtifact stores an artifact blob and metadata.
func (s *MemoryStore) PutArtifact(_ context.Context, envelope []byte, meta ArtifactIndexRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.artifacts[meta.ArtifactID] = ArtifactBlob{
		Metadata: meta,
		Content:  bytes.Clone(envelope),
	}
	return nil
}

// GetArtifact loads an artifact by id.
func (s *MemoryStore) GetArtifact(_ context.Context, artifactID string) (ArtifactBlob, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	artifact, ok := s.artifacts[artifactID]
	if !ok {
		return ArtifactBlob{}, ErrNotFound
	}
	artifact.Content = bytes.Clone(artifact.Content)
	return artifact, nil
}

// ListArtifacts lists artifact metadata by filter.
func (s *MemoryStore) ListArtifacts(_ context.Context, filter ArtifactFilter) ([]ArtifactIndexRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]ArtifactIndexRecord, 0, len(s.artifacts))
	for _, artifact := range s.artifacts {
		if filter.SessionID != "" && artifact.Metadata.SessionID != filter.SessionID {
			continue
		}
		if filter.TaskID != "" && artifact.Metadata.TaskID != filter.TaskID {
			continue
		}
		if filter.Kind != "" && artifact.Metadata.Kind != filter.Kind {
			continue
		}
		out = append(out, artifact.Metadata)
	}
	return out, nil
}

// PutBlob stores a named blob.
func (s *MemoryStore) PutBlob(_ context.Context, ref string, r io.Reader) error {
	content, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("read blob: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.blobs[ref] = content
	return nil
}

// OpenBlob opens a stored blob.
func (s *MemoryStore) OpenBlob(_ context.Context, ref string) (io.ReadCloser, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	content, ok := s.blobs[ref]
	if !ok {
		return nil, ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(bytes.Clone(content))), nil
}
