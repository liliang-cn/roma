package runtime

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/liliang-cn/roma/internal/domain"
	"github.com/liliang-cn/roma/internal/events"
	"github.com/liliang-cn/roma/internal/policy"
	"github.com/liliang-cn/roma/internal/store"
)

// StartRequest describes a runtime launch.
type StartRequest struct {
	ExecutionID string
	SessionID   string
	TaskID      string
	Profile     domain.AgentProfile
	Prompt      string
	WorkingDir  string
	Continuous  bool
	MaxRounds   int
}

// ExecutionState is the tracked supervisor state for a process.
type ExecutionState string

const (
	ExecutionStatePending   ExecutionState = "pending"
	ExecutionStateRunning   ExecutionState = "running"
	ExecutionStateSucceeded ExecutionState = "succeeded"
	ExecutionStateFailed    ExecutionState = "failed"
)

// Execution is the supervisor-tracked record for a runtime invocation.
type Execution struct {
	ID        string
	SessionID string
	TaskID    string
	Profile   domain.AgentProfile
	PID       int
	State     ExecutionState
	StartedAt time.Time
	EndedAt   time.Time
}

// Result captures a runtime execution outcome.
type Result struct {
	ExecutionID string
	Profile     domain.AgentProfile
	Stdout      string
	Stderr      string
}

// Adapter builds a launch command for a specific agent runtime.
type Adapter interface {
	Supports(profile domain.AgentProfile) bool
	BuildCommand(ctx context.Context, req StartRequest) (*exec.Cmd, error)
}

// PTYPreference reports whether the adapter should be launched under a PTY.
type PTYPreference interface {
	RequiresPTY(profile domain.AgentProfile) bool
}

// Supervisor launches agent runtimes.
type Supervisor struct {
	adapters   []Adapter
	events     store.EventStore
	pty        PTYProvider
	now        func() time.Time
	mu         sync.RWMutex
	executions map[string]Execution
	eventSeq   uint64
}

// NewSupervisor constructs a runtime supervisor.
func NewSupervisor(adapters ...Adapter) *Supervisor {
	return &Supervisor{
		adapters:   adapters,
		pty:        newPTYProvider(),
		now:        func() time.Time { return time.Now().UTC() },
		executions: make(map[string]Execution),
	}
}

// NewSupervisorWithEvents constructs a supervisor with event append support.
func NewSupervisorWithEvents(eventStore store.EventStore, adapters ...Adapter) *Supervisor {
	s := NewSupervisor(adapters...)
	s.events = eventStore
	return s
}

// DefaultSupervisor wires the built-in starter-agent adapters.
func DefaultSupervisor() *Supervisor {
	return NewSupervisor(ProfileAdapter{})
}

// NewDefaultSupervisorWithEvents constructs the default user-profile-driven supervisor with event append support.
func NewDefaultSupervisorWithEvents(eventStore store.EventStore) *Supervisor {
	return NewSupervisorWithEvents(eventStore, ProfileAdapter{})
}

// RunAttached launches the runtime and attaches stdio to the current terminal.
func (s *Supervisor) RunAttached(ctx context.Context, req StartRequest) error {
	execID := s.ensureExecutionID(req)
	command, adapter, err := s.resolveCommand(ctx, req)
	if err != nil {
		return err
	}
	if err := s.applyRuntimePolicy(ctx, req, command); err != nil {
		return err
	}

	command.Dir = req.WorkingDir
	if s.shouldUsePTY(adapter, req.Profile) {
		if err := s.runAttachedPTY(req, execID, command); err == nil {
			return nil
		} else if !canFallbackPTY(err) {
			s.markFinished(execID, req.Profile, ExecutionStateFailed)
			return err
		} else if command, _, err = s.resolveCommand(ctx, req); err != nil {
			s.markFinished(execID, req.Profile, ExecutionStateFailed)
			return err
		}
		command.Dir = req.WorkingDir
	}
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	command.Stdin = os.Stdin

	if err := command.Start(); err != nil {
		return fmt.Errorf("run agent %s: %w", req.Profile.ID, err)
	}
	pid := 0
	if command.Process != nil {
		pid = command.Process.Pid
	}
	s.markStarted(req, execID, pid)
	if err := command.Wait(); err != nil {
		s.markFinished(execID, req.Profile, ExecutionStateFailed)
		return fmt.Errorf("run agent %s: %w", req.Profile.ID, err)
	}
	s.markFinished(execID, req.Profile, ExecutionStateSucceeded)
	return nil
}

// RunCaptured launches the runtime and captures stdout and stderr.
func (s *Supervisor) RunCaptured(ctx context.Context, req StartRequest) (Result, error) {
	if req.Continuous {
		return s.runCapturedContinuous(ctx, req)
	}
	return s.runCapturedSingle(ctx, req)
}

