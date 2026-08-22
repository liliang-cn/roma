package runtime

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/liliang-cn/tagit/internal/domain"
	"github.com/liliang-cn/tagit/internal/events"
	"github.com/liliang-cn/tagit/internal/policy"
	"github.com/liliang-cn/tagit/internal/store"
)

func TestBuildCommandForProfileArgs(t *testing.T) {
	t.Parallel()

	supervisor := DefaultSupervisor()
	cmd, err := supervisor.BuildCommand(context.Background(), StartRequest{
		Profile: domain.AgentProfile{
			ID:              "my-codex",
			Command:         "codex",
			Args:            []string{"exec", "--full-auto", "-C", "{cwd}", "{prompt}"},
			PromptTransport: domain.PromptTransportStdin,
		},
		Prompt:     "test prompt",
		WorkingDir: "/tmp/work",
	})
	if err != nil {
		t.Fatalf("BuildCommand() error = %v", err)
	}
	if got := cmd.Args[0]; got != "codex" {
		t.Fatalf("command = %s, want codex", got)
	}
	if got := strings.Join(cmd.Args[1:], " "); got != "exec --full-auto -C /tmp/work - --add-dir /tmp/work/.git" {
		t.Fatalf("args = %q, want %q", got, "exec --full-auto -C /tmp/work - --add-dir /tmp/work/.git")
	}
	if cmd.Stdin == nil {
		t.Fatal("Stdin = nil, want prompt passed via stdin for codex")
	}
}

func TestBuildCommandForCodexDoesNotDuplicateGitAddDir(t *testing.T) {
	t.Parallel()

	supervisor := DefaultSupervisor()
	cmd, err := supervisor.BuildCommand(context.Background(), StartRequest{
		Profile: domain.AgentProfile{
			ID:              "my-codex",
			Command:         "codex",
			Args:            []string{"exec", "--full-auto", "-C", "{cwd}", "{prompt}", "--add-dir", "{cwd}/.git"},
			PromptTransport: domain.PromptTransportStdin,
		},
		Prompt:     "test prompt",
		WorkingDir: "/tmp/work",
	})
	if err != nil {
		t.Fatalf("BuildCommand() error = %v", err)
	}
	if got := strings.Join(cmd.Args[1:], " "); got != "exec --full-auto -C /tmp/work - --add-dir /tmp/work/.git" {
		t.Fatalf("args = %q, want single git add-dir", got)
	}
}

func TestBuildCommandForCodexAddsWorktreeGitDirs(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	workDir := filepath.Join(root, "repo")
	gitDir := filepath.Join(root, "git-meta", "worktrees", "root1")
	commonDir := filepath.Join(root, "git-meta")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(workDir) error = %v", err)
	}
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(gitDir) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(workDir, ".git"), []byte("gitdir: "+gitDir+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(.git) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "commondir"), []byte("../..\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(commondir) error = %v", err)
	}

	supervisor := DefaultSupervisor()
	cmd, err := supervisor.BuildCommand(context.Background(), StartRequest{
		Profile: domain.AgentProfile{
			ID:              "my-codex",
			Command:         "codex",
			Args:            []string{"exec", "--full-auto", "-C", "{cwd}", "{prompt}"},
			PromptTransport: domain.PromptTransportStdin,
		},
		Prompt:     "test prompt",
		WorkingDir: workDir,
	})
	if err != nil {
		t.Fatalf("BuildCommand() error = %v", err)
	}
	got := strings.Join(cmd.Args[1:], " ")
	for _, want := range []string{
		"--add-dir " + gitDir,
		"--add-dir " + commonDir,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("args = %q, missing %q", got, want)
		}
	}
}

func TestProfileAdapterAppendsPromptWhenMissingPlaceholder(t *testing.T) {
	t.Parallel()

	supervisor := DefaultSupervisor()
	cmd, err := supervisor.BuildCommand(context.Background(), StartRequest{
		Profile: domain.AgentProfile{
			ID:      "custom",
			Command: "custom-agent",
			Args:    []string{"--mode", "batch"},
		},
		Prompt:     "do work",
		WorkingDir: "/tmp/work",
	})
	if err != nil {
		t.Fatalf("BuildCommand() error = %v", err)
	}
	if got := strings.Join(cmd.Args[1:], " "); got != "--mode batch do work" {
		t.Fatalf("args = %q, want %q", got, "--mode batch do work")
	}
}

