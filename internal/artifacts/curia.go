package artifacts

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/liliang/roma/internal/domain"
)

const (
	ProposalPayloadSchema      = "roma/proposal/v1"
	BallotPayloadSchema        = "roma/ballot/v1"
	DebateLogPayloadSchema     = "roma/debate_log/v1"
	DecisionPackPayloadSchema  = "roma/decision_pack/v1"
	ExecutionPlanPayloadSchema = "roma/execution_plan/v1"
)

type ProposalPayload struct {
	ProposalID     string            `json:"proposal_id"`
	Summary        string            `json:"summary"`
	Approach       string            `json:"approach"`
	AffectedFiles  []string          `json:"affected_files,omitempty"`
	DesignRisks    []string          `json:"design_risks,omitempty"`
	Tradeoffs      []string          `json:"tradeoffs,omitempty"`
	EstimatedSteps []string          `json:"estimated_steps,omitempty"`
	PatchPlan      []string          `json:"patch_plan,omitempty"`
	Confidence     domain.Confidence `json:"confidence"`
}

type BallotScores struct {
	Correctness     int `json:"correctness"`
	Safety          int `json:"safety"`
	Maintainability int `json:"maintainability"`
	ScopeControl    int `json:"scope_control"`
	Testability     int `json:"testability"`
}

type BallotPayload struct {
	BallotID         string            `json:"ballot_id"`
	TargetProposalID string            `json:"target_proposal_id"`
	ReviewerWeight   int               `json:"reviewer_weight"`
	WeightedScore    int               `json:"weighted_score"`
	Scores           BallotScores      `json:"scores"`
	Critique         string            `json:"critique"`
	Veto             bool              `json:"veto"`
	VetoReason       string            `json:"veto_reason,omitempty"`
	Confidence       domain.Confidence `json:"confidence"`
}

type CuriaScoreEntry struct {
	ProposalID    string `json:"proposal_id"`
	RawScore      int    `json:"raw_score"`
	WeightedScore int    `json:"weighted_score"`
	VetoCount     int    `json:"veto_count"`
	ReviewerCount int    `json:"reviewer_count"`
}

type DebateLogPayload struct {
	DebateLogID         string            `json:"debate_log_id"`
	ProposalIDs         []string          `json:"proposal_ids"`
	BallotIDs           []string          `json:"ballot_ids"`
	DisputeSummary      string            `json:"dispute_summary"`
	DisputeReasons      []string          `json:"dispute_reasons,omitempty"`
	DisputeDetected     bool              `json:"dispute_detected"`
	CriticalVeto        bool              `json:"critical_veto"`
	TopScoreGap         int               `json:"top_score_gap"`
	Scoreboard          []CuriaScoreEntry `json:"scoreboard,omitempty"`
	QuorumReachedAt     string            `json:"quorum_reached_at"`
	ArbitrationRequired bool              `json:"arbitration_required"`
	WinningProposalID   string            `json:"winning_proposal_id,omitempty"`
}

type DecisionPackPayload struct {
	DecisionPackID      string            `json:"decision_pack_id"`
	WinningMode         string            `json:"winning_mode"`
	SelectedProposalIDs []string          `json:"selected_proposal_ids"`
	MergedRationale     string            `json:"merged_rationale"`
	RejectedReasons     []string          `json:"rejected_reasons,omitempty"`
	Scoreboard          []CuriaScoreEntry `json:"scoreboard,omitempty"`
	ExecutionPlanID     string            `json:"execution_plan_id"`
	ApprovalRequired    bool              `json:"approval_required"`
}

type ExecutionPlanPayload struct {
	ExecutionPlanID       string   `json:"execution_plan_id"`
	Goal                  string   `json:"goal"`
	Steps                 []string `json:"steps"`
	ExpectedFiles         []string `json:"expected_files,omitempty"`
	ForbiddenPaths        []string `json:"forbidden_paths,omitempty"`
	RequiredChecks        []string `json:"required_checks,omitempty"`
	ApplyMode             string   `json:"apply_mode"`
	RollbackHint          string   `json:"rollback_hint,omitempty"`
	HumanApprovalRequired bool     `json:"human_approval_required"`
}

type BuildProposalRequest struct {
	SessionID string
	TaskID    string
	RunID     string
	Agent     domain.AgentProfile
	Output    string
}

