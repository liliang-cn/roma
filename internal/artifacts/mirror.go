package artifacts

import (
	"context"

	"github.com/liliang/roma/internal/domain"
)

// MirrorStore mirrors artifact persistence into multiple backends.
type MirrorStore struct {
	backends []Backend
}

// NewMirrorStore constructs a mirrored artifact backend.
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
func (s *MirrorStore) Save(ctx context.Context, envelope domain.ArtifactEnvelope) error {
	for _, item := range s.backends {
		if err := item.Save(ctx, envelope); err != nil {
			return err
		}
	}
	return nil
}

// Get loads from the first backend.
func (s *MirrorStore) Get(ctx context.Context, artifactID string) (domain.ArtifactEnvelope, error) {
	if len(s.backends) == 0 {
		return domain.ArtifactEnvelope{}, nil
	}
	var lastErr error
	for _, item := range s.backends {
		envelope, err := item.Get(ctx, artifactID)
		if err == nil {
			return envelope, nil
		}
		lastErr = err
	}
	return domain.ArtifactEnvelope{}, lastErr
}

// List loads from the first backend.
func (s *MirrorStore) List(ctx context.Context, sessionID string) ([]domain.ArtifactEnvelope, error) {
	if len(s.backends) == 0 {
		return nil, nil
	}
	var lastErr error
	for _, item := range s.backends {
		envelopes, err := item.List(ctx, sessionID)
		if err == nil && len(envelopes) > 0 {
			return envelopes, nil
		}
		if err == nil {
			lastErr = nil
			continue
		}
		lastErr = err
	}
	return nil, lastErr
}
