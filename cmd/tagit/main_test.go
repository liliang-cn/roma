package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liliang-cn/tagit/internal/agents"
	"github.com/liliang-cn/tagit/internal/api"
	"github.com/liliang-cn/tagit/internal/artifacts"
	"github.com/liliang-cn/tagit/internal/domain"
	"github.com/liliang-cn/tagit/internal/events"
	"github.com/liliang-cn/tagit/internal/history"
	"github.com/liliang-cn/tagit/internal/queue"
	runsvc "github.com/liliang-cn/tagit/internal/run"
	"github.com/liliang-cn/tagit/internal/scheduler"
	workspacepkg "github.com/liliang-cn/tagit/internal/workspace"
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
			WorkspacePath:    "/tmp/repo/.tagit/workspaces/sess/task/root",
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

func TestQueueTailEventLinesSemanticRecommendationsStructured(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 11, 15, 0, 0, 0, time.UTC)
	lines := queueTailEventLines([]events.Record{
		{
			ID:         "evt_semantic_curia",
			TaskID:     "task_1",
			Type:       events.TypeCuriaPromotionRecommended,
			OccurredAt: now,
			ReasonCode: "approval_request",
			Payload: map[string]any{
				"classifier_agent_id": "my-codex",
				"risk":                "high",
				"summary":             "Escalate this run into Curia.",
			},
		},
		{
			ID:         "evt_semantic_approval",
			TaskID:     "task_1",
			Type:       events.TypeSemanticApprovalRecommended,
			OccurredAt: now.Add(time.Second),
			ReasonCode: "destructive_write",
			Payload: map[string]any{
				"classifier_agent_id": "my-codex",
				"risk":                "high",
				"summary":             "Require human approval before continuing.",
			},
		},
	}, map[string]struct{}{}, false)
	if len(lines) != 2 {
		t.Fatalf("semantic recommendation lines = %d, want 2", len(lines))
	}
	for _, want := range []string{"[curia-recommend]", "classifier=my-codex", "risk=high", "intent=approval_request"} {
		if !strings.Contains(lines[0], want) {
			t.Fatalf("curia recommendation line = %q, missing %q", lines[0], want)
		}
	}
	for _, want := range []string{"[approval-recommend]", "classifier=my-codex", "risk=high", "intent=destructive_write"} {
		if !strings.Contains(lines[1], want) {
			t.Fatalf("approval recommendation line = %q, missing %q", lines[1], want)
		}
	}
}

func TestParseRunArgsModes(t *testing.T) {
	t.Parallel()

	req, err := parseRunArgs([]string{"--mode", "senate", "--agent", "my-codex", "--with", "my-gemini,my-copilot", "--prompt", "build feature", "--verbose", "-d"})
	if err != nil {
		t.Fatalf("parseRunArgs() with --with error = %v", err)
	}
	if req.Prompt != "build feature" {
		t.Fatalf("prompt = %q, want %q", req.Prompt, "build feature")
	}
	if req.Mode != runsvc.RunModeSenate {
		t.Fatalf("mode = %q, want %q", req.Mode, runsvc.RunModeSenate)
	}
	if !req.Verbose {
		t.Fatal("verbose = false, want true")
	}
	if !req.Detach {
		t.Fatal("detach = false, want true")
	}
	if len(req.Delegates) != 2 || req.Delegates[0] != "my-gemini" || req.Delegates[1] != "my-copilot" {
		t.Fatalf("delegates via --with = %#v, want [my-gemini my-copilot]", req.Delegates)
	}

	req, err = parseRunArgs([]string{"--agent", "my-codex", "--delegate", "my-gemini", "--prompt", "build feature"})
	if err != nil {
		t.Fatalf("parseRunArgs() with --delegate alias error = %v", err)
	}
	if len(req.Delegates) != 1 || req.Delegates[0] != "my-gemini" {
		t.Fatalf("delegates via --delegate = %#v, want [my-gemini]", req.Delegates)
	}

	req, err = parseRunArgs([]string{"--mode", "collab", "--agent", "my-codex", "--prompt", "build feature"})
	if err != nil {
		t.Fatalf("parseRunArgs() with collab error = %v", err)
	}
	if req.Mode != runsvc.RunModeCollab {
		t.Fatalf("mode = %q, want %q", req.Mode, runsvc.RunModeCollab)
	}

	req, err = parseRunArgs([]string{"--mode", "rage", "--agent", "my-codex", "--prompt", "build feature"})
	if err != nil {
		t.Fatalf("parseRunArgs() with rage error = %v", err)
	}
	if req.Mode != runsvc.RunModeRage {
		t.Fatalf("mode = %q, want %q", req.Mode, runsvc.RunModeRage)
	}

	for _, mode := range []string{"relay", "fanout", "caesar"} {
		_, err := parseRunArgs([]string{"--mode", mode, "--agent", "my-codex", "--prompt", "build feature"})
		if err == nil || !strings.Contains(err.Error(), "unsupported run mode") {
			t.Fatalf("parseRunArgs(%q) error = %v, want unsupported run mode", mode, err)
		}
	}
}

