package run

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/liliang-cn/roma/internal/agents"
	"github.com/liliang-cn/roma/internal/domain"
	"github.com/liliang-cn/roma/internal/events"
	"github.com/liliang-cn/roma/internal/history"
	"github.com/liliang-cn/roma/internal/runtime"
	"github.com/liliang-cn/roma/internal/scheduler"
)

// GraphNodeRequest is the user-supplied relay graph node spec.
type GraphNodeRequest struct {
	ID              string              `json:"id"`
	Title           string              `json:"title"`
	Agent           string              `json:"agent"`
	Strategy        domain.TaskStrategy `json:"strategy"`
	Dependencies    []string            `json:"dependencies,omitempty"`
	Senators        []string            `json:"senators,omitempty"`
	Quorum          int                 `json:"quorum,omitempty"`
	ArbitrationMode string              `json:"arbitration_mode,omitempty"`
	Arbitrator      string              `json:"arbitrator,omitempty"`
}

// GraphRequest is a user-supplied graph execution request.
type GraphRequest struct {
	Prompt         string             `json:"prompt"`
	WorkingDir     string             `json:"working_dir"`
	Nodes          []GraphNodeRequest `json:"nodes"`
	SessionID      string             `json:"session_id,omitempty"`
	TaskID         string             `json:"task_id,omitempty"`
	PolicyOverride bool               `json:"policy_override,omitempty"`
	OverrideActor  string             `json:"override_actor,omitempty"`
	Continuous     bool               `json:"continuous,omitempty"`
	MaxRounds      int                `json:"max_rounds,omitempty"`
}

// LoadGraphRequest reads and validates a graph request from JSON.
func LoadGraphRequest(path string) (GraphRequest, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return GraphRequest{}, fmt.Errorf("read graph file: %w", err)
	}
	var req GraphRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return GraphRequest{}, fmt.Errorf("decode graph file: %w", err)
	}
	if req.Prompt == "" {
		return GraphRequest{}, fmt.Errorf("graph prompt is required")
	}
	if len(req.Nodes) == 0 {
		return GraphRequest{}, fmt.Errorf("graph must define at least one node")
	}
	if err := ValidateGraphRequest(req); err != nil {
		return GraphRequest{}, err
	}
	return req, nil
}

// ValidateGraphRequest validates a structured graph request in memory.
func ValidateGraphRequest(req GraphRequest) error {
	if req.Prompt == "" {
		return fmt.Errorf("graph prompt is required")
	}
	if len(req.Nodes) == 0 {
		return fmt.Errorf("graph must define at least one node")
	}
	graph := domain.TaskGraph{Nodes: make([]domain.TaskNodeSpec, 0, len(req.Nodes))}
	for _, node := range req.Nodes {
		graph.Nodes = append(graph.Nodes, domain.TaskNodeSpec{
			ID:              node.ID,
			Title:           node.Title,
			Strategy:        node.Strategy,
			Dependencies:    node.Dependencies,
			Senators:        node.Senators,
			Quorum:          node.Quorum,
			ArbitrationMode: node.ArbitrationMode,
			Arbitrator:      node.Arbitrator,
			SchemaVersion:   "v1",
		})
	}
	return graph.Validate()
}

// RunGraph executes an explicit task graph against resolved agents.
func (s *Service) RunGraph(ctx context.Context, req GraphRequest, stdout io.Writer) error {
	_, err := s.RunGraphWithResult(ctx, req, stdout)
	return err
}

