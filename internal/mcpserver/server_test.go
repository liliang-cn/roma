package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/liliang-cn/tagit/internal/api"
	"github.com/liliang-cn/tagit/internal/memory"
	"github.com/liliang-cn/tagit/internal/queue"
)

// ---------------------------------------------------------------------------
// Fakes for the daemon and memory ports
// ---------------------------------------------------------------------------

type fakeDaemon struct {
	submitted  []api.SubmitRequest
	submitResp api.SubmitResponse
	submitErr  error

	inspectCalls int
	inspect      []api.QueueInspectResponse
	inspectErr   error

	list      []queue.Request
	cancelled []string
	results   map[string]api.ResultShowResponse
}

func (f *fakeDaemon) Submit(_ context.Context, req api.SubmitRequest) (api.SubmitResponse, error) {
	f.submitted = append(f.submitted, req)
	if f.submitErr != nil {
		return api.SubmitResponse{}, f.submitErr
	}
	return f.submitResp, nil
}

func (f *fakeDaemon) QueueInspect(_ context.Context, id string, _ bool) (api.QueueInspectResponse, error) {
	if f.inspectErr != nil {
		return api.QueueInspectResponse{}, f.inspectErr
	}
	if len(f.inspect) == 0 {
		return api.QueueInspectResponse{}, errors.New("no queue job " + id)
	}
	idx := f.inspectCalls
	if idx >= len(f.inspect) {
		idx = len(f.inspect) - 1
	}
	f.inspectCalls++
	return f.inspect[idx], nil
}

func (f *fakeDaemon) QueueList(context.Context) ([]queue.Request, error) { return f.list, nil }

func (f *fakeDaemon) QueueCancel(_ context.Context, id string) (queue.Request, error) {
	f.cancelled = append(f.cancelled, id)
	return queue.Request{ID: id, Status: queue.StatusCancelled}, nil
}

func (f *fakeDaemon) ResultShow(_ context.Context, sessionID string) (api.ResultShowResponse, error) {
	resp, ok := f.results[sessionID]
	if !ok {
		return api.ResultShowResponse{}, errors.New("no result for " + sessionID)
	}
	return resp, nil
}

type fakeMemory struct {
	recalled    []string
	recallScope memory.Scope
	recollect   memory.Recollection
	noted       []string
	noteScope   memory.Scope
	noteTags    []string
}

func (m *fakeMemory) Recall(_ context.Context, scope memory.Scope, query string, _ int) (memory.Recollection, error) {
	m.recalled = append(m.recalled, query)
	m.recallScope = scope
	return m.recollect, nil
}

func (m *fakeMemory) Record(context.Context, memory.RunRecord) error { return nil }

func (m *fakeMemory) Note(_ context.Context, scope memory.Scope, fact string, tags []string) error {
	m.noted = append(m.noted, fact)
	m.noteScope = scope
	m.noteTags = tags
	return nil
}

// ---------------------------------------------------------------------------
// Harness
// ---------------------------------------------------------------------------

func newSession(t *testing.T, opts Options) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()

	serverSession, err := NewServer(opts).Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.0.1"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() {
		_ = clientSession.Close()
		_ = serverSession.Wait()
	})
	return clientSession
}

func toolNames(t *testing.T, session *mcp.ClientSession) map[string]*mcp.Tool {
	t.Helper()
	res, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	out := make(map[string]*mcp.Tool, len(res.Tools))
	for _, tool := range res.Tools {
		out[tool.Name] = tool
	}
	return out
}

func callTool(t *testing.T, session *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("call %s: %v", name, err)
	}
	return res
}

func decodeStructured(t *testing.T, res *mcp.CallToolResult, target any) {
	t.Helper()
	if res.IsError {
		t.Fatalf("tool returned error: %s", textOf(res))
	}
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	if err := json.Unmarshal(raw, target); err != nil {
		t.Fatalf("decode structured content %s: %v", raw, err)
	}
}