func (s *Supervisor) runCapturedSingle(ctx context.Context, req StartRequest) (Result, error) {
	execID := s.ensureExecutionID(req)
	command, adapter, err := s.resolveCommand(ctx, req)
	if err != nil {
		return Result{}, err
	}
	if err := s.applyRuntimePolicy(ctx, req, command); err != nil {
		return Result{}, err
	}

	command.Dir = req.WorkingDir
	if s.shouldUsePTY(adapter, req.Profile) {
		result, err := s.runCapturedPTY(req, execID, command)
		if err == nil {
			return result, nil
		}
		if !canFallbackPTY(err) {
			return result, err
		}
		if command, _, err = s.resolveCommand(ctx, req); err != nil {
			return Result{}, err
		}
		command.Dir = req.WorkingDir
	}

	stdoutPipe, err := command.StdoutPipe()
	if err != nil {
		s.markFinished(execID, req.Profile, ExecutionStateFailed)
		return Result{}, fmt.Errorf("stdout pipe for agent %s: %w", req.Profile.ID, err)
	}
	stderrPipe, err := command.StderrPipe()
	if err != nil {
		s.markFinished(execID, req.Profile, ExecutionStateFailed)
		return Result{}, fmt.Errorf("stderr pipe for agent %s: %w", req.Profile.ID, err)
	}
	if err := command.Start(); err != nil {
		return Result{}, fmt.Errorf("run agent %s: %w", req.Profile.ID, err)
	}
	pid := 0
	if command.Process != nil {
		pid = command.Process.Pid
	}
	s.markStarted(req, execID, pid)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	var copyWG sync.WaitGroup
	copyWG.Add(2)
	go func() {
		defer copyWG.Done()
		s.streamOutput(req, execID, stdoutPipe, &stdout, "stdout")
	}()
	go func() {
		defer copyWG.Done()
		s.streamOutput(req, execID, stderrPipe, &stderr, "stderr")
	}()

	waitErr := command.Wait()
	copyWG.Wait()
	if waitErr != nil {
		s.markFinished(execID, req.Profile, ExecutionStateFailed)
		return Result{
			ExecutionID: execID,
			Profile:     req.Profile,
			Stdout:      stdout.String(),
			Stderr:      stderr.String(),
		}, fmt.Errorf("run agent %s: %w", req.Profile.ID, waitErr)
	}
	s.markFinished(execID, req.Profile, ExecutionStateSucceeded)

	return Result{
		ExecutionID: execID,
		Profile:     req.Profile,
		Stdout:      stdout.String(),
		Stderr:      stderr.String(),
	}, nil
}

func (s *Supervisor) runCapturedContinuous(ctx context.Context, req StartRequest) (Result, error) {
	maxRounds := req.MaxRounds
	if maxRounds <= 0 {
		maxRounds = 3
	}

	baseExecID := s.ensureExecutionID(req)
	currentPrompt := buildContinuousPrompt(req.Prompt, "", 1)
	var stdout strings.Builder
	var stderr strings.Builder

	for round := 1; round <= maxRounds; round++ {
		roundReq := req
		roundReq.ExecutionID = fmt.Sprintf("%s_r%d", baseExecID, round)
		roundReq.Prompt = currentPrompt
		roundReq.Continuous = false
		roundReq.MaxRounds = 0

		result, err := s.runCapturedSingle(ctx, roundReq)
		appendRoundOutput(&stdout, round, result.Stdout)
		appendRoundOutput(&stderr, round, result.Stderr)
		if err != nil {
			return Result{
				ExecutionID: baseExecID,
				Profile:     req.Profile,
				Stdout:      stdout.String(),
				Stderr:      stderr.String(),
			}, err
		}
		if isContinuousDone(result.Stdout, result.Stderr) {
			return Result{
				ExecutionID: baseExecID,
				Profile:     req.Profile,
				Stdout:      stdout.String(),
				Stderr:      stderr.String(),
			}, nil
		}
		currentPrompt = buildContinuousPrompt(req.Prompt, stdout.String(), round+1)
	}

	return Result{
		ExecutionID: baseExecID,
		Profile:     req.Profile,
		Stdout:      stdout.String(),
		Stderr:      stderr.String(),
	}, fmt.Errorf("continuous execution reached max rounds (%d) without ROMA_DONE marker", maxRounds)
}

// BuildCommand resolves an adapter for the profile.
func (s *Supervisor) BuildCommand(ctx context.Context, req StartRequest) (*exec.Cmd, error) {
	command, _, err := s.resolveCommand(ctx, req)
	return command, err
}