type BuildBallotRequest struct {
	SessionID        string
	TaskID           string
	RunID            string
	Agent            domain.AgentProfile
	TargetProposalID string
	ReviewerWeight   int
	WeightedScore    int
	Output           string
}

type BuildDebateLogRequest struct {
	SessionID           string
	TaskID              string
	RunID               string
	ProposalIDs         []string
	BallotIDs           []string
	WinningProposalID   string
	DisputeReasons      []string
	DisputeDetected     bool
	CriticalVeto        bool
	TopScoreGap         int
	Scoreboard          []CuriaScoreEntry
	ArbitrationRequired bool
}

type BuildDecisionPackRequest struct {
	SessionID           string
	TaskID              string
	RunID               string
	WinningMode         string
	SelectedProposalIDs []string
	ExecutionPlanID     string
	ApprovalRequired    bool
	MergedRationale     string
	RejectedReasons     []string
	Scoreboard          []CuriaScoreEntry
}

type BuildExecutionPlanRequest struct {
	SessionID             string
	TaskID                string
	RunID                 string
	Goal                  string
	Proposal              ProposalPayload
	WinningMode           string
	SelectedProposalIDs   []string
	HumanApprovalRequired bool
}

func (s *Service) BuildProposal(_ context.Context, req BuildProposalRequest) (domain.ArtifactEnvelope, error) {
	if req.SessionID == "" || req.TaskID == "" || req.Agent.ID == "" {
		return domain.ArtifactEnvelope{}, fmt.Errorf("session, task, and agent are required")
	}
	payload := ProposalPayload{
		ProposalID:     "prop_" + req.RunID,
		Summary:        summarize(req.Output),
		Approach:       summarizeParagraph(req.Output),
		AffectedFiles:  detectFiles(req.Output),
		DesignRisks:    detectBullets(req.Output, "risk"),
		Tradeoffs:      detectBullets(req.Output, "tradeoff"),
		EstimatedSteps: firstLines(req.Output, 5),
		PatchPlan:      firstLines(req.Output, 6),
		Confidence:     inferConfidence(req.Output),
	}
	return s.buildCuriaEnvelope(req.SessionID, req.TaskID, "art_"+payload.ProposalID, domain.ArtifactKindProposal, ProposalPayloadSchema, domain.ProducerRoleSenator, req.Agent.ID, req.RunID, payload)
}

func (s *Service) BuildBallot(_ context.Context, req BuildBallotRequest) (domain.ArtifactEnvelope, error) {
	if req.SessionID == "" || req.TaskID == "" || req.Agent.ID == "" || req.TargetProposalID == "" {
		return domain.ArtifactEnvelope{}, fmt.Errorf("session, task, agent, and target proposal are required")
	}
	payload := BallotPayload{
		BallotID:         "ballot_" + req.RunID,
		TargetProposalID: req.TargetProposalID,
		ReviewerWeight:   req.ReviewerWeight,
		WeightedScore:    req.WeightedScore,
		Scores:           scoreReview(req.Output),
		Critique:         summarizeParagraph(req.Output),
		Veto:             strings.Contains(strings.ToLower(req.Output), "veto"),
		Confidence:       inferConfidence(req.Output),
	}
	if payload.Veto {
		payload.VetoReason = summarize(req.Output)
	}
	return s.buildCuriaEnvelope(req.SessionID, req.TaskID, "art_"+payload.BallotID, domain.ArtifactKindBallot, BallotPayloadSchema, domain.ProducerRoleReviewer, req.Agent.ID, req.RunID, payload)
}

func (s *Service) BuildDebateLog(_ context.Context, req BuildDebateLogRequest) (domain.ArtifactEnvelope, error) {
	payload := DebateLogPayload{
		DebateLogID:         "debate_" + req.RunID,
		ProposalIDs:         append([]string(nil), req.ProposalIDs...),
		BallotIDs:           append([]string(nil), req.BallotIDs...),
		DisputeSummary:      disputeSummary(req.WinningProposalID, req.ArbitrationRequired),
		DisputeReasons:      append([]string(nil), req.DisputeReasons...),
		DisputeDetected:     req.DisputeDetected,
		CriticalVeto:        req.CriticalVeto,
		TopScoreGap:         req.TopScoreGap,
		Scoreboard:          append([]CuriaScoreEntry(nil), req.Scoreboard...),
		QuorumReachedAt:     s.now().Format(time.RFC3339Nano),
		ArbitrationRequired: req.ArbitrationRequired,
		WinningProposalID:   req.WinningProposalID,
	}
	return s.buildCuriaEnvelope(req.SessionID, req.TaskID, "art_"+payload.DebateLogID, domain.ArtifactKindDebateLog, DebateLogPayloadSchema, domain.ProducerRoleSystem, "roma-curia", req.RunID, payload)
}

