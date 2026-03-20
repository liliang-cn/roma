package run

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/liliang-cn/roma/internal/domain"
	"github.com/liliang-cn/roma/internal/events"
	"github.com/liliang-cn/roma/internal/history"
	"github.com/liliang-cn/roma/internal/scheduler"
)

type repoConflictSummary struct {
	Paths       []string
	StatusLines []string
}

func (s repoConflictSummary) HasConflicts() bool {
	return len(s.Paths) > 0
}

func (s *Service) continueCaesarCoordination(
	ctx context.Context,
	req Request,
	sessionID, taskID string,
	starter domain.AgentProfile,
	assignments []scheduler.NodeAssignment,
	result scheduler.DispatchResult,
	dispatcher *scheduler.Dispatcher,
) ([]scheduler.NodeAssignment, scheduler.DispatchResult, error) {
	if len(assignments) <= 1 {
		return assignments, result, nil
	}

	processedArtifacts := map[string]struct{}{}
	s.handleMergeBackRequests(ctx, req.WorkingDir, collectUnprocessedArtifacts(result, processedArtifacts))

	currentAssignments := append([]scheduler.NodeAssignment(nil), assignments...)
	currentResult := result
	currentWave := append([]string(nil), initialDelegateNodeIDs(assignments)...)
	round := 1

	for len(currentWave) > 0 {
		conflicts, err := inspectRepoConflicts(ctx, req.WorkingDir)
		if err != nil {
			return currentAssignments, currentResult, err
		}
		reviewNode := buildCaesarReviewAssignment(taskID, starter, currentWave, currentAssignments, conflicts, req.Continuous, req.MaxRounds, round)
		if s.tasks != nil {
			lifecycle := scheduler.NewGraphLifecycle(s.tasks, s.events)
			if err := lifecycle.RegisterTask(ctx, sessionID, reviewNode.Node, starter.ID); err != nil {
				return currentAssignments, currentResult, fmt.Errorf("register Caesar review task %s: %w", reviewNode.Node.ID, err)
			}
		}
		currentAssignments = append(currentAssignments, reviewNode)

		resumeResult, err := dispatcher.Resume(ctx, sessionID, req.WorkingDir, req.Prompt, currentAssignments, cloneArtifacts(currentResult.Artifacts))
		currentResult = resumeResult
		s.handleMergeBackRequests(ctx, req.WorkingDir, collectUnprocessedArtifacts(currentResult, processedArtifacts))
		if err != nil {
			return currentAssignments, currentResult, err
		}

		reviewArtifact, ok := currentResult.Artifacts[reviewNode.Node.ID]
		if !ok || reviewArtifact.ID == "" {
			return currentAssignments, currentResult, fmt.Errorf("caesar review node %s produced no artifact", reviewNode.Node.ID)
		}

		requests := extractDelegateRequests(reviewArtifact)
		if len(requests) == 0 {
			if err := ensureConflictFreeConclusion(ctx, req.WorkingDir); err != nil {
				return currentAssignments, currentResult, err
			}
			return currentAssignments, currentResult, nil
		}

		nextWave := make([]string, 0, len(requests))
		for _, request := range requests {
			profile, ok := s.resolveCaesarDelegateTarget(ctx, currentAssignments, request.AgentID)
			if !ok || profile.Availability != domain.AgentAvailabilityAvailable {
				continue
			}

			nodeID := nextDynamicDelegateNodeID(currentAssignments, reviewNode.Node.ID)
			node := domain.TaskNodeSpec{
				ID:            nodeID,
				Title:         "Caesar delegated execution",
				Strategy:      domain.TaskStrategyRelay,
				Dependencies:  []string{reviewNode.Node.ID},
				SchemaVersion: "v1",
			}
			if s.tasks != nil {
				lifecycle := scheduler.NewGraphLifecycle(s.tasks, s.events)
				if err := lifecycle.RegisterTask(ctx, sessionID, node, profile.ID); err != nil {
					return currentAssignments, currentResult, fmt.Errorf("register Caesar delegate task %s: %w", nodeID, err)
				}
			}
			currentAssignments = append(currentAssignments, scheduler.NodeAssignment{
				Node:             node,
				Profile:          profile,
				SemanticReviewer: starter,
				Continuous:       req.Continuous,
				MaxRounds:        req.MaxRounds,
				PromptHint:       buildCaesarDelegatePromptHint(starter, request.Instruction),
			})
			nextWave = append(nextWave, nodeID)
		}

		if len(nextWave) == 0 {
			return currentAssignments, currentResult, fmt.Errorf("caesar emitted no actionable delegate requests")
		}

		resumeResult, err = dispatcher.Resume(ctx, sessionID, req.WorkingDir, req.Prompt, currentAssignments, cloneArtifacts(currentResult.Artifacts))
		currentResult = resumeResult
		s.handleMergeBackRequests(ctx, req.WorkingDir, collectUnprocessedArtifacts(currentResult, processedArtifacts))
		if err != nil {
			return currentAssignments, currentResult, err
		}

		currentWave = nextWave
		round++
	}

	return currentAssignments, currentResult, nil
}