func TestParseRunArgsPromptFile(t *testing.T) {
	t.Parallel()

	req, err := parseRunArgs([]string{"--agent", "my-codex", "--prompt-file", "./prompt.txt"})
	if err != nil {
		t.Fatalf("parseRunArgs() with --prompt-file error = %v", err)
	}
	if req.PromptFile != "./prompt.txt" {
		t.Fatalf("prompt file = %q, want %q", req.PromptFile, "./prompt.txt")
	}
	if req.Prompt != "" {
		t.Fatalf("prompt = %q, want empty until prompt file is read", req.Prompt)
	}
}

func TestParseRunArgsRequiresPromptFlag(t *testing.T) {
	t.Parallel()

	_, err := parseRunArgs([]string{"--agent", "my-codex"})
	if err == nil || !strings.Contains(err.Error(), "one of --prompt or --prompt-file is required") {
		t.Fatalf("parseRunArgs() error = %v, want prompt or prompt-file requirement", err)
	}

	_, err = parseRunArgs([]string{"--agent", "my-codex", "build", "feature"})
	if err == nil || !strings.Contains(err.Error(), `unexpected positional argument "build"; use --prompt or --prompt-file`) {
		t.Fatalf("parseRunArgs() error = %v, want positional argument guidance", err)
	}
}

func TestParseRunArgsRejectsPromptAndPromptFileTogether(t *testing.T) {
	t.Parallel()

	_, err := parseRunArgs([]string{"--agent", "my-codex", "--prompt", "build feature", "--prompt-file", "./prompt.txt"})
	if err == nil || !strings.Contains(err.Error(), "provide only one of --prompt or --prompt-file") {
		t.Fatalf("parseRunArgs() error = %v, want mutual exclusion guidance", err)
	}
}

func TestReadPromptFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "prompt.txt")
	want := "line one\nline two\n"
	if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	got, err := readPromptFile(path)
	if err != nil {
		t.Fatalf("readPromptFile() error = %v", err)
	}
	if got != want {
		t.Fatalf("prompt = %q, want %q", got, want)
	}
}

