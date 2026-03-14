package artifacts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/liliang-cn/roma/internal/domain"
)

const (
	// ReportPayloadSchema is the bootstrap schema name for generic run reports.
	ReportPayloadSchema = "roma/report/v1"
	// SemanticReportPayloadSchema is the agent-assisted semantic classifier schema.
	SemanticReportPayloadSchema = "roma/semantic_report/v1"
	// FinalAnswerPayloadSchema is the user-facing session outcome schema.
	FinalAnswerPayloadSchema = "roma/final_answer/v1"
)

type MergeBackMode string

const (
	MergeBackModeDirectMerge     MergeBackMode = "direct_merge"
	MergeBackModeRequireVote     MergeBackMode = "require_vote"
	MergeBackModeRequireApproval MergeBackMode = "require_approval"
)

// ReportPayload is the minimal structured handoff payload used by the current orchestrator.
type ReportPayload struct {
	ReportID         string            `json:"report_id"`
	Summary          string            `json:"summary"`
	Result           string            `json:"result"`
	Highlights       []string          `json:"highlights,omitempty"`
	OpenIssues       []string          `json:"open_issues,omitempty"`
	FollowUpRequests []FollowUpRequest `json:"follow_up_requests,omitempty"`
	MergeBackRequest *MergeBackRequest `json:"merge_back_request,omitempty"`
	RawOutput        string            `json:"raw_output,omitempty"`
	SourceAgentID    string            `json:"source_agent_id"`
	SourceAgentName  string            `json:"source_agent_name"`
}

// MergeBackRequest captures an agent request for ROMA to evaluate and merge a workspace back.
type MergeBackRequest struct {
	WorkspaceSessionID string        `json:"workspace_session_id"`
	WorkspaceTaskID    string        `json:"workspace_task_id"`
	ChangedFiles       []string      `json:"changed_files,omitempty"`
	Reason             string        `json:"reason,omitempty"`
	RecommendedMode    MergeBackMode `json:"recommended_mode"`
}

// SemanticReportPayload is the structured interpretation emitted by a classifier agent.
type SemanticReportPayload struct {
	ReportID          string            `json:"report_id"`
	SourceSignal      string            `json:"source_signal"`
	SourceReason      string            `json:"source_reason"`
	SourceConfidence  domain.Confidence `json:"source_confidence"`
	SourceText        string            `json:"source_text"`
	ClassifierAgentID string            `json:"classifier_agent_id"`
	Intent            string            `json:"intent"`
	Risk              domain.Confidence `json:"risk"`
	NeedsApproval     bool              `json:"needs_approval"`
	RecommendCuria    bool              `json:"recommend_curia"`
	Summary           string            `json:"summary"`
	RawOutput         string            `json:"raw_output,omitempty"`
}

// FinalAnswerPayload is the user-facing outcome for a completed or paused session.
type FinalAnswerPayload struct {
	FinalAnswerID    string            `json:"final_answer_id"`
	OutcomeType      string            `json:"outcome_type"`
	Summary          string            `json:"summary"`
	Answer           string            `json:"answer"`
	KeyPoints        []string          `json:"key_points,omitempty"`
	ChangedFiles     []string          `json:"changed_files,omitempty"`
	ArtifactRefs     []string          `json:"artifact_refs,omitempty"`
	ApprovalRequired bool              `json:"approval_required,omitempty"`
	NextActions      []string          `json:"next_actions,omitempty"`
	Confidence       domain.Confidence `json:"confidence,omitempty"`
	SourceSessionID  string            `json:"source_session_id"`
	SourceStarterID  string            `json:"source_starter_id,omitempty"`
}

// FollowUpRequest is a structured continuation request emitted by an agent artifact.
type FollowUpRequest struct {
	Kind        string `json:"kind"`
	AgentID     string `json:"agent_id"`
	Instruction string `json:"instruction,omitempty"`
}