func (s *Service) BuildDecisionPack(_ context.Context, req BuildDecisionPackRequest) (domain.ArtifactEnvelope, error) {
	payload := DecisionPackPayload{
		DecisionPackID:      "dp_" + req.RunID,
		WinningMode:         req.WinningMode,
		SelectedProposalIDs: append([]string(nil), req.SelectedProposalIDs...),
		MergedRationale:     req.MergedRationale,
		RejectedReasons:     append([]string(nil), req.RejectedReasons...),
		Scoreboard:          append([]CuriaScoreEntry(nil), req.Scoreboard...),
		ExecutionPlanID:     req.ExecutionPlanID,
		ApprovalRequired:    req.ApprovalRequired,
	}
	return s.buildCuriaEnvelope(req.SessionID, req.TaskID, "art_"+payload.DecisionPackID, domain.ArtifactKindDecisionPack, DecisionPackPayloadSchema, domain.ProducerRoleHuman, "human-arbitration", req.RunID, payload)
}

func (s *Service) BuildExecutionPlan(_ context.Context, req BuildExecutionPlanRequest) (domain.ArtifactEnvelope, error) {
	payload := ExecutionPlanPayload{
		ExecutionPlanID:       "plan_" + req.RunID,
		Goal:                  req.Goal,
		Steps:                 append([]string(nil), req.Proposal.EstimatedSteps...),
		ExpectedFiles:         append([]string(nil), req.Proposal.AffectedFiles...),
		ForbiddenPaths:        []string{".git/", ".roma/"},
		RequiredChecks:        []string{"go test ./...", "go build ./..."},
		ApplyMode:             executionApplyMode(req.WinningMode),
		RollbackHint:          "Reverse-apply the captured worktree patch if validation fails.",
		HumanApprovalRequired: req.HumanApprovalRequired,
	}
	if len(payload.Steps) == 0 {
		payload.Steps = firstLines(req.Proposal.Approach, 4)
	}
	if req.WinningMode == "merge" && len(req.SelectedProposalIDs) > 1 {
		payload.Steps = append([]string{"Merge the selected Curia proposals into one approved execution track."}, payload.Steps...)
	}
	if req.WinningMode == "replace" {
		payload.Steps = append([]string{"Replace the prior dominant proposal with the arbitrated fallback plan."}, payload.Steps...)
	}
	return s.buildCuriaEnvelope(req.SessionID, req.TaskID, "art_"+payload.ExecutionPlanID, domain.ArtifactKindExecutionPlan, ExecutionPlanPayloadSchema, domain.ProducerRoleSystem, "roma-curia", req.RunID, payload)
}

func (s *Service) buildCuriaEnvelope(sessionID, taskID, id string, kind domain.ArtifactKind, schema string, role domain.ProducerRole, agentID, runID string, payload any) (domain.ArtifactEnvelope, error) {
	envelope := domain.ArtifactEnvelope{
		ID:            id,
		Kind:          kind,
		SchemaVersion: "v1",
		Producer: domain.Producer{
			AgentID: agentID,
			Role:    role,
			RunID:   runID,
		},
		SessionID:     sessionID,
		TaskID:        taskID,
		CreatedAt:     s.now(),
		PayloadSchema: schema,
		Payload:       payload,
	}
	checksum, err := checksumEnvelope(envelope)
	if err != nil {
		return domain.ArtifactEnvelope{}, err
	}
	envelope.Checksum = checksum
	return envelope, nil
}

func ProposalFromEnvelope(envelope domain.ArtifactEnvelope) (ProposalPayload, bool) {
	if payload, ok := envelope.Payload.(ProposalPayload); ok {
		return payload, true
	}
	raw, err := json.Marshal(envelope.Payload)
	if err != nil {
		return ProposalPayload{}, false
	}
	var payload ProposalPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ProposalPayload{}, false
	}
	return payload, true
}

