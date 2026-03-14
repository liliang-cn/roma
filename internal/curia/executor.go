package curia

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/liliang-cn/roma/internal/artifacts"
	"github.com/liliang-cn/roma/internal/domain"
	"github.com/liliang-cn/roma/internal/runtime"
)

// Default timeouts for Curia phases
const (
	// scatterTimeout is the max time to wait for all senators to submit proposals
	scatterTimeout = 5 * time.Minute
	// reviewTimeout is the max time to wait for all reviewers to submit ballots
	reviewTimeout = 3 * time.Minute
)

type ExecuteRequest struct {
	SessionID         string
	TaskID            string
	BasePrompt        string
	WorkingDir        string
	NodeTitle         string
	Senators          []domain.AgentProfile
	Quorum            int
	ArbitrationMode   string
	Arbitrator        domain.AgentProfile
	UpstreamArtifacts []domain.ArtifactEnvelope
}

type ExecuteResult struct {
	Primary          domain.ArtifactEnvelope
	RelatedArtifacts []domain.ArtifactEnvelope
}

type Executor struct {
	supervisor *runtime.Supervisor
	artifacts  *artifacts.Service
	reputation *ReputationStore
}

func NewExecutor(workDir string, supervisor *runtime.Supervisor, artifactService *artifacts.Service) *Executor {
	return &Executor{
		supervisor: supervisor,
		artifacts:  artifactService,
		reputation: NewReputationStore(workDir),
	}
}

func (e *Executor) Execute(ctx context.Context, req ExecuteRequest) (ExecuteResult, error) {
	if len(req.Senators) == 0 {
		return ExecuteResult{}, fmt.Errorf("curia requires at least one senator")
	}
	quorum := req.Quorum
	if quorum <= 0 || quorum > len(req.Senators) {
		quorum = min(2, len(req.Senators))
		if quorum == 0 {
			quorum = 1
		}
	}

	proposals, winner, err := e.scatterAndReview(ctx, req, quorum)
	if err != nil {
		return ExecuteResult{}, err
	}
	if winner.dispute.Detected && shouldRunAugustus(req) {
		winner, err = e.runAugustus(ctx, req, proposals, winner)
		if err != nil {
			return ExecuteResult{}, err
		}
	}
	ballots := winner.ballots
	debateLog, err := e.artifacts.BuildDebateLog(ctx, artifacts.BuildDebateLogRequest{
		SessionID:             req.SessionID,
		TaskID:                req.TaskID,
		RunID:                 req.TaskID + "_debate",
		ProposalIDs:           collectProposalIDs(proposals),
		BallotIDs:             collectBallotIDs(ballots),
		WinningProposalID:     winner.proposal.ProposalID,
		DisputeClass:          winner.dispute.Class,
		ArbitrationStrategy:   winner.arbitrationStrategy,
		ArbitrationConfidence: winner.confidence,
		ConsensusStrength:     winner.consensusStrength,
		DisputeReasons:        append([]string(nil), winner.dispute.RejectedReasons...),
		EscalationReasons:     append([]string(nil), winner.escalationReasons...),
		CompetingProposalIDs:  append([]string(nil), winner.competingProposalIDs...),
		DisputeDetected:       winner.dispute.Detected,
		CriticalVeto:          winner.dispute.CriticalVeto,
		TopScoreGap:           winner.dispute.TopScoreGap,
		Scoreboard:            append([]artifacts.CuriaScoreEntry(nil), winner.dispute.Scoreboard...),
		ArbitrationRequired:   winner.dispute.Detected,
	})
	if err != nil {
		return ExecuteResult{}, err
	}
	plan, err := e.artifacts.BuildExecutionPlan(ctx, artifacts.BuildExecutionPlanRequest{
		SessionID:             req.SessionID,
		TaskID:                req.TaskID,
		RunID:                 req.TaskID + "_plan",
		Goal:                  req.BasePrompt,
		Proposal:              winner.proposal,
		WinningMode:           winner.winningMode,
		SelectedProposalIDs:   append([]string(nil), winner.selectedIDs...),
		CompetingProposalIDs:  append([]string(nil), winner.competingProposalIDs...),
		DecisionConfidence:    winner.confidence,
		ConsensusStrength:     winner.consensusStrength,
		ArbitrationStrategy:   winner.arbitrationStrategy,
		EscalationReasons:     append([]string(nil), winner.escalationReasons...),
		ApprovalReason:        approvalReason(winner),
		HumanApprovalRequired: requiresHumanApproval(winner),
	})
	if err != nil {
		return ExecuteResult{}, err
	}
	decisionPack, err := e.artifacts.BuildDecisionPack(ctx, artifacts.BuildDecisionPackRequest{
		SessionID:             req.SessionID,
		TaskID:                req.TaskID,
		RunID:                 req.TaskID + "_decision",
		WinningMode:           winner.winningMode,
		DisputeClass:          winner.dispute.Class,
		ArbitrationStrategy:   winner.arbitrationStrategy,
		ArbitrationConfidence: winner.confidence,
		ConsensusStrength:     winner.consensusStrength,
		Arbitrated:            winner.arbitrated,
		ArbitratorID:          winner.arbitratorID,
		ProducerRole:          winner.producerRole,
		ProducerAgentID:       winner.producerAgentID,
		SelectedProposalIDs:   append([]string(nil), winner.selectedIDs...),
		CompetingProposalIDs:  append([]string(nil), winner.competingProposalIDs...),
		ExecutionPlanID:       plan.ID,
		ApprovalRequired:      requiresHumanApproval(winner),
		ApprovalReason:        approvalReason(winner),
		MergedRationale:       decisionRationale(winner),
		RejectedReasons:       append([]string(nil), winner.rejectedReasons...),
		EscalationReasons:     append([]string(nil), winner.escalationReasons...),
		RiskFlags:             append([]string(nil), winner.riskFlags...),
		ReviewQuestions:       append([]string(nil), winner.reviewQuestions...),
		DissentSummary:        append([]string(nil), winner.dissentSummary...),
		CandidateSummaries:    append([]artifacts.CuriaCandidateSummary(nil), winner.candidateSummaries...),
		ReviewerBreakdown:     append([]artifacts.CuriaReviewContribution(nil), winner.reviewerBreakdown...),
		Scoreboard:            append([]artifacts.CuriaScoreEntry(nil), winner.dispute.Scoreboard...),
	})
	if err != nil {
		return ExecuteResult{}, err
	}
	_ = e.reputation.RecordOutcome(ctx, req.Senators, winner.ballotEnvelopes, winner.selectedIDs, winner.arbitrated)
	related := make([]domain.ArtifactEnvelope, 0, len(proposals)+len(ballots)+2)
	related = append(related, proposals...)
	related = append(related, ballots...)
	related = append(related, debateLog, decisionPack)
	return ExecuteResult{
		Primary:          plan,
		RelatedArtifacts: related,
	}, nil
}

