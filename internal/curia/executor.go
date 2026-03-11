package curia

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/liliang/roma/internal/artifacts"
	"github.com/liliang/roma/internal/domain"
	"github.com/liliang/roma/internal/runtime"
)

type ExecuteRequest struct {
	SessionID         string
	TaskID            string
	BasePrompt        string
	WorkingDir        string
	NodeTitle         string
	Senators          []domain.AgentProfile
	Quorum            int
	UpstreamArtifacts []domain.ArtifactEnvelope
}

type ExecuteResult struct {
	Primary          domain.ArtifactEnvelope
	RelatedArtifacts []domain.ArtifactEnvelope
}

type Executor struct {
	supervisor *runtime.Supervisor
	artifacts  *artifacts.Service
}

func NewExecutor(supervisor *runtime.Supervisor, artifactService *artifacts.Service) *Executor {
	return &Executor{supervisor: supervisor, artifacts: artifactService}
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
	ballots := winner.ballots
	debateLog, err := e.artifacts.BuildDebateLog(ctx, artifacts.BuildDebateLogRequest{
		SessionID:           req.SessionID,
		TaskID:              req.TaskID,
		RunID:               req.TaskID + "_debate",
		ProposalIDs:         collectProposalIDs(proposals),
		BallotIDs:           collectBallotIDs(ballots),
		WinningProposalID:   winner.proposal.ProposalID,
		DisputeDetected:     winner.dispute.Detected,
		CriticalVeto:        winner.dispute.CriticalVeto,
		TopScoreGap:         winner.dispute.TopScoreGap,
		ArbitrationRequired: winner.dispute.Detected,
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
		HumanApprovalRequired: true,
	})
	if err != nil {
		return ExecuteResult{}, err
	}
	decisionPack, err := e.artifacts.BuildDecisionPack(ctx, artifacts.BuildDecisionPackRequest{
		SessionID:           req.SessionID,
		TaskID:              req.TaskID,
		RunID:               req.TaskID + "_decision",
		WinningMode:         winner.winningMode,
		SelectedProposalIDs: append([]string(nil), winner.selectedIDs...),
		ExecutionPlanID:     plan.ID,
		ApprovalRequired:    true,
		MergedRationale:     decisionRationale(winner),
		RejectedReasons:     append([]string(nil), winner.rejectedReasons...),
	})
	if err != nil {
		return ExecuteResult{}, err
	}
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
	proposal        artifacts.ProposalPayload
	ballots         []domain.ArtifactEnvelope
	selectedIDs     []string
	winningMode     string
	rejectedReasons []string
	dispute         disputeResult
}

type disputeResult struct {
	Detected        bool
	CriticalVeto    bool
	TopScoreGap     int
	WinningMode     string
	SelectedIDs     []string
	RejectedReasons []string
}

