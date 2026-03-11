package plans

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/liliang/roma/internal/artifacts"
	"github.com/liliang/roma/internal/domain"
	"github.com/liliang/roma/internal/events"
	"github.com/liliang/roma/internal/policy"
	"github.com/liliang/roma/internal/store"
	workspacepkg "github.com/liliang/roma/internal/workspace"
)

type ApplyOptions struct {
	DryRun              bool
	PolicyOverride      bool
	PolicyOverrideActor string
}

type ApplyResult struct {
	ArtifactID     string                    `json:"artifact_id"`
	SessionID      string                    `json:"session_id"`
	TaskID         string                    `json:"task_id"`
	Workspace      workspacepkg.Prepared     `json:"workspace"`
	Preview        workspacepkg.MergePreview `json:"preview"`
	ChangedPaths   []string                  `json:"changed_paths"`
	PatchBytes     int                       `json:"patch_bytes"`
	DryRun         bool                      `json:"dry_run"`
	Applied        bool                      `json:"applied"`
	RolledBack     bool                      `json:"rolled_back"`
	RollbackHint   string                    `json:"rollback_hint,omitempty"`
	RequiredChecks []string                  `json:"required_checks,omitempty"`
	Violations     []string                  `json:"violations,omitempty"`
	Conflict       bool                      `json:"conflict"`
	ConflictDetail string                    `json:"conflict_detail,omitempty"`
}

type Service struct {
	artifacts  artifacts.Backend
	workspaces *workspacepkg.Manager
	events     store.EventStore
}

type InboxEntry struct {
	ArtifactID            string   `json:"artifact_id"`
	SessionID             string   `json:"session_id"`
	TaskID                string   `json:"task_id"`
	Goal                  string   `json:"goal,omitempty"`
	Status                string   `json:"status"`
	HumanApprovalRequired bool     `json:"human_approval_required"`
	ExpectedFiles         []string `json:"expected_files,omitempty"`
	ForbiddenPaths        []string `json:"forbidden_paths,omitempty"`
	LastEventType         string   `json:"last_event_type,omitempty"`
	LastReason            string   `json:"last_reason,omitempty"`
	LastOccurredAt        string   `json:"last_occurred_at,omitempty"`
	LastApproval          string   `json:"last_approval,omitempty"`
	LastApprovalAt        string   `json:"last_approval_at,omitempty"`
	Violations            []string `json:"violations,omitempty"`
	Conflict              bool     `json:"conflict,omitempty"`
	ConflictDetail        string   `json:"conflict_detail,omitempty"`
}

type ErrorKind string

const (
	ErrorKindApprovalRequired  ErrorKind = "approval_required"
	ErrorKindOverrideForbidden ErrorKind = "override_forbidden"
	ErrorKindValidation        ErrorKind = "validation_failed"
	ErrorKindConflict          ErrorKind = "merge_conflict"
	ErrorKindCheckFailed       ErrorKind = "required_check_failed"
)

type ApplyError struct {
	Kind       ErrorKind
	Message    string
	Violations []string
}

func (e *ApplyError) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return e.Message
	}
	return string(e.Kind)
}

func NewService(artifactStore artifacts.Backend, manager *workspacepkg.Manager, eventStore store.EventStore) *Service {
	return &Service{artifacts: artifactStore, workspaces: manager, events: eventStore}
}

func IsApplyErrorKind(err error, kind ErrorKind) bool {
	var target *ApplyError
	if !errors.As(err, &target) {
		return false
	}
	return target.Kind == kind
}