func TestReadPromptFileRejectsEmptyContent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "prompt.txt")
	if err := os.WriteFile(path, []byte(" \n\t"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	_, err := readPromptFile(path)
	if err == nil || !strings.Contains(err.Error(), "is empty") {
		t.Fatalf("readPromptFile() error = %v, want empty prompt file error", err)
	}
}

func TestLatestQueueRequestForDirPrefersCurrentWorkingDir(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	item, ok := latestQueueRequestForDir([]queue.Request{
		{ID: "job_old_same", WorkingDir: "/tmp/repo", CreatedAt: now.Add(-2 * time.Minute)},
		{ID: "job_new_other", WorkingDir: "/tmp/other", CreatedAt: now},
		{ID: "job_new_same", WorkingDir: "/tmp/repo", CreatedAt: now.Add(-time.Minute)},
	}, "/tmp/repo")
	if !ok {
		t.Fatal("latestQueueRequestForDir() = false, want true")
	}
	if item.ID != "job_new_same" {
		t.Fatalf("job id = %q, want %q", item.ID, "job_new_same")
	}
}

func TestLatestQueueRequestForDirFallsBackToLatestOverall(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	item, ok := latestQueueRequestForDir([]queue.Request{
		{ID: "job_old", WorkingDir: "/tmp/a", CreatedAt: now.Add(-2 * time.Minute)},
		{ID: "job_new", WorkingDir: "/tmp/b", CreatedAt: now},
	}, "/tmp/repo")
	if !ok {
		t.Fatal("latestQueueRequestForDir() = false, want true")
	}
	if item.ID != "job_new" {
		t.Fatalf("job id = %q, want %q", item.ID, "job_new")
	}
}

func TestEnsureDaemonAvailableStartsWhenNeeded(t *testing.T) {
	t.Parallel()

	available := false
	started := false
	err := ensureDaemonAvailable(
		func() bool { return available },
		func() bool { return false },
		func() error { return nil },
		func() error {
			started = true
			available = true
			return nil
		},
		50*time.Millisecond,
		time.Millisecond,
	)
	if err != nil {
		t.Fatalf("ensureDaemonAvailable() error = %v", err)
	}
	if !started {
		t.Fatal("start = false, want true")
	}
}

func TestEnsureDaemonAvailableTimesOut(t *testing.T) {
	t.Parallel()

	err := ensureDaemonAvailable(
		func() bool { return false },
		func() bool { return false },
		func() error { return nil },
		func() error { return nil },
		5*time.Millisecond,
		time.Millisecond,
	)
	if err == nil || !strings.Contains(err.Error(), "tagitd did not become ready") {
		t.Fatalf("ensureDaemonAvailable() error = %v, want readiness timeout", err)
	}
}

func TestEnsureDaemonAvailableRestartsStaleDaemon(t *testing.T) {
	t.Parallel()

	available := false
	stopped := false
	started := false
	err := ensureDaemonAvailable(
		func() bool { return available },
		func() bool { return true },
		func() error {
			stopped = true
			return nil
		},
		func() error {
			started = true
			available = true
			return nil
		},
		50*time.Millisecond,
		time.Millisecond,
	)
	if err != nil {
		t.Fatalf("ensureDaemonAvailable() error = %v", err)
	}
	if !stopped {
		t.Fatal("stop = false, want true")
	}
	if !started {
		t.Fatal("start = false, want true")
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
	home := filepath.Join(t.TempDir(), ".tagit-home")
	t.Setenv("TAGIT_HOME", home)
	roots := candidateQueueRoots("/tmp/project")
	if len(roots) != 1 {
		t.Fatalf("root count = %d, want 1", len(roots))
	}
	if roots[0] != filepath.Clean(home) {
		t.Fatalf("roots[0] = %q, want TAGIT_HOME", roots[0])
	}
}

func TestPrintResultShowPending(t *testing.T) {
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

func TestPrintResultShowIncludesRageReviews(t *testing.T) {
	out := captureStdout(t, func() {
		if err := printResultShow(api.ResultShowResponse{
			Session: history.SessionRecord{
				ID:     "sess_done",
				Status: "succeeded",
			},
			Artifact: domain.ArtifactEnvelope{
				ID:   "art_final",
				Kind: domain.ArtifactKindFinalAnswer,
				Payload: artifacts.FinalAnswerPayload{
					OutcomeType: "completed",
					Summary:     "done",
				},
			},
			RageReviews: []api.RageReviewSummary{{
				Round:    2,
				Progress: "implemented API",
				Missing:  "tests",
				Next:     "run go test",
				Files:    "changed api.go",
				Verify:   "not run",
				PlanOnly: "no",
				Blockers: "unresolved",
			}},
		}); err != nil {
			t.Fatalf("printResultShow() error = %v", err)
		}
	})
	for _, want := range []string{
		"rage_reviews:",
		"round=2 progress=implemented API",
		"missing=tests",
		"next=run go test",
		"files=changed api.go",
		"verify=not run",
		"plan_only=no",
		"blockers=unresolved",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output = %q, missing %q", out, want)
		}
	}
}

func TestRunStatusUsesControlPlaneStateOnly(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".tagit-home")
	repo := t.TempDir()
	t.Setenv("TAGIT_HOME", home)

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatalf("Chdir(repo) error = %v", err)
	}
	defer func() {
		_ = os.Chdir(oldWd)
	}()

	homeQueue := queue.NewStore(home)
	if err := homeQueue.Enqueue(context.Background(), queue.Request{
		ID:           "job_home",
		Prompt:       "home",
		StarterAgent: "starter",
		WorkingDir:   repo,
	}); err != nil {
		t.Fatalf("Enqueue(home) error = %v", err)
	}
	repoQueue := queue.NewStore(repo)
	if err := repoQueue.Enqueue(context.Background(), queue.Request{
		ID:           "job_repo",
		Prompt:       "repo",
		StarterAgent: "starter",
		WorkingDir:   repo,
	}); err != nil {
		t.Fatalf("Enqueue(repo) error = %v", err)
	}

	sessionStore := history.NewStore(home)
	now := time.Now().UTC()
	if err := sessionStore.Save(context.Background(), history.SessionRecord{
		ID:         "sess_status",
		TaskID:     "task_status",
		Prompt:     "status",
		Starter:    "starter",
		WorkingDir: repo,
		Status:     "running",
		CreatedAt:  now,
		UpdatedAt:  now,
	}); err != nil {
		t.Fatalf("Save(session) error = %v", err)
	}
	leaseStore, err := scheduler.NewLeaseStore(home)
	if err != nil {
		t.Fatalf("NewLeaseStore() error = %v", err)
	}
	if err := leaseStore.Acquire(context.Background(), "sess_status", "owner_1"); err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	artifactStore, err := artifacts.NewSQLiteStore(home)
	if err != nil {
		t.Fatalf("NewSQLiteStore(artifacts) error = %v", err)
	}
	if err := artifactStore.Save(context.Background(), domain.ArtifactEnvelope{
		ID:            "art_rage_review",
		Kind:          domain.ArtifactKindRageReview,
		SchemaVersion: "v1",
		Producer:      domain.Producer{AgentID: "starter", Role: domain.ProducerRoleReviewer, RunID: "run_rage_review"},
		SessionID:     "sess_status",
		TaskID:        "task_status",
		CreatedAt:     now,
		PayloadSchema: artifacts.RageReviewPayloadSchema,
		Payload: artifacts.RageReviewPayload{
			ReviewID:       "rage_review_run_rage_review",
			Round:          1,
			Progress:       "started",
			ForemanAgentID: "starter",
		},
	}); err != nil {
		t.Fatalf("Save(rage review) error = %v", err)
	}
	manager := workspacepkg.NewManager(repo, nil)
	prepared, err := manager.Prepare(context.Background(), "sess_status", "task_status", repo, domain.TaskStrategyDirect)
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if err := manager.Release(context.Background(), prepared, "succeeded"); err != nil {
		t.Fatalf("Release() error = %v", err)
	}

	out := captureStdout(t, func() {
		if err := runStatus(context.Background()); err != nil {
			t.Fatalf("runStatus() error = %v", err)
		}
	})
	for _, want := range []string{
		"mode=control-plane-local",
		"queue_items=1",
		"sessions=1",
		"rage_reviews=1",
		"released_workspaces=1",
		"sqlite_path=" + filepath.Clean(filepath.Join(home, ".tagit", "tagit.db")),
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("runStatus() missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, filepath.Clean(filepath.Join(repo, ".tagit", "tagit.db"))) {
		t.Fatalf("runStatus() should not report repo-local sqlite path:\n%s", out)
	}
}

func TestRunWithNoArgsShowsHelp(t *testing.T) {
	out := captureStdout(t, func() {
		if err := run(context.Background(), nil); err != nil {
			t.Fatalf("run(nil) error = %v", err)
		}
	})
	for _, want := range []string{
		"tagit usage:",
		"  tagit --help",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("run(nil) output missing %q:\n%s", want, out)
		}
	}
}

