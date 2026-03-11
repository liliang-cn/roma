package policy

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/liliang/roma/internal/events"
	"github.com/liliang/roma/internal/store"
)

// DecisionKind identifies the policy outcome.
type DecisionKind string

const (
	DecisionAllow DecisionKind = "allow"
	DecisionWarn  DecisionKind = "warn"
	DecisionBlock DecisionKind = "block"
)

// Request describes the execution intent to be classified.
type Request struct {
	SessionID    string
	TaskID       string
	Mode         string
	Prompt       string
	WorkingDir   string
	StarterAgent string
	Delegates    []string
	NodeCount    int
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
				"decision":      decision.Kind,
				"warnings":      decision.Warnings,
				"mode":          req.Mode,
				"working_dir":   req.WorkingDir,
				"starter_agent": req.StarterAgent,
				"delegates":     req.Delegates,
				"node_count":    req.NodeCount,
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
	info, err := os.Stat(req.WorkingDir)
	if err != nil || !info.IsDir() {
		return Decision{Kind: DecisionBlock, Reason: "working_dir_missing"}
	}

	warnings := make([]string, 0, 2)
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
	if len(warnings) > 0 {
		return Decision{
			Kind:     DecisionWarn,
			Reason:   warnings[0],
			Warnings: warnings,
		}
	}
	return Decision{Kind: DecisionAllow, Reason: "allowed"}
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