// BuildReportRequest describes report creation input.
type BuildReportRequest struct {
	SessionID string
	TaskID    string
	RunID     string
	Agent     domain.AgentProfile
	Result    string
	Output    string
	Stderr    string
}

// BuildFinalAnswerRequest describes final-answer creation input.
type BuildFinalAnswerRequest struct {
	SessionID    string
	TaskID       string
	RunID        string
	Status       string
	Prompt       string
	StarterAgent string
	Artifacts    []domain.ArtifactEnvelope
	Err          error
}

// BuildSemanticReportRequest describes semantic-report creation input.
type BuildSemanticReportRequest struct {
	SessionID        string
	TaskID           string
	RunID            string
	Agent            domain.AgentProfile
	SignalKind       string
	SignalReason     string
	SignalConfidence domain.Confidence
	SignalText       string
	Output           string
}

// Service creates structured artifacts for runtime outputs.
type Service struct {
	now func() time.Time
}

// NewService constructs an artifact service.
func NewService() *Service {
	return &Service{
		now: func() time.Time { return time.Now().UTC() },
	}
}

// BuildReport creates a report envelope for a runtime result.
func (s *Service) BuildReport(ctx context.Context, req BuildReportRequest) (domain.ArtifactEnvelope, error) {
	_ = ctx

	if req.SessionID == "" {
		return domain.ArtifactEnvelope{}, fmt.Errorf("session id is required")
	}
	if req.TaskID == "" {
		return domain.ArtifactEnvelope{}, fmt.Errorf("task id is required")
	}
	if req.Agent.ID == "" {
		return domain.ArtifactEnvelope{}, fmt.Errorf("agent id is required")
	}

	payload := ReportPayload{
		ReportID:         "report_" + req.RunID,
		Summary:          summarize(preferredOutput(req.Output, req.Stderr)),
		Result:           req.Result,
		Highlights:       firstLines(preferredOutput(req.Output, req.Stderr), 3),
		FollowUpRequests: parseFollowUpRequests(mergeOutput(req.Output, req.Stderr)),
		RawOutput:        mergeOutput(req.Output, req.Stderr),
		SourceAgentID:    req.Agent.ID,
		SourceAgentName:  req.Agent.DisplayName,
	}
	payload.MergeBackRequest = parseMergeBackRequest(mergeOutput(req.Output, req.Stderr), req.SessionID, req.TaskID)

	envelope := domain.ArtifactEnvelope{
		ID:            "art_" + req.RunID,
		Kind:          domain.ArtifactKindReport,
		SchemaVersion: "v1",
		Producer: domain.Producer{
			AgentID: req.Agent.ID,
			Role:    domain.ProducerRoleExecutor,
			RunID:   req.RunID,
		},
		SessionID:     req.SessionID,
		TaskID:        req.TaskID,
		CreatedAt:     s.now(),
		PayloadSchema: ReportPayloadSchema,
		Payload:       payload,
	}

	checksum, err := checksumEnvelope(envelope)
	if err != nil {
		return domain.ArtifactEnvelope{}, err
	}
	envelope.Checksum = checksum
	return envelope, nil
}

// SummaryFromEnvelope extracts a concise summary from a report artifact.
func SummaryFromEnvelope(envelope domain.ArtifactEnvelope) string {
	if payload, ok := envelope.Payload.(ReportPayload); ok {
		return payload.Summary
	}
	if payload, ok := envelope.Payload.(SemanticReportPayload); ok {
		return payload.Summary
	}
	if payload, ok := envelope.Payload.(FinalAnswerPayload); ok {
		return payload.Summary
	}
	if payload, ok := ProposalFromEnvelope(envelope); ok && payload.Summary != "" {
		return payload.Summary
	}
	if payload, ok := ExecutionPlanFromEnvelope(envelope); ok && payload.Goal != "" {
		if len(payload.Steps) > 0 {
			return payload.Goal + " " + payload.Steps[0]
		}
		return payload.Goal
	}

	raw, err := json.Marshal(envelope.Payload)
	if err != nil {
		return ""
	}
	var payload ReportPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		var final FinalAnswerPayload
		if err := json.Unmarshal(raw, &final); err == nil {
			return final.Summary
		}
		return ""
	}
	return payload.Summary
}