type proposalEnvelope struct {
	envelope domain.ArtifactEnvelope
	proposal artifacts.ProposalPayload
	author   domain.AgentProfile
}

type ballotEnvelope struct {
	envelope domain.ArtifactEnvelope
	ballot   artifacts.BallotPayload
}

type winnerSelection struct {
	proposal           artifacts.ProposalPayload
	ballots            []domain.ArtifactEnvelope
	ballotEnvelopes    []ballotEnvelope
	selectedIDs        []string
	competingProposalIDs []string
	winningMode        string
	arbitrationStrategy string
	confidence         domain.Confidence
	consensusStrength  string
	rejectedReasons    []string
	dissentSummary     []string
	escalationReasons  []string
	riskFlags          []string
	reviewQuestions    []string
	candidateSummaries []artifacts.CuriaCandidateSummary
	reviewerBreakdown  []artifacts.CuriaReviewContribution
	approvalReason     string
	approvalOverride   *bool
	arbitrated         bool
	arbitratorID       string
	producerRole       domain.ProducerRole
	producerAgentID    string
	dispute            disputeResult
}

type disputeResult struct {
	Detected          bool
	Class             string
	Confidence        domain.Confidence
	ConsensusStrength string
	CriticalVeto      bool
	TopScoreGap       int
	WinningMode       string
	SelectedIDs       []string
	CompetingProposalIDs []string
	ArbitrationStrategy string
	RejectedReasons   []string
	DissentSummary    []string
	EscalationReasons []string
	Scoreboard        []artifacts.CuriaScoreEntry
}

type rankedProposal struct {
	id            string
	rawScore      int
	weightedScore int
	vetoCount     int
	reviewerCount int
}

