package artifacts

import (
	"context"

	"github.com/liliang-cn/roma/internal/domain"
)

// Backend captures artifact persistence used by ROMA.
type Backend interface {
	Save(ctx context.Context, envelope domain.ArtifactEnvelope) error
	Get(ctx context.Context, artifactID string) (domain.ArtifactEnvelope, error)
	List(ctx context.Context, sessionID string) ([]domain.ArtifactEnvelope, error)
}