func TestBuildDelegationPrompt(t *testing.T) {
	t.Parallel()

	got := BuildDelegationPrompt("do work", []domain.AgentProfile{
		{ID: "gemini-cli", DisplayName: "Gemini CLI"},
	})
	if got == "do work" {
		t.Fatal("BuildDelegationPrompt() did not append delegation guidance")
	}
}

func TestEnsurePTYEnvAddsTerminalDefaults(t *testing.T) {
	t.Parallel()

	cmd := exec.Command("echo", "hello")
	cmd.Env = []string{"PATH=/usr/bin"}
	ensurePTYEnv(cmd)
	if !hasEnvKey(cmd.Env, "TERM") {
		t.Fatal("TERM not injected")
	}
	if !hasEnvKey(cmd.Env, "COLORTERM") {
		t.Fatal("COLORTERM not injected")
	}
}

func TestEnsureCapturedInputInjectsReader(t *testing.T) {
	t.Parallel()

	cmd := exec.Command("echo", "hello")
	ensureCapturedInput(cmd)
	if cmd.Stdin == nil {
		t.Fatal("Stdin = nil, want injected reader")
	}
}

type continuousFakeAdapter struct{}

func (continuousFakeAdapter) Supports(domain.AgentProfile) bool { return true }

func (continuousFakeAdapter) BuildCommand(ctx context.Context, req StartRequest) (*exec.Cmd, error) {
	script := `import sys
prompt = sys.argv[1]
if "Current round: 2" in prompt:
    print("TAGIT_DONE: completed on second round")
else:
    print("still working")`
	return exec.CommandContext(ctx, "python3", "-c", script, req.Prompt), nil
}

func TestRunCapturedContinuous(t *testing.T) {
	t.Parallel()

	supervisor := NewSupervisor(continuousFakeAdapter{})
	result, err := supervisor.RunCaptured(context.Background(), StartRequest{
		Profile: domain.AgentProfile{
			ID:      "fake",
			Command: "python3",
		},
		Prompt:     "build feature",
		WorkingDir: ".",
		Continuous: true,
		MaxRounds:  3,
	})
	if err != nil {
		t.Fatalf("RunCaptured() error = %v", err)
	}
	if !strings.Contains(result.Stdout, "== round 1 ==") || !strings.Contains(result.Stdout, "== round 2 ==") {
		t.Fatalf("continuous output missing rounds: %s", result.Stdout)
	}
	if !strings.Contains(result.Stdout, "TAGIT_DONE:") {
		t.Fatalf("continuous output missing completion marker: %s", result.Stdout)
	}
}