func (e *Executor) scatterAndReview(ctx context.Context, req ExecuteRequest, quorum int) ([]domain.ArtifactEnvelope, winnerSelection, error) {
	proposalResults := make([]proposalEnvelope, len(req.Senators))
	execIDs := make([]string, len(req.Senators))
	var wg sync.WaitGroup
	var firstErr error
	var mu sync.Mutex
	proposalsDone := make(chan struct{}, 1)
	timedOut := false

	// Launch scatter phase with timeout
	for i, senator := range req.Senators {
		execID := "curia_scatter_" + req.TaskID + "_" + senator.ID
		execIDs[i] = execID
		wg.Add(1)
		go func(i int, senator domain.AgentProfile, execID string) {
			defer wg.Done()
			result, err := e.supervisor.RunCaptured(ctx, runtime.StartRequest{
				ExecutionID: execID,
				SessionID:   req.SessionID,
				TaskID:      req.TaskID,
				Profile:     senator,
				Prompt:      scatterPrompt(req, senator),
				WorkingDir:  req.WorkingDir,
			})
			if err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
				return
			}
			envelope, err := e.artifacts.BuildProposal(ctx, artifacts.BuildProposalRequest{
				SessionID: req.SessionID,
				TaskID:    req.TaskID,
				RunID:     req.TaskID + "_" + senator.ID,
				Agent:     senator,
				Output:    result.Stdout,
			})
			if err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
				return
			}
			mu.Lock()
			payload, _ := artifacts.ProposalFromEnvelope(envelope)
			proposalResults[i] = proposalEnvelope{envelope: envelope, proposal: payload, author: senator}
			// Signal done when we have enough proposals for quorum
			completed := 0
			for _, p := range proposalResults {
				if p.envelope.ID != "" {
					completed++
				}
			}
			if completed >= quorum && firstErr == nil {
				select {
				case proposalsDone <- struct{}{}:
				default:
				}
			}
			mu.Unlock()
		}(i, senator, execID)
	}

	// Wait for quorum proposals or timeout
	select {
	case <-proposalsDone:
		// Quorum reached, proceed
	case <-time.After(scatterTimeout):
		// Timeout reached, kill all pending executions
		timedOut = true
		for _, execID := range execIDs {
			if execID != "" {
				_ = e.supervisor.Terminate(execID)
			}
		}
	case <-ctx.Done():
		// Context cancelled, kill all executions
		for _, execID := range execIDs {
			if execID != "" {
				_ = e.supervisor.Terminate(execID)
			}
		}
		return nil, winnerSelection{}, ctx.Err()
	}

	// Wait for all goroutines to finish
	wg.Wait()

	// If we timed out, also kill any remaining executions that may still be running
	if timedOut {
		for _, execID := range execIDs {
			if execID != "" {
				_ = e.supervisor.Terminate(execID)
			}
		}
	}

	if firstErr != nil {
		return nil, winnerSelection{}, firstErr
	}

	proposals := make([]proposalEnvelope, 0, len(proposalResults))
	for _, item := range proposalResults {
		if item.envelope.ID != "" {
			proposals = append(proposals, item)
		}
	}

	// Check if we have enough proposals (either got quorum or timed out with partial)
	if len(proposals) < quorum {
		if len(proposals) > 0 {
			// Log the timeout but continue with available proposals if any
			// This allows Curia to proceed with fewer than requested senators
		} else {
			return nil, winnerSelection{}, fmt.Errorf("curia quorum not reached: got %d proposals, need %d", len(proposals), quorum)
		}
	}

	ballotResults := make([]ballotEnvelope, 0, len(req.Senators))
	rawScoreByProposal := make(map[string]int, len(proposals))
	scoreByProposal := make(map[string]int, len(proposals))
	vetoByProposal := make(map[string]int, len(proposals))
	reviewerCountByProposal := make(map[string]int, len(proposals))
	for _, senator := range req.Senators {
		target := chooseTargetProposal(senator.ID, proposals)
		if target.proposal.ProposalID == "" {
			continue
		}
		result, err := e.supervisor.RunCaptured(ctx, runtime.StartRequest{
			ExecutionID: "curia_review_" + req.TaskID + "_" + senator.ID,
			SessionID:   req.SessionID,
			TaskID:      req.TaskID,
			Profile:     senator,
			Prompt:      reviewPrompt(req, proposals, senator),
			WorkingDir:  req.WorkingDir,
		})
		if err != nil {
			return nil, winnerSelection{}, err
		}
		chosen := detectTargetProposal(result.Stdout, proposals, target.proposal.ProposalID)
		reviewScores := artifacts.BallotScoresView(result.Stdout)
		reviewVeto := strings.Contains(strings.ToLower(result.Stdout), "veto")
		rawScore := reviewScores.Correctness +
			reviewScores.Safety +
			reviewScores.Maintainability +
			reviewScores.ScopeControl +
			reviewScores.Testability
		reviewerWeight := e.reviewerWeight(ctx, senator)
		weightedScore := weightedBallotScore(artifactsBallotView{
			Scores: struct {
				Correctness     int
				Safety          int
				Maintainability int
				ScopeControl    int
				Testability     int
			}{
				Correctness:     reviewScores.Correctness,
				Safety:          reviewScores.Safety,
				Maintainability: reviewScores.Maintainability,
				ScopeControl:    reviewScores.ScopeControl,
				Testability:     reviewScores.Testability,
			},
			Veto: reviewVeto,
		}, reviewerWeight)
		envelope, err := e.artifacts.BuildBallot(ctx, artifacts.BuildBallotRequest{
			SessionID:        req.SessionID,
			TaskID:           req.TaskID,
			RunID:            req.TaskID + "_" + senator.ID,
			Agent:            senator,
			TargetProposalID: chosen,
			ReviewerWeight:   reviewerWeight,
			WeightedScore:    weightedScore,
			Output:           result.Stdout,
		})
		if err != nil {
			return nil, winnerSelection{}, err
		}
		rawBallot, _ := artifacts.BallotFromEnvelope(envelope)
		ballotResults = append(ballotResults, ballotEnvelope{envelope: envelope, ballot: rawBallot})
		rawScoreByProposal[chosen] += rawScore
		scoreByProposal[chosen] += weightedScore
		reviewerCountByProposal[chosen]++
		if rawBallot.Veto {
			vetoByProposal[chosen]++
		}
	}

	var selected proposalEnvelope
	bestScore := -1 << 20
	for _, item := range proposals {
		score := scoreByProposal[item.proposal.ProposalID]
		if score > bestScore {
			bestScore = score
			selected = item
		}
	}
	ballots := make([]domain.ArtifactEnvelope, 0, len(ballotResults))
	for _, ballot := range ballotResults {
		ballots = append(ballots, ballot.envelope)
	}
	outProposals := make([]domain.ArtifactEnvelope, 0, len(proposals))
	for _, proposal := range proposals {
		outProposals = append(outProposals, proposal.envelope)
	}
	dispute := detectDispute(proposals, rawScoreByProposal, scoreByProposal, vetoByProposal, reviewerCountByProposal)
	selectedIDs := []string{selected.proposal.ProposalID}
	if len(dispute.SelectedIDs) > 0 {
		selectedIDs = append([]string(nil), dispute.SelectedIDs...)
	}
	return outProposals, winnerSelection{
		proposal:           selected.proposal,
		ballots:            ballots,
		ballotEnvelopes:    ballotResults,
		selectedIDs:        selectedIDs,
		competingProposalIDs: append([]string(nil), dispute.CompetingProposalIDs...),
		winningMode:        dispute.WinningMode,
		arbitrationStrategy: dispute.ArbitrationStrategy,
		confidence:         dispute.Confidence,
		consensusStrength:  dispute.ConsensusStrength,
		rejectedReasons:    append([]string(nil), dispute.RejectedReasons...),
		dissentSummary:     append([]string(nil), dispute.DissentSummary...),
		escalationReasons:  append([]string(nil), dispute.EscalationReasons...),
		riskFlags:          buildRiskFlags(selected.proposal, dispute),
		reviewQuestions:    buildReviewQuestions(selected.proposal, dispute),
		candidateSummaries: buildCandidateSummaries(proposals, dispute.Scoreboard),
		reviewerBreakdown:  buildReviewerBreakdown(ballotResults),
		producerRole:       domain.ProducerRoleHuman,
		producerAgentID:    "human-arbitration",
		dispute:            dispute,
	}, nil
}