func (s *Service) runCaesar(ctx context.Context, req Request, starter domain.AgentProfile, delegates []domain.AgentProfile, w io.Writer) (Result, error) {
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
	if req.SessionID != "" {
		if existing, err := s.history.Get(ctx, sessionID); err == nil {
			record.CreatedAt = existing.CreatedAt
		}
	}
	if _, err := s.evaluatePolicy(ctx, sessionID, taskID, "caesar", req.Prompt, req.WorkingDir, req.WorkingDir, nil, starter.ID, req.Delegates, assignmentsOrchestrated(delegates), req.PolicyOverride, req.OverrideActor); err != nil {
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
			"mode":      RunModeCaesar,
		},
	})
	helpOutputs := make(map[string]string, len(delegates))
	for _, d := range delegates {
		helpOutputs[d.ID] = probeAgentHelp(ctx, d)
	}
	assignments := buildCaesarAssignments(taskID, starter, delegates, req.Continuous, req.MaxRounds, helpOutputs)
	dispatcher := scheduler.NewDispatcherWithControlDir(req.WorkingDir, s.controlRoot(req.WorkingDir), s.supervisor, s.events, s.tasks)
	execResult, err := dispatcher.Execute(ctx, sessionID, req.WorkingDir, req.Prompt, assignments)
	if err == nil {
		if updatedAssignments, updatedResult, caesarErr := s.continueCaesarCoordination(ctx, req, sessionID, taskID, starter, assignments, execResult, dispatcher); caesarErr != nil {
			assignments = updatedAssignments
			execResult = updatedResult
			err = caesarErr
		} else {
			assignments = updatedAssignments
			execResult = updatedResult
		}
	}
	if req.Verbose {
		writeRelayResult(w, assignments, execResult)
	}
	if err := s.saveDispatchArtifacts(ctx, execResult); err != nil {
		return Result{}, err
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

func buildCaesarReviewAssignment(taskID string, starter domain.AgentProfile, dependencies []string, assignments []scheduler.NodeAssignment, conflicts repoConflictSummary, continuous bool, maxRounds int, round int) scheduler.NodeAssignment {
	return scheduler.NodeAssignment{
		Node: domain.TaskNodeSpec{
			ID:            fmt.Sprintf("%s_starter_caesar_%d", taskID, round),
			Title:         fmt.Sprintf("Caesar review round %d", round),
			Strategy:      domain.TaskStrategyDirect,
			Dependencies:  append([]string(nil), dependencies...),
			SchemaVersion: "v1",
		},
		Profile:          starter,
		SemanticReviewer: starter,
		Continuous:       continuous,
		MaxRounds:        maxRounds,
		PromptHint:       buildCaesarReviewPromptHint(round, dependencies, assignments, conflicts),
	}
}

func buildCaesarAssignments(taskID string, starter domain.AgentProfile, delegates []domain.AgentProfile, continuous bool, maxRounds int, helpOutputs map[string]string) []scheduler.NodeAssignment {
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
				Dependencies:  []string{clarifyNodeID, bootstrapNodeID},
				SchemaVersion: "v1",
			},
			Profile:          delegate,
			SemanticReviewer: starter,
			Continuous:       continuous,
			MaxRounds:        maxRounds,
			PromptHint:       buildParticipatingCaesarDelegatePromptHint(starter, ""),
		})
	}
	return assignments
}