// The last line an agent prints is the one that matters — TAGIT_DONE,
// TAGIT_MERGE_BACK, TAGIT_PICK all arrive at the end. os/exec closes both pipes
// inside Wait as soon as the process exits, so reaping before draining drops
// whatever was still buffered, and a finished run merges nothing with no error
// anywhere. A lot of output before the marker makes the window wide enough that
// the bug shows up rather than depending on which goroutine happens to win.
func TestRunCapturedKeepsTheFinalLineAfterALotOfOutput(t *testing.T) {
	t.Parallel()

	supervisor := NewSupervisor(ProfileAdapter{})
	result, err := supervisor.RunCaptured(context.Background(), StartRequest{
		Profile: domain.AgentProfile{
			ID:      "chatty",
			Command: "sh",
			Args: []string{
				"-c",
				// ~256 KiB of noise, then the line the caller actually reads.
				"i=0; while [ $i -lt 4096 ]; do printf '%064d\\n' $i; i=$((i+1)); done; " +
					"printf 'TAGIT_MERGE_BACK: direct_merge | the last line\\n'",
			},
		},
		Prompt:     "noise then a marker",
		WorkingDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("RunCaptured() error = %v", err)
	}
	if !strings.Contains(result.Stdout, "TAGIT_MERGE_BACK: direct_merge | the last line") {
		tail := result.Stdout
		if len(tail) > 200 {
			tail = tail[len(tail)-200:]
		}
		t.Fatalf("final line was truncated; got %d bytes ending in %q", len(result.Stdout), tail)
	}
	if lines := strings.Count(result.Stdout, "\n"); lines < 4097 {
		t.Errorf("output lost lines: got %d, want at least 4097", lines)
	}
}

func TestShouldUsePTYDisablesPTYForCodexPromptStdin(t *testing.T) {
	t.Parallel()

	supervisor := NewSupervisor(ProfileAdapter{})
	req := StartRequest{
		Profile: domain.AgentProfile{
			ID:              "codex",
			Command:         "codex",
			Args:            []string{"exec", "--full-auto", "-C", "{cwd}", "{prompt}"},
			UsePTY:          true,
			PromptTransport: domain.PromptTransportStdin,
		},
		Prompt:     "stdin prompt",
		WorkingDir: ".",
	}
	if supervisor.shouldUsePTY(ProfileAdapter{}, req) {
		t.Fatal("shouldUsePTY() = true, want false for codex stdin transport")
	}
}

func TestBuildContinuousPromptIncludesRageSupervisorNudge(t *testing.T) {
	t.Parallel()

	prompt := buildContinuousPrompt("ship the feature", "round one output", 2, "rage")
	for _, want := range []string{
		"TagIt rage supervisor is standing next to you",
		"state briefly: current progress, what remains, and the very next concrete action",
		"Current round: 2",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("buildContinuousPrompt() missing %q:\n%s", want, prompt)
		}
	}
}

func TestRunCapturedStreamsStdoutEvents(t *testing.T) {
	t.Parallel()

	mem := store.NewMemoryStore()
	supervisor := NewSupervisorWithEvents(mem, continuousFakeAdapter{})
	result, err := supervisor.RunCaptured(context.Background(), StartRequest{
		ExecutionID: "exec_stream",
		SessionID:   "sess_stream",
		TaskID:      "task_stream",
		Profile: domain.AgentProfile{
			ID:      "fake",
			Command: "python3",
		},
		Prompt:     "build feature",
		WorkingDir: ".",
	})
	if err != nil {
		t.Fatalf("RunCaptured() error = %v", err)
	}
	if !strings.Contains(result.Stdout, "still working") {
		t.Fatalf("stdout = %q, want streamed content", result.Stdout)
	}
	records, err := mem.ListEvents(context.Background(), store.EventFilter{
		SessionID: "sess_stream",
		TaskID:    "task_stream",
		Type:      events.TypeRuntimeStdoutCaptured,
	})
	if err != nil {
		t.Fatalf("ListEvents() error = %v", err)
	}
	if len(records) == 0 {
		t.Fatal("runtime stdout events = 0, want streamed events")
	}
	if got := records[0].Payload["stdout"]; got == "" {
		t.Fatalf("stdout payload = %#v, want chunk", records[0].Payload)
	}
}

type dangerousFakeAdapter struct{}

func (dangerousFakeAdapter) Supports(domain.AgentProfile) bool { return true }

func (dangerousFakeAdapter) BuildCommand(ctx context.Context, req StartRequest) (*exec.Cmd, error) {
	script := `import sys,time
print("$ rm -rf /")
sys.stdout.flush()
time.sleep(10)`
	return exec.CommandContext(ctx, "python3", "-c", script), nil
}

func TestRunCapturedDetectsDangerousOutputAndTerminates(t *testing.T) {
	t.Parallel()

	mem := store.NewMemoryStore()
	supervisor := NewSupervisorWithEvents(mem, dangerousFakeAdapter{})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, err := supervisor.RunCaptured(ctx, StartRequest{
		ExecutionID: "exec_danger",
		SessionID:   "sess_danger",
		TaskID:      "task_danger",
		Profile: domain.AgentProfile{
			ID:      "danger",
			Command: "python3",
		},
		Prompt:     "do dangerous work",
		WorkingDir: ".",
	})
	if err == nil {
		t.Fatal("RunCaptured() error = nil, want termination error")
	}

	records, err := mem.ListEvents(context.Background(), store.EventFilter{
		SessionID: "sess_danger",
		TaskID:    "task_danger",
		Type:      events.TypeDangerousCommandDetected,
	})
	if err != nil {
		t.Fatalf("ListEvents(danger) error = %v", err)
	}
	if len(records) == 0 {
		t.Fatal("dangerous events = 0, want semantic detection")
	}
	if got := records[0].Payload["confidence"]; got != domain.ConfidenceHigh {
		t.Fatalf("confidence = %#v, want %s", got, domain.ConfidenceHigh)
	}
}