func (e *Executor) reviewerWeight(ctx context.Context, profile domain.AgentProfile) int {
	if e.reputation == nil {
		return reviewerWeight(profile)
	}
	return e.reputation.EffectiveWeight(ctx, profile)
}

func (e *Executor) runAugustus(ctx context.Context, req ExecuteRequest, proposals []domain.ArtifactEnvelope, winner winnerSelection) (winnerSelection, error) {
	if req.Arbitrator.ID == "" {
		return winnerSelection{}, fmt.Errorf("curia arbitration mode augustus requires an arbitrator profile")
	}
	result, err := e.supervisor.RunCaptured(ctx, runtime.StartRequest{
		ExecutionID: "curia_augustus_" + req.TaskID + "_" + req.Arbitrator.ID,
		SessionID:   req.SessionID,
		TaskID:      req.TaskID,
		Profile:     req.Arbitrator,
		Prompt:      augustusPrompt(req, proposals, winner),
		WorkingDir:  req.WorkingDir,
	})
	if err != nil {
		return winnerSelection{}, err
	}
	override := parseAugustusDecision(result.Stdout, proposals)
	if override.winningMode != "" {
		winner.winningMode = override.winningMode
	}
	if override.confidence != "" {
		winner.confidence = override.confidence
	}
	if override.consensusStrength != "" {
		winner.consensusStrength = override.consensusStrength
	} else if winner.consensusStrength == "" {
		winner.consensusStrength = "augustus_resolved"
	}
	if override.arbitrationStrategy != "" {
		winner.arbitrationStrategy = override.arbitrationStrategy
	}
	if len(override.selectedIDs) > 0 {
		winner.selectedIDs = append([]string(nil), override.selectedIDs...)
		if proposal, ok := selectProposalByID(proposals, override.selectedIDs[0]); ok {
			winner.proposal = proposal
		}
	}
	if len(override.competingIDs) > 0 {
		winner.competingProposalIDs = append([]string(nil), override.competingIDs...)
	}
	if override.rationale != "" {
		winner.rejectedReasons = mergeUniqueStrings(winner.rejectedReasons, []string{"augustus arbitration completed"})
		winner.riskFlags = mergeUniqueStrings(winner.riskFlags, []string{"augustus_arbitrated"})
	}
	winner.escalationReasons = mergeUniqueStrings(winner.escalationReasons, override.escalationReasons)
	winner.riskFlags = mergeUniqueStrings(winner.riskFlags, override.riskFlags)
	winner.reviewQuestions = mergeUniqueStrings(winner.reviewQuestions, override.reviewQuestions)
	winner.dissentSummary = mergeUniqueStrings(winner.dissentSummary, override.dissentSummary)
	if override.approvalRequired != nil {
		value := *override.approvalRequired
		winner.approvalOverride = &value
	}
	if override.approvalReason != "" {
		winner.approvalReason = override.approvalReason
	}
	winner.arbitrated = true
	winner.arbitratorID = req.Arbitrator.ID
	winner.producerRole = domain.ProducerRoleArbitrator
	winner.producerAgentID = req.Arbitrator.ID
	if override.rationale != "" {
		winner.reviewQuestions = append([]string{override.rationale}, winner.reviewQuestions...)
	}
	return winner, nil
}

func shouldRunAugustus(req ExecuteRequest) bool {
	if req.Arbitrator.ID == "" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(req.ArbitrationMode)) {
	case "", "augustus", "auto":
		return true
	case "human":
		return false
	default:
		return false
	}
}

type augustusDecision struct {
	winningMode       string
	selectedIDs       []string
	competingIDs      []string
	confidence        domain.Confidence
	consensusStrength string
	arbitrationStrategy string
	approvalRequired  *bool
	approvalReason    string
	rationale         string
	escalationReasons []string
	riskFlags         []string
	reviewQuestions   []string
	dissentSummary    []string
}

