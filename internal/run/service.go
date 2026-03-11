package run

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/liliang-cn/roma/internal/agents"
	"github.com/liliang-cn/roma/internal/artifacts"
	"github.com/liliang-cn/roma/internal/domain"
	"github.com/liliang-cn/roma/internal/events"
	"github.com/liliang-cn/roma/internal/history"
	"github.com/liliang-cn/roma/internal/policy"
	"github.com/liliang-cn/roma/internal/runtime"
	"github.com/liliang-cn/roma/internal/scheduler"
	"github.com/liliang-cn/roma/internal/store"
	"github.com/liliang-cn/roma/internal/taskstore"
)

// Request describes a user-triggered run.
type Request struct {
	Prompt         string
	StarterAgent   string
	WorkingDir     string
	Delegates      []string
	SessionID      string
	TaskID         string
	PolicyOverride bool
	OverrideActor  string
	Continuous     bool
	MaxRounds      int
}

// Result captures persisted run metadata.
type Result struct {
	SessionID   string
	TaskID      string
	Status      string
	ArtifactIDs []string
}

// Service launches a starter coding agent for a prompt.
type Service struct {
	registry   *agents.Registry
	events     store.EventStore
	history    history.Backend
	store      artifacts.Backend
	supervisor *runtime.Supervisor
	tasks      store.TaskStore
}

// NewService constructs a run service.
func NewService(registry *agents.Registry) *Service {
	return &Service{
		registry:   registry,
		events:     nil,
		history:    nil,
		store:      nil,
		supervisor: runtime.DefaultSupervisor(),
		tasks:      nil,
	}
}

// Run starts the selected starter agent and streams its output.
func (s *Service) Run(ctx context.Context, req Request) error {
	_, err := s.RunWithResult(ctx, req)
	return err
}

// RunWithResult starts the selected starter agent and returns persisted metadata.
func (s *Service) RunWithResult(ctx context.Context, req Request) (Result, error) {
	if strings.TrimSpace(req.Prompt) == "" {
		return Result{}, fmt.Errorf("prompt is required")
	}
	profile, ok := s.registry.Resolve(ctx, req.StarterAgent)
	if !ok {
		return Result{}, fmt.Errorf("unknown agent %q", req.StarterAgent)
	}
	if profile.Availability != domain.AgentAvailabilityAvailable {
		return Result{}, fmt.Errorf("agent %q is not available on PATH", profile.ID)
	}
	if err := runtime.ValidateWorkingDir(req.WorkingDir); err != nil {
		return Result{}, err
	}
	s.history = newHistoryBackend(req.WorkingDir)
	s.events = newEventBackend(req.WorkingDir)
	s.store = newArtifactBackend(req.WorkingDir)
	s.tasks = newTaskBackend(req.WorkingDir)
	s.supervisor = runtime.NewSupervisorWithEvents(
		s.events,
		runtime.CodexAdapter{},
		runtime.ClaudeAdapter{},
		runtime.GeminiAdapter{},
		runtime.CopilotAdapter{},
	)

	delegates, err := s.resolveDelegates(ctx, req.Delegates, profile.ID)
	if err != nil {
		return Result{}, err
	}

	if len(delegates) > 0 {
		return s.runOrchestrated(ctx, req, profile, delegates, os.Stdout)
	}

	return s.runDirect(ctx, req, profile, os.Stdout, os.Stderr)
}

func (s *Service) resolveDelegates(ctx context.Context, names []string, starterID string) ([]domain.AgentProfile, error) {
	if len(names) == 0 {
		return nil, nil
	}

	delegates := make([]domain.AgentProfile, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}

		profile, ok := s.registry.Resolve(ctx, name)
		if !ok {
			return nil, fmt.Errorf("unknown delegate agent %q", name)
		}
		if profile.ID == starterID {
			continue
		}
		delegates = append(delegates, profile)
	}

	return delegates, nil
}

