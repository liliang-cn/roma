package policy

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/liliang/roma/internal/events"
	"github.com/liliang/roma/internal/store"
)

// DecisionKind identifies the policy outcome.
type DecisionKind string
type Action string

const (
	DecisionAllow DecisionKind = "allow"
	DecisionWarn  DecisionKind = "warn"
	DecisionBlock DecisionKind = "block"

	ActionRun       Action = "run"
	ActionPlanApply Action = "plan_apply"
)

// Request describes the execution intent to be classified.
type Request struct {
	SessionID      string
	TaskID         string
	Mode           string
	Prompt         string
	WorkingDir     string
	EffectiveDir   string
	PathHints      []string
	StarterAgent   string
	Delegates      []string
	NodeCount      int
	PolicyOverride bool
	OverrideActor  string
}

// Decision is the normalized broker output.
type Decision struct {
	Kind     DecisionKind `json:"kind"`
	Reason   string       `json:"reason"`
	Warnings []string     `json:"warnings,omitempty"`
}

// Broker evaluates execution intent against minimum guardrails.
type Broker interface {
	Evaluate(ctx context.Context, req Request) (Decision, error)
}

// SimpleBroker applies a small set of deterministic pre-flight rules.
type SimpleBroker struct {
	events store.EventStore
	now    func() time.Time
}

// NewSimpleBroker constructs a policy broker backed by the shared event store.
func NewSimpleBroker(eventStore store.EventStore) *SimpleBroker {
	return &SimpleBroker{
		events: eventStore,
		now:    func() time.Time { return time.Now().UTC() },
	}
}

// Evaluate classifies an execution request and records the decision.
func (b *SimpleBroker) Evaluate(ctx context.Context, req Request) (Decision, error) {
	decision := evaluate(req)
	if b.events != nil {
		_ = b.events.AppendEvent(ctx, events.Record{
			ID:         "evt_" + req.SessionID + "_policy_" + string(decision.Kind),
			SessionID:  req.SessionID,
			TaskID:     req.TaskID,
			Type:       events.TypePolicyDecisionRecorded,
			ActorType:  events.ActorTypePolicy,
			OccurredAt: b.now(),
			ReasonCode: decision.Reason,
			Payload: map[string]any{
				"decision":           decision.Kind,
				"warnings":           decision.Warnings,
				"mode":               req.Mode,
				"working_dir":        req.WorkingDir,
				"effective_dir":      req.EffectiveDir,
				"path_hints":         req.PathHints,
				"starter_agent":      req.StarterAgent,
				"delegates":          req.Delegates,
				"node_count":         req.NodeCount,
				"override_requested": req.PolicyOverride,
				"override_actor":     req.OverrideActor,
			},
		})
	}
	return decision, nil
}

// ClassifyCommand evaluates a concrete runtime command before launch.
func (b *SimpleBroker) ClassifyCommand(ctx context.Context, sessionID, taskID string, cmd *exec.Cmd) (Decision, error) {
	decision := classifyCommand(cmd)
	if b.events != nil {
		_ = b.events.AppendEvent(ctx, events.Record{
			ID:         "evt_" + taskID + "_runtime_policy_" + string(decision.Kind),
			SessionID:  sessionID,
			TaskID:     taskID,
			Type:       events.TypePolicyDecisionRecorded,
			ActorType:  events.ActorTypePolicy,
			OccurredAt: b.now(),
			ReasonCode: decision.Reason,
			Payload: map[string]any{
				"phase":    "runtime_command",
				"decision": decision.Kind,
				"command":  commandString(cmd),
				"warnings": decision.Warnings,
			},
		})
	}
	return decision, nil
}

