package classifier

import (
	"context"
	"testing"
	"time"

	"github.com/liliang-cn/roma/internal/domain"
	"github.com/liliang-cn/roma/internal/events"
	"github.com/liliang-cn/roma/internal/policy"
	"github.com/liliang-cn/roma/internal/runtime"
	"github.com/liliang-cn/roma/internal/store"
)

type fakeRunner struct {
	result runtime.Result
	err    error
	last   runtime.StartRequest
}

func (f *fakeRunner) RunCaptured(_ context.Context, req runtime.StartRequest) (runtime.Result, error) {
	f.last = req
	return f.result, f.err
}

type fakeArtifactStore struct {
	items map[string]domain.ArtifactEnvelope
}

func (s *fakeArtifactStore) Save(_ context.Context, envelope domain.ArtifactEnvelope) error {
	if s.items == nil {
		s.items = make(map[string]domain.ArtifactEnvelope)
	}
	s.items[envelope.ID] = envelope
	return nil
}

func (s *fakeArtifactStore) Get(_ context.Context, artifactID string) (domain.ArtifactEnvelope, error) {
	return s.items[artifactID], nil
}

func (s *fakeArtifactStore) List(_ context.Context, _ string) ([]domain.ArtifactEnvelope, error) {
	out := make([]domain.ArtifactEnvelope, 0, len(s.items))
	for _, item := range s.items {
		out = append(out, item)
	}
	return out, nil
}

func TestAgentAnalyzerAnalyzeSignal(t *testing.T) {
	mem := store.NewMemoryStore()
	artifacts := &fakeArtifactStore{}
	runner := &fakeRunner{
		result: runtime.Result{
			Stdout: "intent: approval_request\nrisk: high\nneeds_approval: true\nrecommend_curia: true\nsummary: The agent is asking for risky approval.",
		},
	}

	analyzer := NewAgentAnalyzer(runner, artifacts, mem)
	analyzer.timeout = time.Second
	analyzer.now = func() time.Time { return time.Unix(1700000000, 0).UTC() }

	err := analyzer.AnalyzeSignal(context.Background(), runtime.SemanticAnalysisRequest{
		ExecutionID: "exec_1",
		SessionID:   "sess_1",
		TaskID:      "task_1",
		WorkingDir:  "/tmp/work",
		SourceAgent: domain.AgentProfile{ID: "my-codex", Availability: domain.AgentAvailabilityAvailable},
		ReviewerAgent: domain.AgentProfile{
			ID:           "starter-agent",
			DisplayName:  "Starter Agent",
			Command:      "starter",
			Availability: domain.AgentAvailabilityAvailable,
		},
		Signal: policySignal("approval_requested", "approval phrase", domain.ConfidenceMedium, "approval required before editing"),
	})
	if err != nil {
		t.Fatalf("AnalyzeSignal() error = %v", err)
	}

	if runner.last.Profile.ID != "starter-agent" {
		t.Fatalf("classifier profile = %q, want starter-agent", runner.last.Profile.ID)
	}
	if len(artifacts.items) != 1 {
		t.Fatalf("artifact count = %d, want 1", len(artifacts.items))
	}
	var saved domain.ArtifactEnvelope
	for _, item := range artifacts.items {
		saved = item
	}
	if saved.Kind != domain.ArtifactKindSemanticReport {
		t.Fatalf("artifact kind = %s, want %s", saved.Kind, domain.ArtifactKindSemanticReport)
	}

	records, err := mem.ListEvents(context.Background(), store.EventFilter{
		SessionID: "sess_1",
		TaskID:    "task_1",
		Type:      events.TypeSemanticReportProduced,
	})
	if err != nil {
		t.Fatalf("ListEvents() error = %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("semantic report event count = %d, want 1", len(records))
	}
	if got := records[0].Payload["classifier_agent_id"]; got != "starter-agent" {
		t.Fatalf("classifier_agent_id = %#v, want starter-agent", got)
	}
}

func TestAgentAnalyzerFallsBackToSourceAgent(t *testing.T) {
	mem := store.NewMemoryStore()
	artifacts := &fakeArtifactStore{}
	runner := &fakeRunner{
		result: runtime.Result{
			Stdout: "intent: parse_warning\nrisk: low\nneeds_approval: false\nrecommend_curia: false\nsummary: Low risk parser issue.",
		},
	}

	analyzer := NewAgentAnalyzer(runner, artifacts, mem)
	err := analyzer.AnalyzeSignal(context.Background(), runtime.SemanticAnalysisRequest{
		ExecutionID: "exec_1",
		SessionID:   "sess_1",
		TaskID:      "task_1",
		WorkingDir:  "/tmp/work",
		SourceAgent: domain.AgentProfile{
			ID:           "my-codex",
			DisplayName:  "My Codex",
			Command:      "codex",
			Availability: domain.AgentAvailabilityAvailable,
		},
		Signal: policySignal("parse_warning", "parse", domain.ConfidenceLow, "bad json"),
	})
	if err != nil {
		t.Fatalf("AnalyzeSignal() error = %v", err)
	}
	if runner.last.Profile.ID != "my-codex" {
		t.Fatalf("classifier profile = %q, want my-codex", runner.last.Profile.ID)
	}
	if len(artifacts.items) != 1 {
		t.Fatalf("artifact count = %d, want 1", len(artifacts.items))
	}
}

func policySignal(kind string, reason string, confidence domain.Confidence, text string) policy.StreamSignal {
	var signalKind policy.StreamSignalKind
	switch kind {
	case "approval_requested":
		signalKind = policy.SignalApprovalRequested
	case "dangerous_command_detected":
		signalKind = policy.SignalDangerousCommandDetected
	case "parse_warning":
		signalKind = policy.SignalParseWarning
	}
	return policy.StreamSignal{
		Kind:       signalKind,
		Reason:     reason,
		Confidence: confidence,
		Text:       text,
	}
}
