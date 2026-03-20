package run

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/liliang-cn/roma/internal/artifacts"
	"github.com/liliang-cn/roma/internal/domain"
	"github.com/liliang-cn/roma/internal/events"
	"github.com/liliang-cn/roma/internal/history"
	"github.com/liliang-cn/roma/internal/policy"
	"github.com/liliang-cn/roma/internal/scheduler"
	workspacepkg "github.com/liliang-cn/roma/internal/workspace"
)

type senateCandidate struct {
	NodeID       string
	Profile      domain.AgentProfile
	Artifact     domain.ArtifactEnvelope
	Summary      string
	Body         string
	ChangedFiles []string
}

type senateVote struct {
	VoterID  string
	TargetID string
	Reason   string
}

var senatePickPattern = regexp.MustCompile(`(?i)^ROMA_PICK:\s*([^\s|]+)(?:\s*\|\s*(.*))?$`)

func (s *Service) runSenate(ctx context.Context, req Request, starter domain.AgentProfile, delegates []domain.AgentProfile, w io.Writer) (Result, error) {
	if len(delegates) == 0 {
		return Result{}, fmt.Errorf("senate mode requires at least one delegate agent")
	}

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
	if _, err := s.evaluatePolicy(ctx, sessionID, taskID, "senate", req.Prompt, req.WorkingDir, req.WorkingDir, nil, starter.ID, req.Delegates, 1+len(delegates), req.PolicyOverride, req.OverrideActor); err != nil {
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
			"mode":      RunModeSenate,
		},
	})

	participants := senateParticipants(starter, delegates)
	dispatcher := scheduler.NewDispatcherWithControlDir(req.WorkingDir, s.controlRoot(req.WorkingDir), s.supervisor, s.events, s.tasks)

	assignments := buildSenatePlanProposalAssignments(taskID, participants, req.Continuous, req.MaxRounds)
	result, err := dispatcher.Execute(ctx, sessionID, req.WorkingDir, req.Prompt, assignments)
	if err != nil {
		return s.finalizeSenateResult(ctx, record, starter.ID, nil, result, err, w)
	}

	planProposalIDs := assignmentNodeIDs(assignments)
	planCandidates, err := senateCandidatesForNodeIDs(assignments, result, planProposalIDs)
	if err != nil {
		return s.finalizeSenateResult(ctx, record, starter.ID, nil, result, err, w)
	}
	planVoteAssignments := buildSenateVoteAssignments(taskID, "plan_vote", "Senate plan vote", participants, planCandidates, planProposalIDs, req.Continuous, req.MaxRounds)
	if err := s.registerAssignments(ctx, sessionID, planVoteAssignments); err != nil {
		return s.finalizeSenateResult(ctx, record, starter.ID, nil, result, err, w)
	}
	assignments = append(assignments, planVoteAssignments...)
	result, err = dispatcher.Resume(ctx, sessionID, req.WorkingDir, req.Prompt, assignments, cloneArtifacts(result.Artifacts))
	if err != nil {
		return s.finalizeSenateResult(ctx, record, starter.ID, nil, result, err, w)
	}

	planVotes := extractSenateVotes(result, planVoteAssignments)
	winnerPlan, assignments, result, err := s.resolveSenateWinner(ctx, dispatcher, req, sessionID, taskID, starter, assignments, result, "plan", planCandidates, planVotes)
	if err != nil {
		return s.finalizeSenateResult(ctx, record, starter.ID, nil, result, err, w)
	}

	implementationAssignments := buildSenateImplementationAssignments(taskID, starter, delegates, winnerPlan, req.Continuous, req.MaxRounds)
	if err := s.registerAssignments(ctx, sessionID, implementationAssignments); err != nil {
		return s.finalizeSenateResult(ctx, record, starter.ID, []domain.ArtifactEnvelope{winnerPlan.Artifact}, result, err, w)
	}
	assignments = append(assignments, implementationAssignments...)
	result, err = dispatcher.Resume(ctx, sessionID, req.WorkingDir, req.Prompt, assignments, cloneArtifacts(result.Artifacts))
	if err != nil {
		return s.finalizeSenateResult(ctx, record, starter.ID, []domain.ArtifactEnvelope{winnerPlan.Artifact}, result, err, w)
	}

	implementationIDs := assignmentNodeIDs(implementationAssignments)
	implementationCandidates, err := senateCandidatesForNodeIDs(assignments, result, implementationIDs)
	if err != nil {
		return s.finalizeSenateResult(ctx, record, starter.ID, []domain.ArtifactEnvelope{winnerPlan.Artifact}, result, err, w)
	}
	implementationVoteAssignments := buildSenateVoteAssignments(taskID, "implementation_vote", "Senate implementation vote", participants, implementationCandidates, implementationIDs, req.Continuous, req.MaxRounds)
	if err := s.registerAssignments(ctx, sessionID, implementationVoteAssignments); err != nil {
		return s.finalizeSenateResult(ctx, record, starter.ID, []domain.ArtifactEnvelope{winnerPlan.Artifact}, result, err, w)
	}
	assignments = append(assignments, implementationVoteAssignments...)
	result, err = dispatcher.Resume(ctx, sessionID, req.WorkingDir, req.Prompt, assignments, cloneArtifacts(result.Artifacts))
	if err != nil {
		return s.finalizeSenateResult(ctx, record, starter.ID, []domain.ArtifactEnvelope{winnerPlan.Artifact}, result, err, w)
	}

	implementationVotes := extractSenateVotes(result, implementationVoteAssignments)
	winnerImplementation, assignments, result, err := s.resolveSenateWinner(ctx, dispatcher, req, sessionID, taskID, starter, assignments, result, "implementation", implementationCandidates, implementationVotes)
	if err != nil {
		return s.finalizeSenateResult(ctx, record, starter.ID, []domain.ArtifactEnvelope{winnerPlan.Artifact}, result, err, w)
	}

	if err := s.mergeSenateWinner(ctx, req, sessionID, winnerImplementation); err != nil {
		return s.finalizeSenateResult(ctx, record, starter.ID, []domain.ArtifactEnvelope{winnerPlan.Artifact, winnerImplementation.Artifact}, result, err, w)
	}

	return s.finalizeSenateResult(ctx, record, starter.ID, []domain.ArtifactEnvelope{winnerPlan.Artifact, winnerImplementation.Artifact}, result, nil, w)
}