func evaluate(req Request) Decision {
	if strings.TrimSpace(req.Prompt) == "" {
		return Decision{Kind: DecisionBlock, Reason: "empty_prompt"}
	}
	if strings.TrimSpace(req.WorkingDir) == "" {
		return Decision{Kind: DecisionBlock, Reason: "empty_working_dir"}
	}
	cleaned := filepath.Clean(req.WorkingDir)
	if cleaned == string(filepath.Separator) {
		return Decision{Kind: DecisionBlock, Reason: "working_dir_root_forbidden"}
	}
	effectiveDir := strings.TrimSpace(req.EffectiveDir)
	if effectiveDir == "" {
		effectiveDir = req.WorkingDir
	}
	effectiveClean := filepath.Clean(effectiveDir)
	info, err := os.Stat(req.WorkingDir)
	if err != nil || !info.IsDir() {
		return Decision{Kind: DecisionBlock, Reason: "working_dir_missing"}
	}
	if effectiveClean == string(filepath.Separator) {
		return Decision{Kind: DecisionBlock, Reason: "effective_dir_root_forbidden"}
	}
	if strings.HasSuffix(effectiveClean, string(filepath.Separator)+".git") || filepath.Base(effectiveClean) == ".git" {
		return Decision{Kind: DecisionBlock, Reason: "git_dir_execution_forbidden"}
	}
	if !isEffectiveDirAllowed(cleaned, effectiveClean) {
		return Decision{Kind: DecisionBlock, Reason: "effective_dir_outside_workspace_boundary"}
	}

	warnings := make([]string, 0, 4)
	lowered := strings.ToLower(req.Prompt)
	for _, token := range []string{
		"rm -rf",
		"drop database",
		"delete from",
		"truncate table",
		"sudo ",
		"shutdown",
		"reboot",
		"format disk",
	} {
		if strings.Contains(lowered, token) {
			warnings = append(warnings, "prompt_mentions_destructive_operation")
			break
		}
	}
	if req.NodeCount > 8 {
		warnings = append(warnings, "large_graph_execution")
	}
	if len(req.Delegates) > 2 {
		warnings = append(warnings, "wide_delegate_fanout")
	}
	if protected := detectProtectedPaths(req); len(protected) > 0 {
		warnings = append(warnings, "protected_path_scope")
	}
	if len(warnings) > 0 {
		if req.PolicyOverride {
			if !canOverride(req.OverrideActor) {
				return Decision{
					Kind:     DecisionBlock,
					Reason:   "override_actor_forbidden",
					Warnings: warnings,
				}
			}
			return Decision{
				Kind:     DecisionAllow,
				Reason:   "approved_override",
				Warnings: warnings,
			}
		}
		return Decision{
			Kind:     DecisionWarn,
			Reason:   warnings[0],
			Warnings: warnings,
		}
	}
	return Decision{Kind: DecisionAllow, Reason: "allowed"}
}

func OverrideActor() string {
	if actor := strings.TrimSpace(os.Getenv("ROMA_POLICY_OVERRIDE_ACTOR")); actor != "" {
		return actor
	}
	return "local_owner"
}

func AllowedOverrideActors() []string {
	raw := strings.TrimSpace(os.Getenv("ROMA_POLICY_OVERRIDE_ACTORS"))
	if raw == "" {
		return []string{"local_owner", "admin"}
	}
	out := make([]string, 0, 4)
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, strings.ToLower(part))
		}
	}
	if len(out) == 0 {
		return []string{"local_owner", "admin"}
	}
	return out
}

func canOverride(actor string) bool {
	actor = strings.ToLower(strings.TrimSpace(actor))
	if actor == "" {
		return false
	}
	return slices.Contains(AllowedOverrideActors(), actor)
}

func CanOverrideActor(actor string) bool {
	return canOverride(actor)
}

