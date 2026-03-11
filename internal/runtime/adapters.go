package runtime

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/liliang/roma/internal/domain"
)

// CodexAdapter launches Codex CLI in non-interactive exec mode.
type CodexAdapter struct{}

// Supports reports whether this adapter handles the given profile.
func (CodexAdapter) Supports(profile domain.AgentProfile) bool {
	return profile.ID == "codex-cli"
}

// BuildCommand builds the runtime command.
func (CodexAdapter) BuildCommand(ctx context.Context, req StartRequest) (*exec.Cmd, error) {
	return exec.CommandContext(
		ctx,
		req.Profile.Command,
		"exec",
		"--full-auto",
		"--skip-git-repo-check",
		"--ephemeral",
		"-C", req.WorkingDir,
		req.Prompt,
	), nil
}

// RequiresPTY reports whether Codex should run in a PTY-backed terminal.
func (CodexAdapter) RequiresPTY(profile domain.AgentProfile) bool {
	return profile.ID == "codex-cli"
}

// ClaudeAdapter launches Claude Code in print mode.
type ClaudeAdapter struct{}

// Supports reports whether this adapter handles the given profile.
func (ClaudeAdapter) Supports(profile domain.AgentProfile) bool {
	return profile.ID == "claude-code"
}

// BuildCommand builds the runtime command.
func (ClaudeAdapter) BuildCommand(ctx context.Context, req StartRequest) (*exec.Cmd, error) {
	return exec.CommandContext(
		ctx,
		req.Profile.Command,
		"-p",
		"--permission-mode", "acceptEdits",
		req.Prompt,
	), nil
}

// RequiresPTY reports whether Claude should run in a PTY-backed terminal.
func (ClaudeAdapter) RequiresPTY(profile domain.AgentProfile) bool {
	return profile.ID == "claude-code"
}

// GeminiAdapter launches Gemini CLI in prompt mode.
type GeminiAdapter struct{}

// Supports reports whether this adapter handles the given profile.
func (GeminiAdapter) Supports(profile domain.AgentProfile) bool {
	return profile.ID == "gemini-cli"
}

// BuildCommand builds the runtime command.
func (GeminiAdapter) BuildCommand(ctx context.Context, req StartRequest) (*exec.Cmd, error) {
	return exec.CommandContext(
		ctx,
		req.Profile.Command,
		"-y",
		"-p", req.Prompt,
	), nil
}

// RequiresPTY reports whether Gemini should run in a PTY-backed terminal.
func (GeminiAdapter) RequiresPTY(profile domain.AgentProfile) bool {
	return profile.ID == "gemini-cli"
}

// CopilotAdapter launches Copilot CLI in non-interactive mode.
type CopilotAdapter struct{}

// Supports reports whether this adapter handles the given profile.
func (CopilotAdapter) Supports(profile domain.AgentProfile) bool {
	return profile.ID == "copilot-cli"
}

// BuildCommand builds the runtime command.
func (CopilotAdapter) BuildCommand(ctx context.Context, req StartRequest) (*exec.Cmd, error) {
	return exec.CommandContext(
		ctx,
		req.Profile.Command,
		"-p", req.Prompt,
		"--yolo",
		"--autopilot",
		"--no-ask-user",
		"-s",
	), nil
}

// RequiresPTY reports whether Copilot should run in a PTY-backed terminal.
func (CopilotAdapter) RequiresPTY(profile domain.AgentProfile) bool {
	return profile.ID == "copilot-cli"
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