func augustusPrompt(req ExecuteRequest, proposals []domain.ArtifactEnvelope, winner winnerSelection) string {
	var b strings.Builder
	b.WriteString("ROMA Curia Augustus arbitration phase.\n")
	b.WriteString("Return a final arbitration decision using this exact shape:\n")
	b.WriteString("winning_mode: accept|merge|replace\n")
	b.WriteString("selected_proposals: prop_x[,prop_y]\n")
	b.WriteString("competing_proposals: prop_x[,prop_y]\n")
	b.WriteString("confidence: low|medium|high\n")
	b.WriteString("consensus_strength: strong_consensus|moderate_consensus|disputed_consensus|augustus_resolved\n")
	b.WriteString("arbitration_strategy: accept_highest_score|merge_top_candidates|replace_with_runner_up|augustus_tie_break\n")
	b.WriteString("approval_required: true|false\n")
	b.WriteString("approval_reason: one concise sentence\n")
	b.WriteString("rationale: one concise sentence\n")
	b.WriteString("escalation_reasons:\n- reason\n")
	b.WriteString("risk_flags:\n- flag\n")
	b.WriteString("review_questions:\n- question?\n\n")
	b.WriteString("dissent_summary:\n- dissent note\n\n")
	b.WriteString("Task:\n")
	b.WriteString(req.BasePrompt)
	b.WriteString("\n\nDispute class: ")
	b.WriteString(winner.dispute.Class)
	b.WriteString("\nTop score gap: ")
	b.WriteString(fmt.Sprintf("%d", winner.dispute.TopScoreGap))
	b.WriteString("\nCurrent winning mode: ")
	b.WriteString(winner.winningMode)
	b.WriteString("\nCurrent arbitration strategy: ")
	b.WriteString(winner.arbitrationStrategy)
	b.WriteString("\nCurrent selected proposals: ")
	b.WriteString(strings.Join(winner.selectedIDs, ","))
	if len(winner.competingProposalIDs) > 0 {
		b.WriteString("\nCurrent competing proposals: ")
		b.WriteString(strings.Join(winner.competingProposalIDs, ","))
	}
	if len(winner.escalationReasons) > 0 {
		b.WriteString("\nEscalation reasons:\n")
		for _, reason := range winner.escalationReasons {
			b.WriteString("- ")
			b.WriteString(reason)
			b.WriteString("\n")
		}
	}
	b.WriteString("\n\nProposal scoreboard:\n")
	for _, item := range winner.dispute.Scoreboard {
		b.WriteString(fmt.Sprintf("- %s raw=%d weighted=%d veto=%d reviewers=%d\n", item.ProposalID, item.RawScore, item.WeightedScore, item.VetoCount, item.ReviewerCount))
	}
	if len(winner.reviewerBreakdown) > 0 {
		b.WriteString("\nReviewer breakdown:\n")
		for _, item := range winner.reviewerBreakdown {
			b.WriteString(fmt.Sprintf("- %s -> %s raw=%d weighted=%d veto=%t\n", item.ReviewerID, item.TargetProposalID, item.RawScore, item.WeightedScore, item.Veto))
		}
	}
	b.WriteString("\nProposal summaries:\n")
	for _, envelope := range proposals {
		payload, ok := artifacts.ProposalFromEnvelope(envelope)
		if !ok {
			continue
		}
		b.WriteString(fmt.Sprintf("- %s: %s\n", payload.ProposalID, payload.Summary))
	}
	return b.String()
}

func parseAugustusDecision(output string, proposals []domain.ArtifactEnvelope) augustusDecision {
	var decision augustusDecision
	lines := strings.Split(output, "\n")
	modeRe := regexp.MustCompile(`(?i)^winning_mode:\s*(accept|merge|replace)\s*$`)
	selectedRe := regexp.MustCompile(`(?i)^selected_proposals:\s*(.+)\s*$`)
	competingRe := regexp.MustCompile(`(?i)^competing_proposals:\s*(.+)\s*$`)
	confidenceRe := regexp.MustCompile(`(?i)^confidence:\s*(low|medium|high)\s*$`)
	consensusStrengthRe := regexp.MustCompile(`(?i)^consensus_strength:\s*([a-z_]+)\s*$`)
	arbitrationStrategyRe := regexp.MustCompile(`(?i)^arbitration_strategy:\s*([a-z_]+)\s*$`)
	approvalRequiredRe := regexp.MustCompile(`(?i)^approval_required:\s*(true|false)\s*$`)
	approvalReasonRe := regexp.MustCompile(`(?i)^approval_reason:\s*(.+)\s*$`)
	rationaleRe := regexp.MustCompile(`(?i)^rationale:\s*(.+)\s*$`)
	section := ""
	validIDs := make(map[string]struct{}, len(proposals))
	for _, envelope := range proposals {
		if payload, ok := artifacts.ProposalFromEnvelope(envelope); ok {
			validIDs[payload.ProposalID] = struct{}{}
		}
	}
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		switch {
		case modeRe.MatchString(line):
			decision.winningMode = strings.ToLower(modeRe.FindStringSubmatch(line)[1])
			section = ""
		case selectedRe.MatchString(line):
			section = ""
			decision.selectedIDs = parseProposalIDs(selectedRe.FindStringSubmatch(line)[1], validIDs)
		case competingRe.MatchString(line):
			section = ""
			decision.competingIDs = parseProposalIDs(competingRe.FindStringSubmatch(line)[1], validIDs)
		case confidenceRe.MatchString(line):
			decision.confidence = domain.Confidence(strings.ToLower(confidenceRe.FindStringSubmatch(line)[1]))
			section = ""
		case consensusStrengthRe.MatchString(line):
			decision.consensusStrength = strings.ToLower(consensusStrengthRe.FindStringSubmatch(line)[1])
			section = ""
		case arbitrationStrategyRe.MatchString(line):
			decision.arbitrationStrategy = strings.ToLower(arbitrationStrategyRe.FindStringSubmatch(line)[1])
			section = ""
		case approvalRequiredRe.MatchString(line):
			value := strings.EqualFold(approvalRequiredRe.FindStringSubmatch(line)[1], "true")
			decision.approvalRequired = &value
			section = ""
		case approvalReasonRe.MatchString(line):
			decision.approvalReason = approvalReasonRe.FindStringSubmatch(line)[1]
			section = ""
		case rationaleRe.MatchString(line):
			decision.rationale = rationaleRe.FindStringSubmatch(line)[1]
			section = ""
		case strings.EqualFold(line, "escalation_reasons:"):
			section = "escalation"
		case strings.EqualFold(line, "risk_flags:"):
			section = "risk"
		case strings.EqualFold(line, "review_questions:"):
			section = "review"
		case strings.EqualFold(line, "dissent_summary:"):
			section = "dissent"
		case strings.HasPrefix(line, "- ") && section == "escalation":
			decision.escalationReasons = append(decision.escalationReasons, strings.TrimSpace(strings.TrimPrefix(line, "- ")))
		case strings.HasPrefix(line, "- ") && section == "risk":
			decision.riskFlags = append(decision.riskFlags, strings.TrimSpace(strings.TrimPrefix(line, "- ")))
		case strings.HasPrefix(line, "- ") && section == "review":
			decision.reviewQuestions = append(decision.reviewQuestions, strings.TrimSpace(strings.TrimPrefix(line, "- ")))
		case strings.HasPrefix(line, "- ") && section == "dissent":
			decision.dissentSummary = append(decision.dissentSummary, strings.TrimSpace(strings.TrimPrefix(line, "- ")))
		}
	}
	return decision
}