func TestPrintUsageIncludesActualCommands(t *testing.T) {
	out := captureStdout(t, printUsage)
	for _, want := range []string{
		"  tagit --help",
		"  tagit check [job_id] [--raw]",
		`  tagit run (--prompt "<prompt>" | --prompt-file <path>) [--mode <collab|senate|rage>] [--agent <id>] [--with <id,...>] [--cwd <dir>] [--continuous] [--max-rounds <n>] [-d] [-f] [--verbose] [--policy-override] [--override-actor <id>]`,
		"  tagit <command> --help",
		"  tagit result show <session_id>",
		"  tagit acp status",
		"  artifact    inspect stored artifacts",
		"  debug       inspect sessions, tasks, artifacts, events, plans, and workspaces",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("printUsage() output missing %q:\n%s", want, out)
		}
	}
	for _, unwanted := range []string{
		`tagit run --agent <agent_id> --with <delegates> "prompt"`,
		`  tagit submit [--agent <id>] [--with <id,...>] [--cwd <dir>] [--continuous] [--max-rounds <n>] [--policy-override] [--override-actor <id>] "<prompt>"`,
		`tagit submit --agent <agent_id> --with <delegates> "prompt"`,
		"  tagit help <topic>",
		"  tagit help queue",
		"  tagit help agent",
		"debug                    show debugging commands",
		"debug      low-level inspection of sessions, tasks, artifacts, etc.",
	} {
		if strings.Contains(out, unwanted) {
			t.Fatalf("printUsage() output still contains stale help text %q:\n%s", unwanted, out)
		}
	}
}

