package run

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liliang-cn/tagit/internal/agents"
	"github.com/liliang-cn/tagit/internal/artifacts"
	"github.com/liliang-cn/tagit/internal/domain"
	"github.com/liliang-cn/tagit/internal/history"
	"github.com/liliang-cn/tagit/internal/memory"
	"github.com/liliang-cn/tagit/internal/runtime"
	"github.com/liliang-cn/tagit/internal/scheduler"
	"github.com/liliang-cn/tagit/internal/taskstore"
	workspacepkg "github.com/liliang-cn/tagit/internal/workspace"
)

func TestRunRejectsUnknownAgent(t *testing.T) {
	registry, err := agents.DefaultRegistry()
	if err != nil {
		t.Fatalf("DefaultRegistry() error = %v", err)
	}

	svc := NewService(registry)
	err = svc.Run(context.Background(), Request{
		Prompt:       "test",
		StarterAgent: "missing",
		WorkingDir:   ".",
	})
	if err == nil {
		t.Fatal("Run() error = nil, want error")
	}
}

func TestRunRejectsUnknownDelegate(t *testing.T) {
	registry, err := agents.NewRegistry(domain.AgentProfile{
		ID:           "starter",
		DisplayName:  "Starter",
		Command:      "starter",
		Aliases:      []string{"codex"},
		Availability: domain.AgentAvailabilityAvailable,
	})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	svc := NewService(registry)
	svc.supervisor = runtime.NewSupervisor()
	err = svc.Run(context.Background(), Request{
		Prompt:       "test",
		StarterAgent: "codex",
		WorkingDir:   ".",
		Delegates:    []string{"missing"},
	})
	if err == nil {
		t.Fatal("Run() error = nil, want error")
	}
}

func TestRunWithResultRejectsRageDelegates(t *testing.T) {
	t.Parallel()

	registry, err := agents.NewRegistry(domain.AgentProfile{
		ID:           "starter",
		DisplayName:  "Starter",
		Command:      "sh",
		Aliases:      []string{"codex"},
		Availability: domain.AgentAvailabilityAvailable,
	})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	svc := NewService(registry)
	_, err = svc.RunWithResult(context.Background(), Request{
		Prompt:       "keep going until complete",
		Mode:         RunModeRage,
		StarterAgent: "codex",
		WorkingDir:   ".",
		Delegates:    []string{"worker"},
	})
	if err == nil || !strings.Contains(err.Error(), "rage mode only supports a single agent") {
		t.Fatalf("RunWithResult() error = %v, want rage delegate rejection", err)
	}
}

func TestNormalizeRunRequestRageDefaultsToHighRoundLimit(t *testing.T) {
	t.Parallel()

	mode, req, err := normalizeRunRequest(Request{Mode: RunModeRage})
	if err != nil {
		t.Fatalf("normalizeRunRequest() error = %v", err)
	}
	if mode != RunModeRage {
		t.Fatalf("mode = %q, want %q", mode, RunModeRage)
	}
	if !req.Continuous {
		t.Fatal("Continuous = false, want true")
	}
	if req.MaxRounds != rageDefaultMaxRounds {
		t.Fatalf("MaxRounds = %d, want %d", req.MaxRounds, rageDefaultMaxRounds)
	}
}

func TestNormalizeRunRequestDefaultsSingleAgentToRage(t *testing.T) {
	t.Parallel()

	mode, req, err := normalizeRunRequest(Request{})
	if err != nil {
		t.Fatalf("normalizeRunRequest() error = %v", err)
	}
	if mode != RunModeRage || req.Mode != RunModeRage {
		t.Fatalf("mode = %q req.Mode = %q, want rage", mode, req.Mode)
	}
	if !req.Continuous {
		t.Fatal("Continuous = false, want true")
	}
	if req.MaxRounds != rageDefaultMaxRounds {
		t.Fatalf("MaxRounds = %d, want %d", req.MaxRounds, rageDefaultMaxRounds)
	}
}

func TestNormalizeRunRequestDefaultsMultiAgentToSenate(t *testing.T) {
	t.Parallel()

	mode, req, err := normalizeRunRequest(Request{Delegates: []string{"gemini", "claude"}})
	if err != nil {
		t.Fatalf("normalizeRunRequest() error = %v", err)
	}
	if mode != RunModeSenate || req.Mode != RunModeSenate {
		t.Fatalf("mode = %q req.Mode = %q, want senate", mode, req.Mode)
	}
	if req.Continuous {
		t.Fatal("Continuous = true, want false")
	}
}

func TestBuildRageForemanPromptDemandsExecutionProof(t *testing.T) {
	t.Parallel()

	prompt := buildRageForemanPrompt("ship the feature", "worker says progress", 3)
	for _, want := range []string{
		"`Files:` say whether the worker actually changed files this round",
		"`Verify:` say whether the worker actually ran validation or tests",
		"`PlanOnly:` say `yes` if the worker is still stuck in planning/talking",
		"`Blockers:` say whether the claimed blocker was actually removed",
		"If there is no real verification yet, demand concrete verification next.",
		"You are not done. Do not stop early.",
		"You are wasting compute, GPU time, and electricity if you stop at planning.",
		"No completion claim is valid without concrete file changes and verification.",
		"Resume execution now and remove the blocker for real.",
		"You, the foreman, are the final authority on whether the task is done.",
		"write exactly `Next: DONE`",
		"Review round: 3",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("buildRageForemanPrompt() missing %q:\n%s", want, prompt)
		}
	}
}

func TestForemanDeterminesDone(t *testing.T) {
	t.Parallel()

	done := domain.ArtifactEnvelope{
		Kind: domain.ArtifactKindRageReview,
		Payload: artifacts.RageReviewPayload{
			Progress: "all required work is complete and verified",
			Next:     "",
			Files:    "changed and verified",
			Verify:   "tests passed",
			PlanOnly: "no",
			Blockers: "resolved",
		},
	}
	if !foremanDeterminesDone(done) {
		t.Fatal("foremanDeterminesDone(done) = false, want true")
	}

	notDone := domain.ArtifactEnvelope{
		Kind: domain.ArtifactKindRageReview,
		Payload: artifacts.RageReviewPayload{
			Progress: "worker claims done",
			Next:     "",
			Files:    "changed and verified",
			Verify:   "not run",
			PlanOnly: "no",
			Blockers: "resolved",
		},
	}
	if foremanDeterminesDone(notDone) {
		t.Fatal("foremanDeterminesDone(notDone) = true, want false")
	}
}

// The run used to stop only on a literally empty `Next:` — i.e. it asked a model
// to signal completion by writing nothing. A finished one-line task came back
// six rounds running with `Next: (empty — task complete and verified)`: the
// foreman had confirmed the work every single round, and the loop kept pushing
// the worker because the field was not blank.
func TestForemanDoneWhenNextDescribesAnEmptyField(t *testing.T) {
	t.Parallel()

	for _, next := range []string{
		"",
		"DONE",
		"done",
		"None",
		"nothing",
		"N/A",
		"-",
		"(empty — task complete and verified)", // observed verbatim, 2026-08-22
		"(empty — task complete)",
		"(none)",
	} {
		review := domain.ArtifactEnvelope{
			Kind: domain.ArtifactKindRageReview,
			Payload: artifacts.RageReviewPayload{
				Progress: "verified: the line is in README.md and git diff confirms it",
				Next:     next,
				Files:    "confirmed",
				Verify:   "confirmed",
				PlanOnly: "no",
				Blockers: "none",
			},
		}
		if !foremanDeterminesDone(review) {
			t.Errorf("Next: %q should end the run", next)
		}
	}
}

