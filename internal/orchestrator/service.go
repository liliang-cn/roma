package orchestrator

import (
	"context"
	"fmt"
	"strings"

	"github.com/liliang/roma/internal/artifacts"
	"github.com/liliang/roma/internal/domain"
	"github.com/liliang/roma/internal/runtime"
)

// Request describes a multi-agent execution request.
type Request struct {
	Prompt     string
	WorkingDir string
	SessionID  string
	TaskID     string
	Starter    domain.AgentProfile
	Delegates  []domain.AgentProfile
}

// StepResult captures one agent execution in the orchestration chain.
type StepResult struct {
	Role     string
	Result   runtime.Result
	Prompt   string
	Artifact domain.ArtifactEnvelope
}

// Result is the complete orchestration output.
type Result struct {
	Steps []StepResult
}

// Service performs minimal sequential orchestration across agents.
type Service struct {
	supervisor *runtime.Supervisor
	artifacts  *artifacts.Service
}

// NewService constructs an orchestrator service.
func NewService(supervisor *runtime.Supervisor) *Service {
	return &Service{
		supervisor: supervisor,
		artifacts:  artifacts.NewService(),
	}
}

// RunSequential executes starter and delegates in order, chaining outputs.
func (s *Service) RunSequential(ctx context.Context, req Request) (Result, error) {
	if strings.TrimSpace(req.Prompt) == "" {
		return Result{}, fmt.Errorf("prompt is required")
	}

	steps := make([]StepResult, 0, 1+len(req.Delegates))

	starterPrompt := runtime.BuildDelegationPrompt(req.Prompt, req.Delegates)
	starterResult, err := s.supervisor.RunCaptured(ctx, runtime.StartRequest{
		ExecutionID: "exec_" + req.TaskID + "_starter",
		SessionID:   req.SessionID,
		TaskID:      req.TaskID,
		Profile:     req.Starter,
		Prompt:      starterPrompt,
		WorkingDir:  req.WorkingDir,
	})
	starterArtifact, artifactErr := s.artifacts.BuildReport(ctx, artifacts.BuildReportRequest{
		SessionID: req.SessionID,
		TaskID:    req.TaskID,
		RunID:     req.Starter.ID + "_starter",
		Agent:     req.Starter,
		Result:    resultLabel(err),
		Output:    starterResult.Stdout,
		Stderr:    starterResult.Stderr,
	})
	if artifactErr != nil {
		return Result{}, artifactErr
	}
	steps = append(steps, StepResult{
		Role:     "starter",
		Result:   starterResult,
		Prompt:   starterPrompt,
		Artifact: starterArtifact,
	})
	if err != nil {
		return Result{Steps: steps}, err
	}

	previousSummary := artifacts.SummaryFromEnvelope(starterArtifact)
	for _, delegate := range req.Delegates {
		delegatePrompt := buildDelegatePrompt(req.Prompt, req.Starter, previousSummary)
		delegateResult, runErr := s.supervisor.RunCaptured(ctx, runtime.StartRequest{
			ExecutionID: "exec_" + req.TaskID + "_" + delegate.ID,
			SessionID:   req.SessionID,
			TaskID:      req.TaskID,
			Profile:     delegate,
			Prompt:      delegatePrompt,
			WorkingDir:  req.WorkingDir,
		})
		delegateArtifact, artifactErr := s.artifacts.BuildReport(ctx, artifacts.BuildReportRequest{
			SessionID: req.SessionID,
			TaskID:    req.TaskID,
			RunID:     delegate.ID + "_delegate",
			Agent:     delegate,
			Result:    resultLabel(runErr),
			Output:    delegateResult.Stdout,
			Stderr:    delegateResult.Stderr,
		})
		if artifactErr != nil {
			return Result{Steps: steps}, artifactErr
		}
		steps = append(steps, StepResult{
			Role:     "delegate",
			Result:   delegateResult,
			Prompt:   delegatePrompt,
			Artifact: delegateArtifact,
		})
		if runErr != nil {
			return Result{Steps: steps}, runErr
		}
		previousSummary = artifacts.SummaryFromEnvelope(delegateArtifact)
	}

	return Result{Steps: steps}, nil
}

func buildDelegatePrompt(userPrompt string, starter domain.AgentProfile, priorSummary string) string {
	return strings.TrimSpace(
		"You are assisting a ROMA multi-agent workflow.\n" +
			"Original user request:\n" + userPrompt + "\n\n" +
			"Starter agent: " + starter.DisplayName + " (" + starter.ID + ")\n\n" +
			"Prior structured artifact summary:\n" + priorSummary + "\n\n" +
			"Provide the next useful contribution for this task. Keep your response focused and execution-oriented.",
	)
}

func resultLabel(err error) string {
	if err != nil {
		return "failed"
	}
	return "success"
}