func TestPrintTopicUsageRunIncludesActualFlags(t *testing.T) {
	out := captureStdout(t, func() { printTopicUsage("run") })
	for _, want := range []string{
		"tagit run usage:",
		"--prompt <text>",
		"--prompt-file <path>",
		"--mode <name>",
		"collab, senate, or rage",
		"default mode selection:",
		"one agent -> rage",
		"multiple agents -> senate",
		"--with <id,...>",
		"-d, --detach",
		"-f, --follow",
		"--verbose",
		"--policy-override",
		"--override-actor <name>",
		"rage mode:",
		"Defaults to 10000 rounds unless --max-rounds is set.",
		"collab mode:",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("printTopicUsage(run) missing %q:\n%s", want, out)
		}
	}
}

func TestPrintTopicUsageCheck(t *testing.T) {
	out := captureStdout(t, func() { printTopicUsage("check") })
	for _, want := range []string{
		"tagit check usage:",
		"tagit check [job_id] [--raw]",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("printTopicUsage(check) missing %q:\n%s", want, out)
		}
	}
}

func TestPrintTopicUsageDebugShowsSubcommands(t *testing.T) {
	out := captureStdout(t, func() { printTopicUsage("debug") })
	for _, want := range []string{
		"tagit debug session <subcommand>",
		"tagit debug task <subcommand>",
		"tagit debug artifact <subcommand>",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("printTopicUsage(debug) missing %q:\n%s", want, out)
		}
	}
}