// BuildSemanticReport creates a semantic-report envelope from a classifier-agent output.
func (s *Service) BuildSemanticReport(ctx context.Context, req BuildSemanticReportRequest) (domain.ArtifactEnvelope, error) {
	_ = ctx

	if req.SessionID == "" {
		return domain.ArtifactEnvelope{}, fmt.Errorf("session id is required")
	}
	if req.TaskID == "" {
		return domain.ArtifactEnvelope{}, fmt.Errorf("task id is required")
	}
	if req.Agent.ID == "" {
		return domain.ArtifactEnvelope{}, fmt.Errorf("classifier agent id is required")
	}

	intent, risk, needsApproval, recommendCuria, summary := parseSemanticClassifierOutput(req.Output)
	if summary == "" {
		summary = summarize(preferredOutput(req.Output, req.SignalText))
	}

	payload := SemanticReportPayload{
		ReportID:          "semantic_" + req.RunID,
		SourceSignal:      req.SignalKind,
		SourceReason:      req.SignalReason,
		SourceConfidence:  req.SignalConfidence,
		SourceText:        req.SignalText,
		ClassifierAgentID: req.Agent.ID,
		Intent:            intent,
		Risk:              risk,
		NeedsApproval:     needsApproval,
		RecommendCuria:    recommendCuria,
		Summary:           summary,
		RawOutput:         req.Output,
	}

	envelope := domain.ArtifactEnvelope{
		ID:            "art_" + payload.ReportID,
		Kind:          domain.ArtifactKindSemanticReport,
		SchemaVersion: "v1",
		Producer: domain.Producer{
			AgentID: req.Agent.ID,
			Role:    domain.ProducerRoleReviewer,
			RunID:   req.RunID,
		},
		SessionID:     req.SessionID,
		TaskID:        req.TaskID,
		CreatedAt:     s.now(),
		PayloadSchema: SemanticReportPayloadSchema,
		Payload:       payload,
	}
	checksum, err := checksumEnvelope(envelope)
	if err != nil {
		return domain.ArtifactEnvelope{}, err
	}
	envelope.Checksum = checksum
	return envelope, nil
}

// BuildFinalAnswer creates a user-facing final answer envelope.
func (s *Service) BuildFinalAnswer(ctx context.Context, req BuildFinalAnswerRequest) (domain.ArtifactEnvelope, error) {
	_ = ctx

	if req.SessionID == "" {
		return domain.ArtifactEnvelope{}, fmt.Errorf("session id is required")
	}
	if req.TaskID == "" {
		return domain.ArtifactEnvelope{}, fmt.Errorf("task id is required")
	}

	payload := FinalAnswerPayload{
		FinalAnswerID:   "final_" + req.RunID,
		OutcomeType:     finalAnswerOutcomeType(req.Status, req.Artifacts),
		SourceSessionID: req.SessionID,
		SourceStarterID: req.StarterAgent,
		Confidence:      finalAnswerConfidence(req.Status, req.Err),
	}
	payload.ArtifactRefs = collectArtifactRefs(req.Artifacts)
	payload.ChangedFiles = collectChangedFiles(req.Artifacts)
	payload.ApprovalRequired = finalAnswerApprovalRequired(req.Status, req.Artifacts)
	payload.KeyPoints = collectKeyPoints(req.Artifacts)
	payload.Summary = finalAnswerSummary(req.Status, req.Artifacts, req.Err)
	payload.Answer = finalAnswerBody(req.Status, req.Prompt, req.Artifacts, req.Err)
	payload.NextActions = finalAnswerNextActions(req.Status, payload.ApprovalRequired, payload.ChangedFiles, req.Err)

	envelope := domain.ArtifactEnvelope{
		ID:            "art_" + payload.FinalAnswerID,
		Kind:          domain.ArtifactKindFinalAnswer,
		SchemaVersion: "v1",
		Producer: domain.Producer{
			AgentID: "roma",
			Role:    domain.ProducerRoleSystem,
			RunID:   req.RunID,
		},
		SessionID:     req.SessionID,
		TaskID:        req.TaskID,
		CreatedAt:     s.now(),
		PayloadSchema: FinalAnswerPayloadSchema,
		Payload:       payload,
	}
	checksum, err := checksumEnvelope(envelope)
	if err != nil {
		return domain.ArtifactEnvelope{}, err
	}
	envelope.Checksum = checksum
	return envelope, nil
}