func (s *Service) runOrchestrated(ctx context.Context, req Request, starter domain.AgentProfile, delegates []domain.AgentProfile, w io.Writer) (Result, error) {
	sessionID, taskID := reserveIDs("task", req.SessionID, req.TaskID)
	record := history.SessionRecord{
		ID:         sessionID,
		TaskID:     taskID,
		Prompt:     req.Prompt,
		Starter:    starter.ID,
		Delegates:  req.Delegates,
		WorkingDir: req.WorkingDir,
		Status:     "running",
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}
	if record.CreatedAt.IsZero() {
		record.CreatedAt = time.Now().UTC()
	}
	if req.SessionID != "" {
		if existing, err := s.history.Get(ctx, sessionID); err == nil {
			record.CreatedAt = existing.CreatedAt
		}
	}
	if _, err := s.evaluatePolicy(ctx, sessionID, taskID, "relay", req.Prompt, req.WorkingDir, req.WorkingDir, nil, starter.ID, req.Delegates, assignmentsOrchestrated(delegates), req.PolicyOverride, req.OverrideActor); err != nil {
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
			"starter":   starter.ID,
			"delegates": req.Delegates,
		},
	})
	assignments := make([]scheduler.NodeAssignment, 0, 1+len(delegates))
	assignments = append(assignments, scheduler.NodeAssignment{
		Node: domain.TaskNodeSpec{
			ID:            taskID + "_starter",
			Title:         "Starter execution",
			Strategy:      domain.TaskStrategyDirect,
			SchemaVersion: "v1",
		},
		Profile:    starter,
		Continuous: req.Continuous,
		MaxRounds:  req.MaxRounds,
	})
	prevID := taskID + "_starter"
	for i, delegate := range delegates {
		nodeID := fmt.Sprintf("%s_delegate_%d", taskID, i+1)
		assignments = append(assignments, scheduler.NodeAssignment{
			Node: domain.TaskNodeSpec{
				ID:            nodeID,
				Title:         "Relay delegate execution",
				Strategy:      domain.TaskStrategyRelay,
				Dependencies:  []string{prevID},
				SchemaVersion: "v1",
			},
			Profile:    delegate,
			Continuous: req.Continuous,
			MaxRounds:  req.MaxRounds,
		})
		prevID = nodeID
	}

	dispatcher := scheduler.NewDispatcher(req.WorkingDir, s.supervisor, s.events, s.tasks)
	execResult, err := dispatcher.Execute(ctx, sessionID, req.WorkingDir, req.Prompt, assignments)
	if err != nil {
		var approvalErr *scheduler.ApprovalPendingError
		if errors.As(err, &approvalErr) {
			record.Status = "awaiting_approval"
		} else {
			record.Status = "failed"
		}
		record.UpdatedAt = time.Now().UTC()
		record.ArtifactIDs = collectRelayArtifactIDs(execResult)
		if s.store != nil {
			for _, nodeID := range execResult.Order {
				if artifact := execResult.Artifacts[nodeID]; artifact.ID != "" {
					_ = s.store.Save(ctx, artifact)
					s.appendArtifactStoredEvent(ctx, artifact)
				}
				for _, related := range execResult.RelatedArtifacts[nodeID] {
					if related.ID == "" {
						continue
					}
					_ = s.store.Save(ctx, related)
					s.appendArtifactStoredEvent(ctx, related)
				}
			}
		}
		if s.history != nil {
			_ = s.history.Save(ctx, record)
		}
		s.appendSessionStateEvent(ctx, record)
		writeRelayResult(w, assignments, execResult)
		return Result{
			SessionID:   sessionID,
			TaskID:      taskID,
			Status:      record.Status,
			ArtifactIDs: record.ArtifactIDs,
		}, nil
	}

	writeRelayResult(w, assignments, execResult)
	if s.store != nil {
		for _, nodeID := range execResult.Order {
			artifact := execResult.Artifacts[nodeID]
			if err := s.store.Save(ctx, artifact); err != nil {
				return Result{}, fmt.Errorf("save artifact %s: %w", artifact.ID, err)
			}
			s.appendArtifactStoredEvent(ctx, artifact)
			for _, related := range execResult.RelatedArtifacts[nodeID] {
				if err := s.store.Save(ctx, related); err != nil {
					return Result{}, fmt.Errorf("save artifact %s: %w", related.ID, err)
				}
				s.appendArtifactStoredEvent(ctx, related)
			}
		}
	}
	record.Status = "succeeded"
	record.UpdatedAt = time.Now().UTC()
	record.ArtifactIDs = collectRelayArtifactIDs(execResult)
	if s.history != nil {
		if err := s.history.Save(ctx, record); err != nil {
			return Result{}, fmt.Errorf("save completed session: %w", err)
		}
	}
	s.appendSessionStateEvent(ctx, record)
	_, _ = fmt.Fprintf(w, "session=%s task=%s status=%s\n", record.ID, record.TaskID, record.Status)
	return Result{
		SessionID:   sessionID,
		TaskID:      taskID,
		Status:      record.Status,
		ArtifactIDs: record.ArtifactIDs,
	}, nil
}

