package store

import (
	"context"
	"errors"
	"io"

	"github.com/liliang-cn/roma/internal/domain"
	"github.com/liliang-cn/roma/internal/events"
)

var (
	// ErrNotFound indicates a record is missing.
	ErrNotFound = errors.New("not found")
)

// EventFilter scopes event queries.
type EventFilter struct {
	SessionID string
	TaskID    string
	Type      events.Type
}

// SessionFilter scopes session queries.
type SessionFilter struct{}

// ArtifactFilter scopes artifact queries.
type ArtifactFilter struct {
	SessionID string
	TaskID    string
	Kind      domain.ArtifactKind
}

// SessionStateUpdate updates a session state.
type SessionStateUpdate struct {
	SessionID string
	State     domain.SessionState
}

// TaskStateUpdate updates a task state.
type TaskStateUpdate struct {
	TaskID string
	State  domain.TaskState
}

// ArtifactIndexRecord stores artifact metadata.
type ArtifactIndexRecord struct {
	ArtifactID      string
	Kind            domain.ArtifactKind
	PayloadSchema   string
	SessionID       string
	TaskID          string
	ProducerAgentID string
	ProducerRole    domain.ProducerRole
	Checksum        string
	BlobRef         string
}

// ArtifactBlob stores raw artifact bytes and metadata.
type ArtifactBlob struct {
	Metadata ArtifactIndexRecord
	Content  []byte
}

// EventStore persists immutable events.
type EventStore interface {
	AppendEvent(ctx context.Context, event events.Record) error
	ListEvents(ctx context.Context, filter EventFilter) ([]events.Record, error)
}

// SessionStore persists sessions.
type SessionStore interface {
	CreateSession(ctx context.Context, sess domain.SessionRecord) error
	GetSession(ctx context.Context, sessionID string) (domain.SessionRecord, error)
	ListSessions(ctx context.Context, filter SessionFilter) ([]domain.SessionRecord, error)
	UpdateSessionState(ctx context.Context, update SessionStateUpdate) error
}

// TaskStore persists task records.
type TaskStore interface {
	UpsertTask(ctx context.Context, task domain.TaskRecord) error
	GetTask(ctx context.Context, taskID string) (domain.TaskRecord, error)
	ListTasksBySession(ctx context.Context, sessionID string) ([]domain.TaskRecord, error)
	UpdateTaskState(ctx context.Context, update TaskStateUpdate) error
}

// ArtifactStore persists artifacts.
type ArtifactStore interface {
	PutArtifact(ctx context.Context, envelope []byte, meta ArtifactIndexRecord) error
	GetArtifact(ctx context.Context, artifactID string) (ArtifactBlob, error)
	ListArtifacts(ctx context.Context, filter ArtifactFilter) ([]ArtifactIndexRecord, error)
}

// BlobStore persists auxiliary blobs.
type BlobStore interface {
	PutBlob(ctx context.Context, ref string, r io.Reader) error
	OpenBlob(ctx context.Context, ref string) (io.ReadCloser, error)
}