func checksumEnvelope(envelope domain.ArtifactEnvelope) (string, error) {
	envelope.Checksum = ""
	raw, err := json.Marshal(envelope)
	if err != nil {
		return "", fmt.Errorf("marshal envelope for checksum: %w", err)
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func summarize(output string) string {
	lines := firstLines(output, 2)
	if len(lines) == 0 {
		return "(no output)"
	}
	if len(lines) == 1 {
		return lines[0]
	}
	return lines[0] + " " + lines[1]
}

func preferredOutput(stdout, stderr string) string {
	if trimLine(stdout) != "" {
		return stdout
	}
	return stderr
}

func mergeOutput(stdout, stderr string) string {
	switch {
	case trimLine(stdout) != "" && trimLine(stderr) != "":
		return stdout + "\n[stderr]\n" + stderr
	case trimLine(stdout) != "":
		return stdout
	default:
		return stderr
	}
}

func firstLines(output string, limit int) []string {
	lines := make([]string, 0, limit)
	start := 0
	for i := 0; i < len(output) && len(lines) < limit; i++ {
		if output[i] == '\n' {
			line := trimLine(output[start:i])
			if line != "" {
				lines = append(lines, line)
			}
			start = i + 1
		}
	}
	if len(lines) < limit && start < len(output) {
		line := trimLine(output[start:])
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func parseSemanticClassifierOutput(output string) (intent string, risk domain.Confidence, needsApproval bool, recommendCuria bool, summary string) {
	risk = inferConfidence(output)
	lines := strings.Split(output, "\n")
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		lower := strings.ToLower(line)
		switch {
		case strings.HasPrefix(lower, "intent:"):
			intent = strings.TrimSpace(line[len("intent:"):])
		case strings.HasPrefix(lower, "risk:"):
			switch strings.ToLower(strings.TrimSpace(line[len("risk:"):])) {
			case "high":
				risk = domain.ConfidenceHigh
			case "medium":
				risk = domain.ConfidenceMedium
			case "low":
				risk = domain.ConfidenceLow
			}
		case strings.HasPrefix(lower, "needs_approval:"):
			needsApproval = strings.EqualFold(strings.TrimSpace(line[len("needs_approval:"):]), "true")
		case strings.HasPrefix(lower, "recommend_curia:"):
			recommendCuria = strings.EqualFold(strings.TrimSpace(line[len("recommend_curia:"):]), "true")
		case strings.HasPrefix(lower, "summary:"):
			summary = strings.TrimSpace(line[len("summary:"):])
		}
	}
	return intent, risk, needsApproval, recommendCuria, summary
}

func trimLine(line string) string {
	for len(line) > 0 && (line[0] == ' ' || line[0] == '\n' || line[0] == '\r' || line[0] == '\t') {
		line = line[1:]
	}
	for len(line) > 0 && (line[len(line)-1] == ' ' || line[len(line)-1] == '\n' || line[len(line)-1] == '\r' || line[len(line)-1] == '\t') {
		line = line[:len(line)-1]
	}
	return line
}

func parseFollowUpRequests(output string) []FollowUpRequest {
	lines := strings.Split(output, "\n")
	out := make([]FollowUpRequest, 0)
	seen := make(map[string]struct{})
	for _, line := range lines {
		line = trimLine(line)
		switch {
		case strings.HasPrefix(line, "ROMA_DELEGATE:"):
			agent := trimLine(strings.TrimPrefix(line, "ROMA_DELEGATE:"))
			if agent == "" {
				continue
			}
			key := "delegate::" + agent + "::"
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, FollowUpRequest{
				Kind:    "delegate",
				AgentID: agent,
			})
		case strings.HasPrefix(line, "ROMA_FOLLOWUP:"):
			body := trimLine(strings.TrimPrefix(line, "ROMA_FOLLOWUP:"))
			parts := strings.SplitN(body, "|", 2)
			head := trimLine(parts[0])
			fields := strings.Fields(head)
			if len(fields) < 2 {
				continue
			}
			kind := fields[0]
			agent := fields[1]
			instruction := ""
			if len(parts) == 2 {
				instruction = trimLine(parts[1])
			}
			key := kind + "::" + agent + "::" + instruction
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, FollowUpRequest{
				Kind:        kind,
				AgentID:     agent,
				Instruction: instruction,
			})
		}
	}
	return out
}

func parseMergeBackRequest(output, sessionID, taskID string) *MergeBackRequest {
	lines := strings.Split(output, "\n")
	request := &MergeBackRequest{
		WorkspaceSessionID: sessionID,
		WorkspaceTaskID:    taskID,
	}
	for _, line := range lines {
		line = trimLine(line)
		switch {
		case strings.HasPrefix(line, "ROMA_MERGE_BACK:"):
			body := trimLine(strings.TrimPrefix(line, "ROMA_MERGE_BACK:"))
			parts := strings.SplitN(body, "|", 2)
			mode := MergeBackMode(trimLine(parts[0]))
			switch mode {
			case MergeBackModeDirectMerge, MergeBackModeRequireVote, MergeBackModeRequireApproval:
				request.RecommendedMode = mode
			default:
				continue
			}
			if len(parts) == 2 {
				request.Reason = trimLine(parts[1])
			}
		case strings.HasPrefix(line, "ROMA_MERGE_FILE:"):
			path := trimLine(strings.TrimPrefix(line, "ROMA_MERGE_FILE:"))
			if path != "" {
				request.ChangedFiles = append(request.ChangedFiles, path)
			}
		}
	}
	if request.RecommendedMode == "" {
		return nil
	}
	request.ChangedFiles = uniqueStrings(request.ChangedFiles)
	return request
}

// MergeBackRequestFromEnvelope extracts a merge-back request from a report envelope.
func MergeBackRequestFromEnvelope(envelope domain.ArtifactEnvelope) (MergeBackRequest, bool) {
	report, ok := reportFromEnvelope(envelope)
	if !ok || report.MergeBackRequest == nil {
		return MergeBackRequest{}, false
	}
	return *report.MergeBackRequest, true
}

func uniqueStrings(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
		item = trimLine(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	return out
}

// FinalAnswerFromEnvelope extracts a final-answer payload.
func FinalAnswerFromEnvelope(envelope domain.ArtifactEnvelope) (FinalAnswerPayload, bool) {
	if envelope.Kind != domain.ArtifactKindFinalAnswer {
		return FinalAnswerPayload{}, false
	}
	switch typed := envelope.Payload.(type) {
	case FinalAnswerPayload:
		return typed, true
	}
	raw, err := json.Marshal(envelope.Payload)
	if err != nil {
		return FinalAnswerPayload{}, false
	}
	var payload FinalAnswerPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return FinalAnswerPayload{}, false
	}
	return payload, true
}

// SemanticReportFromEnvelope extracts a semantic-report payload.
func SemanticReportFromEnvelope(envelope domain.ArtifactEnvelope) (SemanticReportPayload, bool) {
	if envelope.Kind != domain.ArtifactKindSemanticReport {
		return SemanticReportPayload{}, false
	}
	switch typed := envelope.Payload.(type) {
	case SemanticReportPayload:
		return typed, true
	}
	raw, err := json.Marshal(envelope.Payload)
	if err != nil {
		return SemanticReportPayload{}, false
	}
	var payload SemanticReportPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return SemanticReportPayload{}, false
	}
	return payload, true
}

