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
)

// ReportPayload is the minimal structured handoff payload used by the current orchestrator.
type ReportPayload struct {
	ReportID         string            `json:"report_id"`
	Summary          string            `json:"summary"`
	Result           string            `json:"result"`
	Highlights       []string          `json:"highlights,omitempty"`
	OpenIssues       []string          `json:"open_issues,omitempty"`
	FollowUpRequests []FollowUpRequest `json:"follow_up_requests,omitempty"`
	RawOutput        string            `json:"raw_output,omitempty"`
	SourceAgentID    string            `json:"source_agent_id"`
	SourceAgentName  string            `json:"source_agent_name"`
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
		return ""
	}
	return payload.Summary
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