func parseProposalIDs(raw string, validIDs map[string]struct{}) []string {
	chunks := strings.Split(raw, ",")
	out := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		id := strings.TrimSpace(chunk)
		if _, ok := validIDs[id]; ok {
			out = append(out, id)
		}
	}
	return out
}

func selectProposalByID(proposals []domain.ArtifactEnvelope, id string) (artifacts.ProposalPayload, bool) {
	for _, envelope := range proposals {
		payload, ok := artifacts.ProposalFromEnvelope(envelope)
		if ok && payload.ProposalID == id {
			return payload, true
		}
	}
	return artifacts.ProposalPayload{}, false
}

func mergeUniqueStrings(base []string, extra []string) []string {
	out := append([]string(nil), base...)
	for _, item := range extra {
		item = strings.TrimSpace(item)
		if item == "" || containsString(out, item) {
			continue
		}
		out = append(out, item)
	}
	return out
}

func buildCandidateSummaries(proposals []proposalEnvelope, scoreboard []artifacts.CuriaScoreEntry) []artifacts.CuriaCandidateSummary {
	byProposal := make(map[string]artifacts.CuriaScoreEntry, len(scoreboard))
	for _, item := range scoreboard {
		byProposal[item.ProposalID] = item
	}
	out := make([]artifacts.CuriaCandidateSummary, 0, len(proposals))
	for _, proposal := range proposals {
		score := byProposal[proposal.proposal.ProposalID]
		out = append(out, artifacts.CuriaCandidateSummary{
			ProposalID:    proposal.proposal.ProposalID,
			Summary:       proposal.proposal.Summary,
			RawScore:      score.RawScore,
			WeightedScore: score.WeightedScore,
			VetoCount:     score.VetoCount,
		})
	}
	return out
}

func buildRiskFlags(proposal artifacts.ProposalPayload, dispute disputeResult) []string {
	flags := make([]string, 0, len(proposal.DesignRisks)+2)
	seen := map[string]struct{}{}
	appendFlag := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		if _, ok := seen[value]; ok {
			return
		}
		seen[value] = struct{}{}
		flags = append(flags, value)
	}
	if dispute.CriticalVeto {
		appendFlag("critical_veto")
	}
	if dispute.Class == "close_score" || dispute.Class == "close_score+critical_veto" {
		appendFlag("close_score")
	}
	for _, reason := range dispute.EscalationReasons {
		appendFlag(reason)
	}
	for _, risk := range proposal.DesignRisks {
		appendFlag(risk)
	}
	for _, reason := range dispute.RejectedReasons {
		appendFlag(reason)
	}
	return flags
}

func buildReviewQuestions(proposal artifacts.ProposalPayload, dispute disputeResult) []string {
	questions := make([]string, 0, 4)
	if dispute.CriticalVeto {
		questions = append(questions, "What concrete flaw caused the leading proposal to be critically vetoed?")
	}
	if dispute.Class == "close_score" || dispute.Class == "close_score+critical_veto" {
		questions = append(questions, "Which tradeoff most clearly separates the top Curia proposals?")
	}
	for _, reason := range dispute.EscalationReasons {
		questions = append(questions, "How should Curia resolve this escalation trigger: "+reason+"?")
		if len(questions) >= 4 {
			break
		}
	}
	for _, risk := range proposal.DesignRisks {
		questions = append(questions, "How will the plan mitigate this risk: "+risk+"?")
		if len(questions) >= 4 {
			break
		}
	}
	if len(questions) == 0 {
		questions = append(questions, "What validation should happen before this Curia execution plan is applied?")
	}
	return questions
}

func buildReviewerBreakdown(ballots []ballotEnvelope) []artifacts.CuriaReviewContribution {
	out := make([]artifacts.CuriaReviewContribution, 0, len(ballots))
	for _, ballot := range ballots {
		reviewerID := ballot.envelope.Producer.AgentID
		raw := ballot.ballot.Scores.Correctness +
			ballot.ballot.Scores.Safety +
			ballot.ballot.Scores.Maintainability +
			ballot.ballot.Scores.ScopeControl +
			ballot.ballot.Scores.Testability
		out = append(out, artifacts.CuriaReviewContribution{
			ReviewerID:       reviewerID,
			TargetProposalID: ballot.ballot.TargetProposalID,
			RawScore:         raw,
			ReviewerWeight:   ballot.ballot.ReviewerWeight,
			WeightedScore:    ballot.ballot.WeightedScore,
			Veto:             ballot.ballot.Veto,
		})
	}
	return out
}