func (s *Supervisor) resolveCommand(ctx context.Context, req StartRequest) (*exec.Cmd, Adapter, error) {
	for _, adapter := range s.adapters {
		if adapter.Supports(req.Profile) {
			command, err := adapter.BuildCommand(ctx, req)
			return command, adapter, err
		}
	}
	return nil, nil, fmt.Errorf("no runtime adapter for agent %q", req.Profile.ID)
}

// GetExecution returns a tracked execution by id.
func (s *Supervisor) GetExecution(id string) (Execution, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	exec, ok := s.executions[id]
	return exec, ok
}

func (s *Supervisor) ensureExecutionID(req StartRequest) string {
	if req.ExecutionID != "" {
		return req.ExecutionID
	}
	return fmt.Sprintf("exec_%d", s.now().UnixNano())
}

func (s *Supervisor) markStarted(req StartRequest, execID string, pid int) {
	exec := Execution{
		ID:        execID,
		SessionID: req.SessionID,
		TaskID:    req.TaskID,
		Profile:   req.Profile,
		PID:       pid,
		State:     ExecutionStateRunning,
		StartedAt: s.now(),
	}
	s.mu.Lock()
	s.executions[execID] = exec
	s.mu.Unlock()
	if s.events != nil {
		_ = s.events.AppendEvent(context.Background(), events.Record{
			ID:         "evt_" + execID + "_started",
			SessionID:  req.SessionID,
			TaskID:     req.TaskID,
			Type:       events.TypeRuntimeStarted,
			ActorType:  events.ActorTypeSystem,
			OccurredAt: exec.StartedAt,
			Payload: map[string]any{
				"execution_id": execID,
				"agent":        req.Profile.ID,
				"pid":          pid,
			},
		})
	}
}

func (s *Supervisor) applyRuntimePolicy(ctx context.Context, req StartRequest, command *exec.Cmd) error {
	if s.events == nil {
		return nil
	}
	decision, err := policy.NewSimpleBroker(s.events).ClassifyCommand(ctx, req.SessionID, req.TaskID, command)
	if err != nil {
		return err
	}
	if decision.Kind == policy.DecisionBlock {
		return fmt.Errorf("policy blocked runtime command: %s", decision.Reason)
	}
	return nil
}

func (s *Supervisor) markFinished(execID string, profile domain.AgentProfile, state ExecutionState) {
	s.mu.Lock()
	exec := s.executions[execID]
	exec.ID = execID
	exec.Profile = profile
	exec.State = state
	exec.EndedAt = s.now()
	s.executions[execID] = exec
	s.mu.Unlock()
	if s.events != nil {
		payload := map[string]any{
			"execution_id": execID,
			"agent":        profile.ID,
		}
		if exec.PID > 0 {
			payload["pid"] = exec.PID
		}
		_ = s.events.AppendEvent(context.Background(), events.Record{
			ID:         "evt_" + execID + "_exited",
			SessionID:  exec.SessionID,
			TaskID:     exec.TaskID,
			Type:       events.TypeRuntimeExited,
			ActorType:  events.ActorTypeSystem,
			OccurredAt: exec.EndedAt,
			ReasonCode: string(state),
			Payload:    payload,
		})
	}
}

func (s *Supervisor) appendOutputEvent(execID string, req StartRequest, stdout string) {
	if s.events == nil || stdout == "" {
		return
	}
	id := fmt.Sprintf("evt_%s_stdout_%d", execID, atomic.AddUint64(&s.eventSeq, 1))
	_ = s.events.AppendEvent(context.Background(), events.Record{
		ID:         id,
		SessionID:  req.SessionID,
		TaskID:     req.TaskID,
		Type:       events.TypeRuntimeStdoutCaptured,
		ActorType:  events.ActorTypeAgent,
		OccurredAt: s.now(),
		Payload: map[string]any{
			"execution_id": execID,
			"agent":       req.Profile.ID,
			"stdout":      stdout,
			"working_dir": req.WorkingDir,
		},
	})
	logRuntimeChunk(req, stdout)
}

func (s *Supervisor) shouldUsePTY(adapter Adapter, profile domain.AgentProfile) bool {
	if s.pty == nil {
		return false
	}
	preference, ok := adapter.(PTYPreference)
	if !ok {
		return false
	}
	return preference.RequiresPTY(profile)
}