func (s *Service) finalizeSenateResult(ctx context.Context, record history.SessionRecord, starterID string, finalArtifacts []domain.ArtifactEnvelope, result scheduler.DispatchResult, runErr error, w io.Writer) (Result, error) {
	if err := s.saveDispatchArtifacts(ctx, result); err != nil {
		return Result{}, err
	}
	if runErr != nil {
		record.Status = "failed"
	} else {
		record.Status = "succeeded"
	}
	record.UpdatedAt = time.Now().UTC()
	record.ArtifactIDs = collectRelayArtifactIDs(result)
	artifactsForFinal := finalArtifacts
	if len(artifactsForFinal) == 0 {
		artifactsForFinal = collectRelayArtifacts(result)
	}
	if finalID, finalErr := s.persistFinalAnswer(ctx, record, starterID, record.Prompt, artifactsForFinal, runErr); finalErr != nil {
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
	if record.Status == "succeeded" && len(finalArtifacts) > 0 {
		s.appendEvent(ctx, events.Record{
			ID:         fmt.Sprintf("evt_%s_senate_winner_%d", record.ID, time.Now().UTC().UnixNano()),
			SessionID:  record.ID,
			TaskID:     record.TaskID,
			Type:       events.TypeTaskGraphSubmitted,
			ActorType:  events.ActorTypeSystem,
			OccurredAt: time.Now().UTC(),
			ReasonCode: "senate_winner_selected",
			Payload: map[string]any{
				"artifact_ids": collectArtifactIDs(finalArtifacts),
			},
		})
	}
	_, _ = fmt.Fprintf(w, "session=%s task=%s status=%s\n", record.ID, record.TaskID, record.Status)
	return Result{
		SessionID:   record.ID,
		TaskID:      record.TaskID,
		Status:      record.Status,
		ArtifactIDs: record.ArtifactIDs,
	}, runErr
}

func (s *Service) saveDispatchArtifacts(ctx context.Context, result scheduler.DispatchResult) error {
	if s.store == nil {
		return nil
	}
	saved := map[string]struct{}{}
	for _, nodeID := range result.Order {
		if artifact := result.Artifacts[nodeID]; artifact.ID != "" {
			if _, ok := saved[artifact.ID]; !ok {
				if err := s.store.Save(ctx, artifact); err != nil {
					return fmt.Errorf("save artifact %s: %w", artifact.ID, err)
				}
				s.appendArtifactStoredEvent(ctx, artifact)
				saved[artifact.ID] = struct{}{}
			}
		}
		for _, related := range result.RelatedArtifacts[nodeID] {
			if related.ID == "" {
				continue
			}
			if _, ok := saved[related.ID]; ok {
				continue
			}
			if err := s.store.Save(ctx, related); err != nil {
				return fmt.Errorf("save artifact %s: %w", related.ID, err)
			}
			s.appendArtifactStoredEvent(ctx, related)
			saved[related.ID] = struct{}{}
		}
	}
	return nil
}

func (s *Service) registerAssignments(ctx context.Context, sessionID string, assignments []scheduler.NodeAssignment) error {
	if s.tasks == nil {
		return nil
	}
	lifecycle := scheduler.NewGraphLifecycle(s.tasks, s.events)
	for _, assignment := range assignments {
		if err := lifecycle.RegisterTask(ctx, sessionID, assignment.Node, assignment.Profile.ID); err != nil {
			return fmt.Errorf("register task %s: %w", assignment.Node.ID, err)
		}
	}
	return nil
}

func senateParticipants(starter domain.AgentProfile, delegates []domain.AgentProfile) []domain.AgentProfile {
	out := make([]domain.AgentProfile, 0, 1+len(delegates))
	seen := map[string]struct{}{}
	add := func(profile domain.AgentProfile) {
		if strings.TrimSpace(profile.ID) == "" {
			return
		}
		if _, ok := seen[profile.ID]; ok {
			return
		}
		seen[profile.ID] = struct{}{}
		out = append(out, profile)
	}
	add(starter)
	for _, delegate := range delegates {
		add(delegate)
	}
	return out
}

func buildSenatePlanProposalAssignments(taskID string, participants []domain.AgentProfile, continuous bool, maxRounds int) []scheduler.NodeAssignment {
	assignments := make([]scheduler.NodeAssignment, 0, len(participants))
	for i, profile := range participants {
		nodeID := fmt.Sprintf("%s_plan_%d", taskID, i+1)
		assignments = append(assignments, scheduler.NodeAssignment{
			Node: domain.TaskNodeSpec{
				ID:            nodeID,
				Title:         "Senate plan proposal",
				Strategy:      domain.TaskStrategyDirect,
				SchemaVersion: "v1",
			},
			Profile:          profile,
			SemanticReviewer: profile,
			Continuous:       continuous,
			MaxRounds:        maxRounds,
			PromptHint:       buildSenatePlanPromptHint(profile),
		})
	}
	return assignments
}

func buildSenatePlanPromptHint(profile domain.AgentProfile) string {
	lines := []string{
		fmt.Sprintf("You are %s participating in the senate planning round.", profile.DisplayName),
		"Do not implement the task and do not edit files in this round.",
		"Produce the best execution plan you can for the original request.",
		"Your plan should be concrete enough that another agent can implement it directly.",
		"",
		"Output a markdown plan with these sections:",
		"1. Objective",
		"2. Proposed approach",
		"3. Step-by-step plan",
		"4. Expected files or areas to inspect/change",
		"5. Risks or unknowns",
	}
	return strings.Join(lines, "\n")
}

func buildSenateImplementationAssignments(taskID string, starter domain.AgentProfile, delegates []domain.AgentProfile, acceptedPlan senateCandidate, continuous bool, maxRounds int) []scheduler.NodeAssignment {
	assignments := make([]scheduler.NodeAssignment, 0, len(delegates))
	for i, delegate := range delegates {
		nodeID := fmt.Sprintf("%s_implementation_%d", taskID, i+1)
		assignments = append(assignments, scheduler.NodeAssignment{
			Node: domain.TaskNodeSpec{
				ID:            nodeID,
				Title:         "Senate implementation candidate",
				Strategy:      domain.TaskStrategyRelay,
				Dependencies:  []string{acceptedPlan.NodeID},
				SchemaVersion: "v1",
			},
			Profile:          delegate,
			SemanticReviewer: starter,
			Continuous:       continuous,
			MaxRounds:        maxRounds,
			PromptHint:       buildSenateImplementationPromptHint(starter, acceptedPlan),
		})
	}
	return assignments
}

func buildSenateImplementationPromptHint(starter domain.AgentProfile, acceptedPlan senateCandidate) string {
	lines := []string{
		fmt.Sprintf("The starter agent %s coordinated planning and will not implement code.", starter.DisplayName),
		"Implement the accepted plan below in your own isolated workspace.",
		"Do not coordinate, vote, or ask other agents to work. Your job is implementation only.",
		"When your implementation is ready for evaluation, emit:",
		"ROMA_MERGE_BACK: require_vote | implementation candidate ready",
		"Also emit one ROMA_MERGE_FILE: <relative/path> line per changed file you want reviewed.",
		"",
		"Accepted plan:",
		acceptedPlan.Body,
	}
	return strings.Join(lines, "\n")
}

func buildSenateVoteAssignments(taskID, suffix, title string, voters []domain.AgentProfile, candidates []senateCandidate, dependencies []string, continuous bool, maxRounds int) []scheduler.NodeAssignment {
	assignments := make([]scheduler.NodeAssignment, 0, len(voters))
	for i, voter := range voters {
		nodeID := fmt.Sprintf("%s_%s_%d", taskID, suffix, i+1)
		assignments = append(assignments, scheduler.NodeAssignment{
			Node: domain.TaskNodeSpec{
				ID:            nodeID,
				Title:         title,
				Strategy:      domain.TaskStrategyDirect,
				Dependencies:  append([]string(nil), dependencies...),
				SchemaVersion: "v1",
			},
			Profile:          voter,
			SemanticReviewer: voter,
			Continuous:       continuous,
			MaxRounds:        maxRounds,
			PromptHint:       buildSenateVotePromptHint(title, candidates, nil),
		})
	}
	return assignments
}

func buildSenateTieBreakAssignment(taskID, suffix, title string, starter domain.AgentProfile, candidates []senateCandidate, voteCounts map[string]int, dependencies []string, continuous bool, maxRounds int) scheduler.NodeAssignment {
	return scheduler.NodeAssignment{
		Node: domain.TaskNodeSpec{
			ID:            fmt.Sprintf("%s_%s_tiebreak", taskID, suffix),
			Title:         title,
			Strategy:      domain.TaskStrategyDirect,
			Dependencies:  append([]string(nil), dependencies...),
			SchemaVersion: "v1",
		},
		Profile:          starter,
		SemanticReviewer: starter,
		Continuous:       continuous,
		MaxRounds:        maxRounds,
		PromptHint:       buildSenateVotePromptHint(title, candidates, voteCounts),
	}
}

func buildSenateVotePromptHint(title string, candidates []senateCandidate, voteCounts map[string]int) string {
	lines := []string{
		fmt.Sprintf("You are reviewing candidates for %s.", title),
		"Do not edit files or implement code in this round.",
		"Pick exactly one candidate.",
		"Output exactly one selection line in this format:",
		"ROMA_PICK: <candidate_id> | <brief reason>",
		"",
		"Use only one of these exact candidate IDs:",
	}
	for _, candidate := range candidates {
		lines = append(lines, "- "+candidate.NodeID)
	}
	if len(voteCounts) > 0 {
		lines = append(lines, "", "Current tied vote counts:")
		for _, candidate := range candidates {
			lines = append(lines, fmt.Sprintf("- %s votes=%d", candidate.NodeID, voteCounts[candidate.NodeID]))
		}
	}
	lines = append(lines, "", "Candidate details:")
	for _, candidate := range candidates {
		lines = append(lines, fmt.Sprintf("- %s by %s", candidate.NodeID, candidate.Profile.DisplayName))
		if candidate.Summary != "" {
			lines = append(lines, "  Summary: "+candidate.Summary)
		}
		if len(candidate.ChangedFiles) > 0 {
			lines = append(lines, "  Changed files: "+strings.Join(candidate.ChangedFiles, ", "))
		}
		if candidate.Body != "" {
			lines = append(lines, "  Details:")
			for _, line := range strings.Split(candidate.Body, "\n") {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				lines = append(lines, "    "+line)
			}
		}
	}
	return strings.Join(lines, "\n")
}

func senateCandidatesForNodeIDs(assignments []scheduler.NodeAssignment, result scheduler.DispatchResult, nodeIDs []string) ([]senateCandidate, error) {
	profiles := map[string]domain.AgentProfile{}
	for _, assignment := range assignments {
		profiles[assignment.Node.ID] = assignment.Profile
	}
	out := make([]senateCandidate, 0, len(nodeIDs))
	for _, nodeID := range nodeIDs {
		artifact, ok := result.Artifacts[nodeID]
		if !ok || artifact.ID == "" {
			return nil, fmt.Errorf("senate candidate %s produced no artifact", nodeID)
		}
		report, ok := senateReportPayload(artifact)
		if !ok {
			return nil, fmt.Errorf("senate candidate %s did not produce a report payload", nodeID)
		}
		changedFiles := []string(nil)
		if report.MergeBackRequest != nil {
			changedFiles = append(changedFiles, report.MergeBackRequest.ChangedFiles...)
		}
		out = append(out, senateCandidate{
			NodeID:       nodeID,
			Profile:      profiles[nodeID],
			Artifact:     artifact,
			Summary:      strings.TrimSpace(artifacts.SummaryFromEnvelope(artifact)),
			Body:         senateCandidateBody(report.RawOutput),
			ChangedFiles: changedFiles,
		})
	}
	return out, nil
}

func senateReportPayload(envelope domain.ArtifactEnvelope) (artifacts.ReportPayload, bool) {
	switch typed := envelope.Payload.(type) {
	case artifacts.ReportPayload:
		return typed, true
	}
	raw, err := json.Marshal(envelope.Payload)
	if err != nil {
		return artifacts.ReportPayload{}, false
	}
	var payload artifacts.ReportPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return artifacts.ReportPayload{}, false
	}
	return payload, true
}