func finalAnswerOutcomeType(status string, items []domain.ArtifactEnvelope) string {
	switch status {
	case "awaiting_approval":
		return "pending_approval"
	case "failed", "failed_recoverable", "failed_terminal":
		return "failure"
	}
	for _, item := range items {
		if item.Kind == domain.ArtifactKindExecutionPlan {
			return "code_change"
		}
	}
	return "answer"
}

func finalAnswerConfidence(status string, err error) domain.Confidence {
	switch {
	case err != nil:
		return domain.ConfidenceLow
	case status == "awaiting_approval":
		return domain.ConfidenceMedium
	default:
		return domain.ConfidenceHigh
	}
}

func finalAnswerApprovalRequired(status string, items []domain.ArtifactEnvelope) bool {
	if status == "awaiting_approval" {
		return true
	}
	for _, item := range items {
		payload, ok := ExecutionPlanFromEnvelope(item)
		if ok && payload.HumanApprovalRequired {
			return true
		}
	}
	return false
}

func finalAnswerSummary(status string, items []domain.ArtifactEnvelope, err error) string {
	if status == "awaiting_approval" {
		for _, item := range items {
			if plan, ok := ExecutionPlanFromEnvelope(item); ok && plan.Goal != "" {
				return "Execution plan is ready and waiting for approval: " + plan.Goal
			}
		}
		return "Execution is waiting for approval."
	}
	if err != nil {
		return "Execution failed: " + trimLine(err.Error())
	}
	for i := len(items) - 1; i >= 0; i-- {
		if summary := finalAnswerArtifactSummary(items[i]); summary != "" {
			return summary
		}
	}
	return "Execution completed."
}