func scatterPrompt(req ExecuteRequest, senator domain.AgentProfile) string {
	var b strings.Builder
	b.WriteString("ROMA Curia scatter phase.\n")
	b.WriteString("Produce one executable proposal only.\n")
	b.WriteString("Task:\n")
	b.WriteString(req.BasePrompt)
	b.WriteString("\n\nNode:\n")
	b.WriteString(req.NodeTitle)
	b.WriteString("\n\nRespond with a concise implementation proposal including approach, affected files, risks, and steps.\n")
	_ = senator
	return b.String()
}

func reviewPrompt(req ExecuteRequest, proposals []proposalEnvelope, senator domain.AgentProfile) string {
	var b strings.Builder
	b.WriteString("ROMA Curia blind review phase.\n")
	b.WriteString("Review the anonymous proposals below and choose the strongest one.\n")
	b.WriteString("Task:\n")
	b.WriteString(req.BasePrompt)
	b.WriteString("\n\nProposals:\n")
	// Build anonymous ID mapping to hide author identity
	anonIDByProposal := make(map[string]string, len(proposals))
	for i, proposal := range proposals {
		anonID := fmt.Sprintf("proposal_%d", i+1)
		anonIDByProposal[proposal.proposal.ProposalID] = anonID
		b.WriteString("- ")
		b.WriteString(anonID)
		b.WriteString(": ")
		b.WriteString(proposal.proposal.Summary)
		b.WriteString("\n")
	}
	b.WriteString("\nReply by naming the best proposal id (e.g., proposal_1) and giving a short critique.\n")
	_ = senator
	_ = anonIDByProposal // TODO: pass to ballot detection to map back
	return b.String()
}

func chooseTargetProposal(reviewerID string, proposals []proposalEnvelope) proposalEnvelope {
	for _, proposal := range proposals {
		if proposal.author.ID != reviewerID {
			return proposal
		}
	}
	if len(proposals) > 0 {
		return proposals[0]
	}
	return proposalEnvelope{}
}

func detectTargetProposal(output string, proposals []proposalEnvelope, fallback string) string {
	for _, proposal := range proposals {
		if strings.Contains(output, proposal.proposal.ProposalID) {
			return proposal.proposal.ProposalID
		}
	}
	return fallback
}

func collectProposalIDs(items []domain.ArtifactEnvelope) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		if payload, ok := artifacts.ProposalFromEnvelope(item); ok {
			out = append(out, payload.ProposalID)
		}
	}
	return out
}

