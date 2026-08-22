package run

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/liliang-cn/tagit/internal/agents"
	"github.com/liliang-cn/tagit/internal/artifacts"
	"github.com/liliang-cn/tagit/internal/classifier"
	"github.com/liliang-cn/tagit/internal/domain"
	"github.com/liliang-cn/tagit/internal/events"
	"github.com/liliang-cn/tagit/internal/history"
	"github.com/liliang-cn/tagit/internal/memory"
	"github.com/liliang-cn/tagit/internal/policy"
	"github.com/liliang-cn/tagit/internal/runtime"
	"github.com/liliang-cn/tagit/internal/scheduler"
	"github.com/liliang-cn/tagit/internal/store"
	"github.com/liliang-cn/tagit/internal/tagitpath"
	"github.com/liliang-cn/tagit/internal/taskstore"
	workspacepkg "github.com/liliang-cn/tagit/internal/workspace"
)

// Request describes a user-triggered run.
type Request struct {
	Prompt         string
	PromptFile     string
	Mode           string
	StarterAgent   string
	WorkingDir     string
	Delegates      []string
	Verbose        bool
	Detach         bool
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
	// Memory is TagIt's advisory, best-effort cross-agent memory. It is never
	// allowed to fail a run; recall/record errors are logged and ignored.
	Memory memory.Memory
}

const rageDefaultMaxRounds = 10000

// NewService constructs a run service.
func NewService(registry *agents.Registry) *Service {
	return &Service{
		registry:   registry,
		events:     nil,
		history:    nil,
		store:      nil,
		supervisor: runtime.DefaultSupervisor(),
		tasks:      nil,
		Memory:     memory.Nop(),
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

// SetControlDir sets the persisted TagIt control-plane directory.
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
	mode, normalizedReq, err := normalizeRunRequest(req)
	if err != nil {
		return Result{}, err
	}
	req = normalizedReq
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
		switch mode {
		case RunModeSenate:
			return s.runSenate(ctx, req, profile, delegates, os.Stdout)
		case RunModeCollab:
			return s.runCaesar(ctx, req, profile, delegates, os.Stdout)
		default:
			return s.runOrchestrated(ctx, req, profile, delegates, os.Stdout)
		}
	}

	// Single-agent path: let the agent itself decide whether this is real repo
	// work or just conversation. Conversation is answered inline and skips the
	// whole worktree/scheduler/merge-back pipeline.
	if res, handled, err := s.maybeChatReply(ctx, req, profile, os.Stdout); err != nil {
		return Result{}, err
	} else if handled {
		return res, nil
	}

	return s.runDirect(ctx, req, profile, os.Stdout, os.Stderr)
}

