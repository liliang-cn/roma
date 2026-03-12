package classifier

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/liliang-cn/roma/internal/artifacts"
	"github.com/liliang-cn/roma/internal/domain"
	"github.com/liliang-cn/roma/internal/events"
	"github.com/liliang-cn/roma/internal/runtime"
	"github.com/liliang-cn/roma/internal/store"
)

// Runner captures the runtime dependency needed by the semantic analyzer.
type Runner interface {
	RunCaptured(ctx context.Context, req runtime.StartRequest) (runtime.Result, error)
}

// AgentAnalyzer invokes a system-selected agent to interpret runtime signals.
type AgentAnalyzer struct {
	runner  Runner
	builder *artifacts.Service
	store   artifacts.Backend
	events  store.EventStore
	now     func() time.Time
	timeout time.Duration
}

// NewAgentAnalyzer constructs an agent-backed semantic analyzer.
func NewAgentAnalyzer(runner Runner, artifactStore artifacts.Backend, eventStore store.EventStore) *AgentAnalyzer {
	if runner == nil {
		runner = runtime.DefaultSupervisor()
	}
	return &AgentAnalyzer{
		runner:  runner,
		builder: artifacts.NewService(),
		store:   artifactStore,
		events:  eventStore,
		now:     func() time.Time { return time.Now().UTC() },
		timeout: 20 * time.Second,
	}
}

// AnalyzeSignal runs the configured classifier agent for one emitted stream signal.
func (a *AgentAnalyzer) AnalyzeSignal(ctx context.Context, req runtime.SemanticAnalysisRequest) error {
	if a == nil || a.runner == nil || a.builder == nil || a.store == nil {
		return nil
	}

	profile, ok := a.selectReviewer(req)
	if !ok || profile.Availability != domain.AgentAvailabilityAvailable {
		return nil
	}

	runCtx := ctx
	if runCtx == nil {
		runCtx = context.Background()
	}
	timeout := a.timeout
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	runCtx, cancel := context.WithTimeout(runCtx, timeout)
	defer cancel()

	result, err := a.runner.RunCaptured(runCtx, runtime.StartRequest{
		ExecutionID: req.ExecutionID + "_semantic",
		SessionID:   req.SessionID,
		TaskID:      req.TaskID,
		Profile:     profile,
		Prompt:      buildClassifierPrompt(req),
		WorkingDir:  req.WorkingDir,
	})
	if err != nil {
		return fmt.Errorf("run classifier agent %s: %w", profile.ID, err)
	}

	envelope, err := a.builder.BuildSemanticReport(runCtx, artifacts.BuildSemanticReportRequest{
		SessionID:        req.SessionID,
		TaskID:           req.TaskID,
		RunID:            req.ExecutionID,
		Agent:            profile,
		SignalKind:       string(req.Signal.Kind),
		SignalReason:     req.Signal.Reason,
		SignalConfidence: req.Signal.Confidence,
		SignalText:       req.Signal.Text,
		Output:           mergeOutput(result.Stdout, result.Stderr),
	})
	if err != nil {
		return fmt.Errorf("build semantic report: %w", err)
	}
	if err := a.store.Save(runCtx, envelope); err != nil {
		return fmt.Errorf("save semantic report %s: %w", envelope.ID, err)
	}
	a.appendArtifactStored(runCtx, envelope)
	a.appendSemanticReportProduced(runCtx, envelope)
	return nil
}

func (a *AgentAnalyzer) appendArtifactStored(ctx context.Context, envelope domain.ArtifactEnvelope) {
	if a.events == nil {
		return
	}
	_ = a.events.AppendEvent(ctx, events.Record{
		ID:         "evt_" + envelope.ID + "_stored",
		SessionID:  envelope.SessionID,
		TaskID:     envelope.TaskID,
		Type:       events.TypeArtifactStored,
		ActorType:  events.ActorTypeSystem,
		OccurredAt: a.now(),
		Payload: map[string]any{
			"artifact_id": envelope.ID,
			"kind":        envelope.Kind,
			"schema":      envelope.PayloadSchema,
		},
	})
}