func EvaluatePathAction(action Action, paths []string, override bool, actor string) Decision {
	protected := []string{".github/**", "infra/**", "migrations/**", "auth/**", "billing/**"}
	alwaysForbidden := []string{".git/**", ".roma/**"}

	normalized := make([]string, 0, len(paths))
	for _, path := range paths {
		path = strings.ToLower(strings.ReplaceAll(filepath.Clean(path), "\\", "/"))
		if path != "." && path != "" {
			normalized = append(normalized, path)
		}
	}

	violations := make([]string, 0)
	protectedHits := make([]string, 0)
	for _, path := range normalized {
		for _, pattern := range alwaysForbidden {
			if matchesPath(pattern, path) {
				violations = append(violations, "forbidden_path:"+path)
				break
			}
		}
		for _, pattern := range protected {
			if matchesPath(pattern, path) {
				protectedHits = append(protectedHits, path)
				break
			}
		}
	}
	if len(violations) > 0 {
		return Decision{
			Kind:     DecisionBlock,
			Reason:   "forbidden_path_action",
			Warnings: violations,
		}
	}
	if len(protectedHits) == 0 {
		return Decision{Kind: DecisionAllow, Reason: "allowed"}
	}
	if action == ActionPlanApply {
		if override {
			if !canOverride(actor) {
				return Decision{
					Kind:     DecisionBlock,
					Reason:   "override_actor_forbidden",
					Warnings: protectedHits,
				}
			}
			return Decision{
				Kind:     DecisionAllow,
				Reason:   "approved_override",
				Warnings: protectedHits,
			}
		}
		return Decision{
			Kind:     DecisionBlock,
			Reason:   "protected_path_apply_requires_override",
			Warnings: protectedHits,
		}
	}
	return Decision{
		Kind:     DecisionWarn,
		Reason:   "protected_path_scope",
		Warnings: protectedHits,
	}
}

func isEffectiveDirAllowed(baseDir, effectiveDir string) bool {
	baseDir = filepath.Clean(baseDir)
	effectiveDir = filepath.Clean(effectiveDir)
	if effectiveDir == baseDir {
		return true
	}
	worktreeRoot := filepath.Join(baseDir, ".roma", "workspaces")
	return effectiveDir == worktreeRoot || strings.HasPrefix(effectiveDir, worktreeRoot+string(filepath.Separator))
}

func detectProtectedPaths(req Request) []string {
	protected := []string{".github/", "infra/", "migrations/", "auth/", "billing/"}
	lowered := strings.ToLower(req.Prompt)
	out := make([]string, 0, len(protected))
	for _, token := range protected {
		if strings.Contains(lowered, token) && !slices.Contains(out, token) {
			out = append(out, token)
		}
	}
	for _, hint := range req.PathHints {
		hint = strings.ToLower(strings.ReplaceAll(filepath.Clean(hint), "\\", "/"))
		for _, token := range protected {
			if strings.Contains(hint, token) && !slices.Contains(out, token) {
				out = append(out, token)
			}
		}
	}
	return out
}

func matchesPath(pattern, path string) bool {
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

func classifyCommand(cmd *exec.Cmd) Decision {
	if cmd == nil {
		return Decision{Kind: DecisionBlock, Reason: "runtime_command_missing"}
	}
	joined := strings.ToLower(commandString(cmd))
	base := strings.ToLower(filepath.Base(cmd.Path))
	switch base {
	case "sh", "bash", "zsh", "cmd", "powershell", "pwsh":
		return Decision{
			Kind:     DecisionWarn,
			Reason:   "shell_runtime_command",
			Warnings: []string{"runtime_shell_wrapper"},
		}
	}
	if strings.Contains(joined, " sudo ") || strings.HasPrefix(joined, "sudo ") {
		return Decision{
			Kind:     DecisionWarn,
			Reason:   "privileged_runtime_command",
			Warnings: []string{"runtime_privileged_command"},
		}
	}
	return Decision{Kind: DecisionAllow, Reason: "runtime_command_allowed"}
}

func commandString(cmd *exec.Cmd) string {
	if len(cmd.Args) > 0 {
		return strings.Join(cmd.Args, " ")
	}
	if cmd.Path == "" {
		return ""
	}
	return fmt.Sprintf("%s", cmd.Path)
}