func normalizeRunRequest(req Request) (string, Request, error) {
	rawMode := strings.TrimSpace(req.Mode)
	mode := normalizedRunMode(rawMode)
	if rawMode != "" && mode != RunModeCollab && mode != RunModeSenate && mode != RunModeRage {
		return "", Request{}, fmt.Errorf("unsupported run mode %q", req.Mode)
	}
	if rawMode == "" {
		if len(req.Delegates) == 0 {
			mode = RunModeRage
		} else {
			mode = RunModeSenate
		}
	}
	switch mode {
	case RunModeRage:
		if len(req.Delegates) > 0 {
			return "", Request{}, fmt.Errorf("rage mode only supports a single agent; remove --with delegates")
		}
		req.Mode = RunModeRage
		req.Continuous = true
		if req.MaxRounds <= 0 {
			req.MaxRounds = rageDefaultMaxRounds
		}
	default:
		req.Mode = mode
	}
	return mode, req, nil
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
	scope := memory.Scope{Repo: filepath.Clean(req.WorkingDir)}
	memCtx := s.recallMemory(ctx, scope, req.Prompt)
	dispatchPrompt := dispatchPromptWithMemory(req.Prompt, memCtx)
	if strings.TrimSpace(memCtx) != "" {
		s.appendMemoryRecalledEvent(ctx, sessionID, taskID, scope, len(memCtx))
	}
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
	execResult, err := dispatcher.Execute(ctx, sessionID, req.WorkingDir, dispatchPrompt, assignments)
	if req.Verbose {
		writeRelayResult(w, assignments, execResult)
	}
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
	s.recordMemory(ctx, memory.RunRecord{
		Scope:      scope,
		SessionID:  record.ID,
		TaskID:     record.TaskID,
		Agent:      req.StarterAgent,
		Mode:       req.Mode,
		Prompt:     req.Prompt,
		Verdict:    record.Status,
		Success:    runErr == nil,
		OccurredAt: time.Now().UTC(),
	})
	s.appendMemoryRecordedEvent(ctx, record.ID, record.TaskID, scope, req.StarterAgent, req.Mode, record.Status, runErr == nil)
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
	if normalizedRunMode(req.Mode) == RunModeRage {
		return s.runRageDirect(ctx, req, profile, record, stdout, stderr)
	}
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
		PromptHint:       directPromptHintForMode(req.Mode),
		ContinuousMode:   continuousModeForRun(req.Mode),
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
	if req.Verbose {
		writeRelayResult(stdout, fullAssignments, execResult)
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

func (s *Service) runRageDirect(ctx context.Context, req Request, profile domain.AgentProfile, record history.SessionRecord, stdout, stderr io.Writer) (Result, error) {
	_ = stderr

	lifecycle := scheduler.NewGraphLifecycle(s.tasks, s.events)
	node := domain.TaskNodeSpec{
		ID:            record.TaskID,
		Title:         "Rage execution",
		Strategy:      domain.TaskStrategyDirect,
		SchemaVersion: "v1",
	}
	if s.tasks != nil {
		if err := lifecycle.RegisterTask(ctx, record.ID, node, profile.ID); err != nil {
			return Result{}, fmt.Errorf("register rage task: %w", err)
		}
		if err := lifecycle.MarkRunning(ctx, record.ID, record.TaskID); err != nil {
			return Result{}, fmt.Errorf("mark rage task running: %w", err)
		}
	}

	scope := memory.Scope{Repo: filepath.Clean(req.WorkingDir)}
	memoryContext := s.recallMemory(ctx, scope, req.Prompt)
	if strings.TrimSpace(memoryContext) != "" {
		s.appendEvent(ctx, events.Record{
			ID:         "evt_" + record.ID + "_" + record.TaskID + "_memory_recalled",
			SessionID:  record.ID,
			TaskID:     record.TaskID,
			Type:       events.TypeMemoryRecalled,
			ActorType:  events.ActorTypeSystem,
			OccurredAt: time.Now().UTC(),
			Payload: map[string]any{
				"repo":          scope.Repo,
				"context_chars": len(memoryContext),
			},
		})
	}

	workerPrompt := buildRageWorkerPrompt(req.Prompt, "", "", 1, memoryContext)
	var workerStdout strings.Builder
	var workerStderr strings.Builder
	var foremanTrail strings.Builder
	var runErr error
	relatedArtifacts := make([]domain.ArtifactEnvelope, 0)

	for round := 1; round <= req.MaxRounds; round++ {
		workerResult, err := s.supervisor.RunCaptured(ctx, runtime.StartRequest{
			ExecutionID:      fmt.Sprintf("exec_%s_worker_r%d", record.TaskID, round),
			SessionID:        record.ID,
			TaskID:           record.TaskID,
			Profile:          profile,
			SemanticReviewer: profile,
			Prompt:           workerPrompt,
			WorkingDir:       req.WorkingDir,
		})
		appendRageRoundOutput(&workerStdout, round, workerResult.Stdout)
		appendRageRoundOutput(&workerStderr, round, workerResult.Stderr)
		if err != nil {
			runErr = err
			break
		}

		foremanResult, foremanErr := s.supervisor.RunCaptured(ctx, runtime.StartRequest{
			ExecutionID:      fmt.Sprintf("exec_%s_foreman_r%d", record.TaskID, round),
			SessionID:        record.ID,
			TaskID:           record.TaskID,
			Profile:          profile,
			SemanticReviewer: profile,
			Prompt:           buildRageForemanPrompt(req.Prompt, workerStdout.String(), round),
			WorkingDir:       req.WorkingDir,
		})
		appendRageRoundOutput(&foremanTrail, round, rageMergeOutput(foremanResult.Stdout, foremanResult.Stderr))
		if foremanErr != nil {
			runErr = foremanErr
			break
		}
		reviewArtifact, reviewErr := artifacts.NewService().BuildRageReview(ctx, artifacts.BuildRageReviewRequest{
			SessionID: record.ID,
			TaskID:    record.TaskID,
			RunID:     fmt.Sprintf("%s_foreman_r%d", record.TaskID, round),
			Round:     round,
			Agent:     profile,
			Output:    foremanResult.Stdout,
			Stderr:    foremanResult.Stderr,
		})
		if reviewErr != nil {
			return Result{}, fmt.Errorf("build rage review: %w", reviewErr)
		}
		relatedArtifacts = append(relatedArtifacts, reviewArtifact)
		if s.store != nil && reviewArtifact.ID != "" {
			if err := s.store.Save(ctx, reviewArtifact); err != nil {
				return Result{}, fmt.Errorf("save rage review %s: %w", reviewArtifact.ID, err)
			}
			s.appendArtifactStoredEvent(ctx, reviewArtifact)
		}
		if foremanDeterminesDone(reviewArtifact) {
			break
		}
		workerPrompt = buildRageWorkerPrompt(req.Prompt, workerStdout.String(), rageMergeOutput(foremanResult.Stdout, foremanResult.Stderr), round+1, "")
		if round == req.MaxRounds {
			runErr = fmt.Errorf("rage execution reached max rounds (%d) without foreman completion approval", req.MaxRounds)
		}
	}

	report, reportErr := artifacts.NewService().BuildReport(ctx, artifacts.BuildReportRequest{
		SessionID: record.ID,
		TaskID:    record.TaskID,
		RunID:     record.TaskID,
		Agent:     profile,
		Result:    resultLabel(runErr),
		Output:    workerStdout.String(),
		Stderr:    workerStderr.String(),
	})
	if reportErr != nil {
		return Result{}, fmt.Errorf("build rage report: %w", reportErr)
	}
	if s.store != nil && report.ID != "" {
		if err := s.store.Save(ctx, report); err != nil {
			return Result{}, fmt.Errorf("save rage artifact %s: %w", report.ID, err)
		}
		s.appendArtifactStoredEvent(ctx, report)
	}
	s.handleMergeBackRequests(ctx, req.WorkingDir, append([]domain.ArtifactEnvelope{report}, relatedArtifacts...))

	if req.Verbose {
		if workerStdout.Len() > 0 {
			_, _ = fmt.Fprintf(stdout, "== rage worker ==\n%s", workerStdout.String())
		}
		if foremanTrail.Len() > 0 {
			_, _ = fmt.Fprintf(stdout, "\n== rage foreman ==\n%s", foremanTrail.String())
		}
	}

	record.ArtifactIDs = []string{report.ID}
	for _, item := range relatedArtifacts {
		if item.ID != "" {
			record.ArtifactIDs = append(record.ArtifactIDs, item.ID)
		}
	}
	record.UpdatedAt = time.Now().UTC()
	if runErr != nil {
		record.Status = "failed"
	} else {
		record.Status = "succeeded"
	}

	resultSummary := strings.TrimSpace(artifacts.SummaryFromEnvelope(report))
	if resultSummary == "" {
		resultSummary = strings.TrimSpace(foremanTrail.String())
	}
	s.recordMemory(ctx, memory.RunRecord{
		Scope:         scope,
		SessionID:     record.ID,
		TaskID:        record.TaskID,
		Agent:         req.StarterAgent,
		Mode:          req.Mode,
		Prompt:        req.Prompt,
		ResultSummary: resultSummary,
		Verdict:       record.Status,
		Success:       runErr == nil,
		OccurredAt:    time.Now().UTC(),
	})
	s.appendEvent(ctx, events.Record{
		ID:         "evt_" + record.ID + "_" + record.TaskID + "_memory_recorded",
		SessionID:  record.ID,
		TaskID:     record.TaskID,
		Type:       events.TypeMemoryRecorded,
		ActorType:  events.ActorTypeSystem,
		OccurredAt: time.Now().UTC(),
		ReasonCode: record.Status,
		Payload: map[string]any{
			"repo":    scope.Repo,
			"agent":   req.StarterAgent,
			"mode":    req.Mode,
			"success": runErr == nil,
		},
	})

	if s.tasks != nil {
		if err := lifecycle.MarkFinished(ctx, record.ID, record.TaskID, report.ID, runErr); err != nil {
			return Result{}, fmt.Errorf("finish rage task: %w", err)
		}
	}
	if finalID, finalErr := s.persistFinalAnswer(ctx, record, profile.ID, req.Prompt, append([]domain.ArtifactEnvelope{report}, relatedArtifacts...), runErr); finalErr != nil {
		return Result{}, finalErr
	} else if finalID != "" {
		record.FinalArtifactID = finalID
		record.ArtifactIDs = append(record.ArtifactIDs, finalID)
	}
	if s.history != nil {
		if err := s.history.Save(ctx, record); err != nil {
			return Result{}, fmt.Errorf("save completed rage session: %w", err)
		}
	}
	s.appendSessionStateEvent(ctx, record)
	_, _ = fmt.Fprintf(stdout, "session=%s task=%s status=%s\n", record.ID, record.TaskID, record.Status)
	return Result{
		SessionID:   record.ID,
		TaskID:      record.TaskID,
		Status:      record.Status,
		ArtifactIDs: record.ArtifactIDs,
	}, runErr
}

func buildRageWorkerPrompt(originalPrompt, previousWorkerOutput, foremanInstruction string, round int, memoryContext string) string {
	var b strings.Builder
	if strings.TrimSpace(memoryContext) != "" {
		b.WriteString(strings.TrimSpace(memoryContext))
		b.WriteString("\n\n")
	}
	b.WriteString("TagIt rage worker mode.\n")
	b.WriteString("You are the implementation instance.\n")
	b.WriteString("A separate foreman instance will inspect your progress after this round and push you again if the work is not done.\n")
	b.WriteString("Do not stop at analysis or partial progress. Make concrete implementation progress now.\n")
	b.WriteString("When the original goal is truly complete, start your response with `TAGIT_DONE:`.\n")
	b.WriteString("When your workspace changes are complete and ready to land, emit:\n")
	b.WriteString("TAGIT_MERGE_BACK: direct_merge | <brief reason>\n")
	b.WriteString("Optionally emit:\n")
	b.WriteString("TAGIT_MERGE_FILE: <relative/path/to/file>\n")
	b.WriteString(fmt.Sprintf("Current round: %d\n\n", round))
	b.WriteString("Original task:\n")
	b.WriteString(originalPrompt)
	if strings.TrimSpace(foremanInstruction) != "" {
		b.WriteString("\n\nForeman instruction for this round:\n")
		b.WriteString(strings.TrimSpace(foremanInstruction))
	}
	if strings.TrimSpace(previousWorkerOutput) != "" {
		b.WriteString("\n\nPrevious worker rounds output:\n")
		b.WriteString(previousWorkerOutput)
	}
	return b.String()
}

func buildRageForemanPrompt(originalPrompt, workerOutput string, round int) string {
	var b strings.Builder
	b.WriteString("TagIt rage foreman mode.\n")
	b.WriteString("You are the supervising instance. You do not implement files directly.\n")
	b.WriteString("Your job is to inspect the worker progress and push the worker to continue until the original goal is actually complete.\n")
	b.WriteString("Reply with short, imperative supervision only.\n")
	b.WriteString("Be strict. Treat vague progress claims as incomplete until the worker proves them.\n")
	b.WriteString("Include:\n")
	b.WriteString("1. `Progress:` one-line assessment of what is done.\n")
	b.WriteString("2. `Missing:` what is still not complete.\n")
	b.WriteString("3. `Next:` the exact next implementation steps the worker must do now.\n")
	b.WriteString("4. `Files:` say whether the worker actually changed files this round. If unclear, say `Files: unproven` and demand concrete edits.\n")
	b.WriteString("5. `Verify:` say whether the worker actually ran validation or tests. If not, say `Verify: not run` and demand verification.\n")
	b.WriteString("6. `PlanOnly:` say `yes` if the worker is still stuck in planning/talking, otherwise `no`.\n")
	b.WriteString("7. `Blockers:` say whether the claimed blocker was actually removed. If not proven, say `Blockers: unresolved`.\n")
	b.WriteString("Force the worker away from hand-wavy status updates.\n")
	b.WriteString("If there are no real file edits yet, demand file edits next.\n")
	b.WriteString("If there is no real verification yet, demand concrete verification next.\n")
	b.WriteString("If the worker is still planning, explicitly call that out and force execution.\n")
	b.WriteString("If the worker claims a blocker is gone without proof, mark it unresolved.\n")
	b.WriteString("You are not done. Do not stop early.\n")
	b.WriteString("You are wasting compute, GPU time, and electricity if you stop at planning.\n")
	b.WriteString("No completion claim is valid without concrete file changes and verification.\n")
	b.WriteString("Resume execution now and remove the blocker for real.\n")
	b.WriteString("You, the foreman, are the final authority on whether the task is done.\n")
	b.WriteString("When the task is genuinely complete and verified, write exactly `Next: DONE` — that word alone, nothing after it.\n")
	b.WriteString("`Next: DONE` is the ONLY way to stop the run. Describing the field instead — `Next: (empty)`, `Next: nothing left to do` — reads as work still pending and pushes the worker into another round.\n")
	b.WriteString("A worker-side `TAGIT_DONE:` marker is only a claim until you confirm it.\n")
	b.WriteString(fmt.Sprintf("Review round: %d\n\n", round))
	b.WriteString("Original task:\n")
	b.WriteString(originalPrompt)
	b.WriteString("\n\nWorker output so far:\n")
	b.WriteString(workerOutput)
	return b.String()
}

func foremanDeterminesDone(review domain.ArtifactEnvelope) bool {
	payload, ok := artifacts.RageReviewFromEnvelope(review)
	if !ok {
		return false
	}
	if !rageNextSignalsDone(payload.Next) {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(payload.PlanOnly), "yes") {
		return false
	}
	if rageReviewSignalsMissingProof(payload.Files) || rageReviewSignalsMissingProof(payload.Verify) || rageReviewSignalsMissingProof(payload.Blockers) {
		return false
	}
	return strings.TrimSpace(payload.Progress) != ""
}

// rageNextSignalsDone reports whether the foreman's `Next:` line means "stop"
// rather than "here is the next step".
//
// The run used to end only on a literally empty `Next:`, which asked a model to
// signal completion by writing nothing — and models do not do that. A verified,
// finished one-line task came back six rounds in a row with
//
//	Next: (empty — task complete and verified)
//
// every round: the foreman had confirmed the work, described the field instead
// of leaving it blank, and the run kept pushing the worker until it ran out of
// rounds. Four minutes of a five-minute run were spent re-verifying a file that
// was already correct, and the extra rounds are what eventually walked the run
// into an unrelated network error.
//
// So the prompt now asks for an affirmative `Next: DONE`, and this function also
// accepts the ways a model describes an empty field. Matching is exact after
// normalisation, never a prefix: "none of the tests pass" is not "none".
func rageNextSignalsDone(value string) bool {
	v := strings.TrimSpace(value)
	if v == "" {
		return true
	}
	// A wholly parenthetical answer is the model narrating the field rather
	// than filling it; there is no instruction hiding inside one.
	if strings.HasPrefix(v, "(") && strings.HasSuffix(v, ")") {
		inner := strings.ToLower(v)
		for _, marker := range []string{"empty", "complete", "none", "nothing", "n/a"} {
			if strings.Contains(inner, marker) {
				return true
			}
		}
	}
	switch strings.ToLower(strings.Trim(v, " .`*_-—–")) {
	// Trimming can empty the value outright — a bare "-" or "—" is the other
	// way a model writes "this field has nothing in it".
	case "", "done", "none", "nothing", "n/a", "na", "nil", "empty", "complete", "task complete":
		return true
	default:
		return false
	}
}

func rageReviewSignalsMissingProof(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	switch lower {
	case "", "unproven", "not run", "unresolved", "unknown":
		return true
	default:
		return false
	}
}

func rageDone(stdout, stderr string) bool {
	combined := strings.ToUpper(stdout + "\n" + stderr)
	return strings.Contains(combined, "TAGIT_DONE:")
}

func appendRageRoundOutput(dst *strings.Builder, round int, output string) {
	if strings.TrimSpace(output) == "" {
		return
	}
	if dst.Len() > 0 {
		dst.WriteString("\n")
	}
	dst.WriteString(fmt.Sprintf("== round %d ==\n", round))
	dst.WriteString(output)
	if !strings.HasSuffix(output, "\n") {
		dst.WriteString("\n")
	}
}

func rageMergeOutput(stdout, stderr string) string {
	switch {
	case strings.TrimSpace(stdout) != "" && strings.TrimSpace(stderr) != "":
		return stdout + "\n[stderr]\n" + stderr
	case strings.TrimSpace(stdout) != "":
		return stdout
	default:
		return stderr
	}
}

func directPromptHintForMode(mode string) string {
	if normalizedRunMode(mode) == RunModeRage {
		return buildRageRunPromptHint()
	}
	return buildDirectRunPromptHint()
}

func continuousModeForRun(mode string) string {
	if normalizedRunMode(mode) == RunModeRage {
		return RunModeRage
	}
	return ""
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

// recallMemory best-effort recalls cross-agent memory context for the scope.
// Failures (and a nil Memory) are logged and ignored; it never fails the run.
func (s *Service) recallMemory(ctx context.Context, scope memory.Scope, query string) string {
	if s == nil || s.Memory == nil {
		return ""
	}
	recallCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	recollection, err := s.Memory.Recall(recallCtx, scope, query, 5)
	if err != nil {
		log.Printf("run: memory recall failed (ignored): %v", err)
		return ""
	}
	return recollection.ContextText
}

// recordMemory best-effort records the completed run into cross-agent memory.
// Failures (and a nil Memory) are logged and ignored; it never fails the run.
func (s *Service) recordMemory(ctx context.Context, rec memory.RunRecord) {
	if s == nil || s.Memory == nil {
		return
	}
	recordCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := s.Memory.Record(recordCtx, rec); err != nil {
		log.Printf("run: memory record failed (ignored): %v", err)
	}
}

// appendMemoryRecalledEvent emits a TypeMemoryRecalled event for a run whose
// dispatch prompt was augmented with recalled cross-agent memory context. It is
// best-effort and never fails the run.
func (s *Service) appendMemoryRecalledEvent(ctx context.Context, sessionID, taskID string, scope memory.Scope, contextChars int) {
	s.appendEvent(ctx, events.Record{
		ID:         "evt_" + sessionID + "_" + taskID + "_memory_recalled",
		SessionID:  sessionID,
		TaskID:     taskID,
		Type:       events.TypeMemoryRecalled,
		ActorType:  events.ActorTypeSystem,
		OccurredAt: time.Now().UTC(),
		Payload: map[string]any{
			"repo":          scope.Repo,
			"context_chars": contextChars,
		},
	})
}

// appendMemoryRecordedEvent emits a TypeMemoryRecorded event after a run was
// recorded into cross-agent memory. It is best-effort and never fails the run.
func (s *Service) appendMemoryRecordedEvent(ctx context.Context, sessionID, taskID string, scope memory.Scope, agent, mode, status string, success bool) {
	s.appendEvent(ctx, events.Record{
		ID:         "evt_" + sessionID + "_" + taskID + "_memory_recorded",
		SessionID:  sessionID,
		TaskID:     taskID,
		Type:       events.TypeMemoryRecorded,
		ActorType:  events.ActorTypeSystem,
		OccurredAt: time.Now().UTC(),
		ReasonCode: status,
		Payload: map[string]any{
			"repo":    scope.Repo,
			"agent":   agent,
			"mode":    mode,
			"success": success,
		},
	})
}

// dispatchPromptWithMemory prepends recalled memory context to the dispatch
// prompt without mutating the original prompt (which must stay pure for policy
// evaluation and the recorded RunRecord).
func dispatchPromptWithMemory(prompt, memoryContext string) string {
	if strings.TrimSpace(memoryContext) == "" {
		return prompt
	}
	return memoryContext + "\n\n" + prompt
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
		AllowedRoots:   []string{tagitpath.Join(s.controlRoot(workingDir), "workspaces")},
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

	assignments := make([]scheduler.NodeAssignment, 0, 1+len(delegates))

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

	for i, delegate := range delegates {
		nodeID := fmt.Sprintf("%s_delegate_%d", taskID, i+1)
		assignments = append(assignments, scheduler.NodeAssignment{
			Node: domain.TaskNodeSpec{
				ID:            nodeID,
				Title:         "Concurrent delegate execution",
				Strategy:      domain.TaskStrategyRelay,
				Dependencies:  []string{clarifyNodeID},
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