func (s *Service) runDirect(ctx context.Context, req Request, profile domain.AgentProfile, stdout, stderr io.Writer) (Result, error) {
	sessionID, taskID := reserveIDs("task", req.SessionID, req.TaskID)
	record := history.SessionRecord{
		ID:         sessionID,
		TaskID:     taskID,
		Prompt:     req.Prompt,
		Starter:    profile.ID,
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
	if _, err := s.evaluatePolicy(ctx, sessionID, taskID, "direct", req.Prompt, req.WorkingDir, req.WorkingDir, nil, profile.ID, nil, 1, req.PolicyOverride, req.OverrideActor); err != nil {
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
			"starter": profile.ID,
		},
	})
	assignments := []scheduler.NodeAssignment{{
		Node: domain.TaskNodeSpec{
			ID:            taskID,
			Title:         "Direct execution",
			Strategy:      domain.TaskStrategyDirect,
			SchemaVersion: "v1",
		},
		Profile:    profile,
		Continuous: req.Continuous,
		MaxRounds:  req.MaxRounds,
	}}
	dispatcher := scheduler.NewDispatcher(req.WorkingDir, s.supervisor, s.events, s.tasks)
	execResult, err := dispatcher.Execute(ctx, sessionID, req.WorkingDir, req.Prompt, assignments)
	fullAssignments := append([]scheduler.NodeAssignment(nil), assignments...)
	if err == nil {
		if updatedAssignments, updatedResult, dynamicDelegates, dynamicErr := s.extendDynamicDelegations(ctx, sessionID, req.WorkingDir, req.Prompt, fullAssignments, execResult); dynamicErr != nil {
			fullAssignments = updatedAssignments
			execResult = updatedResult
			err = dynamicErr
		} else {
			fullAssignments = updatedAssignments
			execResult = updatedResult
			record.Delegates = append(record.Delegates, dynamicDelegates...)
		}
	}
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
	writeRelayResult(stdout, fullAssignments, execResult)

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

func writeRelayResult(w io.Writer, assignments []scheduler.NodeAssignment, result scheduler.DispatchResult) {
	for _, nodeID := range result.Order {
		artifact := result.Artifacts[nodeID]
		assignment := findAssignment(assignments, nodeID)
		summary := artifacts.SummaryFromEnvelope(artifact)
		_, _ = fmt.Fprintf(
			w,
			"== relay node: %s (%s) ==\n%s\n",
			assignment.Profile.DisplayName,
			nodeID,
			summary,
		)
		_, _ = fmt.Fprintf(w, "artifact=%s checksum=%s\n", artifact.ID, artifact.Checksum)
	}
}

func collectRelayArtifactIDs(result scheduler.DispatchResult) []string {
	out := make([]string, 0, len(result.Order))
	for _, nodeID := range result.Order {
		if artifact := result.Artifacts[nodeID]; artifact.ID != "" {
			out = append(out, artifact.ID)
		}
		for _, related := range result.RelatedArtifacts[nodeID] {
			if related.ID != "" {
				out = append(out, related.ID)
			}
		}
	}
	return out
}

func findAssignment(assignments []scheduler.NodeAssignment, nodeID string) scheduler.NodeAssignment {
	for _, assignment := range assignments {
		if assignment.Node.ID == nodeID {
			return assignment
		}
	}
	return scheduler.NodeAssignment{}
}