func (a *AgentAnalyzer) appendSemanticReportProduced(ctx context.Context, envelope domain.ArtifactEnvelope) {
	if a.events == nil {
		return
	}
	payload, _ := artifacts.SemanticReportFromEnvelope(envelope)
	_ = a.events.AppendEvent(ctx, events.Record{
		ID:         "evt_" + envelope.ID + "_semantic",
		SessionID:  envelope.SessionID,
		TaskID:     envelope.TaskID,
		Type:       events.TypeSemanticReportProduced,
		ActorType:  events.ActorTypeAgent,
		OccurredAt: a.now(),
		ReasonCode: payload.Intent,
		Payload: map[string]any{
			"artifact_id":         envelope.ID,
			"classifier_agent_id": payload.ClassifierAgentID,
			"source_signal":       payload.SourceSignal,
			"risk":                payload.Risk,
			"needs_approval":      payload.NeedsApproval,
			"recommend_curia":     payload.RecommendCuria,
			"summary":             payload.Summary,
		},
	})
	if payload.NeedsApproval {
		_ = a.events.AppendEvent(ctx, events.Record{
			ID:         "evt_" + envelope.ID + "_approval_recommended",
			SessionID:  envelope.SessionID,
			TaskID:     envelope.TaskID,
			Type:       events.TypeSemanticApprovalRecommended,
			ActorType:  events.ActorTypeAgent,
			OccurredAt: a.now(),
			ReasonCode: payload.Intent,
			Payload: map[string]any{
				"artifact_id":         envelope.ID,
				"classifier_agent_id": payload.ClassifierAgentID,
				"risk":                payload.Risk,
				"summary":             payload.Summary,
			},
		})
	}
	if payload.RecommendCuria {
		_ = a.events.AppendEvent(ctx, events.Record{
			ID:         "evt_" + envelope.ID + "_curia_recommended",
			SessionID:  envelope.SessionID,
			TaskID:     envelope.TaskID,
			Type:       events.TypeCuriaPromotionRecommended,
			ActorType:  events.ActorTypeAgent,
			OccurredAt: a.now(),
			ReasonCode: payload.Intent,
			Payload: map[string]any{
				"artifact_id":         envelope.ID,
				"classifier_agent_id": payload.ClassifierAgentID,
				"risk":                payload.Risk,
				"summary":             payload.Summary,
			},
		})
	}
}

func buildClassifierPrompt(req runtime.SemanticAnalysisRequest) string {
	text := strings.TrimSpace(req.Signal.Text)
	if text == "" {
		text = "(no direct excerpt)"
	}
	return strings.TrimSpace(fmt.Sprintf(`You are ROMA's semantic runtime classifier.

Read the runtime output excerpt and return exactly these lines:
intent: <short phrase>
risk: <low|medium|high>
needs_approval: <true|false>
recommend_curia: <true|false>
summary: <one short sentence>

Treat protected paths (.github/, infra/, migrations/, auth/, billing/), breaking changes,
schema changes, and destructive operations as strong signals for high risk and Curia escalation.

Signal kind: %s
Signal reason: %s
Signal confidence: %s
Source agent: %s
Working directory: %s

Runtime excerpt:
%s`,
		req.Signal.Kind,
		req.Signal.Reason,
		req.Signal.Confidence,
		req.SourceAgent.ID,
		req.WorkingDir,
		text,
	))
}

func (a *AgentAnalyzer) selectReviewer(req runtime.SemanticAnalysisRequest) (domain.AgentProfile, bool) {
	if strings.TrimSpace(req.ReviewerAgent.ID) != "" && req.ReviewerAgent.Availability == domain.AgentAvailabilityAvailable {
		return req.ReviewerAgent, true
	}
	if strings.TrimSpace(req.SourceAgent.ID) != "" && req.SourceAgent.Availability == domain.AgentAvailabilityAvailable {
		return req.SourceAgent, true
	}
	return domain.AgentProfile{}, false
}

func mergeOutput(stdout, stderr string) string {
	switch {
	case strings.TrimSpace(stdout) != "" && strings.TrimSpace(stderr) != "":
		return stdout + "\n[stderr]\n" + stderr
	case strings.TrimSpace(stdout) != "":
		return stdout
	default:
		return stderr
	}
}