func (s *Service) Inbox(ctx context.Context, sessionID string) ([]InboxEntry, error) {
	envelopes, err := s.artifacts.List(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	var eventItems []events.Record
	if s.events != nil {
		eventItems, _ = s.events.ListEvents(ctx, store.EventFilter{SessionID: sessionID})
	}
	latestByArtifact := latestPlanEventByArtifact(eventItems)
	approvalByArtifact := latestPlanApprovalByArtifact(eventItems)
	out := make([]InboxEntry, 0)
	for _, envelope := range envelopes {
		if envelope.Kind != domain.ArtifactKindExecutionPlan {
			continue
		}
		payload, ok := artifacts.ExecutionPlanFromEnvelope(envelope)
		if !ok {
			continue
		}
		entry := InboxEntry{
			ArtifactID:            envelope.ID,
			SessionID:             envelope.SessionID,
			TaskID:                envelope.TaskID,
			Goal:                  payload.Goal,
			HumanApprovalRequired: payload.HumanApprovalRequired,
			ExpectedFiles:         append([]string(nil), payload.ExpectedFiles...),
			ForbiddenPaths:        append([]string(nil), payload.ForbiddenPaths...),
			Status:                "ready",
		}
		if payload.HumanApprovalRequired {
			entry.Status = "pending_approval"
		}
		if latest, ok := latestByArtifact[envelope.ID]; ok {
			entry.LastEventType = string(latest.Type)
			entry.LastReason = latest.ReasonCode
			entry.LastOccurredAt = latest.OccurredAt.Format(time.RFC3339)
			if values, ok := payloadStrings(latest.Payload, "violations"); ok {
				entry.Violations = values
			}
			if value, ok := latest.Payload["conflict"].(bool); ok {
				entry.Conflict = value
			}
			if value, ok := latest.Payload["conflict_detail"].(string); ok {
				entry.ConflictDetail = value
			}
			entry.Status = inboxStatus(payload, latest)
		}
		if approval, ok := approvalByArtifact[envelope.ID]; ok {
			entry.LastApproval = string(approval.Type)
			entry.LastApprovalAt = approval.OccurredAt.Format(time.RFC3339)
			entry.Status = inboxStatusWithApproval(entry.Status, approval)
		}
		out = append(out, entry)
	}
	slices.SortFunc(out, func(a, b InboxEntry) int {
		switch {
		case a.LastOccurredAt < b.LastOccurredAt:
			return 1
		case a.LastOccurredAt > b.LastOccurredAt:
			return -1
		case a.ArtifactID < b.ArtifactID:
			return -1
		case a.ArtifactID > b.ArtifactID:
			return 1
		default:
			return 0
		}
	})
	return out, nil
}

func (s *Service) Approve(ctx context.Context, artifactID, actor string) error {
	envelope, _, err := s.Inspect(ctx, artifactID)
	if err != nil {
		return err
	}
	return s.appendApprovalEvent(ctx, envelope, events.TypePlanApproved, actor)
}

func (s *Service) Reject(ctx context.Context, artifactID, actor string) error {
	envelope, _, err := s.Inspect(ctx, artifactID)
	if err != nil {
		return err
	}
	return s.appendApprovalEvent(ctx, envelope, events.TypePlanRejected, actor)
}

func (s *Service) Inspect(ctx context.Context, artifactID string) (domain.ArtifactEnvelope, artifacts.ExecutionPlanPayload, error) {
	envelope, err := s.artifacts.Get(ctx, artifactID)
	if err != nil {
		return domain.ArtifactEnvelope{}, artifacts.ExecutionPlanPayload{}, err
	}
	if envelope.Kind != domain.ArtifactKindExecutionPlan {
		return domain.ArtifactEnvelope{}, artifacts.ExecutionPlanPayload{}, fmt.Errorf("artifact %s is not an execution plan", artifactID)
	}
	plan, ok := artifacts.ExecutionPlanFromEnvelope(envelope)
	if !ok {
		return domain.ArtifactEnvelope{}, artifacts.ExecutionPlanPayload{}, fmt.Errorf("artifact %s has invalid execution plan payload", artifactID)
	}
	return envelope, plan, nil
}

func (s *Service) Apply(ctx context.Context, sessionID, taskID, artifactID string, opts ApplyOptions) (ApplyResult, error) {
	envelope, plan, err := s.Inspect(ctx, artifactID)
	if err != nil {
		return ApplyResult{}, err
	}
	if sessionID == "" {
		sessionID = envelope.SessionID
	}
	if taskID == "" {
		taskID = envelope.TaskID
	}
	prepared, err := s.workspaces.Get(ctx, sessionID, taskID)
	if err != nil {
		return ApplyResult{}, err
	}
	changed, err := s.workspaces.ChangedPaths(ctx, prepared)
	if err != nil {
		return ApplyResult{}, err
	}
	preview, err := s.workspaces.PreviewMerge(ctx, prepared)
	if err != nil {
		return ApplyResult{}, err
	}
	result := ApplyResult{
		ArtifactID:     artifactID,
		SessionID:      sessionID,
		TaskID:         taskID,
		Workspace:      prepared,
		Preview:        preview,
		ChangedPaths:   changed,
		DryRun:         opts.DryRun,
		RollbackHint:   plan.RollbackHint,
		RequiredChecks: append([]string(nil), plan.RequiredChecks...),
	}
	if !opts.DryRun && plan.HumanApprovalRequired {
		approved, rejected := s.planApprovalState(ctx, artifactID, sessionID)
		switch {
		case rejected:
			err := &ApplyError{Kind: ErrorKindApprovalRequired, Message: "execution plan has been explicitly rejected"}
			s.appendRejectedEvent(ctx, result, plan, err)
			return result, err
		case approved:
		case !opts.PolicyOverride:
			err := &ApplyError{Kind: ErrorKindApprovalRequired, Message: "execution plan requires approval override or explicit approval"}
			s.appendRejectedEvent(ctx, result, plan, err)
			return result, err
		case !policy.CanOverrideActor(opts.PolicyOverrideActor):
			err := &ApplyError{Kind: ErrorKindOverrideForbidden, Message: "execution plan override actor forbidden"}
			s.appendRejectedEvent(ctx, result, plan, err)
			return result, err
		}
	}
	violations := validatePlanPaths(plan, changed)
	actionDecision := policy.EvaluatePathAction(policy.ActionPlanApply, changed, opts.PolicyOverride, opts.PolicyOverrideActor)
	if actionDecision.Kind == policy.DecisionBlock {
		violations = append(violations, policyWarningsAsViolations(actionDecision)...)
	}
	if len(violations) > 0 {
		result.Violations = append([]string(nil), violations...)
		err := &ApplyError{Kind: ErrorKindValidation, Message: strings.Join(violations, "; "), Violations: append([]string(nil), violations...)}
		s.appendRejectedEvent(ctx, result, plan, err)
		return result, err
	}
	result.PatchBytes = preview.PatchBytes
	if opts.DryRun {
		if preview.Conflict {
			result.Conflict = true
			result.ConflictDetail = preview.ConflictDetail
		}
		s.appendAppliedEvent(ctx, result, plan, "dry_run")
		return result, nil
	}
	if preview.Conflict {
		result.Conflict = true
		result.ConflictDetail = preview.ConflictDetail
		applyErr := &ApplyError{Kind: ErrorKindConflict, Message: preview.ConflictDetail}
		s.appendRejectedEvent(ctx, result, plan, applyErr)
		return result, applyErr
	}
	if err := s.workspaces.MergeBack(ctx, prepared); err != nil {
		result.Conflict = true
		result.ConflictDetail = err.Error()
		applyErr := &ApplyError{Kind: ErrorKindConflict, Message: err.Error()}
		s.appendRejectedEvent(ctx, result, plan, applyErr)
		return result, applyErr
	}
	if err := runRequiredChecks(ctx, prepared.BaseDir, plan.RequiredChecks); err != nil {
		_ = s.workspaces.RollbackMerge(ctx, prepared)
		result.RolledBack = true
		applyErr := &ApplyError{Kind: ErrorKindCheckFailed, Message: err.Error()}
		s.appendRejectedEvent(ctx, result, plan, applyErr)
		return result, applyErr
	}
	result.Applied = true
	s.appendAppliedEvent(ctx, result, plan, "applied")
	return result, nil
}

func (s *Service) Rollback(ctx context.Context, sessionID, taskID, artifactID string) (ApplyResult, error) {
	envelope, plan, err := s.Inspect(ctx, artifactID)
	if err != nil {
		return ApplyResult{}, err
	}
	if sessionID == "" {
		sessionID = envelope.SessionID
	}
	if taskID == "" {
		taskID = envelope.TaskID
	}
	prepared, err := s.workspaces.Get(ctx, sessionID, taskID)
	if err != nil {
		return ApplyResult{}, err
	}
	changed, _ := s.workspaces.ChangedPaths(ctx, prepared)
	preview, _ := s.workspaces.PreviewMerge(ctx, prepared)
	result := ApplyResult{
		ArtifactID:     artifactID,
		SessionID:      sessionID,
		TaskID:         taskID,
		Workspace:      prepared,
		Preview:        preview,
		ChangedPaths:   changed,
		PatchBytes:     preview.PatchBytes,
		RollbackHint:   plan.RollbackHint,
		RequiredChecks: append([]string(nil), plan.RequiredChecks...),
	}
	if err := s.workspaces.RollbackMerge(ctx, prepared); err != nil {
		return result, err
	}
	result.RolledBack = true
	s.appendRollbackEvent(ctx, result, plan)
	return result, nil
}

func validatePlanPaths(plan artifacts.ExecutionPlanPayload, changedPaths []string) []string {
	var violations []string
	expected := make(map[string]struct{}, len(plan.ExpectedFiles))
	for _, path := range plan.ExpectedFiles {
		expected[normalizePlanPath(path)] = struct{}{}
	}
	for _, changed := range changedPaths {
		normalized := normalizePlanPath(changed)
		if len(expected) > 0 {
			if _, ok := expected[normalized]; !ok {
				violations = append(violations, fmt.Sprintf("execution plan path violation: changed path %s not declared in expected_files", changed))
			}
		}
		for _, forbidden := range plan.ForbiddenPaths {
			if matchesPlanPath(forbidden, normalized) {
				violations = append(violations, fmt.Sprintf("execution plan forbidden path: %s", changed))
			}
		}
	}
	return violations
}

func matchesPlanPath(pattern, path string) bool {
	pattern = normalizePlanPath(pattern)
	path = normalizePlanPath(path)
	switch {
	case strings.HasSuffix(pattern, "/**"):
		prefix := strings.TrimSuffix(pattern, "/**")
		return path == prefix || strings.HasPrefix(path, prefix+"/")
	case strings.HasSuffix(pattern, "/"):
		return path == strings.TrimSuffix(pattern, "/") || strings.HasPrefix(path, pattern)
	default:
		match, _ := filepath.Match(pattern, path)
		return match || path == pattern
	}
}

func normalizePlanPath(path string) string {
	path = filepath.Clean(path)
	return strings.ReplaceAll(path, "\\", "/")
}

func runRequiredChecks(ctx context.Context, dir string, checks []string) error {
	for _, check := range checks {
		check = strings.TrimSpace(check)
		if check == "" {
			continue
		}
		if !strings.Contains(check, " ") {
			continue
		}
		cmd := exec.CommandContext(ctx, "sh", "-lc", check)
		cmd.Dir = dir
		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("required check %q failed: %w (%s)", check, err, strings.TrimSpace(string(output)))
		}
	}
	return nil
}