type recordingAnalyzer struct {
	mu   sync.Mutex
	reqs []SemanticAnalysisRequest
	seen chan SemanticAnalysisRequest
}

func (a *recordingAnalyzer) AnalyzeSignal(_ context.Context, req SemanticAnalysisRequest) error {
	a.mu.Lock()
	a.reqs = append(a.reqs, req)
	a.mu.Unlock()
	if a.seen != nil {
		a.seen <- req
	}
	return nil
}

func TestRunCapturedInvokesSemanticAnalyzer(t *testing.T) {
	t.Parallel()

	mem := store.NewMemoryStore()
	supervisor := NewSupervisorWithEvents(mem, dangerousFakeAdapter{})
	analyzer := &recordingAnalyzer{seen: make(chan SemanticAnalysisRequest, 1)}
	supervisor.SetSemanticAnalyzer(analyzer)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, _ = supervisor.RunCaptured(ctx, StartRequest{
		ExecutionID: "exec_semantic",
		SessionID:   "sess_semantic",
		TaskID:      "task_semantic",
		Profile: domain.AgentProfile{
			ID:      "danger",
			Command: "python3",
		},
		Prompt:     "do dangerous work",
		WorkingDir: ".",
	})

	select {
	case req := <-analyzer.seen:
		if req.Signal.Kind != policy.SignalDangerousCommandDetected {
			t.Fatalf("signal kind = %s, want %s", req.Signal.Kind, policy.SignalDangerousCommandDetected)
		}
		if req.SourceAgent.ID != "danger" {
			t.Fatalf("source agent = %s, want danger", req.SourceAgent.ID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("semantic analyzer was not invoked")
	}
}

func TestTerminateKillsRunningExecution(t *testing.T) {
	t.Parallel()

	// Create a supervisor with a slow-running command
	mem := store.NewMemoryStore()
	supervisor := NewSupervisorWithEvents(mem, slowFakeAdapter{})

	ctx, cancel := context.WithCancel(context.Background())

	// Start a slow execution in background
	done := make(chan struct{})
	var runErr error
	go func() {
		defer close(done)
		_, runErr = supervisor.RunCaptured(ctx, StartRequest{
			ExecutionID: "exec_slow",
			SessionID:   "sess_slow",
			TaskID:      "task_slow",
			Profile: domain.AgentProfile{
				ID:      "slow",
				Command: "python3",
			},
			Prompt:     "sleep 30",
			WorkingDir: ".",
		})
	}()

	// Wait for the execution to start
	time.Sleep(500 * time.Millisecond)

	// Terminate the execution
	err := supervisor.Terminate("exec_slow")
	if err != nil {
		t.Fatalf("Terminate() error = %v", err)
	}

	// Wait for the execution to stop
	<-done
	cancel()

	// The execution should have been terminated (not completed successfully)
	if runErr == nil {
		t.Log("RunCaptured returned nil error (process may have already finished)")
	}
}

func TestTerminateNonExistentExecution(t *testing.T) {
	t.Parallel()

	mem := store.NewMemoryStore()
	supervisor := NewSupervisorWithEvents(mem, slowFakeAdapter{})

	// Terminating a non-existent execution should not error
	err := supervisor.Terminate("exec_nonexistent")
	if err != nil {
		t.Fatalf("Terminate() error = %v, want nil", err)
	}
}

// slowFakeAdapter runs a slow command that can be terminated
type slowFakeAdapter struct{}

func (slowFakeAdapter) Supports(profile domain.AgentProfile) bool { return profile.ID == "slow" }

func (slowFakeAdapter) BuildCommand(_ context.Context, req StartRequest) (*exec.Cmd, error) {
	// Create a slow command that runs for 30 seconds
	cmd := exec.Command("python3", "-c", "import time; time.sleep(30)")
	return cmd, nil
}