func TestRunCommandsSupportDashHelp(t *testing.T) {
	testCases := []struct {
		name string
		args []string
		want string
	}{
		{name: "start", args: []string{"start", "--help"}, want: "tagit start usage:"},
		{name: "stop", args: []string{"stop", "--help"}, want: "tagit stop usage:"},
		{name: "status", args: []string{"status", "--help"}, want: "tagit status usage:"},
		{name: "approve", args: []string{"approve", "--help"}, want: "tagit approve usage:"},
		{name: "reject", args: []string{"reject", "--help"}, want: "tagit reject usage:"},
		{name: "cancel", args: []string{"cancel", "--help"}, want: "tagit cancel usage:"},
		{name: "replay", args: []string{"replay", "--help"}, want: "tagit replay usage:"},
		{name: "recover", args: []string{"recover", "--help"}, want: "tagit recover usage:"},
		{name: "acp status", args: []string{"acp", "status", "--help"}, want: "tagit acp status usage:"},
		{name: "agent add", args: []string{"agent", "add", "--help"}, want: "tagit agent add usage:"},
		{name: "artifact show", args: []string{"artifact", "show", "--help"}, want: "tagit artifact show usage:"},
		{name: "event show", args: []string{"event", "show", "--help"}, want: "tagit event show usage:"},
		{name: "plan apply", args: []string{"plan", "apply", "--help"}, want: "tagit plan apply usage:"},
		{name: "queue show", args: []string{"queue", "show", "--help"}, want: "tagit queue show usage:"},
		{name: "result show", args: []string{"result", "show", "--help"}, want: "tagit result show usage:"},
		{name: "session inspect", args: []string{"session", "inspect", "--help"}, want: "tagit session inspect usage:"},
		{name: "task approve", args: []string{"task", "approve", "--help"}, want: "tagit task approve usage:"},
		{name: "workspace merge", args: []string{"workspace", "merge", "--help"}, want: "tagit workspace merge usage:"},
		{name: "graph run", args: []string{"graph", "run", "--help"}, want: "tagit graph run usage:"},
		{name: "policy check", args: []string{"policy", "check", "--help"}, want: "tagit policy check usage:"},
		{name: "check", args: []string{"check", "--help"}, want: "tagit check usage:"},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			out := captureStdout(t, func() {
				if err := run(context.Background(), tc.args); err != nil {
					t.Fatalf("run(%v) error = %v", tc.args, err)
				}
			})
			if !strings.Contains(out, tc.want) {
				t.Fatalf("run(%v) output missing %q:\n%s", tc.args, tc.want, out)
			}
		})
	}
}

func TestRunHelpCommandRemoved(t *testing.T) {
	err := run(context.Background(), []string{"help"})
	if err == nil {
		t.Fatal("run(help) error = nil, want removal error")
	}
	for _, want := range []string{
		`"tagit help" has been removed`,
		`tagit --help`,
		`tagit <command> --help`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("run(help) error = %q, want substring %q", err, want)
		}
	}
}

func TestRunSubcommandHelpCommandRemoved(t *testing.T) {
	testCases := []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "agent help",
			args: []string{"agent", "help"},
			want: []string{`"tagit agent help" has been removed`, `tagit agent --help`},
		},
		{
			name: "agent add help",
			args: []string{"agent", "add", "help"},
			want: []string{`"tagit agent add help" has been removed`, `tagit agent add --help`},
		},
		{
			name: "queue help",
			args: []string{"queue", "help"},
			want: []string{`"tagit queue help" has been removed`, `tagit queue --help`},
		},
		{
			name: "debug help",
			args: []string{"debug", "help"},
			want: []string{`"tagit debug help" has been removed`, `tagit debug --help`, `tagit debug <topic> --help`},
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			err := run(context.Background(), tc.args)
			if err == nil {
				t.Fatalf("run(%v) error = nil, want removal error", tc.args)
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("run(%v) error = %q, want substring %q", tc.args, err, want)
				}
			}
		})
	}
}