// Anything that actually names work has to keep the loop running — including
// the words that only look like the empty markers.
func TestForemanNotDoneWhenNextNamesWork(t *testing.T) {
	t.Parallel()

	for _, next := range []string{
		"Add a test for the append",
		"none of the tests pass — fix them",
		"nothing is verified yet, run the suite",
		"(continue with the remaining files)",
		"Done? No — re-run the linter first",
	} {
		review := domain.ArtifactEnvelope{
			Kind: domain.ArtifactKindRageReview,
			Payload: artifacts.RageReviewPayload{
				Progress: "partial",
				Next:     next,
				Files:    "confirmed",
				Verify:   "confirmed",
				PlanOnly: "no",
				Blockers: "none",
			},
		}
		if foremanDeterminesDone(review) {
			t.Errorf("Next: %q should keep the run going", next)
		}
	}
}

// The completion channel has to be affirmative, so the prompt has to ask for it.
func TestForemanPromptAsksForAnAffirmativeDoneToken(t *testing.T) {
	t.Parallel()

	prompt := buildRageForemanPrompt("append a line", "worker said TAGIT_DONE", 1)
	if !strings.Contains(prompt, "`Next: DONE`") {
		t.Error("the foreman is never told how to signal completion")
	}
	if strings.Contains(prompt, "Only leave `Next:` empty") {
		t.Error("the prompt still asks for completion to be signalled by an absence")
	}
}