func (s *Supervisor) runAttachedPTY(req StartRequest, execID string, command *exec.Cmd) error {
	session, err := s.pty.Start(command)
	if err != nil {
		return fmt.Errorf("start PTY for %s: %w", req.Profile.ID, err)
	}
	defer session.Close()
	pid := 0
	if command.Process != nil {
		pid = command.Process.Pid
	}
	s.markStarted(req, execID, pid)

	copyDone := make(chan error, 2)
	go func() {
		_, copyErr := io.Copy(os.Stdout, session)
		copyDone <- copyErr
	}()
	go func() {
		_, copyErr := io.Copy(session, os.Stdin)
		copyDone <- copyErr
	}()

	waitErr := session.Wait()
	s.markFinished(execID, req.Profile, mapExecutionState(waitErr))
	_ = session.Close()

	for i := 0; i < 2; i++ {
		<-copyDone
	}
	if waitErr != nil {
		return fmt.Errorf("run agent %s: %w", req.Profile.ID, waitErr)
	}
	return nil
}

func (s *Supervisor) runCapturedPTY(req StartRequest, execID string, command *exec.Cmd) (Result, error) {
	session, err := s.pty.Start(command)
	if err != nil {
		return Result{}, fmt.Errorf("start PTY for %s: %w", req.Profile.ID, err)
	}
	defer session.Close()
	pid := 0
	if command.Process != nil {
		pid = command.Process.Pid
	}
	s.markStarted(req, execID, pid)

	var output bytes.Buffer
	readErrCh := make(chan error, 1)
	go func() {
		readErrCh <- s.streamOutput(req, execID, session, &output, "pty")
	}()
	waitErr := session.Wait()
	readErr := <-readErrCh
	s.markFinished(execID, req.Profile, mapExecutionState(waitErr))

	combined := output.String()
	result := Result{
		ExecutionID: execID,
		Profile:     req.Profile,
		Stdout:      combined,
	}
	if readErr != nil && !isExpectedPTYReadError(readErr) {
		return result, fmt.Errorf("read PTY output for %s: %w", req.Profile.ID, readErr)
	}
	if waitErr != nil {
		return result, fmt.Errorf("run agent %s: %w", req.Profile.ID, waitErr)
	}
	return result, nil
}

func (s *Supervisor) streamOutput(req StartRequest, execID string, reader io.Reader, dst *bytes.Buffer, stream string) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		chunk := line + "\n"
		dst.WriteString(chunk)
		if stream != "stderr" {
			s.appendOutputEvent(execID, req, chunk)
		} else {
			logRuntimeChunk(req, chunk)
		}
	}
	if err := scanner.Err(); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
	return nil
}

func logRuntimeChunk(req StartRequest, chunk string) {
	chunk = strings.TrimSpace(chunk)
	if chunk == "" {
		return
	}
	if len(chunk) > 240 {
		chunk = chunk[:237] + "..."
	}
	log.Printf("romad output session=%s task=%s agent=%s: %s", req.SessionID, req.TaskID, req.Profile.ID, chunk)
}

func mapExecutionState(err error) ExecutionState {
	if err != nil {
		return ExecutionStateFailed
	}
	return ExecutionStateSucceeded
}

func canFallbackPTY(err error) bool {
	if err == nil {
		return false
	}
	if os.IsPermission(err) || errors.Is(err, os.ErrPermission) {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "/dev/ptmx") ||
		strings.Contains(message, "pty provider is not implemented")
}

func isExpectedPTYReadError(err error) bool {
	return errors.Is(err, syscall.EIO)
}

func buildContinuousPrompt(originalPrompt, previousOutput string, round int) string {
	var b strings.Builder
	b.WriteString("ROMA continuous execution mode.\n")
	b.WriteString("Keep working on the same task across rounds.\n")
	b.WriteString("When the task is complete, start your response with `ROMA_DONE:`.\n")
	b.WriteString("If the task is not complete, keep making concrete progress and ROMA will continue you.\n")
	b.WriteString(fmt.Sprintf("Current round: %d\n\n", round))
	b.WriteString("Original task:\n")
	b.WriteString(originalPrompt)
	if strings.TrimSpace(previousOutput) != "" {
		b.WriteString("\n\nPrevious rounds output:\n")
		b.WriteString(previousOutput)
	}
	return b.String()
}

func appendRoundOutput(dst *strings.Builder, round int, output string) {
	if strings.TrimSpace(output) == "" {
		return
	}
	if dst.Len() > 0 {
		dst.WriteString("\n")
	}
	dst.WriteString(fmt.Sprintf("== round %d ==\n", round))
	dst.WriteString(output)
	if !strings.HasSuffix(output, "\n") {
		dst.WriteString("\n")
	}
}

func isContinuousDone(stdout, stderr string) bool {
	combined := strings.ToUpper(stdout + "\n" + stderr)
	return strings.Contains(combined, "ROMA_DONE:")
}