func textOf(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

// ---------------------------------------------------------------------------
// Tool surface
// ---------------------------------------------------------------------------

func TestToolsListExposesTheTagitSurface(t *testing.T) {
	session := newSession(t, Options{Daemon: &fakeDaemon{}, Memory: &fakeMemory{}})

	tools := toolNames(t, session)
	for _, want := range []string{
		"tagit_submit",
		"tagit_job_status",
		"tagit_job_wait",
		"tagit_job_cancel",
		"tagit_job_list",
		"tagit_result",
		"tagit_memory_recall",
		"tagit_memory_note",
	} {
		if _, ok := tools[want]; !ok {
			t.Errorf("tool %q missing from tools/list", want)
		}
	}
}

func TestReadOnlyModeHidesWriteTools(t *testing.T) {
	session := newSession(t, Options{Daemon: &fakeDaemon{}, Memory: &fakeMemory{}, ReadOnly: true})

	tools := toolNames(t, session)
	for _, hidden := range []string{"tagit_submit", "tagit_job_cancel", "tagit_memory_note"} {
		if _, ok := tools[hidden]; ok {
			t.Errorf("write tool %q must not be exposed in read-only mode", hidden)
		}
	}
	if _, ok := tools["tagit_job_status"]; !ok {
		t.Error("read-only mode must still expose tagit_job_status")
	}
}

func TestMemoryToolsOmittedWhenNoMemoryConfigured(t *testing.T) {
	session := newSession(t, Options{Daemon: &fakeDaemon{}})

	tools := toolNames(t, session)
	for _, hidden := range []string{"tagit_memory_recall", "tagit_memory_note"} {
		if _, ok := tools[hidden]; ok {
			t.Errorf("tool %q must not be exposed without a memory port", hidden)
		}
	}
}

func TestSubmitSchemaNeverExposesPolicyOverride(t *testing.T) {
	session := newSession(t, Options{Daemon: &fakeDaemon{}})

	tool, ok := toolNames(t, session)["tagit_submit"]
	if !ok {
		t.Fatal("tagit_submit missing")
	}
	raw, err := json.Marshal(tool.InputSchema)
	if err != nil {
		t.Fatalf("marshal input schema: %v", err)
	}
	if strings.Contains(string(raw), "policy_override") {
		t.Errorf("tagit_submit must not let callers bypass the policy broker: %s", raw)
	}
}

// ---------------------------------------------------------------------------
// tagit_submit
// ---------------------------------------------------------------------------

func TestSubmitEnqueuesThroughTheDaemon(t *testing.T) {
	daemon := &fakeDaemon{submitResp: api.SubmitResponse{JobID: "job_1"}}
	session := newSession(t, Options{Daemon: daemon})

	res := callTool(t, session, "tagit_submit", map[string]any{
		"prompt":        "add a healthcheck endpoint",
		"working_dir":   "/repo",
		"mode":          "rage",
		"starter_agent": "claude",
	})

	var out SubmitOutput
	decodeStructured(t, res, &out)
	if out.JobID != "job_1" {
		t.Errorf("job_id = %q, want job_1", out.JobID)
	}
	if len(daemon.submitted) != 1 {
		t.Fatalf("daemon got %d submits, want 1", len(daemon.submitted))
	}
	got := daemon.submitted[0]
	if got.Prompt != "add a healthcheck endpoint" {
		t.Errorf("prompt = %q", got.Prompt)
	}
	if got.WorkingDir != "/repo" {
		t.Errorf("working_dir = %q, want /repo", got.WorkingDir)
	}
	if got.Mode != "rage" || got.StarterAgent != "claude" {
		t.Errorf("mode/agent = %q/%q", got.Mode, got.StarterAgent)
	}
	if got.PolicyOverride {
		t.Error("submit must never set PolicyOverride")
	}
}

func TestSubmitFallsBackToDefaultWorkingDir(t *testing.T) {
	daemon := &fakeDaemon{submitResp: api.SubmitResponse{JobID: "job_2"}}
	session := newSession(t, Options{Daemon: daemon, DefaultWorkingDir: "/default/repo"})

	callTool(t, session, "tagit_submit", map[string]any{"prompt": "ship it"})

	if len(daemon.submitted) != 1 {
		t.Fatalf("daemon got %d submits, want 1", len(daemon.submitted))
	}
	if got := daemon.submitted[0].WorkingDir; got != "/default/repo" {
		t.Errorf("working_dir = %q, want /default/repo", got)
	}
}

func TestSubmitRejectsEmptyPrompt(t *testing.T) {
	daemon := &fakeDaemon{}
	session := newSession(t, Options{Daemon: daemon})

	res := callTool(t, session, "tagit_submit", map[string]any{"prompt": "   ", "working_dir": "/repo"})

	if !res.IsError {
		t.Fatal("empty prompt must produce a tool error")
	}
	if len(daemon.submitted) != 0 {
		t.Errorf("daemon must not be called, got %d submits", len(daemon.submitted))
	}
}

func TestSubmitRequiresAWorkingDir(t *testing.T) {
	daemon := &fakeDaemon{}
	session := newSession(t, Options{Daemon: daemon})

	res := callTool(t, session, "tagit_submit", map[string]any{"prompt": "ship it"})

	if !res.IsError {
		t.Fatal("missing working_dir with no default must produce a tool error")
	}
	if len(daemon.submitted) != 0 {
		t.Errorf("daemon must not be called, got %d submits", len(daemon.submitted))
	}
}

func TestSubmitSurfacesDaemonErrors(t *testing.T) {
	daemon := &fakeDaemon{submitErr: errors.New("daemon unreachable")}
	session := newSession(t, Options{Daemon: daemon})

	res := callTool(t, session, "tagit_submit", map[string]any{"prompt": "x", "working_dir": "/repo"})

	if !res.IsError {
		t.Fatal("daemon failure must produce a tool error")
	}
	if !strings.Contains(textOf(res), "daemon unreachable") {
		t.Errorf("error text = %q, want it to mention the daemon failure", textOf(res))
	}
}

// ---------------------------------------------------------------------------
// tagit_job_status / tagit_job_wait / tagit_job_cancel / tagit_job_list
// ---------------------------------------------------------------------------

func TestJobStatusSummarizesTheJob(t *testing.T) {
	daemon := &fakeDaemon{inspect: []api.QueueInspectResponse{{
		Job: queue.Request{
			ID:           "job_1",
			Status:       queue.StatusRunning,
			SessionID:    "sess_1",
			Mode:         "rage",
			StarterAgent: "claude",
			WorkingDir:   "/repo",
			UpdatedAt:    time.Date(2026, 7, 31, 10, 0, 0, 0, time.UTC),
		},
		PendingApprovalTaskIDs: []string{"task_a"},
	}}}
	session := newSession(t, Options{Daemon: daemon})

	res := callTool(t, session, "tagit_job_status", map[string]any{"job_id": "job_1"})

	var out JobOutput
	decodeStructured(t, res, &out)
	if out.Status != "running" {
		t.Errorf("status = %q, want running", out.Status)
	}
	if out.SessionID != "sess_1" {
		t.Errorf("session_id = %q, want sess_1", out.SessionID)
	}
	if out.Terminal {
		t.Error("running job must not be reported as terminal")
	}
	if len(out.PendingApprovalTaskIDs) != 1 || out.PendingApprovalTaskIDs[0] != "task_a" {
		t.Errorf("pending approvals = %v, want [task_a]", out.PendingApprovalTaskIDs)
	}
}

func TestJobStatusMarksTerminalStates(t *testing.T) {
	for _, status := range []queue.Status{
		queue.StatusSucceeded, queue.StatusFailed, queue.StatusRejected, queue.StatusCancelled,
	} {
		t.Run(string(status), func(t *testing.T) {
			daemon := &fakeDaemon{inspect: []api.QueueInspectResponse{{
				Job: queue.Request{ID: "job_1", Status: status},
			}}}
			session := newSession(t, Options{Daemon: daemon})

			res := callTool(t, session, "tagit_job_status", map[string]any{"job_id": "job_1"})

			var out JobOutput
			decodeStructured(t, res, &out)
			if !out.Terminal {
				t.Errorf("%s must be reported as terminal", status)
			}
		})
	}
}

func TestJobWaitPollsUntilTerminal(t *testing.T) {
	daemon := &fakeDaemon{inspect: []api.QueueInspectResponse{
		{Job: queue.Request{ID: "job_1", Status: queue.StatusPending}},
		{Job: queue.Request{ID: "job_1", Status: queue.StatusRunning}},
		{Job: queue.Request{ID: "job_1", Status: queue.StatusSucceeded, SessionID: "sess_1"}},
	}}
	session := newSession(t, Options{Daemon: daemon, PollInterval: time.Millisecond})

	res := callTool(t, session, "tagit_job_wait", map[string]any{"job_id": "job_1"})

	var out JobOutput
	decodeStructured(t, res, &out)
	if out.Status != "succeeded" {
		t.Errorf("status = %q, want succeeded", out.Status)
	}
	if !out.Terminal {
		t.Error("terminal must be true after the job succeeds")
	}
	if daemon.inspectCalls < 3 {
		t.Errorf("inspect called %d times, want at least 3 (it must keep polling)", daemon.inspectCalls)
	}
}

func TestJobWaitReturnsTheLastStatusWhenItTimesOut(t *testing.T) {
	daemon := &fakeDaemon{inspect: []api.QueueInspectResponse{
		{Job: queue.Request{ID: "job_1", Status: queue.StatusRunning}},
	}}
	session := newSession(t, Options{Daemon: daemon, PollInterval: time.Millisecond})

	res := callTool(t, session, "tagit_job_wait", map[string]any{
		"job_id":          "job_1",
		"timeout_seconds": 1,
	})

	var out JobOutput
	decodeStructured(t, res, &out)
	if out.Status != "running" {
		t.Errorf("status = %q, want running", out.Status)
	}
	if out.Terminal {
		t.Error("a timed-out wait must not claim the job is terminal")
	}
	if !out.TimedOut {
		t.Error("timed_out must be true when the wait budget is exhausted")
	}
}

func TestJobCancelCancelsThroughTheDaemon(t *testing.T) {
	daemon := &fakeDaemon{}
	session := newSession(t, Options{Daemon: daemon})

	res := callTool(t, session, "tagit_job_cancel", map[string]any{"job_id": "job_9"})

	var out JobOutput
	decodeStructured(t, res, &out)
	if out.Status != "cancelled" {
		t.Errorf("status = %q, want cancelled", out.Status)
	}
	if len(daemon.cancelled) != 1 || daemon.cancelled[0] != "job_9" {
		t.Errorf("cancelled = %v, want [job_9]", daemon.cancelled)
	}
}

func TestJobListReturnsTheMostRecentJobsFirst(t *testing.T) {
	daemon := &fakeDaemon{list: []queue.Request{
		{ID: "job_1", Status: queue.StatusSucceeded, CreatedAt: time.Date(2026, 7, 31, 9, 0, 0, 0, time.UTC)},
		{ID: "job_2", Status: queue.StatusRunning, CreatedAt: time.Date(2026, 7, 31, 11, 0, 0, 0, time.UTC)},
		{ID: "job_3", Status: queue.StatusPending, CreatedAt: time.Date(2026, 7, 31, 10, 0, 0, 0, time.UTC)},
	}}
	session := newSession(t, Options{Daemon: daemon})

	res := callTool(t, session, "tagit_job_list", map[string]any{"limit": 2})

	var out JobListOutput
	decodeStructured(t, res, &out)
	if len(out.Jobs) != 2 {
		t.Fatalf("got %d jobs, want 2", len(out.Jobs))
	}
	if out.Jobs[0].JobID != "job_2" || out.Jobs[1].JobID != "job_3" {
		t.Errorf("jobs = %s,%s; want job_2,job_3 (newest first)", out.Jobs[0].JobID, out.Jobs[1].JobID)
	}
}

// ---------------------------------------------------------------------------
// tagit_result
// ---------------------------------------------------------------------------

func TestResultReturnsTheFinalSessionOutcome(t *testing.T) {
	daemon := &fakeDaemon{results: map[string]api.ResultShowResponse{
		"sess_1": {Message: "all tests pass"},
	}}
	session := newSession(t, Options{Daemon: daemon})

	res := callTool(t, session, "tagit_result", map[string]any{"session_id": "sess_1"})

	var out ResultOutput
	decodeStructured(t, res, &out)
	if out.Message != "all tests pass" {
		t.Errorf("message = %q, want 'all tests pass'", out.Message)
	}
	if out.Pending {
		t.Error("pending must be false for a finished session")
	}
}

func TestResultAcceptsAJobIDAndResolvesItsSession(t *testing.T) {
	daemon := &fakeDaemon{
		inspect: []api.QueueInspectResponse{{
			Job: queue.Request{ID: "job_1", Status: queue.StatusSucceeded, SessionID: "sess_1"},
		}},
		results: map[string]api.ResultShowResponse{"sess_1": {Message: "done"}},
	}
	session := newSession(t, Options{Daemon: daemon})

	res := callTool(t, session, "tagit_result", map[string]any{"job_id": "job_1"})

	var out ResultOutput
	decodeStructured(t, res, &out)
	if out.SessionID != "sess_1" {
		t.Errorf("session_id = %q, want sess_1", out.SessionID)
	}
	if out.Message != "done" {
		t.Errorf("message = %q, want done", out.Message)
	}
}

// ---------------------------------------------------------------------------
// memory tools
// ---------------------------------------------------------------------------

func TestMemoryRecallReturnsTheInjectableContext(t *testing.T) {
	mem := &fakeMemory{recollect: memory.Recollection{
		ContextText: "previous run touched internal/api",
		Episodes:    []memory.Episode{{Summary: "added /health", Agent: "claude", Mode: "rage", Success: true}},
		Knowledge:   []memory.Fact{{Text: "build with GOWORK=off", Tags: []string{"build"}}},
	}}
	session := newSession(t, Options{Daemon: &fakeDaemon{}, Memory: mem})

	res := callTool(t, session, "tagit_memory_recall", map[string]any{
		"repo":  "/repo",
		"query": "health endpoint",
	})

	var out RecallOutput
	decodeStructured(t, res, &out)
	if out.Context != "previous run touched internal/api" {
		t.Errorf("context = %q", out.Context)
	}
	if len(out.Episodes) != 1 || out.Episodes[0].Summary != "added /health" {
		t.Errorf("episodes = %+v", out.Episodes)
	}
	if len(out.Facts) != 1 || out.Facts[0].Text != "build with GOWORK=off" {
		t.Errorf("facts = %+v", out.Facts)
	}
	if mem.recallScope.Repo != "/repo" {
		t.Errorf("recall scope repo = %q, want /repo", mem.recallScope.Repo)
	}
	if len(mem.recalled) != 1 || mem.recalled[0] != "health endpoint" {
		t.Errorf("recalled queries = %v", mem.recalled)
	}
}

func TestMemoryNoteStoresTheFact(t *testing.T) {
	mem := &fakeMemory{}
	session := newSession(t, Options{Daemon: &fakeDaemon{}, Memory: mem})

	res := callTool(t, session, "tagit_memory_note", map[string]any{
		"repo": "/repo",
		"fact": "the daemon needs codesign after an in-place binary swap",
		"tags": []any{"ops"},
	})

	var out NoteOutput
	decodeStructured(t, res, &out)
	if !out.OK {
		t.Error("ok must be true")
	}
	if len(mem.noted) != 1 || mem.noted[0] != "the daemon needs codesign after an in-place binary swap" {
		t.Errorf("noted = %v", mem.noted)
	}
	if mem.noteScope.Repo != "/repo" {
		t.Errorf("note scope repo = %q, want /repo", mem.noteScope.Repo)
	}
	if len(mem.noteTags) != 1 || mem.noteTags[0] != "ops" {
		t.Errorf("tags = %v, want [ops]", mem.noteTags)
	}
}

func TestMemoryToolsFallBackToTheDefaultWorkingDir(t *testing.T) {
	mem := &fakeMemory{}
	session := newSession(t, Options{Daemon: &fakeDaemon{}, Memory: mem, DefaultWorkingDir: "/default/repo"})

	callTool(t, session, "tagit_memory_recall", map[string]any{"query": "anything"})

	if mem.recallScope.Repo != "/default/repo" {
		t.Errorf("recall scope repo = %q, want /default/repo", mem.recallScope.Repo)
	}
}
