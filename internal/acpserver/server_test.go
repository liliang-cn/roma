package acpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/liliang-cn/roma/internal/agents"
	"github.com/liliang-cn/roma/internal/domain"
	"github.com/liliang-cn/roma/internal/queue"
)

// ---------------------------------------------------------------------------
// Fake queue backend for tests
// ---------------------------------------------------------------------------

type fakeQueue struct {
	jobs map[string]queue.Request
}

func newFakeQueue() *fakeQueue {
	return &fakeQueue{jobs: make(map[string]queue.Request)}
}

func (f *fakeQueue) Enqueue(_ context.Context, req queue.Request) error {
	f.jobs[req.ID] = req
	return nil
}

func (f *fakeQueue) Update(_ context.Context, req queue.Request) error {
	f.jobs[req.ID] = req
	return nil
}

func (f *fakeQueue) Get(_ context.Context, id string) (queue.Request, error) {
	req, ok := f.jobs[id]
	if !ok {
		return queue.Request{}, &notFoundError{id: id}
	}
	return req, nil
}

func (f *fakeQueue) List(_ context.Context) ([]queue.Request, error) {
	out := make([]queue.Request, 0, len(f.jobs))
	for _, r := range f.jobs {
		out = append(out, r)
	}
	return out, nil
}

func (f *fakeQueue) NextPending(_ context.Context) (queue.Request, bool, error) {
	for _, r := range f.jobs {
		if r.Status == queue.StatusPending {
			return r, true, nil
		}
	}
	return queue.Request{}, false, nil
}

func (f *fakeQueue) RecoverInterrupted(_ context.Context) error { return nil }

type notFoundError struct{ id string }

func (e *notFoundError) Error() string { return "not found: " + e.id }

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func testRegistry(t *testing.T, profiles ...domain.AgentProfile) *agents.Registry {
	t.Helper()
	reg, err := agents.NewRegistry(profiles...)
	if err != nil {
		t.Fatalf("build registry: %v", err)
	}
	return reg
}

