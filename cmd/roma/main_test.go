package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/liliang-cn/roma/internal/artifacts"
	"github.com/liliang-cn/roma/internal/domain"
	"github.com/liliang-cn/roma/internal/events"
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

func TestQueueExecutionPhase(t *testing.T) {
	t.Parallel()

	req := queue.Request{Delegates: []string{"my-gemini", "my-copilot"}}
	if got := queueExecutionPhase(req, "task_1_starter_bootstrap"); got != "bootstrap" {
		t.Fatalf("bootstrap phase = %q, want bootstrap", got)
	}
	if got := queueExecutionPhase(req, "task_1_delegate_1"); got != "fanout" {
		t.Fatalf("fanout phase = %q, want fanout", got)
	}
	if got := queueExecutionPhase(queue.Request{}, "task_1"); got != "" {
		t.Fatalf("single-agent phase = %q, want empty", got)
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