func (s *Service) appendAppliedEvent(ctx context.Context, result ApplyResult, plan artifacts.ExecutionPlanPayload, reason string) {
	s.appendEvent(ctx, events.TypePlanApplied, result, plan, reason, nil)
}

func (s *Service) appendRollbackEvent(ctx context.Context, result ApplyResult, plan artifacts.ExecutionPlanPayload) {
	s.appendEvent(ctx, events.TypePlanRolledBack, result, plan, "rolled_back", nil)
}

func (s *Service) appendRejectedEvent(ctx context.Context, result ApplyResult, plan artifacts.ExecutionPlanPayload, err error) {
	payload := map[string]any{}
	var applyErr *ApplyError
	if errors.As(err, &applyErr) {
		payload["error_kind"] = applyErr.Kind
		if len(applyErr.Violations) > 0 {
			payload["violations"] = applyErr.Violations
		}
	}
	s.appendEvent(ctx, events.TypePlanApplyRejected, result, plan, errorReason(err), payload)
}

func (s *Service) appendEvent(ctx context.Context, eventType events.Type, result ApplyResult, plan artifacts.ExecutionPlanPayload, reason string, extra map[string]any) {
	if s.events == nil {
		return
	}
	payload := map[string]any{
		"artifact_id":       result.ArtifactID,
		"execution_plan_id": plan.ExecutionPlanID,
		"changed_paths":     result.ChangedPaths,
		"patch_bytes":       result.PatchBytes,
		"preview_can_apply": result.Preview.CanApply,
		"dry_run":           result.DryRun,
		"applied":           result.Applied,
		"rolled_back":       result.RolledBack,
		"rollback_hint":     result.RollbackHint,
		"required_checks":   result.RequiredChecks,
	}
	if len(result.Violations) > 0 {
		payload["violations"] = result.Violations
	}
	if result.Conflict {
		payload["conflict"] = true
		payload["conflict_detail"] = result.ConflictDetail
	}
	for key, value := range extra {
		payload[key] = value
	}
	_ = s.events.AppendEvent(ctx, events.Record{
		ID:         fmt.Sprintf("evt_%s_%s_%s_%d", result.SessionID, result.TaskID, strings.ToLower(string(eventType)), time.Now().UTC().UnixNano()),
		SessionID:  result.SessionID,
		TaskID:     result.TaskID,
		Type:       eventType,
		ActorType:  events.ActorTypeHuman,
		OccurredAt: time.Now().UTC(),
		ReasonCode: reason,
		Payload:    payload,
	})
}