func BallotFromEnvelope(envelope domain.ArtifactEnvelope) (BallotPayload, bool) {
	if payload, ok := envelope.Payload.(BallotPayload); ok {
		return payload, true
	}
	raw, err := json.Marshal(envelope.Payload)
	if err != nil {
		return BallotPayload{}, false
	}
	var payload BallotPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return BallotPayload{}, false
	}
	return payload, true
}

func DebateLogFromEnvelope(envelope domain.ArtifactEnvelope) (DebateLogPayload, bool) {
	if payload, ok := envelope.Payload.(DebateLogPayload); ok {
		return payload, true
	}
	raw, err := json.Marshal(envelope.Payload)
	if err != nil {
		return DebateLogPayload{}, false
	}
	var payload DebateLogPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return DebateLogPayload{}, false
	}
	return payload, true
}

func DecisionPackFromEnvelope(envelope domain.ArtifactEnvelope) (DecisionPackPayload, bool) {
	if payload, ok := envelope.Payload.(DecisionPackPayload); ok {
		return payload, true
	}
	raw, err := json.Marshal(envelope.Payload)
	if err != nil {
		return DecisionPackPayload{}, false
	}
	var payload DecisionPackPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return DecisionPackPayload{}, false
	}
	return payload, true
}

func executionApplyMode(winningMode string) string {
	switch winningMode {
	case "merge":
		return "proposal_merge"
	case "replace":
		return "proposal_replace"
	default:
		return "proposal_accept"
	}
}

func ExecutionPlanFromEnvelope(envelope domain.ArtifactEnvelope) (ExecutionPlanPayload, bool) {
	if payload, ok := envelope.Payload.(ExecutionPlanPayload); ok {
		return payload, true
	}
	raw, err := json.Marshal(envelope.Payload)
	if err != nil {
		return ExecutionPlanPayload{}, false
	}
	var payload ExecutionPlanPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ExecutionPlanPayload{}, false
	}
	return payload, true
}

func detectFiles(output string) []string {
	lines := strings.Split(output, "\n")
	out := make([]string, 0, 4)
	seen := map[string]struct{}{}
	for _, line := range lines {
		line = strings.TrimSpace(strings.TrimPrefix(line, "-"))
		if strings.Contains(line, "/") && (strings.Contains(line, ".go") || strings.Contains(line, ".md") || strings.Contains(line, ".json") || strings.Contains(line, ".yaml") || strings.Contains(line, ".yml")) {
			fields := strings.Fields(line)
			path := fields[0]
			if _, ok := seen[path]; ok {
				continue
			}
			seen[path] = struct{}{}
			out = append(out, path)
		}
	}
	return out
}

func detectBullets(output, token string) []string {
	token = strings.ToLower(token)
	lines := strings.Split(output, "\n")
	out := make([]string, 0, 3)
	for _, line := range lines {
		text := strings.ToLower(strings.TrimSpace(line))
		if strings.Contains(text, token) {
			out = append(out, strings.TrimSpace(strings.TrimPrefix(line, "-")))
		}
	}
	return out
}

func summarizeParagraph(output string) string {
	text := strings.Join(firstLines(output, 4), " ")
	if text == "" {
		return "(no output)"
	}
	return text
}

func inferConfidence(output string) domain.Confidence {
	lowered := strings.ToLower(output)
	switch {
	case strings.Contains(lowered, "high confidence"):
		return domain.ConfidenceHigh
	case strings.Contains(lowered, "low confidence"), strings.Contains(lowered, "unsure"):
		return domain.ConfidenceLow
	default:
		return domain.ConfidenceMedium
	}
}

func scoreReview(output string) BallotScores {
	lowered := strings.ToLower(output)
	base := 3
	if strings.Contains(lowered, "strong") || strings.Contains(lowered, "best") {
		base = 4
	}
	if strings.Contains(lowered, "excellent") {
		base = 5
	}
	if strings.Contains(lowered, "weak") {
		base = 2
	}
	return BallotScores{
		Correctness:     base,
		Safety:          base,
		Maintainability: base,
		ScopeControl:    base,
		Testability:     base,
	}
}

func BallotScoresView(output string) BallotScores {
	return scoreReview(output)
}

func disputeSummary(winningProposalID string, arbitrationRequired bool) string {
	if arbitrationRequired {
		return "Curia minimal required human-first arbitration due to close or vetoed ballots."
	}
	if winningProposalID == "" {
		return "Curia minimal reached quorum without a winner."
	}
	return "Curia minimal selected a winning proposal without escalation."
}