func buildCaesarReviewPromptHint(round int, dependencies []string, assignments []scheduler.NodeAssignment, conflicts repoConflictSummary) string {
	lines := []string{
		fmt.Sprintf("You are Caesar review round %d.", round),
		"You are still only the coordinator. Do not edit files or implement the task yourself.",
		"Review the delegate outputs above and ask one question only: is the main task done?",
		"If more implementation work is needed, emit one or more lines in this exact format:",
		"ROMA_FOLLOWUP: delegate <target_id> | <instruction>",
		"If the task is complete, emit ROMA_DONE: <brief summary> and do not emit any follow-up lines.",
		"Only delegate concrete implementation work to the agents; keep all coordination with Caesar.",
	}
	targets := caesarDelegateTargets(dependencies, assignments)
	if len(targets) > 0 {
		lines = append(lines, "")
		lines = append(lines, "CRITICAL — use ONLY these exact target IDs in ROMA_FOLLOWUP lines:")
		for _, id := range targets {
			lines = append(lines, fmt.Sprintf("  ROMA_FOLLOWUP: delegate %s | <your instruction here>", id))
		}
		lines = append(lines, "The target_id field must be one of: "+strings.Join(targets, ", "))
	}
	if conflicts.HasConflicts() {
		lines = append(lines, "Main workspace currently has unresolved git conflicts. Do not emit ROMA_DONE until all of them are resolved.")
		lines = append(lines, "Conflict status:")
		for _, line := range conflicts.StatusLines {
			lines = append(lines, "- "+line)
		}
	}
	return strings.Join(lines, "\n")
}

func buildStarterBootstrapPromptHint(starter domain.AgentProfile, delegates []domain.AgentProfile, helpOutputs map[string]string) string {
	lines := []string{
		fmt.Sprintf("You are Caesar, the starter agent (%s).", starter.DisplayName),
		"You participate in implementation and coordination in this mode.",
		"Do an initial implementation pass yourself if useful, and also establish the bootstrap plan for delegates.",
		"Your output should help the delegates understand what to do next.",
		"When your starter workspace is ready to land, emit `ROMA_MERGE_BACK: direct_merge | <reason>` and optionally `ROMA_MERGE_FILE: <path>` lines.",
		"You may still leave follow-up refinement to later Caesar review rounds.",
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

// buildDirectRunPromptHint returns the prompt hint for a single-agent direct run.
// It tells the agent to emit ROMA_MERGE_BACK so the workspace is automatically
// merged back after the task completes.
func buildDirectRunPromptHint() string {
	return strings.Join([]string{
		"You are the sole executor for this task.",
		"When your workspace changes are complete and ready to land, emit:",
		"ROMA_MERGE_BACK: direct_merge | <brief reason>",
		"Optionally list each changed file with:",
		"ROMA_MERGE_FILE: <relative/path/to/file>",
	}, "\n")
}

func buildCaesarDelegatePromptHint(starter domain.AgentProfile, instruction string) string {
	lines := []string{
		fmt.Sprintf("The starter agent %s is Caesar only and will not implement code.", starter.DisplayName),
		"You own the concrete implementation work for this node.",
		"When your workspace is ready to land, emit `ROMA_MERGE_BACK: direct_merge | <reason>` and optionally `ROMA_MERGE_FILE: <path>` lines.",
	}
	if strings.TrimSpace(instruction) != "" {
		lines = append(lines, "Caesar instruction: "+strings.TrimSpace(instruction))
	}
	return strings.Join(lines, "\n")
}

func buildParticipatingCaesarDelegatePromptHint(starter domain.AgentProfile, instruction string) string {
	lines := []string{
		fmt.Sprintf("The starter agent %s is acting as an active Caesar and may also contribute implementation.", starter.DisplayName),
		"You own concrete implementation work for this node and should complement the starter contribution instead of duplicating it.",
		"When your workspace is ready to land, emit `ROMA_MERGE_BACK: direct_merge | <reason>` and optionally `ROMA_MERGE_FILE: <path>` lines.",
	}
	if strings.TrimSpace(instruction) != "" {
		lines = append(lines, "Caesar instruction: "+strings.TrimSpace(instruction))
	}
	return strings.Join(lines, "\n")
}

func initialDelegateNodeIDs(assignments []scheduler.NodeAssignment) []string {
	out := make([]string, 0, len(assignments))
	for _, assignment := range assignments {
		if strings.Contains(assignment.Node.ID, "_delegate_") {
			out = append(out, assignment.Node.ID)
		}
	}
	return out
}

func collectUnprocessedArtifacts(result scheduler.DispatchResult, seen map[string]struct{}) []domain.ArtifactEnvelope {
	out := make([]domain.ArtifactEnvelope, 0, len(result.Order))
	for _, nodeID := range result.Order {
		if artifact := result.Artifacts[nodeID]; artifact.ID != "" {
			if _, ok := seen[artifact.ID]; !ok {
				seen[artifact.ID] = struct{}{}
				out = append(out, artifact)
			}
		}
		for _, related := range result.RelatedArtifacts[nodeID] {
			if related.ID == "" {
				continue
			}
			if _, ok := seen[related.ID]; ok {
				continue
			}
			seen[related.ID] = struct{}{}
			out = append(out, related)
		}
	}
	return out
}

func (s *Service) resolveCaesarDelegateTarget(ctx context.Context, assignments []scheduler.NodeAssignment, raw string) (domain.AgentProfile, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return domain.AgentProfile{}, false
	}
	// Exact node ID match.
	for _, assignment := range assignments {
		if assignment.Node.ID == raw {
			if agentID := strings.TrimSpace(assignment.Profile.ID); agentID != "" {
				if profile, ok := s.registry.Resolve(ctx, agentID); ok {
					return profile, true
				}
			}
			return assignment.Profile, assignment.Profile.ID != ""
		}
	}
	// Suffix match: Caesar may emit short forms like "delegate_1" for node "task_x_delegate_1".
	for _, assignment := range assignments {
		if !strings.HasSuffix(assignment.Node.ID, "_"+raw) {
			continue
		}
		agentID := strings.TrimSpace(assignment.Profile.ID)
		if agentID == "" {
			continue
		}
		if profile, ok := s.registry.Resolve(ctx, agentID); ok {
			return profile, true
		}
		return assignment.Profile, true
	}
	return domain.AgentProfile{}, false
}

func caesarDelegateTargets(dependencies []string, assignments []scheduler.NodeAssignment) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(dependencies))
	for _, dep := range dependencies {
		for _, assignment := range assignments {
			if assignment.Node.ID != dep {
				continue
			}
			if _, ok := seen[assignment.Node.ID]; ok {
				continue
			}
			seen[assignment.Node.ID] = struct{}{}
			out = append(out, assignment.Node.ID)
		}
	}
	return out
}