func finalAnswerBody(status, prompt string, items []domain.ArtifactEnvelope, err error) string {
	summary := finalAnswerSummary(status, items, err)
	if status == "awaiting_approval" {
		return summary + " Review the proposed plan before applying it."
	}
	if err != nil {
		return summary
	}
	if len(items) == 0 {
		return summary
	}
	points := collectKeyPoints(items)
	if len(points) == 0 {
		return summary
	}
	return summary + "\n\nKey points:\n- " + strings.Join(points, "\n- ")
}

func finalAnswerNextActions(status string, approvalRequired bool, changedFiles []string, err error) []string {
	switch {
	case approvalRequired:
		return []string{"Review the final plan.", "Approve or reject the execution plan before applying changes."}
	case err != nil:
		return []string{"Inspect the session events.", "Retry the run after fixing the reported failure."}
	case len(changedFiles) > 0:
		return []string{"Preview the execution plan.", "Apply or rollback the planned changes."}
	default:
		return []string{"Review the final answer.", "Start a follow-up run if more work is needed."}
	}
}

func collectArtifactRefs(items []domain.ArtifactEnvelope) []string {
	out := make([]string, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		if item.ID == "" {
			continue
		}
		if _, ok := seen[item.ID]; ok {
			continue
		}
		seen[item.ID] = struct{}{}
		out = append(out, item.ID)
	}
	return out
}

func collectChangedFiles(items []domain.ArtifactEnvelope) []string {
	out := make([]string, 0)
	seen := map[string]struct{}{}
	for _, item := range items {
		if plan, ok := ExecutionPlanFromEnvelope(item); ok {
			for _, file := range plan.ExpectedFiles {
				if _, exists := seen[file]; exists {
					continue
				}
				seen[file] = struct{}{}
				out = append(out, file)
			}
		}
		if proposal, ok := ProposalFromEnvelope(item); ok {
			for _, file := range proposal.AffectedFiles {
				if _, exists := seen[file]; exists {
					continue
				}
				seen[file] = struct{}{}
				out = append(out, file)
			}
		}
	}
	return out
}