func TestRunSubmitRemoved(t *testing.T) {
	err := run(context.Background(), []string{"submit", "do something"})
	if err == nil {
		t.Fatal("run(submit) error = nil, want removal error")
	}
	if !strings.Contains(err.Error(), `use "tagit run" instead`) {
		t.Fatalf("run(submit) error = %q, want migration hint", err)
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
	home := filepath.Join(t.TempDir(), ".tagit-home")
	t.Setenv("TAGIT_HOME", home)
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
	home := filepath.Join(t.TempDir(), ".tagit-home")
	t.Setenv("TAGIT_HOME", home)
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

func TestParseMCPOptionsDefaultsToTheCurrentDirectory(t *testing.T) {
	wd := t.TempDir()

	opts, err := parseMCPOptions(nil, wd)
	if err != nil {
		t.Fatalf("parseMCPOptions() error = %v", err)
	}
	if opts.workingDir != wd {
		t.Errorf("workingDir = %q, want %q", opts.workingDir, wd)
	}
	if opts.readOnly {
		t.Error("readOnly must default to false")
	}
	if !opts.withMemory {
		t.Error("memory must be enabled by default")
	}
}

func TestParseMCPOptionsHonoursFlags(t *testing.T) {
	wd := t.TempDir()
	repo := t.TempDir()

	opts, err := parseMCPOptions([]string{"--cwd", repo, "--read-only", "--no-memory"}, wd)
	if err != nil {
		t.Fatalf("parseMCPOptions() error = %v", err)
	}
	if opts.workingDir != repo {
		t.Errorf("workingDir = %q, want %q", opts.workingDir, repo)
	}
	if !opts.readOnly {
		t.Error("--read-only must set readOnly")
	}
	if opts.withMemory {
		t.Error("--no-memory must disable memory")
	}
}

func TestParseMCPOptionsResolvesRelativeCwd(t *testing.T) {
	wd := t.TempDir()
	if err := os.MkdirAll(filepath.Join(wd, "sub"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	opts, err := parseMCPOptions([]string{"--cwd", "sub"}, wd)
	if err != nil {
		t.Fatalf("parseMCPOptions() error = %v", err)
	}
	if want := filepath.Join(wd, "sub"); opts.workingDir != want {
		t.Errorf("workingDir = %q, want %q", opts.workingDir, want)
	}
}

func TestParseMCPOptionsTakesTheTokenFromTheEnvironment(t *testing.T) {
	// A token passed as a flag is visible in `ps` to every user on the host, so
	// the environment is the documented path and has to work on its own.
	t.Setenv("TAGIT_MCP_TOKEN", "from-env")

	opts, err := parseMCPOptions([]string{"--http", ":43821"}, t.TempDir())
	if err != nil {
		t.Fatalf("parseMCPOptions() error = %v", err)
	}
	if opts.httpAddr != ":43821" {
		t.Errorf("httpAddr = %q, want :43821", opts.httpAddr)
	}
	if opts.httpToken != "from-env" {
		t.Errorf("httpToken = %q, want the value from TAGIT_MCP_TOKEN", opts.httpToken)
	}
}

func TestParseMCPOptionsPrefersAnExplicitTokenOverTheEnvironment(t *testing.T) {
	t.Setenv("TAGIT_MCP_TOKEN", "from-env")

	opts, err := parseMCPOptions([]string{"--http", ":43821", "--token", "explicit"}, t.TempDir())
	if err != nil {
		t.Fatalf("parseMCPOptions() error = %v", err)
	}
	if opts.httpToken != "explicit" {
		t.Errorf("httpToken = %q, want the explicit flag to win", opts.httpToken)
	}
}

// A token with no --http reads like "this is protected" while stdio quietly
// ignores it. Say so instead.
func TestParseMCPOptionsRejectsATokenWithoutHTTP(t *testing.T) {
	if _, err := parseMCPOptions([]string{"--token", "secret"}, t.TempDir()); err == nil {
		t.Fatal("parseMCPOptions() error = nil, want an error for --token without --http")
	}
}

func TestParseMCPOptionsRequiresAValueForHTTP(t *testing.T) {
	t.Setenv("TAGIT_MCP_TOKEN", "")
	if _, err := parseMCPOptions([]string{"--http"}, t.TempDir()); err == nil {
		t.Fatal("parseMCPOptions() error = nil, want an error for a missing --http value")
	}
}

// stdio stays the default: nothing about the new flags changes the local path.
func TestParseMCPOptionsDefaultsToStdio(t *testing.T) {
	t.Setenv("TAGIT_MCP_TOKEN", "")
	opts, err := parseMCPOptions(nil, t.TempDir())
	if err != nil {
		t.Fatalf("parseMCPOptions() error = %v", err)
	}
	if opts.httpAddr != "" || opts.httpToken != "" {
		t.Errorf("want stdio by default, got addr=%q token set=%v", opts.httpAddr, opts.httpToken != "")
	}
}

func TestParseMCPOptionsRejectsUnknownFlags(t *testing.T) {
	if _, err := parseMCPOptions([]string{"--nope"}, t.TempDir()); err == nil {
		t.Fatal("parseMCPOptions() error = nil, want an error for an unknown flag")
	}
}

func TestParseMCPOptionsRequiresAValueForCwd(t *testing.T) {
	if _, err := parseMCPOptions([]string{"--cwd"}, t.TempDir()); err == nil {
		t.Fatal("parseMCPOptions() error = nil, want an error for a missing --cwd value")
	}
}
