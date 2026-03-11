package history

import "context"

// Backend captures the session history capabilities used by ROMA execution paths.
type Backend interface {
	Save(ctx context.Context, record SessionRecord) error
	Get(ctx context.Context, sessionID string) (SessionRecord, error)
	List(ctx context.Context) ([]SessionRecord, error)
	RecoverInterrupted(ctx context.Context) error
}

// MirrorStore writes the same session record into multiple backends.
type MirrorStore struct {
	backends []Backend
}

// NewMirrorStore constructs a mirrored session history backend.
func NewMirrorStore(backends ...Backend) *MirrorStore {
	out := make([]Backend, 0, len(backends))
	for _, item := range backends {
		if item != nil {
			out = append(out, item)
		}
	}
	return &MirrorStore{backends: out}
}

// Save persists to every configured backend.
func (s *MirrorStore) Save(ctx context.Context, record SessionRecord) error {
	for _, item := range s.backends {
		if err := item.Save(ctx, record); err != nil {
			return err
		}
	}
	return nil
}

// Get loads from the first backend.
func (s *MirrorStore) Get(ctx context.Context, sessionID string) (SessionRecord, error) {
	if len(s.backends) == 0 {
		return SessionRecord{}, nil
	}
	return s.backends[0].Get(ctx, sessionID)
}

// List loads from the first backend.
func (s *MirrorStore) List(ctx context.Context) ([]SessionRecord, error) {
	if len(s.backends) == 0 {
		return nil, nil
	}
	return s.backends[0].List(ctx)
}

// RecoverInterrupted updates every configured backend.
func (s *MirrorStore) RecoverInterrupted(ctx context.Context) error {
	for _, item := range s.backends {
		if err := item.RecoverInterrupted(ctx); err != nil {
			return err
		}
	}
	return nil
}