func collectBallotIDs(items []domain.ArtifactEnvelope) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		if payload, ok := artifacts.BallotFromEnvelope(item); ok {
			out = append(out, payload.BallotID)
		}
	}
	return out
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func detectDispute(proposals []proposalEnvelope, rawScoreByProposal map[string]int, scoreByProposal map[string]int, vetoByProposal map[string]int, reviewerCountByProposal map[string]int) disputeResult {
	ranked := make([]rankedProposal, 0, len(proposals))
	for _, proposal := range proposals {
		ranked = append(ranked, rankedProposal{
			id:            proposal.proposal.ProposalID,
			rawScore:      rawScoreByProposal[proposal.proposal.ProposalID],
			weightedScore: scoreByProposal[proposal.proposal.ProposalID],
			vetoCount:     vetoByProposal[proposal.proposal.ProposalID],
			reviewerCount: reviewerCountByProposal[proposal.proposal.ProposalID],
		})
	}
	if len(ranked) == 0 {
		return disputeResult{
			WinningMode:         "accept",
			Class:               "none",
			Confidence:          domain.ConfidenceMedium,
			ConsensusStrength:   "moderate_consensus",
			ArbitrationStrategy: "accept_highest_score",
		}
	}
	for i := 0; i < len(ranked); i++ {
		for j := i + 1; j < len(ranked); j++ {
			if ranked[j].weightedScore > ranked[i].weightedScore {
				ranked[i], ranked[j] = ranked[j], ranked[i]
			}
		}
	}
	result := disputeResult{
		WinningMode:         "accept",
		Class:               "none",
		Confidence:          domain.ConfidenceMedium,
		ConsensusStrength:   "moderate_consensus",
		SelectedIDs:         []string{ranked[0].id},
		CompetingProposalIDs: []string{ranked[0].id},
		ArbitrationStrategy: "accept_highest_score",
	}
	if len(ranked) > 1 {
		result.TopScoreGap = ranked[0].weightedScore - ranked[1].weightedScore
		result.CompetingProposalIDs = []string{ranked[0].id, ranked[1].id}
		if len(ranked) > 2 && ranked[0].weightedScore-ranked[2].weightedScore <= 5 {
			result.CompetingProposalIDs = append(result.CompetingProposalIDs, ranked[2].id)
			result.EscalationReasons = append(result.EscalationReasons, "crowded_scoreboard")
		}
		if abs(ranked[0].reviewerCount-ranked[1].reviewerCount) <= 1 && result.TopScoreGap <= 5 {
			result.EscalationReasons = append(result.EscalationReasons, "split_reviewer_support")
		}
		switch {
		case result.TopScoreGap >= 8:
			result.Confidence = domain.ConfidenceHigh
			result.ConsensusStrength = "strong_consensus"
		case result.TopScoreGap >= 4:
			result.Confidence = domain.ConfidenceMedium
			result.ConsensusStrength = "moderate_consensus"
		default:
			result.Detected = true
			result.Class = "close_score"
			result.WinningMode = "merge"
			result.Confidence = domain.ConfidenceLow
			result.ConsensusStrength = "disputed_consensus"
			result.SelectedIDs = []string{ranked[0].id, ranked[1].id}
			result.ArbitrationStrategy = "merge_top_candidates"
			result.RejectedReasons = append(result.RejectedReasons, "top proposals are too close to accept one without merge review")
			result.EscalationReasons = append(result.EscalationReasons, "close_score")
		}
	} else {
		result.Confidence = domain.ConfidenceHigh
		result.ConsensusStrength = "strong_consensus"
	}
	if vetoByProposal[ranked[0].id] > 0 {
		result.Detected = true
		result.CriticalVeto = true
		result.Confidence = domain.ConfidenceLow
		result.EscalationReasons = append(result.EscalationReasons, "critical_veto")
		if result.Class == "close_score" {
			result.Class = "close_score+critical_veto"
		} else {
			result.Class = "critical_veto"
		}
		if len(ranked) > 1 {
			result.WinningMode = "replace"
			result.ArbitrationStrategy = "replace_with_runner_up"
			result.ConsensusStrength = "veto_replacement"
			result.SelectedIDs = []string{ranked[1].id}
			result.RejectedReasons = append(result.RejectedReasons, fmt.Sprintf("%s received a critical veto and was replaced by %s", ranked[0].id, ranked[1].id))
		} else {
			result.WinningMode = "merge"
			result.ArbitrationStrategy = "augustus_tie_break"
			result.ConsensusStrength = "blocked_by_veto"
			if len(result.SelectedIDs) == 0 {
				result.SelectedIDs = []string{ranked[0].id}
			}
		}
	}
	if totalVetoes(ranked) >= 2 {
		result.Detected = true
		result.Confidence = domain.ConfidenceLow
		result.EscalationReasons = append(result.EscalationReasons, "multi_veto")
		if result.ArbitrationStrategy == "accept_highest_score" {
			result.ArbitrationStrategy = "augustus_tie_break"
		}
		if result.Class == "none" {
			result.Class = "multi_veto"
		}
	}
	result.EscalationReasons = mergeUniqueStrings(nil, result.EscalationReasons)
	result.Scoreboard = make([]artifacts.CuriaScoreEntry, 0, len(ranked))
	for _, proposal := range ranked {
		result.Scoreboard = append(result.Scoreboard, artifacts.CuriaScoreEntry{
			ProposalID:    proposal.id,
			RawScore:      proposal.rawScore,
			WeightedScore: proposal.weightedScore,
			VetoCount:     proposal.vetoCount,
			ReviewerCount: proposal.reviewerCount,
		})
	}
	for _, proposal := range ranked {
		if containsString(result.SelectedIDs, proposal.id) {
			continue
		}
		result.RejectedReasons = append(result.RejectedReasons, fmt.Sprintf("%s scored lower in Curia review", proposal.id))
	}
	result.DissentSummary = buildDissentSummary(ranked, result.SelectedIDs)
	return result
}

func buildDissentSummary(ranked []rankedProposal, selectedIDs []string) []string {
	out := make([]string, 0, len(ranked))
	for _, proposal := range ranked {
		if containsString(selectedIDs, proposal.id) {
			continue
		}
		out = append(out, fmt.Sprintf("%s was not selected (weighted=%d veto=%d)", proposal.id, proposal.weightedScore, proposal.vetoCount))
	}
	return out
}

func decisionRationale(winner winnerSelection) string {
	switch winner.winningMode {
	case "merge":
		return "Curia selected a merge path after detecting a tightly clustered scoreboard that benefits from combined execution."
	case "replace":
		return "Curia selected a replacement path after dispute handling rejected the prior leader."
	default:
		return "Curia selected the highest-scoring proposal without requiring a merge or replacement path."
	}
}

func requiresHumanApproval(winner winnerSelection) bool {
	if winner.approvalOverride != nil {
		return *winner.approvalOverride
	}
	if !winner.arbitrated {
		return true
	}
	if winner.confidence != domain.ConfidenceHigh {
		return true
	}
	if len(winner.escalationReasons) > 1 {
		return true
	}
	if winner.winningMode == "merge" && winner.consensusStrength != "augustus_resolved" && winner.consensusStrength != "strong_consensus" {
		return true
	}
	return false
}

func approvalReason(winner winnerSelection) string {
	if winner.approvalReason != "" {
		return winner.approvalReason
	}
	if !winner.arbitrated {
		return "human arbitration remains required because Curia did not run an automated arbitrator"
	}
	if winner.confidence != domain.ConfidenceHigh {
		return "automated arbitration confidence is below high"
	}
	if len(winner.escalationReasons) > 1 {
		return "multiple unresolved escalation factors still require human approval"
	}
	if winner.winningMode == "merge" && winner.consensusStrength != "augustus_resolved" && winner.consensusStrength != "strong_consensus" {
		return "merge-mode execution still needs a human approval gate"
	}
	return "high-confidence automated arbitration cleared the execution plan"
}

func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

func totalVetoes(items []rankedProposal) int {
	total := 0
	for _, item := range items {
		total += item.vetoCount
	}
	return total
}

func containsString(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}