func errorReason(err error) string {
	var applyErr *ApplyError
	if errors.As(err, &applyErr) {
		return string(applyErr.Kind)
	}
	if err == nil {
		return ""
	}
	return "error"
}

func policyWarningsAsViolations(decision policy.Decision) []string {
	if len(decision.Warnings) == 0 {
		if decision.Reason == "" {
			return nil
		}
		return []string{decision.Reason}
	}
	out := make([]string, 0, len(decision.Warnings))
	for _, warning := range decision.Warnings {
		out = append(out, fmt.Sprintf("%s: %s", decision.Reason, warning))
	}
	return out
}

func latestPlanEventByArtifact(items []events.Record) map[string]events.Record {
	out := make(map[string]events.Record)
	for _, item := range items {
		switch item.Type {
		case events.TypePlanApplied, events.TypePlanRolledBack, events.TypePlanApplyRejected:
		default:
			continue
		}
		artifactID, ok := item.Payload["artifact_id"].(string)
		if !ok || artifactID == "" {
			continue
		}
		existing, exists := out[artifactID]
		if !exists || item.OccurredAt.After(existing.OccurredAt) {
			out[artifactID] = item
		}
	}
	return out
}

func latestPlanApprovalByArtifact(items []events.Record) map[string]events.Record {
	out := make(map[string]events.Record)
	for _, item := range items {
		switch item.Type {
		case events.TypePlanApproved, events.TypePlanRejected:
		default:
			continue
		}
		artifactID, ok := item.Payload["artifact_id"].(string)
		if !ok || artifactID == "" {
			continue
		}
		existing, exists := out[artifactID]
		if !exists || item.OccurredAt.After(existing.OccurredAt) {
			out[artifactID] = item
		}
	}
	return out
}