func (s *Service) appendArtifactStoredEvent(ctx context.Context, artifact domain.ArtifactEnvelope) {
	s.appendEvent(ctx, events.Record{
		ID:         "evt_" + artifact.ID + "_stored",
		SessionID:  artifact.SessionID,
		TaskID:     artifact.TaskID,
		Type:       events.TypeArtifactStored,
		ActorType:  events.ActorTypeSystem,
		OccurredAt: time.Now().UTC(),
		Payload: map[string]any{
			"artifact_id":     artifact.ID,
			"kind":            artifact.Kind,
			"producer_agent":  artifact.Producer.AgentID,
			"payload_schema":  artifact.PayloadSchema,
			"schema_version":  artifact.SchemaVersion,
			"artifact_checks": artifact.Checksum,
		},
	})
}

func (s *Service) appendSessionStateEvent(ctx context.Context, record history.SessionRecord) {
	s.appendEvent(ctx, events.Record{
		ID:         "evt_" + record.ID + "_state_" + record.Status,
		SessionID:  record.ID,
		TaskID:     record.TaskID,
		Type:       events.TypeSessionStateChanged,
		ActorType:  events.ActorTypeSystem,
		OccurredAt: record.UpdatedAt,
		ReasonCode: record.Status,
		Payload: map[string]any{
			"starter":      record.Starter,
			"delegate_cnt": len(record.Delegates),
			"artifact_ids": record.ArtifactIDs,
		},
	})
}

func (s *Service) appendEvent(ctx context.Context, event events.Record) {
	if s.events == nil {
		return
	}
	_ = s.events.AppendEvent(ctx, event)
}

func resultLabel(err error) string {
	if err != nil {
		return "failed"
	}
	return "success"
}

func (s *Service) evaluatePolicy(ctx context.Context, sessionID, taskID, mode, prompt, workingDir, effectiveDir string, pathHints []string, starter string, delegates []string, nodeCount int, policyOverride bool, overrideActor string) (policy.Decision, error) {
	if policyOverride && strings.TrimSpace(overrideActor) == "" {
		overrideActor = policy.OverrideActor()
	}
	decision, err := policy.NewSimpleBroker(s.events).Evaluate(ctx, policy.Request{
		SessionID:      sessionID,
		TaskID:         taskID,
		Mode:           mode,
		Prompt:         prompt,
		WorkingDir:     workingDir,
		EffectiveDir:   effectiveDir,
		PathHints:      pathHints,
		StarterAgent:   starter,
		Delegates:      delegates,
		NodeCount:      nodeCount,
		PolicyOverride: policyOverride,
		OverrideActor:  overrideActor,
	})
	if err != nil {
		return policy.Decision{}, err
	}
	if decision.Kind == policy.DecisionBlock {
		return decision, fmt.Errorf("policy blocked execution: %s", decision.Reason)
	}
	return decision, nil
}

func assignmentsOrchestrated(delegates []domain.AgentProfile) int {
	return 1 + len(delegates)
}

func newHistoryBackend(workDir string) history.Backend {
	fileStore := history.NewStore(workDir)
	sqliteStore, err := history.NewSQLiteStore(workDir)
	if err != nil {
		return fileStore
	}
	return history.NewMirrorStore(fileStore, sqliteStore)
}

func newEventBackend(workDir string) store.EventStore {
	fileStore := store.NewFileEventStore(workDir)
	sqliteStore, err := store.NewSQLiteEventStore(workDir)
	if err != nil {
		return fileStore
	}
	return store.NewMultiEventStore(fileStore, sqliteStore)
}

func newTaskBackend(workDir string) store.TaskStore {
	fileStore := taskstore.NewStore(workDir)
	sqliteStore, err := taskstore.NewSQLiteStore(workDir)
	if err != nil {
		return fileStore
	}
	return taskstore.NewMirrorStore(fileStore, sqliteStore)
}

func newArtifactBackend(workDir string) artifacts.Backend {
	fileStore := artifacts.NewFileStore(workDir)
	sqliteStore, err := artifacts.NewSQLiteStore(workDir)
	if err != nil {
		return fileStore
	}
	return artifacts.NewMirrorStore(sqliteStore, fileStore)
}

func newID(prefix string) string {
	return fmt.Sprintf("%s_%d", prefix, time.Now().UTC().UnixNano())
}

func reserveIDs(taskPrefix, sessionID, taskID string) (string, string) {
	if sessionID == "" {
		sessionID = newID("sess")
	}
	if taskID == "" {
		taskID = newID(taskPrefix)
	}
	return sessionID, taskID
}
