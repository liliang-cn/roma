package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liliang-cn/roma/internal/agents"
	"github.com/liliang-cn/roma/internal/api"
	"github.com/liliang-cn/roma/internal/artifacts"
	"github.com/liliang-cn/roma/internal/domain"
	"github.com/liliang-cn/roma/internal/events"
	"github.com/liliang-cn/roma/internal/history"
	"github.com/liliang-cn/roma/internal/queue"
)

func TestQueueCuriaSuffix(t *testing.T) {
	t.Parallel()

	items := []domain.ArtifactEnvelope{
		{
			Kind: domain.ArtifactKindDebateLog,
			Payload: artifacts.DebateLogPayload{
				DisputeClass: "close_score",
			},
		},
		{
			Kind: domain.ArtifactKindDecisionPack,
			Payload: artifacts.DecisionPackPayload{
				WinningMode:  "merge",
				Arbitrated:   true,
				ArbitratorID: "claude-code",
			},
		},
	}

	got := queueCuriaSuffix(items)
	want := "curia mode=merge arbitrated=claude-code dispute=close_score"
	if got != want {
		t.Fatalf("queueCuriaSuffix() = %q, want %q", got, want)
	}
}

func TestParseQueueArgsTailRaw(t *testing.T) {
	t.Parallel()

	status, mode, subcommand, subArg, raw, err := parseQueueArgs([]string{"tail", "--raw", "job_123"})
	if err != nil {
		t.Fatalf("parseQueueArgs() error = %v", err)
	}
	if status != "" || mode != "" {
		t.Fatalf("unexpected filters: status=%q mode=%q", status, mode)
	}
	if subcommand != "tail" || subArg != "job_123" {
		t.Fatalf("tail parse = (%q, %q), want (tail, job_123)", subcommand, subArg)
	}
	if !raw {
		t.Fatal("raw = false, want true")
	}
}

func TestParseQueueArgsAttach(t *testing.T) {
	t.Parallel()

	status, mode, subcommand, subArg, raw, err := parseQueueArgs([]string{"attach", "job_456"})
	if err != nil {
		t.Fatalf("parseQueueArgs() error = %v", err)
	}
	if status != "" || mode != "" || raw {
		t.Fatalf("unexpected attach parse state: status=%q mode=%q raw=%t", status, mode, raw)
	}
	if subcommand != "attach" || subArg != "job_456" {
		t.Fatalf("attach parse = (%q, %q), want (attach, job_456)", subcommand, subArg)
	}
}

func TestQueueTailEventLinesStructured(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 11, 15, 0, 0, 0, time.UTC)
	lines := queueTailEventLines([]events.Record{
		{
			ID:         "evt_1",
			TaskID:     "task_1",
			Type:       events.TypeRuntimeStarted,
			OccurredAt: now,
			Payload: map[string]any{
				"agent":        "my-codex",
				"execution_id": "exec_1",
				"pid":          4242,
			},
		},
	}, map[string]struct{}{}, false)
	if len(lines) != 1 {
		t.Fatalf("structured lines = %d, want 1", len(lines))
	}
	want := `[runtime-start] time=2026-03-11T15:00:00Z task=task_1 exec=exec_1 agent=my-codex pid=4242`
	if lines[0] != want {
		t.Fatalf("structured line = %q, want %q", lines[0], want)
	}
}

func TestFormatQueueTailLineIncludesStructuredLiveMetadata(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 12, 10, 0, 0, 0, time.UTC)
	line := formatQueueTailLine(api.QueueInspectResponse{
		Job: queue.Request{
			ID:     "job_1",
			Status: queue.StatusRunning,
		},
		Live: &api.RuntimeLiveSummary{
			Phase:            "fanout",
			CurrentRound:     2,
			ParticipantCount: 3,
			CurrentTaskID:    "task_delegate",
			CurrentAgentID:   "my-codex",
			ProcessPID:       4242,
			WorkspacePath:    "/tmp/repo/.roma/workspaces/sess/task/root",
			WorkspaceMode:    "isolated_write",
			LastOutputAt:     &now,
		},
	})
	for _, want := range []string{
		"phase=fanout",
		"round=2",
		"agents=3",
		"task=task_delegate",
		"agent=my-codex",
		"pid=4242",
		"workspace_mode=isolated_write",
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("formatQueueTailLine() = %q, missing %q", line, want)
		}
	}
}