func senateCandidateBody(raw string) string {
	lines := strings.Split(stripSenateANSI(raw), "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "ROMA_MERGE_") || strings.HasPrefix(strings.ToLower(line), "tokens used") || line == "--------" {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

func extractSenateVotes(result scheduler.DispatchResult, assignments []scheduler.NodeAssignment) []senateVote {
	out := make([]senateVote, 0, len(assignments))
	for _, assignment := range assignments {
		artifact, ok := result.Artifacts[assignment.Node.ID]
		if !ok {
			continue
		}
		report, ok := senateReportPayload(artifact)
		if !ok {
			continue
		}
		targetID, reason, ok := extractSenatePick(report.RawOutput)
		if !ok {
			continue
		}
		out = append(out, senateVote{
			VoterID:  assignment.Profile.ID,
			TargetID: targetID,
			Reason:   reason,
		})
	}
	return out
}

func extractSenatePick(output string) (string, string, bool) {
	lines := strings.Split(stripSenateANSI(output), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		match := senatePickPattern.FindStringSubmatch(line)
		if len(match) == 0 {
			continue
		}
		return strings.TrimSpace(match[1]), strings.TrimSpace(match[2]), true
	}
	return "", "", false
}

func (s *Service) resolveSenateWinner(ctx context.Context, dispatcher *scheduler.Dispatcher, req Request, sessionID, taskID string, starter domain.AgentProfile, assignments []scheduler.NodeAssignment, result scheduler.DispatchResult, stage string, candidates []senateCandidate, votes []senateVote) (senateCandidate, []scheduler.NodeAssignment, scheduler.DispatchResult, error) {
	counts := map[string]int{}
	for _, vote := range votes {
		counts[vote.TargetID]++
	}
	best := make([]senateCandidate, 0, len(candidates))
	maxVotes := -1
	for _, candidate := range candidates {
		count := counts[candidate.NodeID]
		switch {
		case count > maxVotes:
			maxVotes = count
			best = []senateCandidate{candidate}
		case count == maxVotes:
			best = append(best, candidate)
		}
	}
	if len(best) == 1 {
		return best[0], assignments, result, nil
	}
	deps := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		deps = append(deps, candidate.NodeID)
	}
	tiebreak := buildSenateTieBreakAssignment(taskID, stage, "Senate "+stage+" tiebreak", starter, best, counts, deps, req.Continuous, req.MaxRounds)
	if s.tasks != nil {
		lifecycle := scheduler.NewGraphLifecycle(s.tasks, s.events)
		if err := lifecycle.RegisterTask(ctx, sessionID, tiebreak.Node, starter.ID); err != nil {
			return senateCandidate{}, assignments, result, fmt.Errorf("register senate tiebreak task %s: %w", tiebreak.Node.ID, err)
		}
	}
	assignments = append(assignments, tiebreak)
	resumeResult, err := dispatcher.Resume(ctx, sessionID, req.WorkingDir, req.Prompt, assignments, cloneArtifacts(result.Artifacts))
	if err != nil {
		return senateCandidate{}, assignments, resumeResult, err
	}
	result = resumeResult
	artifact, ok := result.Artifacts[tiebreak.Node.ID]
	if !ok {
		return senateCandidate{}, assignments, result, fmt.Errorf("senate tiebreak produced no artifact")
	}
	report, ok := senateReportPayload(artifact)
	if !ok {
		return senateCandidate{}, assignments, result, fmt.Errorf("senate tiebreak did not produce a report")
	}
	targetID, _, ok := extractSenatePick(report.RawOutput)
	if !ok {
		return senateCandidate{}, assignments, result, fmt.Errorf("senate tiebreak produced no ROMA_PICK")
	}
	for _, candidate := range best {
		if candidate.NodeID == targetID {
			return candidate, assignments, result, nil
		}
	}
	return senateCandidate{}, assignments, result, fmt.Errorf("senate tiebreak selected unknown candidate %q", targetID)
}

func (s *Service) mergeSenateWinner(ctx context.Context, req Request, sessionID string, candidate senateCandidate) error {
	manager := workspacepkg.NewManager(s.controlRoot(req.WorkingDir), s.events)
	prepared, err := manager.Get(ctx, sessionID, candidate.NodeID)
	if err != nil {
		return fmt.Errorf("load winning workspace: %w", err)
	}
	changedPaths, err := manager.ChangedPaths(ctx, prepared)
	if err != nil {
		return fmt.Errorf("inspect winning workspace changes: %w", err)
	}
	if len(changedPaths) == 0 {
		return nil
	}
	decision := policy.EvaluatePathAction(policy.ActionPlanApply, changedPaths, req.PolicyOverride, req.OverrideActor)
	if decision.Kind == policy.DecisionBlock {
		return fmt.Errorf("policy blocked winning merge: %s", decision.Reason)
	}
	preview, err := manager.PreviewMerge(ctx, prepared)
	if err != nil {
		return fmt.Errorf("preview winning merge: %w", err)
	}
	if !preview.CanApply || preview.Conflict {
		return fmt.Errorf("winning merge has conflicts")
	}
	if err := manager.MergeBackAs(ctx, prepared, events.ActorTypeSystem); err != nil {
		return fmt.Errorf("merge winning implementation: %w", err)
	}
	return nil
}

func assignmentNodeIDs(assignments []scheduler.NodeAssignment) []string {
	out := make([]string, 0, len(assignments))
	for _, assignment := range assignments {
		out = append(out, assignment.Node.ID)
	}
	return out
}

func collectArtifactIDs(items []domain.ArtifactEnvelope) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		if item.ID != "" {
			out = append(out, item.ID)
		}
	}
	return out
}

var senateANSIPattern = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]|\x1b\][^\a]*\a`)

func stripSenateANSI(text string) string {
	return senateANSIPattern.ReplaceAllString(text, "")
}