// RunGraphWithResult executes an explicit task graph and returns persisted metadata.
func (s *Service) RunGraphWithResult(ctx context.Context, req GraphRequest, stdout io.Writer) (Result, error) {
	if req.WorkingDir == "" {
		return Result{}, fmt.Errorf("working directory is required")
	}
	s.history = newHistoryBackend(req.WorkingDir)
	s.events = newEventBackend(req.WorkingDir)
	s.store = newArtifactBackend(req.WorkingDir)
	s.tasks = newTaskBackend(req.WorkingDir)
	s.supervisor = runtime.NewDefaultSupervisorWithEvents(s.events)

	assignments := make([]scheduler.NodeAssignment, 0, len(req.Nodes))
	for _, node := range req.Nodes {
		profile, ok := s.registry.Resolve(ctx, node.Agent)
		if !ok {
			return Result{}, fmt.Errorf("unknown agent %q for node %q", node.Agent, node.ID)
		}
		if profile.Availability != domain.AgentAvailabilityAvailable {
			return Result{}, fmt.Errorf("agent %q is not available on PATH", profile.ID)
		}
		assignments = append(assignments, scheduler.NodeAssignment{
			Node: domain.TaskNodeSpec{
				ID:              node.ID,
				Title:           node.Title,
				Strategy:        node.Strategy,
				Dependencies:    node.Dependencies,
				Senators:        node.Senators,
				Quorum:          node.Quorum,
				ArbitrationMode: node.ArbitrationMode,
				Arbitrator:      node.Arbitrator,
				SchemaVersion:   "v1",
			},
			Profile:              profile,
			CuriaProfiles:        resolveCuriaProfiles(ctx, s.registry, node.Senators, profile.ID),
			CuriaQuorum:          node.Quorum,
			CuriaArbitrator:      resolveArbitratorProfile(ctx, s.registry, node.Arbitrator),
			CuriaArbitrationMode: node.ArbitrationMode,
			Continuous:           req.Continuous,
			MaxRounds:            req.MaxRounds,
		})
	}

	sessionID, taskID := reserveIDs("task_graph", req.SessionID, req.TaskID)
	record := history.SessionRecord{
		ID:         sessionID,
		TaskID:     taskID,
		Prompt:     req.Prompt,
		Starter:    assignments[0].Profile.ID,
		WorkingDir: req.WorkingDir,
		Status:     "running",
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}
	if req.SessionID != "" {
		if existing, err := s.history.Get(ctx, sessionID); err == nil {
			record.CreatedAt = existing.CreatedAt
		}
	}
	if _, err := s.evaluatePolicy(ctx, sessionID, taskID, "graph", req.Prompt, req.WorkingDir, req.WorkingDir, nil, assignments[0].Profile.ID, nil, len(assignments), req.PolicyOverride, req.OverrideActor); err != nil {
		return Result{}, err
	}
	if s.history != nil {
		if err := s.history.Save(ctx, record); err != nil {
			return Result{}, fmt.Errorf("save running session: %w", err)
		}
	}
	s.appendEvent(ctx, events.Record{
		ID:         "evt_" + sessionID + "_created",
		SessionID:  sessionID,
		TaskID:     taskID,
		Type:       events.TypeSessionCreated,
		ActorType:  events.ActorTypeSystem,
		OccurredAt: record.CreatedAt,
		Payload: map[string]any{
			"mode":       "graph",
			"node_count": len(assignments),
		},
	})
	dispatcher := scheduler.NewDispatcher(req.WorkingDir, s.supervisor, s.events, s.tasks)
	execResult, err := dispatcher.Execute(ctx, sessionID, req.WorkingDir, req.Prompt, assignments)
	fullAssignments := append([]scheduler.NodeAssignment(nil), assignments...)
	if err == nil {
		if updatedAssignments, updatedResult, _, dynamicErr := s.extendDynamicDelegations(ctx, sessionID, req.WorkingDir, req.Prompt, fullAssignments, execResult); dynamicErr != nil {
			fullAssignments = updatedAssignments
			execResult = updatedResult
			err = dynamicErr
		} else {
			fullAssignments = updatedAssignments
			execResult = updatedResult
		}
	}
	writeRelayResult(stdout, fullAssignments, execResult)
	for _, nodeID := range execResult.Order {
		artifact := execResult.Artifacts[nodeID]
		if s.store != nil && artifact.ID != "" {
			if saveErr := s.store.Save(ctx, artifact); saveErr != nil {
				return Result{}, fmt.Errorf("save artifact %s: %w", artifact.ID, saveErr)
			}
			s.appendArtifactStoredEvent(ctx, artifact)
		}
		for _, related := range execResult.RelatedArtifacts[nodeID] {
			if s.store != nil && related.ID != "" {
				if saveErr := s.store.Save(ctx, related); saveErr != nil {
					return Result{}, fmt.Errorf("save artifact %s: %w", related.ID, saveErr)
				}
				s.appendArtifactStoredEvent(ctx, related)
			}
		}
	}

	record.ArtifactIDs = collectRelayArtifactIDs(execResult)
	record.UpdatedAt = time.Now().UTC()
	if err != nil {
		var approvalErr *scheduler.ApprovalPendingError
		if errors.As(err, &approvalErr) {
			record.Status = "awaiting_approval"
			err = nil
		} else {
			record.Status = "failed"
		}
	} else {
		record.Status = "succeeded"
	}
	if finalID, finalErr := s.persistFinalAnswer(ctx, record, assignments[0].Profile.ID, req.Prompt, collectRelayArtifacts(execResult), err); finalErr != nil {
		return Result{}, finalErr
	} else if finalID != "" {
		record.FinalArtifactID = finalID
		record.ArtifactIDs = append(record.ArtifactIDs, finalID)
	}
	if s.history != nil {
		if saveErr := s.history.Save(ctx, record); saveErr != nil {
			return Result{}, fmt.Errorf("save completed session: %w", saveErr)
		}
	}
	s.appendSessionStateEvent(ctx, record)
	_, _ = fmt.Fprintf(stdout, "session=%s task=%s status=%s\n", record.ID, record.TaskID, record.Status)
	return Result{
		SessionID:   sessionID,
		TaskID:      taskID,
		Status:      record.Status,
		ArtifactIDs: record.ArtifactIDs,
	}, err
}

func resolveCuriaProfiles(ctx context.Context, registry *agents.Registry, names []string, fallbackAgent string) []domain.AgentProfile {
	out := make([]domain.AgentProfile, 0, len(names))
	seen := map[string]struct{}{}
	for _, name := range names {
		profile, ok := registry.Resolve(ctx, name)
		if !ok || profile.Availability != domain.AgentAvailabilityAvailable {
			continue
		}
		if _, exists := seen[profile.ID]; exists {
			continue
		}
		seen[profile.ID] = struct{}{}
		out = append(out, profile)
	}
	if len(out) > 0 {
		return out
	}
	for _, profile := range registry.WithResolvedAvailability(ctx) {
		if profile.Availability != domain.AgentAvailabilityAvailable {
			continue
		}
		if _, exists := seen[profile.ID]; exists {
			continue
		}
		seen[profile.ID] = struct{}{}
		out = append(out, profile)
	}
	if len(out) == 0 {
		if profile, ok := registry.Resolve(ctx, fallbackAgent); ok {
			out = append(out, profile)
		}
	}
	return out
}

func resolveArbitratorProfile(ctx context.Context, registry *agents.Registry, name string) domain.AgentProfile {
	if name == "" {
		return domain.AgentProfile{}
	}
	profile, ok := registry.Resolve(ctx, name)
	if !ok || profile.Availability != domain.AgentAvailabilityAvailable {
		return domain.AgentProfile{}
	}
	return profile
}