func TestRunWithResultRageContinuesUntilDone(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	controlDir := t.TempDir()
	initRunGitRepo(t, workDir)

	script := strings.Join([]string{
		`prompt="$1"`,
		`if printf '%s' "$prompt" | grep -q "You are TagIt's semantic runtime classifier."; then`,
		`  printf 'intent: test rage review\n'`,
		`  printf 'risk: low\n'`,
		`  printf 'needs_approval: false\n'`,
		`  printf 'recommend_curia: false\n'`,
		`  printf 'summary: test semantic review\n'`,
		`elif printf '%s' "$prompt" | grep -q "TagIt rage foreman mode"; then`,
		`  if printf '%s' "$prompt" | grep -q "objective implemented for real"; then`,
		`    printf 'Progress: implementation complete and verified\n'`,
		`    printf 'Missing:\n'`,
		`    printf 'Files: changed rage.txt\n'`,
		`    printf 'Verify: tests passed\n'`,
		`    printf 'PlanOnly: no\n'`,
		`    printf 'Blockers: resolved\n'`,
		`    printf 'Next:\n'`,
		`  elif printf '%s' "$prompt" | grep -q "objective implemented too early"; then`,
		`    printf 'Progress: first pass landed\n'`,
		`    printf 'Missing: final implementation and merge markers are not complete\n'`,
		`    printf 'Files: changed rage.txt scaffold\n'`,
		`    printf 'Verify: not run\n'`,
		`    printf 'PlanOnly: no\n'`,
		`    printf 'Blockers: unresolved\n'`,
		`    printf 'Next: finish the implementation, write rage.txt, emit TAGIT_DONE, and emit TAGIT_MERGE_BACK.\n'`,
		`  else`,
		`    printf 'Progress: review fallback\n'`,
		`    printf 'Missing:\n'`,
		`    printf 'Files: changed rage.txt\n'`,
		`    printf 'Verify: tests passed\n'`,
		`    printf 'PlanOnly: no\n'`,
		`    printf 'Blockers: resolved\n'`,
		`    printf 'Next:\n'`,
		`  fi`,
		`elif printf '%s' "$prompt" | grep -q "Current round: 2"; then`,
		`  printf 'done\n' > rage.txt`,
		`  printf 'TAGIT_DONE: objective implemented for real\n'`,
		`  printf 'TAGIT_MERGE_BACK: direct_merge | rage mode complete\n'`,
		`  printf 'TAGIT_MERGE_FILE: rage.txt\n'`,
		`elif printf '%s' "$prompt" | grep -q "Current round: 1"; then`,
		`  printf 'TAGIT_DONE: objective implemented too early\n'`,
		`else`,
		`  printf 'fallback worker output\n'`,
		`fi`,
	}, "\n")

	registry, err := agents.NewRegistry(domain.AgentProfile{
		ID:           "starter",
		DisplayName:  "Starter",
		Command:      "sh",
		Args:         []string{"-c", script, "starter", "{prompt}"},
		Availability: domain.AgentAvailabilityAvailable,
	})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	svc := NewService(registry)
	svc.SetControlDir(controlDir)
	result, err := svc.RunWithResult(context.Background(), Request{
		Prompt:       "implement the objective fully",
		Mode:         RunModeRage,
		StarterAgent: "starter",
		WorkingDir:   workDir,
	})
	if err != nil {
		t.Fatalf("RunWithResult() error = %v", err)
	}
	if result.Status != "succeeded" {
		t.Fatalf("status = %s, want succeeded", result.Status)
	}
	content, err := os.ReadFile(filepath.Join(workDir, "rage.txt"))
	if err != nil {
		t.Fatalf("ReadFile(rage.txt) error = %v", err)
	}
	if strings.TrimSpace(string(content)) != "done" {
		t.Fatalf("rage.txt = %q, want done", strings.TrimSpace(string(content)))
	}

	sessionStore, err := history.NewSQLiteStore(controlDir)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	record, err := sessionStore.Get(context.Background(), result.SessionID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if record.FinalArtifactID == "" {
		t.Fatal("final artifact id = empty, want final answer artifact")
	}

	artifactStore, err := artifacts.NewSQLiteStore(controlDir)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	envelope, err := artifactStore.Get(context.Background(), record.FinalArtifactID)
	if err != nil {
		t.Fatalf("Get(final artifact) error = %v", err)
	}
	payload, ok := artifacts.FinalAnswerFromEnvelope(envelope)
	if !ok {
		t.Fatalf("FinalAnswerFromEnvelope(%T) = false, want true", envelope.Payload)
	}
	if !strings.Contains(payload.Answer, "== round 2 ==") {
		t.Fatalf("final answer missing continuous rounds:\n%s", payload.Answer)
	}
	if !strings.Contains(payload.Answer, "TAGIT_DONE: objective implemented too early") {
		t.Fatalf("final answer missing first-round worker claim:\n%s", payload.Answer)
	}
	if !strings.Contains(payload.Answer, "TAGIT_DONE: objective implemented for real") {
		t.Fatalf("final answer missing second-round completion marker:\n%s", payload.Answer)
	}
}

type fakeMemory struct{ recorded int }

func (f *fakeMemory) Recall(context.Context, memory.Scope, string, int) (memory.Recollection, error) {
	return memory.Recollection{ContextText: "MEMORY-CONTEXT-MARKER"}, nil
}
func (f *fakeMemory) Record(context.Context, memory.RunRecord) error             { f.recorded++; return nil }
func (f *fakeMemory) Note(context.Context, memory.Scope, string, []string) error { return nil }

func TestRunWithResultRageRecallsAndRecordsMemory(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	controlDir := t.TempDir()
	initRunGitRepo(t, workDir)

	promptDump := filepath.Join(controlDir, "worker_prompt.txt")

	// Single-round rage run (MaxRounds: 1). The worker dumps the prompt it
	// received in round 1 so the test can assert the injected memory context.
	// Whether the foreman approves completion or the loop hits the round cap,
	// the run finishes deterministically after one round and Record is invoked.
	script := strings.Join([]string{
		`prompt="$1"`,
		`if printf '%s' "$prompt" | grep -q "You are TagIt's semantic runtime classifier."; then`,
		`  printf 'intent: test rage review\n'`,
		`  printf 'risk: low\n'`,
		`  printf 'needs_approval: false\n'`,
		`  printf 'recommend_curia: false\n'`,
		`  printf 'summary: test semantic review\n'`,
		`elif printf '%s' "$prompt" | grep -q "TagIt rage foreman mode"; then`,
		`  printf 'Progress: implementation complete and verified\n'`,
		`  printf 'Missing:\n'`,
		`  printf 'Files: changed rage.txt\n'`,
		`  printf 'Verify: tests passed\n'`,
		`  printf 'PlanOnly: no\n'`,
		`  printf 'Blockers: resolved\n'`,
		`  printf 'Next:\n'`,
		`elif printf '%s' "$prompt" | grep -q "TagIt rage worker mode"; then`,
		`  printf '%s' "$prompt" > "` + promptDump + `"`,
		`  printf 'done\n' > rage.txt`,
		`  printf 'TAGIT_DONE: objective implemented for real\n'`,
		`  printf 'TAGIT_MERGE_BACK: direct_merge | rage mode complete\n'`,
		`  printf 'TAGIT_MERGE_FILE: rage.txt\n'`,
		`else`,
		`  printf 'fallback worker output\n'`,
		`fi`,
	}, "\n")

	registry, err := agents.NewRegistry(domain.AgentProfile{
		ID:           "starter",
		DisplayName:  "Starter",
		Command:      "sh",
		Args:         []string{"-c", script, "starter", "{prompt}"},
		Availability: domain.AgentAvailabilityAvailable,
	})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	mem := &fakeMemory{}
	svc := NewService(registry)
	svc.SetControlDir(controlDir)
	svc.Memory = mem

	// MaxRounds: 1 keeps the run deterministic (exactly one worker round) so the
	// captured prompt and the Record call are stable under -race. The run may
	// finish as succeeded (foreman approves) or as a controlled max-rounds
	// failure; either way recall/inject/record must have run.
	_, _ = svc.RunWithResult(context.Background(), Request{
		Prompt:       "implement the objective fully",
		Mode:         RunModeRage,
		StarterAgent: "starter",
		WorkingDir:   workDir,
		MaxRounds:    1,
	})

	if mem.recorded == 0 {
		t.Fatal("memory Record was not called, want recorded > 0")
	}

	sentPrompt, err := os.ReadFile(promptDump)
	if err != nil {
		t.Fatalf("ReadFile(worker prompt) error = %v", err)
	}
	if !strings.Contains(string(sentPrompt), "MEMORY-CONTEXT-MARKER") {
		t.Fatalf("worker prompt missing injected memory context:\n%s", string(sentPrompt))
	}
	if !strings.Contains(string(sentPrompt), "Current round: 1") {
		t.Fatalf("captured prompt is not the first-round worker prompt:\n%s", string(sentPrompt))
	}
}

func TestRunCollabRecallsAndRecordsMemory(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	controlDir := t.TempDir()
	initRunGitRepo(t, workDir)

	delegatePromptDump := filepath.Join(controlDir, "collab_delegate_prompt.txt")

	starterScript := strings.Join([]string{
		`prompt="$1"`,
		`if printf '%s' "$prompt" | grep -q "Starter prompt clarification"; then`,
		`  printf 'clarified spec\n'`,
		`elif printf '%s' "$prompt" | grep -q "Starter Caesar coordination"; then`,
		`  printf 'bootstrap ready\n'`,
		`elif printf '%s' "$prompt" | grep -q "Caesar review round"; then`,
		`  printf 'TAGIT_DONE: delegated work is complete\n'`,
		`else`,
		`  printf 'starter should only clarify, bootstrap, and review\n' >&2`,
		`  exit 9`,
		`fi`,
	}, "\n")
	workerScript := strings.Join([]string{
		`prompt="$1"`,
		`printf '%s' "$prompt" > "` + delegatePromptDump + `"`,
		`printf 'delegated work\n' > delegated.txt`,
		`printf 'delegated work complete\nTAGIT_MERGE_BACK: direct_merge | delegated work ready\nTAGIT_MERGE_FILE: delegated.txt\n'`,
	}, "\n")

	registry, err := agents.NewRegistry(
		domain.AgentProfile{
			ID:           "caesar",
			DisplayName:  "Caesar",
			Command:      "sh",
			Args:         []string{"-c", starterScript, "starter", "{prompt}"},
			Availability: domain.AgentAvailabilityAvailable,
		},
		domain.AgentProfile{
			ID:           "worker",
			DisplayName:  "Worker",
			Command:      "sh",
			Args:         []string{"-c", workerScript, "worker", "{prompt}"},
			Availability: domain.AgentAvailabilityAvailable,
		},
	)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	mem := &fakeMemory{}
	svc := NewService(registry)
	svc.SetControlDir(controlDir)
	svc.Memory = mem

	result, err := svc.RunWithResult(context.Background(), Request{
		Prompt:       "coordinate a low-risk sample file update",
		Mode:         RunModeCollab,
		StarterAgent: "caesar",
		Delegates:    []string{"worker"},
		WorkingDir:   workDir,
	})
	if err != nil {
		t.Fatalf("RunWithResult() error = %v", err)
	}
	if result.Status != "succeeded" {
		t.Fatalf("status = %s, want succeeded", result.Status)
	}
	if mem.recorded == 0 {
		t.Fatal("memory Record was not called, want recorded > 0")
	}

	sentPrompt, err := os.ReadFile(delegatePromptDump)
	if err != nil {
		t.Fatalf("ReadFile(delegate prompt) error = %v", err)
	}
	if !strings.Contains(string(sentPrompt), "MEMORY-CONTEXT-MARKER") {
		t.Fatalf("delegate prompt missing injected memory context:\n%s", string(sentPrompt))
	}
}

func TestRunSenateRecallsAndRecordsMemory(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	controlDir := t.TempDir()
	initRunGitRepo(t, workDir)

	planPromptDump := filepath.Join(controlDir, "senate_plan_prompt.txt")

	starterScript := strings.Join([]string{
		`prompt="$1"`,
		`if printf '%s' "$prompt" | grep -q "Senate plan proposal"; then`,
		`  printf '%s' "$prompt" > "` + planPromptDump + `"`,
		`  printf 'STARTER PLAN\n'`,
		`elif printf '%s' "$prompt" | grep -q "Senate plan vote"; then`,
		`  printf 'starter abstains\n'`,
		`elif printf '%s' "$prompt" | grep -q "Senate plan tiebreak"; then`,
		`  target=$(printf '%s\n' "$prompt" | sed -n 's/^- \(.*_plan_3\)$/\1/p' | head -n1)`,
		`  printf 'TAGIT_PICK: %s | choose delegate two plan\n' "$target"`,
		`elif printf '%s' "$prompt" | grep -q "Senate implementation vote"; then`,
		`  printf 'starter abstains\n'`,
		`elif printf '%s' "$prompt" | grep -q "Senate implementation tiebreak"; then`,
		`  target=$(printf '%s\n' "$prompt" | sed -n 's/^- \(.*_implementation_1\)$/\1/p' | head -n1)`,
		`  printf 'TAGIT_PICK: %s | choose delegate one implementation\n' "$target"`,
		`else`,
		`  printf 'unexpected starter prompt\n' >&2`,
		`  exit 9`,
		`fi`,
	}, "\n")
	workerOneScript := strings.Join([]string{
		`prompt="$1"`,
		`if printf '%s' "$prompt" | grep -q "Senate plan proposal"; then`,
		`  printf 'PLAN ONE\n'`,
		`elif printf '%s' "$prompt" | grep -q "Senate plan vote"; then`,
		`  target=$(printf '%s\n' "$prompt" | sed -n 's/^- \(.*_plan_2\)$/\1/p' | head -n1)`,
		`  printf 'TAGIT_PICK: %s | vote for plan one\n' "$target"`,
		`elif printf '%s' "$prompt" | grep -q "Senate implementation candidate"; then`,
		`  printf 'delegate one\n' > winner.txt`,
		`  printf 'TAGIT_MERGE_BACK: require_vote | candidate ready\nTAGIT_MERGE_FILE: winner.txt\n'`,
		`elif printf '%s' "$prompt" | grep -q "Senate implementation vote"; then`,
		`  target=$(printf '%s\n' "$prompt" | sed -n 's/^- \(.*_implementation_1\)$/\1/p' | head -n1)`,
		`  printf 'TAGIT_PICK: %s | vote for implementation one\n' "$target"`,
		`else`,
		`  printf 'unexpected worker one prompt\n' >&2`,
		`  exit 11`,
		`fi`,
	}, "\n")
	workerTwoScript := strings.Join([]string{
		`prompt="$1"`,
		`if printf '%s' "$prompt" | grep -q "Senate plan proposal"; then`,
		`  printf 'PLAN TWO\n'`,
		`elif printf '%s' "$prompt" | grep -q "Senate plan vote"; then`,
		`  target=$(printf '%s\n' "$prompt" | sed -n 's/^- \(.*_plan_3\)$/\1/p' | head -n1)`,
		`  printf 'TAGIT_PICK: %s | vote for plan two\n' "$target"`,
		`elif printf '%s' "$prompt" | grep -q "Senate implementation candidate"; then`,
		`  printf 'delegate two\n' > loser.txt`,
		`  printf 'TAGIT_MERGE_BACK: require_vote | candidate ready\nTAGIT_MERGE_FILE: loser.txt\n'`,
		`elif printf '%s' "$prompt" | grep -q "Senate implementation vote"; then`,
		`  target=$(printf '%s\n' "$prompt" | sed -n 's/^- \(.*_implementation_2\)$/\1/p' | head -n1)`,
		`  printf 'TAGIT_PICK: %s | vote for implementation two\n' "$target"`,
		`else`,
		`  printf 'unexpected worker two prompt\n' >&2`,
		`  exit 13`,
		`fi`,
	}, "\n")

	registry, err := agents.NewRegistry(
		domain.AgentProfile{
			ID:           "starter",
			DisplayName:  "Starter",
			Command:      "sh",
			Args:         []string{"-c", starterScript, "starter", "{prompt}"},
			Availability: domain.AgentAvailabilityAvailable,
		},
		domain.AgentProfile{
			ID:           "worker-one",
			DisplayName:  "Worker One",
			Command:      "sh",
			Args:         []string{"-c", workerOneScript, "worker-one", "{prompt}"},
			Availability: domain.AgentAvailabilityAvailable,
		},
		domain.AgentProfile{
			ID:           "worker-two",
			DisplayName:  "Worker Two",
			Command:      "sh",
			Args:         []string{"-c", workerTwoScript, "worker-two", "{prompt}"},
			Availability: domain.AgentAvailabilityAvailable,
		},
	)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	mem := &fakeMemory{}
	svc := NewService(registry)
	svc.SetControlDir(controlDir)
	svc.Memory = mem

	result, err := svc.RunWithResult(context.Background(), Request{
		Prompt:       "implement a winner-takes-all senate flow",
		Mode:         RunModeSenate,
		StarterAgent: "starter",
		Delegates:    []string{"worker-one", "worker-two"},
		WorkingDir:   workDir,
	})
	if err != nil {
		t.Fatalf("RunWithResult() error = %v", err)
	}
	if result.Status != "succeeded" {
		t.Fatalf("status = %s, want succeeded", result.Status)
	}
	if mem.recorded == 0 {
		t.Fatal("memory Record was not called, want recorded > 0")
	}

	sentPrompt, err := os.ReadFile(planPromptDump)
	if err != nil {
		t.Fatalf("ReadFile(plan prompt) error = %v", err)
	}
	if !strings.Contains(string(sentPrompt), "MEMORY-CONTEXT-MARKER") {
		t.Fatalf("senate plan prompt missing injected memory context:\n%s", string(sentPrompt))
	}
}

func TestWriteRelayResult(t *testing.T) {
	var buf strings.Builder
	writeRelayResult(&buf, []scheduler.NodeAssignment{
		{
			Node: domain.TaskNodeSpec{ID: "task_1"},
			Profile: domain.AgentProfile{
				ID:          "codex-cli",
				DisplayName: "Codex CLI",
			},
		},
	}, scheduler.DispatchResult{
		Order: []string{"task_1"},
		Artifacts: map[string]domain.ArtifactEnvelope{
			"task_1": {
				ID: "art_1",
				Payload: artifacts.ReportPayload{
					Summary: "starter output",
				},
				Checksum: "sha256:test",
			},
		},
	})
	if !strings.Contains(buf.String(), "starter output") {
		t.Fatal("writeRelayResult() missing output")
	}
	if !strings.Contains(buf.String(), "artifact=art_1") {
		t.Fatal("writeRelayResult() missing artifact line")
	}
}

func TestRunReturnsAwaitingApprovalOnPolicyWarn(t *testing.T) {
	registry, err := agents.NewRegistry(domain.AgentProfile{
		ID:           "codex-cli",
		DisplayName:  "Codex CLI",
		Command:      "sh",
		Aliases:      []string{"codex"},
		Availability: domain.AgentAvailabilityAvailable,
	})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	workDir := t.TempDir()
	svc := NewService(registry)
	result, err := svc.RunWithResult(context.Background(), Request{
		Prompt:       "drop database and then summarize the risk",
		Mode:         RunModeCollab,
		StarterAgent: "codex",
		WorkingDir:   workDir,
	})
	if err != nil {
		t.Fatalf("RunWithResult() error = %v", err)
	}
	if result.Status != "awaiting_approval" {
		t.Fatalf("status = %s, want awaiting_approval", result.Status)
	}

	sessionStore, err := history.NewSQLiteStore(workDir)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	record, err := sessionStore.Get(context.Background(), result.SessionID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if record.Status != "awaiting_approval" {
		t.Fatalf("record status = %s, want awaiting_approval", record.Status)
	}
	if record.FinalArtifactID == "" {
		t.Fatal("final artifact id = empty, want final answer artifact")
	}
	taskStore, err := taskstore.NewSQLiteStore(workDir)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	tasks, err := taskStore.ListTasksBySession(context.Background(), result.SessionID)
	if err != nil {
		t.Fatalf("ListTasksBySession() error = %v", err)
	}
	if len(tasks) != 1 || tasks[0].State != domain.TaskStateAwaitingApproval {
		t.Fatalf("tasks = %#v, want one awaiting approval task", tasks)
	}
	leaseStore, err := scheduler.NewLeaseStore(workDir)
	if err != nil {
		t.Fatalf("NewLeaseStore() error = %v", err)
	}
	lease, err := leaseStore.Get(context.Background(), result.SessionID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if len(lease.PendingApprovalTaskIDs) != 1 || lease.PendingApprovalTaskIDs[0] != tasks[0].ID {
		t.Fatalf("pending approvals = %#v, want [%s]", lease.PendingApprovalTaskIDs, tasks[0].ID)
	}
	artifactStore, err := artifacts.NewSQLiteStore(workDir)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	envelope, err := artifactStore.Get(context.Background(), record.FinalArtifactID)
	if err != nil {
		t.Fatalf("Get(final artifact) error = %v", err)
	}
	if envelope.Kind != domain.ArtifactKindFinalAnswer {
		t.Fatalf("final artifact kind = %s, want %s", envelope.Kind, domain.ArtifactKindFinalAnswer)
	}
}

func TestRunWithDelegatesPropagatesExecutionFailure(t *testing.T) {
	registry, err := agents.NewRegistry(
		domain.AgentProfile{
			ID:           "claude",
			DisplayName:  "Claude",
			Command:      "sh",
			Args:         []string{"-c", "printf 'starter ok\\n'"},
			Availability: domain.AgentAvailabilityAvailable,
		},
		domain.AgentProfile{
			ID:           "codex",
			DisplayName:  "Codex",
			Command:      "sh",
			Args:         []string{"-c", "printf 'codex failed\\n' >&2; exit 7"},
			Availability: domain.AgentAvailabilityAvailable,
		},
	)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	workDir := t.TempDir()
	svc := NewService(registry)
	result, err := svc.RunWithResult(context.Background(), Request{
		Prompt:       "refactor the CLI",
		StarterAgent: "claude",
		WorkingDir:   workDir,
		Delegates:    []string{"codex"},
	})
	if err == nil {
		t.Fatal("RunWithResult() error = nil, want delegate failure")
	}
	if result.Status != "failed" {
		t.Fatalf("status = %s, want failed", result.Status)
	}

	sessionStore, err := history.NewSQLiteStore(workDir)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	record, err := sessionStore.Get(context.Background(), result.SessionID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if record.Status != "failed" {
		t.Fatalf("record status = %s, want failed", record.Status)
	}
	if record.FinalArtifactID == "" {
		t.Fatal("final artifact id = empty, want failure final answer artifact")
	}
}

func TestBuildOrchestratedAssignmentsFanOutAfterStarterClarify(t *testing.T) {
	starter := domain.AgentProfile{ID: "starter", DisplayName: "Starter"}
	delegates := []domain.AgentProfile{
		{ID: "gemini", DisplayName: "Gemini"},
		{ID: "copilot", DisplayName: "Copilot"},
	}

	assignments := buildOrchestratedAssignments("task_1", starter, delegates, true, 3, nil)
	// clarify + 2 delegates = 3
	if len(assignments) != 3 {
		t.Fatalf("assignment count = %d, want 3", len(assignments))
	}

	clarify := assignments[0]

	if clarify.Node.ID != "task_1_starter_clarify" {
		t.Fatalf("clarify node id = %q, want task_1_starter_clarify", clarify.Node.ID)
	}
	for _, assignment := range assignments[1:] {
		if got := assignment.Node.Dependencies; len(got) != 1 || got[0] != "task_1_starter_clarify" {
			t.Fatalf("delegate %s dependencies = %#v, want [task_1_starter_clarify]", assignment.Node.ID, got)
		}
		if assignment.SemanticReviewer.ID != "starter" {
			t.Fatalf("delegate %s reviewer = %q, want starter", assignment.Node.ID, assignment.SemanticReviewer.ID)
		}
	}
	for _, assignment := range assignments[1:] {
		if strings.Contains(strings.ToLower(assignment.PromptHint), "active contributor") {
			t.Fatalf("delegate prompt hint = %q, want no starter worker language", assignment.PromptHint)
		}
	}
}

func TestBuildOrchestratedAssignmentsIncludesClarifyNode(t *testing.T) {
	starter := domain.AgentProfile{ID: "starter", DisplayName: "Starter"}
	delegates := []domain.AgentProfile{
		{ID: "agent-a", DisplayName: "Agent A"},
	}

	assignments := buildOrchestratedAssignments("task_x", starter, delegates, false, 1, nil)
	if len(assignments) != 2 {
		t.Fatalf("assignment count = %d, want 2 (clarify + 1 delegate)", len(assignments))
	}

	clarify := assignments[0]
	delegate := assignments[1]

	if clarify.Node.ID != "task_x_starter_clarify" {
		t.Fatalf("clarify node id = %q, want task_x_starter_clarify", clarify.Node.ID)
	}
	if clarify.Node.Title != "Starter prompt clarification" {
		t.Fatalf("clarify node title = %q, want Starter prompt clarification", clarify.Node.Title)
	}
	if len(clarify.Node.Dependencies) != 0 {
		t.Fatalf("clarify dependencies = %#v, want none", clarify.Node.Dependencies)
	}
	if clarify.Profile.ID != "starter" {
		t.Fatalf("clarify profile = %q, want starter", clarify.Profile.ID)
	}

	// delegate depends on clarify
	if got := delegate.Node.Dependencies; len(got) != 1 || got[0] != "task_x_starter_clarify" {
		t.Fatalf("delegate dependencies = %#v, want [task_x_starter_clarify]", got)
	}
}

func TestBuildStarterClarifyPromptHintMentionsDelegates(t *testing.T) {
	starter := domain.AgentProfile{ID: "starter", DisplayName: "My Starter"}
	delegates := []domain.AgentProfile{
		{ID: "agent-1", DisplayName: "Agent One", Capabilities: []string{"go", "python"}},
		{ID: "agent-2", DisplayName: "Agent Two", Capabilities: []string{"frontend"}},
	}

	hint := buildStarterClarifyPromptHint(starter, delegates, nil)

	if !strings.Contains(hint, "Agent One") {
		t.Fatalf("prompt hint missing delegate name Agent One: %q", hint)
	}
	if !strings.Contains(hint, "Agent Two") {
		t.Fatalf("prompt hint missing delegate name Agent Two: %q", hint)
	}
	if !strings.Contains(hint, "structured task specification") {
		t.Fatalf("prompt hint missing core instruction: %q", hint)
	}
	if !strings.Contains(hint, "Do not implement the task yourself") {
		t.Fatalf("prompt hint missing no-implementation directive: %q", hint)
	}
	if strings.Contains(hint, "bootstrap") {
		t.Fatalf("clarify prompt hint should not reference bootstrap: %q", hint)
	}
}

func TestMaybePromoteOrchestratedToCuriaForProtectedScope(t *testing.T) {
	registry, err := agents.NewRegistry(
		domain.AgentProfile{ID: "my-codex", DisplayName: "My Codex", Command: "sh", HealthcheckArgs: []string{"-c", "exit 0"}, Availability: domain.AgentAvailabilityAvailable},
		domain.AgentProfile{ID: "my-gemini", DisplayName: "My Gemini", Command: "sh", HealthcheckArgs: []string{"-c", "exit 0"}, Availability: domain.AgentAvailabilityAvailable},
		domain.AgentProfile{ID: "my-copilot", DisplayName: "My Copilot", Command: "sh", HealthcheckArgs: []string{"-c", "exit 0"}, Availability: domain.AgentAvailabilityAvailable},
		domain.AgentProfile{ID: "my-claude", DisplayName: "My Claude", Command: "sh", HealthcheckArgs: []string{"-c", "exit 0"}, Availability: domain.AgentAvailabilityAvailable},
	)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	svc := NewService(registry)
	assignments, reasons := svc.maybePromoteOrchestratedToCuria(
		context.Background(),
		"Refactor auth and billing flows with a breaking change",
		t.TempDir(),
		"task_1",
		domain.AgentProfile{ID: "my-codex", DisplayName: "My Codex", Command: "sh", Availability: domain.AgentAvailabilityAvailable},
		[]domain.AgentProfile{
			{ID: "my-gemini", DisplayName: "My Gemini", Command: "sh", Availability: domain.AgentAvailabilityAvailable},
			{ID: "my-copilot", DisplayName: "My Copilot", Command: "sh", Availability: domain.AgentAvailabilityAvailable},
		},
		true,
		4,
	)
	if len(assignments) != 1 {
		t.Fatalf("assignment count = %d, want 1", len(assignments))
	}
	if assignments[0].Node.Strategy != domain.TaskStrategyCuria {
		t.Fatalf("strategy = %s, want curia", assignments[0].Node.Strategy)
	}
	if len(assignments[0].CuriaProfiles) != 3 {
		t.Fatalf("curia profiles = %d, want 3", len(assignments[0].CuriaProfiles))
	}
	if assignments[0].CuriaArbitrationMode != "augustus" {
		t.Fatalf("arbitration mode = %q, want augustus", assignments[0].CuriaArbitrationMode)
	}
	if len(reasons) == 0 {
		t.Fatal("reasons = empty, want auto-curia reasons")
	}
}

func TestMaybePromoteOrchestratedToCuriaIgnoresAvoidanceConstraints(t *testing.T) {
	registry, err := agents.NewRegistry(
		domain.AgentProfile{ID: "my-codex", DisplayName: "My Codex", Command: "sh", HealthcheckArgs: []string{"-c", "exit 0"}, Availability: domain.AgentAvailabilityAvailable},
		domain.AgentProfile{ID: "my-gemini", DisplayName: "My Gemini", Command: "sh", HealthcheckArgs: []string{"-c", "exit 0"}, Availability: domain.AgentAvailabilityAvailable},
		domain.AgentProfile{ID: "my-claude", DisplayName: "My Claude", Command: "sh", HealthcheckArgs: []string{"-c", "exit 0"}, Availability: domain.AgentAvailabilityAvailable},
	)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	svc := NewService(registry)
	assignments, reasons := svc.maybePromoteOrchestratedToCuria(
		context.Background(),
		"Build a TODO app. Do not touch auth, billing, or migrations. Avoid .github/ paths.",
		t.TempDir(),
		"task_1",
		domain.AgentProfile{ID: "my-codex", DisplayName: "My Codex", Command: "sh", Availability: domain.AgentAvailabilityAvailable},
		[]domain.AgentProfile{
			{ID: "my-gemini", DisplayName: "My Gemini", Command: "sh", Availability: domain.AgentAvailabilityAvailable},
			{ID: "my-claude", DisplayName: "My Claude", Command: "sh", Availability: domain.AgentAvailabilityAvailable},
		},
		true,
		4,
	)
	if len(assignments) != 0 {
		t.Fatalf("assignment count = %d, want 0", len(assignments))
	}
	if len(reasons) != 0 {
		t.Fatalf("reasons = %#v, want none", reasons)
	}
}

func TestMaybePromoteGraphAssignmentsToCuria(t *testing.T) {
	registry, err := agents.NewRegistry(
		domain.AgentProfile{ID: "my-codex", DisplayName: "My Codex", Command: "sh", HealthcheckArgs: []string{"-c", "exit 0"}, Availability: domain.AgentAvailabilityAvailable},
		domain.AgentProfile{ID: "my-gemini", DisplayName: "My Gemini", Command: "sh", HealthcheckArgs: []string{"-c", "exit 0"}, Availability: domain.AgentAvailabilityAvailable},
		domain.AgentProfile{ID: "my-copilot", DisplayName: "My Copilot", Command: "sh", HealthcheckArgs: []string{"-c", "exit 0"}, Availability: domain.AgentAvailabilityAvailable},
		domain.AgentProfile{ID: "my-claude", DisplayName: "My Claude", Command: "sh", HealthcheckArgs: []string{"-c", "exit 0"}, Availability: domain.AgentAvailabilityAvailable},
	)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	svc := NewService(registry)
	assignments, reasons := svc.maybePromoteGraphAssignmentsToCuria(context.Background(), "Apply a database migration for auth", t.TempDir(), []scheduler.NodeAssignment{{
		Node: domain.TaskNodeSpec{
			ID:       "node_1",
			Title:    "Auth migration",
			Strategy: domain.TaskStrategyDirect,
		},
		Profile: domain.AgentProfile{ID: "my-codex", DisplayName: "My Codex", Command: "sh", Availability: domain.AgentAvailabilityAvailable},
	}})
	if len(assignments) != 1 {
		t.Fatalf("assignment count = %d, want 1", len(assignments))
	}
	if assignments[0].Node.Strategy != domain.TaskStrategyCuria {
		t.Fatalf("strategy = %s, want curia", assignments[0].Node.Strategy)
	}
	if assignments[0].CuriaQuorum != 2 {
		t.Fatalf("curia quorum = %d, want 2", assignments[0].CuriaQuorum)
	}
	if !strings.Contains(assignments[0].Node.Title, "[auto-curia]") {
		t.Fatalf("title = %q, want [auto-curia] suffix", assignments[0].Node.Title)
	}
	if len(reasons) != 1 {
		t.Fatalf("reasons = %#v, want one promotion reason", reasons)
	}
}

func TestRunDirectAutoMergeBackRequest(t *testing.T) {
	workDir := t.TempDir()
	initRunGitRepo(t, workDir)
	registry, err := agents.NewRegistry(domain.AgentProfile{
		ID:          "auto-merge",
		DisplayName: "Auto Merge",
		Command:     "sh",
		Args: []string{
			"-c",
			"printf 'auto merge\\n' > auto-merge.txt && printf 'TAGIT_MERGE_BACK: direct_merge | ready to merge\\nTAGIT_MERGE_FILE: auto-merge.txt\\n'",
		},
		Availability: domain.AgentAvailabilityAvailable,
	})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	svc := NewService(registry)
	result, err := svc.RunWithResult(context.Background(), Request{
		Prompt:       "auto merge probe",
		Mode:         RunModeCollab,
		StarterAgent: "auto-merge",
		WorkingDir:   workDir,
	})
	if err != nil {
		t.Fatalf("RunWithResult() error = %v", err)
	}
	if result.Status != "succeeded" {
		t.Fatalf("status = %s, want succeeded", result.Status)
	}

	manager := workspacepkg.NewManager(workDir, nil)
	prepared, err := manager.Get(context.Background(), result.SessionID, result.TaskID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if prepared.Status != "merged" {
		t.Fatalf("workspace status = %q, want merged", prepared.Status)
	}
	content, err := os.ReadFile(filepath.Join(workDir, "auto-merge.txt"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if strings.TrimSpace(string(content)) != "auto merge" {
		t.Fatalf("content = %q, want auto merge", strings.TrimSpace(string(content)))
	}
}

func TestRunDirectAutoMergeBackRequestUsesControlRootWorkspaceMetadata(t *testing.T) {
	workDir := t.TempDir()
	controlDir := t.TempDir()
	initRunGitRepo(t, workDir)
	registry, err := agents.NewRegistry(domain.AgentProfile{
		ID:          "auto-merge",
		DisplayName: "Auto Merge",
		Command:     "sh",
		Args: []string{
			"-c",
			"printf 'control root merge\\n' > control-root-merge.txt && printf 'TAGIT_MERGE_BACK: direct_merge | ready to merge\\nTAGIT_MERGE_FILE: control-root-merge.txt\\n'",
		},
		Availability: domain.AgentAvailabilityAvailable,
	})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	svc := NewService(registry)
	svc.SetControlDir(controlDir)
	result, err := svc.RunWithResult(context.Background(), Request{
		Prompt:       "auto merge probe",
		Mode:         RunModeCollab,
		StarterAgent: "auto-merge",
		WorkingDir:   workDir,
	})
	if err != nil {
		t.Fatalf("RunWithResult() error = %v", err)
	}
	if result.Status != "succeeded" {
		t.Fatalf("status = %s, want succeeded", result.Status)
	}

	content, err := os.ReadFile(filepath.Join(workDir, "control-root-merge.txt"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if strings.TrimSpace(string(content)) != "control root merge" {
		t.Fatalf("content = %q, want control root merge", strings.TrimSpace(string(content)))
	}
}

func TestRunDirectMergeBackRequestRequireVoteDoesNotAutoMerge(t *testing.T) {
	workDir := t.TempDir()
	initRunGitRepo(t, workDir)
	registry, err := agents.NewRegistry(domain.AgentProfile{
		ID:          "vote-merge",
		DisplayName: "Vote Merge",
		Command:     "sh",
		Args: []string{
			"-c",
			"printf 'vote merge\\n' > vote-merge.txt && printf 'TAGIT_MERGE_BACK: require_vote | let Curia decide\\nTAGIT_MERGE_FILE: vote-merge.txt\\n'",
		},
		Availability: domain.AgentAvailabilityAvailable,
	})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	svc := NewService(registry)
	result, err := svc.RunWithResult(context.Background(), Request{
		Prompt:       "vote merge probe",
		Mode:         RunModeCollab,
		StarterAgent: "vote-merge",
		WorkingDir:   workDir,
	})
	if err != nil {
		t.Fatalf("RunWithResult() error = %v", err)
	}
	if result.Status != "succeeded" {
		t.Fatalf("status = %s, want succeeded", result.Status)
	}

	manager := workspacepkg.NewManager(workDir, nil)
	prepared, err := manager.Get(context.Background(), result.SessionID, result.TaskID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if prepared.Status != "released" {
		t.Fatalf("workspace status = %q, want released", prepared.Status)
	}
	if _, err := os.Stat(filepath.Join(workDir, "vote-merge.txt")); !os.IsNotExist(err) {
		t.Fatalf("expected base file absent before TagIt merge, stat err = %v", err)
	}
}

func TestRunCollabStarterBootstrapsThenDelegates(t *testing.T) {
	workDir := t.TempDir()
	controlDir := t.TempDir()
	initRunGitRepo(t, workDir)

	starterScript := strings.Join([]string{
		`prompt="$1"`,
		`if printf '%s' "$prompt" | grep -q "Starter prompt clarification"; then`,
		`  printf 'clarified spec\n'`,
		`elif printf '%s' "$prompt" | grep -q "Starter Caesar coordination"; then`,
		`  printf 'bootstrap ready\n'`,
		`elif printf '%s' "$prompt" | grep -q "Caesar review round"; then`,
		`  printf 'TAGIT_DONE: delegated work is complete\n'`,
		`else`,
		`  printf 'starter should only clarify, bootstrap, and review\n' >&2`,
		`  exit 9`,
		`fi`,
	}, "\n")
	workerScript := strings.Join([]string{
		`printf 'delegated work\n' > delegated.txt`,
		`printf 'delegated work complete\nTAGIT_MERGE_BACK: direct_merge | delegated work ready\nTAGIT_MERGE_FILE: delegated.txt\n'`,
	}, "\n")

	registry, err := agents.NewRegistry(
		domain.AgentProfile{
			ID:           "caesar",
			DisplayName:  "Caesar",
			Command:      "sh",
			Args:         []string{"-c", starterScript, "starter", "{prompt}"},
			Availability: domain.AgentAvailabilityAvailable,
		},
		domain.AgentProfile{
			ID:           "worker",
			DisplayName:  "Worker",
			Command:      "sh",
			Args:         []string{"-c", workerScript, "worker", "{prompt}"},
			Availability: domain.AgentAvailabilityAvailable,
		},
	)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	svc := NewService(registry)
	svc.SetControlDir(controlDir)
	result, err := svc.RunWithResult(context.Background(), Request{
		Prompt:       "coordinate a low-risk sample file update",
		Mode:         RunModeCollab,
		StarterAgent: "caesar",
		Delegates:    []string{"worker"},
		WorkingDir:   workDir,
	})
	if err != nil {
		t.Fatalf("RunWithResult() error = %v", err)
	}
	if result.Status != "succeeded" {
		t.Fatalf("status = %s, want succeeded", result.Status)
	}

	delegatedContent, err := os.ReadFile(filepath.Join(workDir, "delegated.txt"))
	if err != nil {
		t.Fatalf("ReadFile(delegated.txt) error = %v", err)
	}
	if strings.TrimSpace(string(delegatedContent)) != "delegated work" {
		t.Fatalf("delegated.txt = %q, want delegated work", strings.TrimSpace(string(delegatedContent)))
	}
	if _, err := os.Stat(filepath.Join(workDir, "second.txt")); !os.IsNotExist(err) {
		t.Fatalf("expected no follow-up second.txt, stat err = %v", err)
	}

	taskStore, err := taskstore.NewSQLiteStore(controlDir)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	tasks, err := taskStore.ListTasksBySession(context.Background(), result.SessionID)
	if err != nil {
		t.Fatalf("ListTasksBySession() error = %v", err)
	}
	clarifyCount := 0
	bootstrapCount := 0
	reviewCount := 0
	for _, task := range tasks {
		if task.Title == "Starter prompt clarification" {
			clarifyCount++
		}
		if task.Title == "Starter Caesar coordination" {
			bootstrapCount++
		}
		if strings.Contains(task.Title, "Caesar review round") {
			reviewCount++
		}
	}
	if clarifyCount != 1 {
		t.Fatalf("clarify task count = %d, want 1", clarifyCount)
	}
	if bootstrapCount != 1 {
		t.Fatalf("bootstrap task count = %d, want 1", bootstrapCount)
	}
	if reviewCount != 1 {
		t.Fatalf("review task count = %d, want 1", reviewCount)
	}
}

func TestRunCaesarStarterParticipatesWithBootstrapAndFollowUp(t *testing.T) {
	workDir := t.TempDir()
	controlDir := t.TempDir()
	initRunGitRepo(t, workDir)

	starterScript := strings.Join([]string{
		`prompt="$1"`,
		`if printf '%s' "$prompt" | grep -q "Starter prompt clarification"; then`,
		`  printf 'clarified spec\n'`,
		`elif printf '%s' "$prompt" | grep -q "Starter Caesar coordination"; then`,
		`  printf 'starter bootstrap\n' > starter.txt`,
		`  printf 'bootstrap ready\nTAGIT_MERGE_BACK: direct_merge | starter bootstrap ready\nTAGIT_MERGE_FILE: starter.txt\n'`,
		`elif printf '%s' "$prompt" | grep -q "Caesar review round"; then`,
		`  if printf '%s' "$prompt" | grep -q "second pass complete"; then`,
		`    printf 'TAGIT_DONE: all work is complete\n'`,
		`  else`,
		`    target=$(printf '%s\n' "$prompt" | sed -n 's/.*\(delegate_1\).*/\1/p' | head -n1)`,
		`    if [ -z "$target" ]; then target=delegate_1; fi`,
		`    printf 'TAGIT_FOLLOWUP: delegate %s | second pass\n' "$target"`,
		`  fi`,
		`else`,
		`  printf 'unexpected starter prompt\n' >&2`,
		`  exit 9`,
		`fi`,
	}, "\n")
	workerScript := strings.Join([]string{
		`prompt="$1"`,
		`if printf '%s' "$prompt" | grep -q "second pass"; then`,
		`  printf 'second\n' > second.txt`,
		`  printf 'second pass complete\nTAGIT_MERGE_BACK: direct_merge | second pass ready\nTAGIT_MERGE_FILE: second.txt\n'`,
		`else`,
		`  printf 'first\n' > first.txt`,
		`  printf 'first pass complete\nTAGIT_MERGE_BACK: direct_merge | first pass ready\nTAGIT_MERGE_FILE: first.txt\n'`,
		`fi`,
	}, "\n")

	registry, err := agents.NewRegistry(
		domain.AgentProfile{
			ID:           "caesar",
			DisplayName:  "Caesar",
			Command:      "sh",
			Args:         []string{"-c", starterScript, "starter", "{prompt}"},
			Availability: domain.AgentAvailabilityAvailable,
		},
		domain.AgentProfile{
			ID:           "worker",
			DisplayName:  "Worker",
			Command:      "sh",
			Args:         []string{"-c", workerScript, "worker", "{prompt}"},
			Availability: domain.AgentAvailabilityAvailable,
		},
	)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	svc := NewService(registry)
	svc.SetControlDir(controlDir)
	result, err := svc.RunWithResult(context.Background(), Request{
		Prompt:       "coordinate a low-risk sample file update",
		Mode:         RunModeCollab,
		StarterAgent: "caesar",
		Delegates:    []string{"worker"},
		WorkingDir:   workDir,
	})
	if err != nil {
		t.Fatalf("RunWithResult() error = %v", err)
	}
	if result.Status != "succeeded" {
		t.Fatalf("status = %s, want succeeded", result.Status)
	}
	starterContent, err := os.ReadFile(filepath.Join(workDir, "starter.txt"))
	if err != nil {
		t.Fatalf("ReadFile(starter.txt) error = %v", err)
	}
	if strings.TrimSpace(string(starterContent)) != "starter bootstrap" {
		t.Fatalf("starter.txt = %q, want starter bootstrap", strings.TrimSpace(string(starterContent)))
	}
	secondContent, err := os.ReadFile(filepath.Join(workDir, "second.txt"))
	if err != nil {
		t.Fatalf("ReadFile(second.txt) error = %v", err)
	}
	if strings.TrimSpace(string(secondContent)) != "second" {
		t.Fatalf("second.txt = %q, want second", strings.TrimSpace(string(secondContent)))
	}
}

func TestRunSenateVotesOnPlanAndImplementationThenMergesWinner(t *testing.T) {
	workDir := t.TempDir()
	controlDir := t.TempDir()
	initRunGitRepo(t, workDir)

	starterScript := strings.Join([]string{
		`prompt="$1"`,
		`if printf '%s' "$prompt" | grep -q "Senate plan proposal"; then`,
		`  printf 'STARTER PLAN\n'`,
		`elif printf '%s' "$prompt" | grep -q "Senate plan vote"; then`,
		`  printf 'starter abstains\n'`,
		`elif printf '%s' "$prompt" | grep -q "Senate plan tiebreak"; then`,
		`  target=$(printf '%s\n' "$prompt" | sed -n 's/^- \(.*_plan_3\)$/\1/p' | head -n1)`,
		`  printf 'TAGIT_PICK: %s | choose delegate two plan\n' "$target"`,
		`elif printf '%s' "$prompt" | grep -q "Senate implementation vote"; then`,
		`  printf 'starter abstains\n'`,
		`elif printf '%s' "$prompt" | grep -q "Senate implementation tiebreak"; then`,
		`  target=$(printf '%s\n' "$prompt" | sed -n 's/^- \(.*_implementation_1\)$/\1/p' | head -n1)`,
		`  printf 'TAGIT_PICK: %s | choose delegate one implementation\n' "$target"`,
		`else`,
		`  printf 'unexpected starter prompt\n' >&2`,
		`  exit 9`,
		`fi`,
	}, "\n")
	workerOneScript := strings.Join([]string{
		`prompt="$1"`,
		`if printf '%s' "$prompt" | grep -q "Senate plan proposal"; then`,
		`  printf 'PLAN ONE\n'`,
		`elif printf '%s' "$prompt" | grep -q "Senate plan vote"; then`,
		`  target=$(printf '%s\n' "$prompt" | sed -n 's/^- \(.*_plan_2\)$/\1/p' | head -n1)`,
		`  printf 'TAGIT_PICK: %s | vote for plan one\n' "$target"`,
		`elif printf '%s' "$prompt" | grep -q "Senate implementation candidate"; then`,
		`  printf 'delegate one\n' > winner.txt`,
		`  printf 'TAGIT_MERGE_BACK: require_vote | candidate ready\nTAGIT_MERGE_FILE: winner.txt\n'`,
		`elif printf '%s' "$prompt" | grep -q "Senate implementation vote"; then`,
		`  target=$(printf '%s\n' "$prompt" | sed -n 's/^- \(.*_implementation_1\)$/\1/p' | head -n1)`,
		`  printf 'TAGIT_PICK: %s | vote for implementation one\n' "$target"`,
		`else`,
		`  printf 'unexpected worker one prompt\n' >&2`,
		`  exit 11`,
		`fi`,
	}, "\n")
	workerTwoScript := strings.Join([]string{
		`prompt="$1"`,
		`if printf '%s' "$prompt" | grep -q "Senate plan proposal"; then`,
		`  printf 'PLAN TWO\n'`,
		`elif printf '%s' "$prompt" | grep -q "Senate plan vote"; then`,
		`  target=$(printf '%s\n' "$prompt" | sed -n 's/^- \(.*_plan_3\)$/\1/p' | head -n1)`,
		`  printf 'TAGIT_PICK: %s | vote for plan two\n' "$target"`,
		`elif printf '%s' "$prompt" | grep -q "Senate implementation candidate"; then`,
		`  printf 'delegate two\n' > loser.txt`,
		`  printf 'TAGIT_MERGE_BACK: require_vote | candidate ready\nTAGIT_MERGE_FILE: loser.txt\n'`,
		`elif printf '%s' "$prompt" | grep -q "Senate implementation vote"; then`,
		`  target=$(printf '%s\n' "$prompt" | sed -n 's/^- \(.*_implementation_2\)$/\1/p' | head -n1)`,
		`  printf 'TAGIT_PICK: %s | vote for implementation two\n' "$target"`,
		`else`,
		`  printf 'unexpected worker two prompt\n' >&2`,
		`  exit 13`,
		`fi`,
	}, "\n")

	registry, err := agents.NewRegistry(
		domain.AgentProfile{
			ID:           "starter",
			DisplayName:  "Starter",
			Command:      "sh",
			Args:         []string{"-c", starterScript, "starter", "{prompt}"},
			Availability: domain.AgentAvailabilityAvailable,
		},
		domain.AgentProfile{
			ID:           "worker-one",
			DisplayName:  "Worker One",
			Command:      "sh",
			Args:         []string{"-c", workerOneScript, "worker-one", "{prompt}"},
			Availability: domain.AgentAvailabilityAvailable,
		},
		domain.AgentProfile{
			ID:           "worker-two",
			DisplayName:  "Worker Two",
			Command:      "sh",
			Args:         []string{"-c", workerTwoScript, "worker-two", "{prompt}"},
			Availability: domain.AgentAvailabilityAvailable,
		},
	)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	svc := NewService(registry)
	svc.SetControlDir(controlDir)
	result, err := svc.RunWithResult(context.Background(), Request{
		Prompt:       "implement a winner-takes-all senate flow",
		Mode:         RunModeSenate,
		StarterAgent: "starter",
		Delegates:    []string{"worker-one", "worker-two"},
		WorkingDir:   workDir,
	})
	if err != nil {
		t.Fatalf("RunWithResult() error = %v", err)
	}
	if result.Status != "succeeded" {
		t.Fatalf("status = %s, want succeeded", result.Status)
	}
	content, err := os.ReadFile(filepath.Join(workDir, "winner.txt"))
	if err != nil {
		t.Fatalf("ReadFile(winner.txt) error = %v", err)
	}
	if strings.TrimSpace(string(content)) != "delegate one" {
		t.Fatalf("winner.txt = %q, want delegate one", strings.TrimSpace(string(content)))
	}
	if _, err := os.Stat(filepath.Join(workDir, "loser.txt")); !os.IsNotExist(err) {
		t.Fatalf("expected loser.txt absent after senate merge, stat err = %v", err)
	}
}

func initRunGitRepo(t *testing.T, dir string) {
	t.Helper()
	runGitCommand(t, dir, "init")
	runGitCommand(t, dir, "config", "user.email", "tagit@example.com")
	runGitCommand(t, dir, "config", "user.name", "TagIt")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("tagit\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	runGitCommand(t, dir, "add", "README.md")
	runGitCommand(t, dir, "commit", "-m", "init")
}

func runGitCommand(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s error = %v (%s)", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
}
