package run

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/liliang-cn/roma/internal/agents"
	"github.com/liliang-cn/roma/internal/artifacts"
	"github.com/liliang-cn/roma/internal/classifier"
	"github.com/liliang-cn/roma/internal/domain"
	"github.com/liliang-cn/roma/internal/events"
	"github.com/liliang-cn/roma/internal/history"
	"github.com/liliang-cn/roma/internal/policy"
	"github.com/liliang-cn/roma/internal/runtime"
	"github.com/liliang-cn/roma/internal/scheduler"
	"github.com/liliang-cn/roma/internal/store"
	"github.com/liliang-cn/roma/internal/taskstore"
	workspacepkg "github.com/liliang-cn/roma/internal/workspace"
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
	controlDir string
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

// ReloadUserConfig refreshes the runner registry from the configured user agent config path.
func (s *Service) ReloadUserConfig() error {
	if s == nil || s.registry == nil {
		return nil
	}
	path := strings.TrimSpace(s.registry.UserConfigPath())
	if path == "" {
		return nil
	}
	return s.registry.LoadUserConfig(path)
}

// SetControlDir sets the persisted ROMA control-plane directory.
func (s *Service) SetControlDir(dir string) {
	s.controlDir = strings.TrimSpace(dir)
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
	s.history = s.newHistoryBackend(req.WorkingDir)
	s.events = s.newEventBackend(req.WorkingDir)
	s.store = s.newArtifactBackend(req.WorkingDir)
	s.tasks = s.newTaskBackend(req.WorkingDir)
	s.supervisor = s.newSupervisor(req.WorkingDir)

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
	helpOutputs := make(map[string]string, len(delegates))
	for _, d := range delegates {
		helpOutputs[d.ID] = probeAgentHelp(ctx, d)
	}
	assignments := buildOrchestratedAssignments(taskID, starter, delegates, req.Continuous, req.MaxRounds, helpOutputs)
	if upgraded, reasons := s.maybePromoteOrchestratedToCuria(ctx, req.Prompt, req.WorkingDir, taskID, starter, delegates, req.Continuous, req.MaxRounds); len(upgraded) > 0 {
		assignments = upgraded
		s.appendEvent(ctx, events.Record{
			ID:         "evt_" + sessionID + "_auto_curia",
			SessionID:  sessionID,
			TaskID:     taskID,
			Type:       events.TypeTaskGraphSubmitted,
			ActorType:  events.ActorTypeScheduler,
			OccurredAt: time.Now().UTC(),
			ReasonCode: "auto_curia_upgrade",
			Payload: map[string]any{
				"reasons": reasons,
				"mode":    "relay",
			},
		})
	}

	dispatcher := scheduler.NewDispatcherWithControlDir(req.WorkingDir, s.controlRoot(req.WorkingDir), s.supervisor, s.events, s.tasks)
	execResult, err := dispatcher.Execute(ctx, sessionID, req.WorkingDir, req.Prompt, assignments)
	if err == nil && len(delegates) > 0 {
		if updatedAssignments, updatedResult, caesarErr := s.continueCaesarCoordination(ctx, req, sessionID, taskID, starter, assignments, execResult, dispatcher); caesarErr != nil {
			assignments = updatedAssignments
			execResult = updatedResult
			err = caesarErr
		} else {
			assignments = updatedAssignments
			execResult = updatedResult
		}
	}
	writeRelayResult(w, assignments, execResult)
	if s.store != nil {
		for _, nodeID := range execResult.Order {
			artifact := execResult.Artifacts[nodeID]
			if artifact.ID != "" {
				if saveErr := s.store.Save(ctx, artifact); saveErr != nil {
					return Result{}, fmt.Errorf("save artifact %s: %w", artifact.ID, saveErr)
				}
				s.appendArtifactStoredEvent(ctx, artifact)
			}
			for _, related := range execResult.RelatedArtifacts[nodeID] {
				if related.ID == "" {
					continue
				}
				if saveErr := s.store.Save(ctx, related); saveErr != nil {
					return Result{}, fmt.Errorf("save artifact %s: %w", related.ID, saveErr)
				}
				s.appendArtifactStoredEvent(ctx, related)
			}
		}
	}

	runErr := err
	if err != nil {
		var approvalErr *scheduler.ApprovalPendingError
		if errors.As(err, &approvalErr) {
			record.Status = "awaiting_approval"
			runErr = nil
		} else {
			record.Status = "failed"
		}
	} else {
		s.handleMergeBackRequests(ctx, req.WorkingDir, collectRelayArtifacts(execResult))
		record.Status = "succeeded"
	}
	record.UpdatedAt = time.Now().UTC()
	record.ArtifactIDs = collectRelayArtifactIDs(execResult)
	if finalID, finalErr := s.persistFinalAnswer(ctx, record, starter.ID, req.Prompt, collectRelayArtifacts(execResult), runErr); finalErr != nil {
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
	_, _ = fmt.Fprintf(w, "session=%s task=%s status=%s\n", record.ID, record.TaskID, record.Status)
	return Result{
		SessionID:   sessionID,
		TaskID:      taskID,
		Status:      record.Status,
		ArtifactIDs: record.ArtifactIDs,
	}, runErr
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
		Profile:          profile,
		SemanticReviewer: profile,
		Continuous:       req.Continuous,
		MaxRounds:        req.MaxRounds,
	}}
	dispatcher := scheduler.NewDispatcherWithControlDir(req.WorkingDir, s.controlRoot(req.WorkingDir), s.supervisor, s.events, s.tasks)
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
	s.handleMergeBackRequests(ctx, req.WorkingDir, collectRelayArtifacts(execResult))
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
	if finalID, finalErr := s.persistFinalAnswer(ctx, record, profile.ID, req.Prompt, collectRelayArtifacts(execResult), err); finalErr != nil {
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

func collectRelayArtifacts(result scheduler.DispatchResult) []domain.ArtifactEnvelope {
	out := make([]domain.ArtifactEnvelope, 0, len(result.Order))
	for _, nodeID := range result.Order {
		if artifact := result.Artifacts[nodeID]; artifact.ID != "" {
			out = append(out, artifact)
		}
		for _, related := range result.RelatedArtifacts[nodeID] {
			if related.ID != "" {
				out = append(out, related)
			}
		}
	}
	return out
}

func (s *Service) persistFinalAnswer(ctx context.Context, record history.SessionRecord, starterID, prompt string, related []domain.ArtifactEnvelope, runErr error) (string, error) {
	if s.store == nil {
		return "", nil
	}
	runID := record.TaskID
	if runID == "" {
		runID = record.ID
	}
	envelope, err := artifacts.NewService().BuildFinalAnswer(ctx, artifacts.BuildFinalAnswerRequest{
		SessionID:    record.ID,
		TaskID:       record.TaskID,
		RunID:        runID,
		Status:       record.Status,
		Prompt:       prompt,
		StarterAgent: starterID,
		Artifacts:    related,
		Err:          runErr,
	})
	if err != nil {
		return "", fmt.Errorf("build final answer: %w", err)
	}
	if err := s.store.Save(ctx, envelope); err != nil {
		return "", fmt.Errorf("save final answer %s: %w", envelope.ID, err)
	}
	s.appendArtifactStoredEvent(ctx, envelope)
	return envelope.ID, nil
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
	if len(delegates) == 0 {
		return 1
	}
	return 1 + len(delegates)
}

func buildOrchestratedAssignments(taskID string, starter domain.AgentProfile, delegates []domain.AgentProfile, continuous bool, maxRounds int, helpOutputs map[string]string) []scheduler.NodeAssignment {
	if len(delegates) == 0 {
		return []scheduler.NodeAssignment{{
			Node: domain.TaskNodeSpec{
				ID:            taskID + "_starter",
				Title:         "Starter execution",
				Strategy:      domain.TaskStrategyDirect,
				SchemaVersion: "v1",
			},
			Profile:          starter,
			SemanticReviewer: starter,
			Continuous:       continuous,
			MaxRounds:        maxRounds,
		}}
	}

	assignments := make([]scheduler.NodeAssignment, 0, 2+len(delegates))

	clarifyNodeID := taskID + "_starter_clarify"
	assignments = append(assignments, scheduler.NodeAssignment{
		Node: domain.TaskNodeSpec{
			ID:            clarifyNodeID,
			Title:         "Starter prompt clarification",
			Strategy:      domain.TaskStrategyDirect,
			SchemaVersion: "v1",
		},
		Profile:          starter,
		SemanticReviewer: starter,
		Continuous:       continuous,
		MaxRounds:        maxRounds,
		PromptHint:       buildStarterClarifyPromptHint(starter, delegates, helpOutputs),
	})

	bootstrapNodeID := taskID + "_starter_bootstrap"
	assignments = append(assignments, scheduler.NodeAssignment{
		Node: domain.TaskNodeSpec{
			ID:            bootstrapNodeID,
			Title:         "Starter Caesar coordination",
			Strategy:      domain.TaskStrategyDirect,
			Dependencies:  []string{clarifyNodeID},
			SchemaVersion: "v1",
		},
		Profile:          starter,
		SemanticReviewer: starter,
		Continuous:       continuous,
		MaxRounds:        maxRounds,
		PromptHint:       buildStarterBootstrapPromptHint(starter, delegates, helpOutputs),
	})
	for i, delegate := range delegates {
		nodeID := fmt.Sprintf("%s_delegate_%d", taskID, i+1)
		assignments = append(assignments, scheduler.NodeAssignment{
			Node: domain.TaskNodeSpec{
				ID:            nodeID,
				Title:         "Concurrent delegate execution",
				Strategy:      domain.TaskStrategyRelay,
				Dependencies:  []string{bootstrapNodeID},
				SchemaVersion: "v1",
			},
			Profile:          delegate,
			SemanticReviewer: starter,
			Continuous:       continuous,
			MaxRounds:        maxRounds,
			PromptHint:       buildCaesarDelegatePromptHint(starter, ""),
		})
	}
	return assignments
}

func buildStarterClarifyPromptHint(starter domain.AgentProfile, delegates []domain.AgentProfile, helpOutputs map[string]string) string {
	lines := []string{
		fmt.Sprintf("You are %s, the coordinating starter agent.", starter.DisplayName),
		"Your task is to rewrite the user input into a clear, structured task specification.",
		"Do not implement the task yourself. Only produce the enhanced specification.",
		"",
		"Output a markdown document with the following sections:",
		"1. **Objective** – a concise statement of the overall goal",
		"2. **Constraints** – any explicit or implicit constraints (languages, frameworks, style, etc.)",
		"3. **Scope per delegate** – for each delegate agent, describe their specific area of responsibility",
		"4. **Expected deliverables** – what each delegate should produce",
		"",
		"Keep the specification clear and unambiguous so the delegate agents can execute with minimal overlap.",
	}
	if len(delegates) > 0 {
		lines = append(lines, "", "Available delegate agents:")
		for _, delegate := range delegates {
			summary := "- " + delegateAutomationSummary(delegate)
			if out := strings.TrimSpace(helpOutputs[delegate.ID]); out != "" {
				summary += "\n  capability probe output:\n"
				for _, hl := range strings.Split(out, "\n") {
					summary += "    " + hl + "\n"
				}
			}
			lines = append(lines, summary)
		}
	}
	return strings.Join(lines, "\n")
}

func buildStarterBootstrapPromptHint(starter domain.AgentProfile, delegates []domain.AgentProfile, helpOutputs map[string]string) string {
	lines := []string{
		fmt.Sprintf("You are Caesar, the coordinating starter agent (%s).", starter.DisplayName),
		"You do not implement the task yourself.",
		"The upstream clarify node has already produced a structured task specification; use it as the basis for your work assignment plan.",
		"Your job is to produce the shared bootstrap plan for the delegate agents and coordinate their work.",
		"Explicitly summarize how work should be split so the later parallel agents can execute with minimal overlap.",
		"Keep ownership of coordination, progress tracking, and follow-up questions; delegate concrete implementation.",
		"You may run `<command> --help` (or any healthcheck command) yourself if you need more detail about an agent's capabilities.",
	}
	if len(delegates) > 0 {
		names := make([]string, 0, len(delegates))
		for _, delegate := range delegates {
			names = append(names, fmt.Sprintf("%s (%s)", delegate.DisplayName, delegate.ID))
		}
		lines = append(lines, "Delegate agents: "+strings.Join(names, ", "))
		lines = append(lines, "Known delegate profiles:")
		for _, delegate := range delegates {
			summary := "- " + delegateAutomationSummary(delegate)
			if out := strings.TrimSpace(helpOutputs[delegate.ID]); out != "" {
				summary += "\n  capability probe output:\n"
				for _, hl := range strings.Split(out, "\n") {
					summary += "    " + hl + "\n"
				}
			}
			lines = append(lines, summary)
		}
	}
	return strings.Join(lines, "\n")
}

// probeAgentHelp runs the agent's healthcheck command and returns the first 30
// lines of combined stdout/stderr output. Returns empty string on any error or
// when no healthcheck args are configured. The probe runs with a 5-second timeout.
func probeAgentHelp(ctx context.Context, profile domain.AgentProfile) string {
	if profile.Command == "" || len(profile.HealthcheckArgs) == 0 {
		return ""
	}
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(probeCtx, profile.Command, profile.HealthcheckArgs...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	_ = cmd.Run()
	raw := strings.TrimSpace(buf.String())
	if raw == "" {
		return ""
	}
	// Return at most 30 lines to keep the prompt concise.
	allLines := strings.Split(raw, "\n")
	if len(allLines) > 30 {
		allLines = allLines[:30]
	}
	return strings.Join(allLines, "\n")
}

func delegateAutomationSummary(profile domain.AgentProfile) string {
	parts := []string{fmt.Sprintf("%s (%s)", profile.DisplayName, profile.ID)}
	if profile.Command != "" {
		parts = append(parts, "command="+filepath.Base(profile.Command))
	}
	if len(profile.Capabilities) > 0 {
		parts = append(parts, "capabilities="+strings.Join(profile.Capabilities, ","))
	}
	if len(profile.Args) > 0 {
		parts = append(parts, "args="+strings.Join(profile.Args, " "))
	}
	if profile.UsePTY {
		parts = append(parts, "pty=true")
	}
	if profile.SupportsMCP {
		parts = append(parts, "mcp=true")
	}
	if profile.SupportsJSONOutput {
		parts = append(parts, "json=true")
	}
	return strings.Join(parts, " | ")
}

func (s *Service) handleMergeBackRequests(ctx context.Context, workDir string, items []domain.ArtifactEnvelope) {
	if strings.TrimSpace(workDir) == "" {
		return
	}
	manager := workspacepkg.NewManager(s.controlRoot(workDir), s.events)
	for _, envelope := range items {
		request, ok := artifacts.MergeBackRequestFromEnvelope(envelope)
		if !ok {
			continue
		}
		sessionID := request.WorkspaceSessionID
		if strings.TrimSpace(sessionID) == "" {
			sessionID = envelope.SessionID
		}
		taskID := request.WorkspaceTaskID
		if strings.TrimSpace(taskID) == "" {
			taskID = envelope.TaskID
		}
		s.appendEvent(ctx, events.Record{
			ID:         fmt.Sprintf("evt_%s_%s_merge_back_requested", sessionID, taskID),
			SessionID:  sessionID,
			TaskID:     taskID,
			Type:       events.TypeMergeBackRequested,
			ActorType:  events.ActorTypeSystem,
			OccurredAt: time.Now().UTC(),
			ReasonCode: string(request.RecommendedMode),
			Payload: map[string]any{
				"source_artifact_id":      envelope.ID,
				"source_agent_id":         envelope.Producer.AgentID,
				"recommended_mode":        request.RecommendedMode,
				"reason":                  request.Reason,
				"requested_changed_files": request.ChangedFiles,
			},
		})
		if request.RecommendedMode != artifacts.MergeBackModeDirectMerge {
			continue
		}
		prepared, err := manager.Get(ctx, sessionID, taskID)
		if err != nil {
			s.appendMergeBackRejected(ctx, sessionID, taskID, "workspace_not_found", request, nil, err)
			continue
		}
		changedPaths, err := manager.ChangedPaths(ctx, prepared)
		if err != nil {
			s.appendMergeBackRejected(ctx, sessionID, taskID, "changed_paths_error", request, nil, err)
			continue
		}
		if len(changedPaths) == 0 {
			s.appendMergeBackRejected(ctx, sessionID, taskID, "no_changed_files", request, nil, nil)
			continue
		}
		decision := policy.EvaluatePathAction(policy.ActionPlanApply, changedPaths, false, "")
		if decision.Kind == policy.DecisionBlock {
			s.appendMergeBackRejected(ctx, sessionID, taskID, decision.Reason, request, changedPaths, nil)
			continue
		}
		preview, err := manager.PreviewMerge(ctx, prepared)
		if err != nil {
			s.appendMergeBackRejected(ctx, sessionID, taskID, "preview_error", request, changedPaths, err)
			continue
		}
		if !preview.CanApply || preview.Conflict {
			s.appendMergeBackRejected(ctx, sessionID, taskID, "merge_conflict", request, changedPaths, nil)
			continue
		}
		if err := manager.MergeBackAs(ctx, prepared, events.ActorTypeSystem); err != nil {
			s.appendMergeBackRejected(ctx, sessionID, taskID, "merge_back_failed", request, changedPaths, err)
			continue
		}
	}
}

func (s *Service) appendMergeBackRejected(ctx context.Context, sessionID, taskID, reason string, request artifacts.MergeBackRequest, changed []string, err error) {
	payload := map[string]any{
		"recommended_mode":        request.RecommendedMode,
		"reason":                  request.Reason,
		"requested_changed_files": request.ChangedFiles,
		"actual_changed_files":    changed,
	}
	if err != nil {
		payload["error"] = err.Error()
	}
	s.appendEvent(ctx, events.Record{
		ID:         fmt.Sprintf("evt_%s_%s_merge_back_rejected_%d", sessionID, taskID, time.Now().UTC().UnixNano()),
		SessionID:  sessionID,
		TaskID:     taskID,
		Type:       events.TypeMergeBackRejected,
		ActorType:  events.ActorTypeSystem,
		OccurredAt: time.Now().UTC(),
		ReasonCode: reason,
		Payload:    payload,
	})
}

func (s *Service) controlRoot(workDir string) string {
	if s != nil && strings.TrimSpace(s.controlDir) != "" {
		return s.controlDir
	}
	return workDir
}

func (s *Service) newHistoryBackend(workDir string) history.Backend {
	controlDir := s.controlRoot(workDir)
	fileStore := history.NewStore(controlDir)
	sqliteStore, err := history.NewSQLiteStore(controlDir)
	if err != nil {
		return fileStore
	}
	return history.NewMirrorStore(fileStore, sqliteStore)
}

func (s *Service) newEventBackend(workDir string) store.EventStore {
	controlDir := s.controlRoot(workDir)
	fileStore := store.NewFileEventStore(controlDir)
	sqliteStore, err := store.NewSQLiteEventStore(controlDir)
	if err != nil {
		return fileStore
	}
	return store.NewMultiEventStore(fileStore, sqliteStore)
}

func (s *Service) newTaskBackend(workDir string) store.TaskStore {
	controlDir := s.controlRoot(workDir)
	fileStore := taskstore.NewStore(controlDir)
	sqliteStore, err := taskstore.NewSQLiteStore(controlDir)
	if err != nil {
		return fileStore
	}
	return taskstore.NewMirrorStore(fileStore, sqliteStore)
}

func (s *Service) newArtifactBackend(workDir string) artifacts.Backend {
	controlDir := s.controlRoot(workDir)
	fileStore := artifacts.NewFileStore(controlDir)
	sqliteStore, err := artifacts.NewSQLiteStore(controlDir)
	if err != nil {
		return fileStore
	}
	return artifacts.NewMirrorStore(sqliteStore, fileStore)
}

func (s *Service) newSupervisor(_ string) *runtime.Supervisor {
	supervisor := runtime.NewDefaultSupervisorWithEvents(s.events)
	supervisor.SetSemanticAnalyzer(classifier.NewAgentAnalyzer(runtime.DefaultSupervisor(), s.store, s.events))
	return supervisor
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