func inspectRepoConflicts(ctx context.Context, workDir string) (repoConflictSummary, error) {
	if strings.TrimSpace(workDir) == "" {
		return repoConflictSummary{}, nil
	}
	cmd := exec.CommandContext(ctx, "git", "-C", workDir, "status", "--porcelain")
	output, err := cmd.Output()
	if err != nil {
		if out := strings.TrimSpace(string(output)); strings.Contains(out, "not a git repository") {
			return repoConflictSummary{}, nil
		}
		if exitErr, ok := err.(*exec.ExitError); ok {
			text := strings.TrimSpace(string(exitErr.Stderr))
			if strings.Contains(text, "not a git repository") {
				return repoConflictSummary{}, nil
			}
		}
		return repoConflictSummary{}, fmt.Errorf("git status --porcelain: %w", err)
	}

	seen := map[string]struct{}{}
	summary := repoConflictSummary{}
	for _, raw := range strings.Split(string(output), "\n") {
		line := strings.TrimRight(raw, "\r")
		if len(line) < 3 || !isUnmergedStatus(line[:2]) {
			continue
		}
		path := strings.TrimSpace(line[3:])
		if path == "" {
			continue
		}
		path = filepath.Clean(path)
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		summary.Paths = append(summary.Paths, path)
		summary.StatusLines = append(summary.StatusLines, line[:2]+" "+path)
	}
	return summary, nil
}

func isUnmergedStatus(status string) bool {
	switch strings.TrimSpace(status) {
	case "DD", "AU", "UD", "UA", "DU", "AA", "UU":
		return true
	default:
		return false
	}
}

func ensureConflictFreeConclusion(ctx context.Context, workDir string) error {
	conflicts, err := inspectRepoConflicts(ctx, workDir)
	if err != nil {
		return err
	}
	if !conflicts.HasConflicts() {
		return nil
	}
	return fmt.Errorf("repository conflicts remain unresolved: %s", strings.Join(conflicts.Paths, ", "))
}
