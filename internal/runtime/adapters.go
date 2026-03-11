package runtime

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/liliang-cn/roma/internal/domain"
)

// ProfileAdapter launches coding agents from user-provided command/args profiles.
type ProfileAdapter struct{}

// Supports reports whether the profile is runnable.
func (ProfileAdapter) Supports(profile domain.AgentProfile) bool {
	return strings.TrimSpace(profile.Command) != ""
}

// BuildCommand builds a launch command from the profile's configured args.
func (ProfileAdapter) BuildCommand(ctx context.Context, req StartRequest) (*exec.Cmd, error) {
	if strings.TrimSpace(req.Profile.Command) == "" {
		return nil, fmt.Errorf("agent %q has no command configured", req.Profile.ID)
	}
	args, err := buildProfileArgs(req)
	if err != nil {
		return nil, err
	}
	return exec.CommandContext(ctx, req.Profile.Command, args...), nil
}

// RequiresPTY reports whether the profile should be launched in a PTY.
func (ProfileAdapter) RequiresPTY(profile domain.AgentProfile) bool {
	if profile.UsePTY {
		return true
	}
	if profile.Metadata == nil {
		return false
	}
	return truthy(profile.Metadata["pty"]) || truthy(profile.Metadata["use_pty"])
}

func buildProfileArgs(req StartRequest) ([]string, error) {
	args := make([]string, 0, len(req.Profile.Args)+1)
	promptReferenced := false
	for _, arg := range req.Profile.Args {
		expanded, usedPrompt := expandProfileArg(arg, req)
		promptReferenced = promptReferenced || usedPrompt
		if strings.TrimSpace(expanded) == "" {
			continue
		}
		args = append(args, expanded)
	}
	if !promptReferenced && strings.TrimSpace(req.Prompt) != "" {
		args = append(args, req.Prompt)
	}
	return args, nil
}

func expandProfileArg(arg string, req StartRequest) (string, bool) {
	replacements := map[string]string{
		"{prompt}":       req.Prompt,
		"{cwd}":          req.WorkingDir,
		"{working_dir}":  req.WorkingDir,
		"{session_id}":   req.SessionID,
		"{task_id}":      req.TaskID,
		"{execution_id}": req.ExecutionID,
	}
	expanded := arg
	promptReferenced := false
	for placeholder, value := range replacements {
		if strings.Contains(expanded, placeholder) {
			if placeholder == "{prompt}" {
				promptReferenced = true
			}
			expanded = strings.ReplaceAll(expanded, placeholder, value)
		}
	}
	return expanded, promptReferenced
}

func truthy(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// BuildDelegationPrompt augments the starter prompt with allowed delegate agents.
func BuildDelegationPrompt(prompt string, delegates []domain.AgentProfile) string {
	if len(delegates) == 0 {
		return prompt
	}

	names := make([]string, 0, len(delegates))
	for _, delegate := range delegates {
		names = append(names, delegate.DisplayName+" ("+delegate.ID+")")
	}

	return prompt + "\n\n" +
		"Available secondary coding agents for delegation if useful:\n" +
		"- " + strings.Join(names, "\n- ") + "\n" +
		"Use them only when they materially improve execution, and preserve clear task ownership."
}

// ValidateWorkingDir checks basic runtime launch preconditions.
func ValidateWorkingDir(workingDir string) error {
	if strings.TrimSpace(workingDir) == "" {
		return fmt.Errorf("working directory is required")
	}
	return nil
}