func TestFormatQueueTailLineIncludesSummaryCounts(t *testing.T) {
	t.Parallel()

	line := formatQueueTailLine(api.QueueInspectResponse{
		Job: queue.Request{
			ID:     "job_1",
			Status: queue.StatusRunning,
		},
		ArtifactCount: 2,
		EventCount:    9,
	})
	for _, want := range []string{"artifacts=2", "events=9"} {
		if !strings.Contains(line, want) {
			t.Fatalf("formatQueueTailLine() = %q, missing %q", line, want)
		}
	}
}

func TestQueueTailEventLinesRaw(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 11, 15, 0, 0, 0, time.UTC)
	lines := queueTailEventLines([]events.Record{
		{
			ID:         "evt_1",
			TaskID:     "task_1",
			Type:       events.TypeRuntimeStdoutCaptured,
			OccurredAt: now,
			Payload: map[string]any{
				"agent":  "my-codex",
				"stdout": "scan started\n",
			},
		},
	}, map[string]struct{}{}, true)
	if len(lines) != 1 {
		t.Fatalf("raw lines = %d, want 1", len(lines))
	}
	if lines[0] != "scan started" {
		t.Fatalf("raw line = %q, want %q", lines[0], "scan started")
	}
}

func TestQueueTailEventLinesSemanticStructured(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 11, 15, 0, 0, 0, time.UTC)
	lines := queueTailEventLines([]events.Record{
		{
			ID:         "evt_sem_1",
			TaskID:     "task_1",
			Type:       events.TypeDangerousCommandDetected,
			OccurredAt: now,
			ReasonCode: "dangerous_shell_rm_root",
			Payload: map[string]any{
				"agent":      "my-codex",
				"confidence": "high",
				"text":       "$ rm -rf /",
			},
		},
	}, map[string]struct{}{}, false)
	if len(lines) != 1 {
		t.Fatalf("semantic lines = %d, want 1", len(lines))
	}
	for _, want := range []string{"[dangerous]", "confidence=high", `text="$ rm -rf /"`} {
		if !strings.Contains(lines[0], want) {
			t.Fatalf("structured line = %q, missing %q", lines[0], want)
		}
	}
}

func TestQueueTailEventLinesSemanticReportStructured(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 11, 15, 0, 0, 0, time.UTC)
	lines := queueTailEventLines([]events.Record{
		{
			ID:         "evt_semantic_report",
			TaskID:     "task_1",
			Type:       events.TypeSemanticReportProduced,
			OccurredAt: now,
			ReasonCode: "approval_request",
			Payload: map[string]any{
				"classifier_agent_id": "my-codex",
				"risk":                "high",
				"summary":             "The agent is asking for risky approval.",
			},
		},
	}, map[string]struct{}{}, false)
	if len(lines) != 1 {
		t.Fatalf("semantic report lines = %d, want 1", len(lines))
	}
	for _, want := range []string{"[semantic]", "classifier=my-codex", "risk=high", "intent=approval_request"} {
		if !strings.Contains(lines[0], want) {
			t.Fatalf("structured line = %q, missing %q", lines[0], want)
		}
	}
}