func testServer(t *testing.T, reg *agents.Registry, q queue.Backend) *Server {
	t.Helper()
	return NewServer(0, reg, q)
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestListAgents_Empty(t *testing.T) {
	srv := testServer(t, testRegistry(t), newFakeQueue())
	req := httptest.NewRequest(http.MethodGet, "/acp/v1/agents", nil)
	w := httptest.NewRecorder()

	srv.handleListAgents(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var got []AgentInfo
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want empty slice, got %d agents", len(got))
	}
}

func TestListAgents_WithProfiles(t *testing.T) {
	profile := domain.AgentProfile{
		ID:           "claude-cli",
		DisplayName:  "Claude CLI",
		Command:      "claude",
		Availability: domain.AgentAvailabilityAvailable,
	}
	srv := testServer(t, testRegistry(t, profile), newFakeQueue())
	req := httptest.NewRequest(http.MethodGet, "/acp/v1/agents", nil)
	w := httptest.NewRecorder()

	srv.handleListAgents(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var got []AgentInfo
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 agent, got %d", len(got))
	}
	if got[0].ID != "claude-cli" {
		t.Errorf("want id=claude-cli, got %q", got[0].ID)
	}
	if got[0].Name != "Claude CLI" {
		t.Errorf("want name=Claude CLI, got %q", got[0].Name)
	}
}

func TestCreateThread_MissingAgentID(t *testing.T) {
	srv := testServer(t, testRegistry(t), newFakeQueue())
	body := `{"messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest(http.MethodPost, "/acp/v1/threads", bytes.NewBufferString(body))
	w := httptest.NewRecorder()

	srv.handleCreateThread(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", w.Code)
	}
}

func TestCreateThread_UnknownAgent(t *testing.T) {
	srv := testServer(t, testRegistry(t), newFakeQueue())
	body := `{"agent_id":"no-such-agent","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/acp/v1/threads", bytes.NewBufferString(body))
	w := httptest.NewRecorder()

	srv.handleCreateThread(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", w.Code)
	}
}

func TestCreateThread_Success(t *testing.T) {
	profile := domain.AgentProfile{
		ID:           "claude-cli",
		DisplayName:  "Claude CLI",
		Command:      "claude",
		Availability: domain.AgentAvailabilityAvailable,
	}
	q := newFakeQueue()
	srv := testServer(t, testRegistry(t, profile), q)

	body := `{"agent_id":"claude-cli","messages":[{"role":"user","content":"write tests"}]}`
	req := httptest.NewRequest(http.MethodPost, "/acp/v1/threads", bytes.NewBufferString(body))
	w := httptest.NewRecorder()

	srv.handleCreateThread(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d; body: %s", w.Code, w.Body.String())
	}

	var got Thread
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.AgentID != "claude-cli" {
		t.Errorf("want agent_id=claude-cli, got %q", got.AgentID)
	}
	if got.Status != ThreadStatusPending {
		t.Errorf("want status=pending, got %q", got.Status)
	}
	if got.ID == "" {
		t.Error("want non-empty thread id")
	}

	// Verify job was enqueued.
	stored, err := q.Get(context.Background(), got.ID)
	if err != nil {
		t.Fatalf("job not enqueued: %v", err)
	}
	if stored.Prompt != "write tests" {
		t.Errorf("want prompt=%q, got %q", "write tests", stored.Prompt)
	}
}

func TestGetThread_NotFound(t *testing.T) {
	srv := testServer(t, testRegistry(t), newFakeQueue())
	req := httptest.NewRequest(http.MethodGet, "/acp/v1/threads/nonexistent", nil)
	req.SetPathValue("id", "nonexistent")
	w := httptest.NewRecorder()

	srv.handleGetThread(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", w.Code)
	}
}

func TestGetThread_Success(t *testing.T) {
	q := newFakeQueue()
	now := time.Now().UTC()
	_ = q.Enqueue(context.Background(), queue.Request{
		ID:           "acp_thread_1",
		StarterAgent: "claude-cli",
		Status:       queue.StatusRunning,
		CreatedAt:    now,
		UpdatedAt:    now,
	})

	srv := testServer(t, testRegistry(t), q)
	req := httptest.NewRequest(http.MethodGet, "/acp/v1/threads/acp_thread_1", nil)
	req.SetPathValue("id", "acp_thread_1")
	w := httptest.NewRecorder()

	srv.handleGetThread(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d; body: %s", w.Code, w.Body.String())
	}
	var got Thread
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ID != "acp_thread_1" {
		t.Errorf("want id=acp_thread_1, got %q", got.ID)
	}
	if got.Status != ThreadStatusRunning {
		t.Errorf("want status=running, got %q", got.Status)
	}
}

func TestQueueStatusToThread(t *testing.T) {
	cases := []struct {
		in   queue.Status
		want ThreadStatus
	}{
		{queue.StatusPending, ThreadStatusPending},
		{queue.StatusRunning, ThreadStatusRunning},
		{queue.StatusAwaitingApproval, ThreadStatusRunning},
		{queue.StatusSucceeded, ThreadStatusCompleted},
		{queue.StatusFailed, ThreadStatusFailed},
		{queue.StatusRejected, ThreadStatusFailed},
		{queue.StatusCancelled, ThreadStatusCancelled},
	}
	for _, c := range cases {
		got := queueStatusToThread(c.in)
		if got != c.want {
			t.Errorf("queueStatusToThread(%q): want %q, got %q", c.in, c.want, got)
		}
	}
}

func TestNewServer_DefaultPort(t *testing.T) {
	srv := NewServer(0, testRegistry(t), newFakeQueue())
	if srv.Port() != DefaultPort {
		t.Errorf("want default port %d, got %d", DefaultPort, srv.Port())
	}
}
