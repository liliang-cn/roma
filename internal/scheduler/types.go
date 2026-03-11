package scheduler

import (
	"github.com/liliang-cn/roma/internal/domain"
)

// NodeAssignment binds a task node to an agent profile and runtime settings.
type NodeAssignment struct {
	Node                 domain.TaskNodeSpec
	Profile              domain.AgentProfile
	CuriaProfiles        []domain.AgentProfile
	CuriaQuorum          int
	CuriaArbitrator      domain.AgentProfile
	CuriaArbitrationMode string
	PromptHint           string
	Continuous           bool
	MaxRounds            int
}

// DispatchResult captures scheduler-owned execution results.
type DispatchResult struct {
	Artifacts        map[string]domain.ArtifactEnvelope
	RelatedArtifacts map[string][]domain.ArtifactEnvelope
	Order            []string
}