func inboxStatus(plan artifacts.ExecutionPlanPayload, latest events.Record) string {
	switch latest.Type {
	case events.TypePlanApplied:
		if latest.ReasonCode == "dry_run" {
			if plan.HumanApprovalRequired {
				return "pending_approval"
			}
			return "previewed"
		}
		return "applied"
	case events.TypePlanRolledBack:
		return "rolled_back"
	case events.TypePlanApplyRejected:
		switch latest.ReasonCode {
		case string(ErrorKindApprovalRequired), "protected_path_apply_requires_override", string(ErrorKindOverrideForbidden):
			return "pending_approval"
		case string(ErrorKindConflict), string(ErrorKindValidation), string(ErrorKindCheckFailed):
			return "attention_required"
		default:
			return "rejected"
		}
	default:
		if plan.HumanApprovalRequired {
			return "pending_approval"
		}
		return "ready"
	}
}

func inboxStatusWithApproval(current string, approval events.Record) string {
	switch approval.Type {
	case events.TypePlanApproved:
		if current == "applied" || current == "rolled_back" {
			return current
		}
		return "approved"
	case events.TypePlanRejected:
		if current == "applied" || current == "rolled_back" {
			return current
		}
		return "rejected"
	default:
		return current
	}
}

func payloadStrings(payload map[string]any, key string) ([]string, bool) {
	if payload == nil {
		return nil, false
	}
	value, ok := payload[key]
	if !ok {
		return nil, false
	}
	switch typed := value.(type) {
	case []string:
		return typed, true
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			text, ok := item.(string)
			if ok {
				out = append(out, text)
			}
		}
		return out, true
	default:
		return nil, false
	}
}

func (s *Service) planApprovalState(ctx context.Context, artifactID, sessionID string) (approved bool, rejected bool) {
	if s.events == nil {
		return false, false
	}
	items, err := s.events.ListEvents(ctx, store.EventFilter{SessionID: sessionID})
	if err != nil {
		return false, false
	}
	latest, ok := latestPlanApprovalByArtifact(items)[artifactID]
	if !ok {
		return false, false
	}
	return latest.Type == events.TypePlanApproved, latest.Type == events.TypePlanRejected
}

func (s *Service) appendApprovalEvent(ctx context.Context, envelope domain.ArtifactEnvelope, eventType events.Type, actor string) error {
	if s.events == nil {
		return nil
	}
	if actor == "" {
		actor = policy.OverrideActor()
	}
	return s.events.AppendEvent(ctx, events.Record{
		ID:         fmt.Sprintf("evt_%s_%s_%s_%d", envelope.SessionID, envelope.TaskID, strings.ToLower(string(eventType)), time.Now().UTC().UnixNano()),
		SessionID:  envelope.SessionID,
		TaskID:     envelope.TaskID,
		Type:       eventType,
		ActorType:  events.ActorTypeHuman,
		OccurredAt: time.Now().UTC(),
		ReasonCode: strings.ToLower(strings.TrimPrefix(string(eventType), "Plan")),
		Payload: map[string]any{
			"artifact_id": envelope.ID,
			"actor":       actor,
		},
	})
}