func collectKeyPoints(items []domain.ArtifactEnvelope) []string {
	out := make([]string, 0, 4)
	for i := len(items) - 1; i >= 0 && len(out) < 4; i-- {
		summary := finalAnswerArtifactSummary(items[i])
		if summary == "" {
			continue
		}
		duplicate := false
		for _, existing := range out {
			if existing == summary {
				duplicate = true
				break
			}
		}
		if !duplicate {
			out = append(out, summary)
		}
	}
	return out
}

func finalAnswerArtifactSummary(envelope domain.ArtifactEnvelope) string {
	if report, ok := reportFromEnvelope(envelope); ok {
		if summary := extractMeaningfulReportLine(report.RawOutput); summary != "" {
			return summary
		}
		if summary := extractMeaningfulReportLine(report.Result); summary != "" {
			return summary
		}
	}
	return SummaryFromEnvelope(envelope)
}

func reportFromEnvelope(envelope domain.ArtifactEnvelope) (ReportPayload, bool) {
	if envelope.Kind != domain.ArtifactKindReport {
		return ReportPayload{}, false
	}
	switch typed := envelope.Payload.(type) {
	case ReportPayload:
		return typed, true
	}
	raw, err := json.Marshal(envelope.Payload)
	if err != nil {
		return ReportPayload{}, false
	}
	var payload ReportPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ReportPayload{}, false
	}
	return payload, true
}

func extractMeaningfulReportLine(output string) string {
	lines := strings.Split(output, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := trimLine(lines[i])
		if line == "" || ignoreReportLine(line) {
			continue
		}
		return line
	}
	return ""
}

func ignoreReportLine(line string) bool {
	lower := strings.ToLower(line)
	switch {
	case isNumericTokenLine(line):
		return true
	case line == "--------":
		return true
	case lower == "user" || lower == "codex" || lower == "exec":
		return true
	case strings.HasPrefix(lower, "openai codex v"):
		return true
	case strings.HasPrefix(lower, "workdir:"),
		strings.HasPrefix(lower, "model:"),
		strings.HasPrefix(lower, "provider:"),
		strings.HasPrefix(lower, "approval:"),
		strings.HasPrefix(lower, "sandbox:"),
		strings.HasPrefix(lower, "reasoning effort:"),
		strings.HasPrefix(lower, "reasoning summaries:"),
		strings.HasPrefix(lower, "session id:"),
		strings.HasPrefix(lower, "tokens used"),
		strings.HasPrefix(lower, "mcp:"),
		strings.HasPrefix(lower, "mcp startup:"),
		strings.HasPrefix(lower, "roma relay execution node."),
		strings.HasPrefix(lower, "original request:"),
		strings.HasPrefix(lower, "current node:"),
		strings.HasPrefix(lower, "provide the contribution for this node only."),
		strings.HasPrefix(lower, "roma continuous execution mode."),
		strings.HasPrefix(lower, "keep working on the same task"),
		strings.HasPrefix(lower, "when the task is complete"),
		strings.HasPrefix(lower, "if the task is not complete"),
		strings.HasPrefix(lower, "current round:"),
		strings.HasPrefix(lower, "original task:"),
		strings.HasPrefix(lower, "/usr/bin/"),
		strings.Contains(lower, "succeeded in "),
		strings.HasPrefix(lower, "# "),
		strings.HasPrefix(lower, "## "):
		return true
	}
	return false
}

func isNumericTokenLine(line string) bool {
	line = strings.TrimSpace(line)
	if line == "" {
		return false
	}
	for _, r := range line {
		switch {
		case r >= '0' && r <= '9':
		case r == ',' || r == '.':
		default:
			return false
		}
	}
	return true
}