func (e *Executor) scatterAndReview(ctx context.Context, req ExecuteRequest, quorum int) ([]domain.ArtifactEnvelope, winnerSelection, error) {
	proposalResults := make([]proposalEnvelope, len(req.Senators))
	var wg sync.WaitGroup
	var firstErr error
	var mu sync.Mutex
	for i, senator := range req.Senators {
		wg.Add(1)
		go func(i int, senator domain.AgentProfile) {
			defer wg.Done()
			result, err := e.supervisor.RunCaptured(ctx, runtime.StartRequest{
				ExecutionID: "curia_scatter_" + req.TaskID + "_" + senator.ID,
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
			payload, _ := artifacts.ProposalFromEnvelope(envelope)
			proposalResults[i] = proposalEnvelope{envelope: envelope, proposal: payload, author: senator}
		}(i, senator)
	}
	wg.Wait()
	if firstErr != nil {
		return nil, winnerSelection{}, firstErr
	}
	proposals := make([]proposalEnvelope, 0, len(proposalResults))
	for _, item := range proposalResults {
		if item.envelope.ID != "" {
			proposals = append(proposals, item)
		}
	}
	if len(proposals) < quorum {
		return nil, winnerSelection{}, fmt.Errorf("curia quorum not reached: got %d proposals, need %d", len(proposals), quorum)
	}

	ballotResults := make([]ballotEnvelope, 0, len(req.Senators))
	scoreByProposal := make(map[string]int, len(proposals))
	vetoByProposal := make(map[string]int, len(proposals))
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
		envelope, err := e.artifacts.BuildBallot(ctx, artifacts.BuildBallotRequest{
			SessionID:        req.SessionID,
			TaskID:           req.TaskID,
			RunID:            req.TaskID + "_" + senator.ID,
			Agent:            senator,
			TargetProposalID: chosen,
			Output:           result.Stdout,
		})
		if err != nil {
			return nil, winnerSelection{}, err
		}
		rawBallot, _ := artifacts.BallotFromEnvelope(envelope)
		ballotResults = append(ballotResults, ballotEnvelope{envelope: envelope, ballot: rawBallot})
		scoreByProposal[chosen] += rawBallot.Scores.Correctness + rawBallot.Scores.Safety + rawBallot.Scores.Maintainability + rawBallot.Scores.ScopeControl + rawBallot.Scores.Testability
		if rawBallot.Veto {
			scoreByProposal[chosen] -= 10
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
	dispute := detectDispute(proposals, scoreByProposal, vetoByProposal)
	selectedIDs := []string{selected.proposal.ProposalID}
	if len(dispute.SelectedIDs) > 0 {
		selectedIDs = append([]string(nil), dispute.SelectedIDs...)
	}
	return outProposals, winnerSelection{
		proposal:        selected.proposal,
		ballots:         ballots,
		selectedIDs:     selectedIDs,
		winningMode:     dispute.WinningMode,
		rejectedReasons: append([]string(nil), dispute.RejectedReasons...),
		dispute:         dispute,
	}, nil
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
	for _, proposal := range proposals {
		b.WriteString("- ")
		b.WriteString(proposal.proposal.ProposalID)
		b.WriteString(": ")
		b.WriteString(proposal.proposal.Summary)
		b.WriteString("\n")
	}
	b.WriteString("\nReply by naming the best proposal id and giving a short critique.\n")
	_ = senator
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

func detectDispute(proposals []proposalEnvelope, scoreByProposal map[string]int, vetoByProposal map[string]int) disputeResult {
	type rankedProposal struct {
		id    string
		score int
	}
	ranked := make([]rankedProposal, 0, len(proposals))
	for _, proposal := range proposals {
		ranked = append(ranked, rankedProposal{
			id:    proposal.proposal.ProposalID,
			score: scoreByProposal[proposal.proposal.ProposalID],
		})
	}
	if len(ranked) == 0 {
		return disputeResult{WinningMode: "accept"}
	}
	for i := 0; i < len(ranked); i++ {
		for j := i + 1; j < len(ranked); j++ {
			if ranked[j].score > ranked[i].score {
				ranked[i], ranked[j] = ranked[j], ranked[i]
			}
		}
	}
	result := disputeResult{
		WinningMode: "accept",
		SelectedIDs: []string{ranked[0].id},
	}
	if len(ranked) > 1 {
		result.TopScoreGap = ranked[0].score - ranked[1].score
		if result.TopScoreGap <= 3 {
			result.Detected = true
			result.WinningMode = "merge"
			result.SelectedIDs = []string{ranked[0].id, ranked[1].id}
		}
	}
	if vetoByProposal[ranked[0].id] > 0 {
		result.Detected = true
		result.CriticalVeto = true
		result.WinningMode = "merge"
		if len(result.SelectedIDs) == 0 {
			result.SelectedIDs = []string{ranked[0].id}
		}
	}
	for _, proposal := range ranked {
		if containsString(result.SelectedIDs, proposal.id) {
			continue
		}
		result.RejectedReasons = append(result.RejectedReasons, fmt.Sprintf("%s scored lower in Curia review", proposal.id))
	}
	return result
}

func decisionRationale(winner winnerSelection) string {
	switch winner.winningMode {
	case "merge":
		return "Curia detected a close or veto-affected outcome and selected a merge decision for human review."
	case "replace":
		return "Curia selected a replacement decision after dispute handling."
	default:
		return "Curia selected the highest-scoring proposal."
	}
}

func containsString(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}