func TestParseRunArgsWithAlias(t *testing.T) {
	t.Parallel()

	req, err := parseRunArgs([]string{"--agent", "my-codex", "--with", "my-gemini,my-copilot", "build", "feature"})
	if err != nil {
		t.Fatalf("parseRunArgs() with --with error = %v", err)
	}
	if len(req.Delegates) != 2 || req.Delegates[0] != "my-gemini" || req.Delegates[1] != "my-copilot" {
		t.Fatalf("delegates via --with = %#v, want [my-gemini my-copilot]", req.Delegates)
	}

	req, err = parseRunArgs([]string{"--agent", "my-codex", "--delegate", "my-gemini", "build", "feature"})
	if err != nil {
		t.Fatalf("parseRunArgs() with --delegate alias error = %v", err)
	}
	if len(req.Delegates) != 1 || req.Delegates[0] != "my-gemini" {
		t.Fatalf("delegates via --delegate = %#v, want [my-gemini]", req.Delegates)
	}
}

func TestRunAgentsAddRejectsMetaFlag(t *testing.T) {
	t.Parallel()

	registry, err := agents.DefaultRegistry()
	if err != nil {
		t.Fatalf("DefaultRegistry() error = %v", err)
	}
	err = runAgents(context.Background(), registry, []string{
		"add", "my-codex", "My Codex", "/usr/bin/codex", "--meta", "role=classifier",
	})
	if err == nil {
		t.Fatal("runAgents() error = nil, want unknown argument")
	}
	if !strings.Contains(err.Error(), `unknown argument "--meta"`) {
		t.Fatalf("runAgents() error = %q, want unknown argument --meta", err)
	}
}

func TestCandidateQueueRootsIncludesWorkspaceAndHome(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".roma-home")
	t.Setenv("ROMA_HOME", home)
	roots := candidateQueueRoots("/tmp/project")
	if len(roots) != 1 {
		t.Fatalf("root count = %d, want 1", len(roots))
	}
	if roots[0] != filepath.Clean(home) {
		t.Fatalf("roots[0] = %q, want ROMA_HOME", roots[0])
	}
}

func TestPrintResultShowPending(t *testing.T) {
	t.Parallel()

	out := captureStdout(t, func() {
		if err := printResultShow(api.ResultShowResponse{
			Session: history.SessionRecord{
				ID:     "sess_pending",
				Status: "running",
			},
			Pending: true,
			Message: "result is not ready yet; session status is running",
		}); err != nil {
			t.Fatalf("printResultShow() error = %v", err)
		}
	})
	for _, want := range []string{
		"session=sess_pending",
		"status=running",
		"pending=true",
		"message=result is not ready yet; session status is running",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output = %q, missing %q", out, want)
		}
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error = %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = old }()

	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	return string(data)
}

func TestFindQueueRequestAcrossRootsFallsBackToHome(t *testing.T) {
	wd := t.TempDir()
	home := filepath.Join(t.TempDir(), ".roma-home")
	t.Setenv("ROMA_HOME", home)
	store := queue.NewStore(home)
	if err := store.Enqueue(context.Background(), queue.Request{
		ID:           "job_home",
		Prompt:       "test",
		StarterAgent: "starter",
		WorkingDir:   wd,
	}); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}

	item, root, err := findQueueRequestAcrossRoots(context.Background(), wd, "job_home")
	if err != nil {
		t.Fatalf("findQueueRequestAcrossRoots() error = %v", err)
	}
	if root != home {
		t.Fatalf("root = %q, want %q", root, home)
	}
	if item.ID != "job_home" {
		t.Fatalf("item id = %q, want job_home", item.ID)
	}
}

func TestResolveQueueClientRootUsesFoundHomeJob(t *testing.T) {
	wd := t.TempDir()
	home := filepath.Join(t.TempDir(), ".roma-home")
	t.Setenv("ROMA_HOME", home)
	store := queue.NewStore(home)
	if err := store.Enqueue(context.Background(), queue.Request{
		ID:           "job_home_root",
		Prompt:       "test",
		StarterAgent: "starter",
		WorkingDir:   wd,
	}); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}

	if got := resolveQueueClientRoot(context.Background(), wd, "job_home_root"); got != home {
		t.Fatalf("resolveQueueClientRoot() = %q, want %q", got, home)
	}
}
