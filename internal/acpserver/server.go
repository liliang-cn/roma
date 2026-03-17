// Package acpserver exposes ROMA agents over the Agent Client Protocol (ACP).
//
// The server listens on a standard HTTP port (default 8090) and implements
// the core ACP endpoints so that remote ACP clients can discover agents,
// create threads (run jobs), and inspect thread state.
//
// TODO: when github.com/coder/acp-go-sdk is available, replace the local
// stub types below with the SDK types and wire up the SDK server helpers.
package acpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/liliang-cn/roma/internal/agents"
	"github.com/liliang-cn/roma/internal/queue"
	"github.com/liliang-cn/roma/internal/romapath"
)

// ---------------------------------------------------------------------------
// ACP stub types
// TODO: replace with github.com/coder/acp-go-sdk types once the dependency
//       is added via: go get github.com/coder/acp-go-sdk
// ---------------------------------------------------------------------------

// AgentInfo describes a discoverable ACP agent.
type AgentInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// ThreadStatus mirrors the ACP thread lifecycle states.
type ThreadStatus string

const (
	ThreadStatusPending   ThreadStatus = "pending"
	ThreadStatusRunning   ThreadStatus = "running"
	ThreadStatusCompleted ThreadStatus = "completed"
	ThreadStatusFailed    ThreadStatus = "failed"
	ThreadStatusCancelled ThreadStatus = "cancelled"
)

// Thread is an ACP conversation / run unit backed by a ROMA queue job.
type Thread struct {
	ID        string       `json:"id"`
	AgentID   string       `json:"agent_id"`
	Status    ThreadStatus `json:"status"`
	CreatedAt time.Time    `json:"created_at"`
	UpdatedAt time.Time    `json:"updated_at"`
	Error     string       `json:"error,omitempty"`
}

// Message is a single prompt or reply within a thread.
type Message struct {
	// Role is "user" for prompts and "agent" for replies.
	Role    string `json:"role"`
	Content string `json:"content"`
}

// CreateThreadRequest is the ACP payload for starting a new thread.
type CreateThreadRequest struct {
	AgentID  string    `json:"agent_id"`
	Messages []Message `json:"messages"`
}

// ---------------------------------------------------------------------------
// Server
// ---------------------------------------------------------------------------

// Server is the ACP HTTP server.
type Server struct {
	port     int
	registry *agents.Registry
	queue    queue.Backend
	httpSrv  *http.Server
}

// DefaultPort is the default ACP listener port.
const DefaultPort = 8090

// Config holds the configuration for constructing an ACP Server.
type Config struct {
	Port       int
	WorkingDir string
	Registry   *agents.Registry
	Queue      queue.Backend
}

// NewServerFromConfig constructs an ACP server from a Config.
func NewServerFromConfig(cfg Config) *Server {
	return NewServer(cfg.Port, cfg.Registry, cfg.Queue)
}

// NewServer constructs an ACP server that maps ROMA agents and queue jobs to
// ACP Agents and Threads respectively.
func NewServer(port int, registry *agents.Registry, queueBackend queue.Backend) *Server {
	if port <= 0 {
		port = DefaultPort
	}
	s := &Server{
		port:     port,
		registry: registry,
		queue:    queueBackend,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /acp/v1/agents", s.handleListAgents)
	mux.HandleFunc("POST /acp/v1/threads", s.handleCreateThread)
	mux.HandleFunc("GET /acp/v1/threads/{id}", s.handleGetThread)

	s.httpSrv = &http.Server{
		Addr:         fmt.Sprintf(":%d", port),
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
	}
	return s
}

// Port returns the configured listen port.
func (s *Server) Port() int {
	return s.port
}

// Start begins serving ACP requests and blocks until ctx is cancelled.
// A non-http.ErrServerClosed error is returned on unexpected failure.
func (s *Server) Start(ctx context.Context) error {
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.httpSrv.Shutdown(shutCtx); err != nil {
			log.Printf("acpserver shutdown error: %v", err)
		}
	}()

	log.Printf("acpserver listening on :%d", s.port)
	if err := s.httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("acpserver listen: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------------

// handleListAgents returns all registered ROMA agents as ACP AgentInfo records.
// TODO: when acp-go-sdk is available, delegate to its ListAgents handler.
func (s *Server) handleListAgents(w http.ResponseWriter, r *http.Request) {
	profiles := s.registry.List(r.Context())
	out := make([]AgentInfo, 0, len(profiles))
	for _, p := range profiles {
		out = append(out, AgentInfo{
			ID:   p.ID,
			Name: p.DisplayName,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// handleCreateThread accepts an ACP CreateThreadRequest and submits a ROMA
// queue job, returning the new Thread with status=pending.
// TODO: when acp-go-sdk is available, use its request parsing and response
//       helpers; map sdk.Thread <-> queue.Request.
func (s *Server) handleCreateThread(w http.ResponseWriter, r *http.Request) {
	var req CreateThreadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if req.AgentID == "" {
		writeError(w, http.StatusBadRequest, "agent_id is required")
		return
	}
	if _, ok := s.registry.Get(req.AgentID); !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("agent %q not found", req.AgentID))
		return
	}

	prompt := extractPrompt(req.Messages)
	jobID := fmt.Sprintf("acp_%d", time.Now().UTC().UnixNano())
	qReq := queue.Request{
		ID:           jobID,
		Prompt:       prompt,
		StarterAgent: req.AgentID,
		WorkingDir:   romapath.HomeDir(),
		Status:       queue.StatusPending,
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}
	if err := s.queue.Enqueue(r.Context(), qReq); err != nil {
		writeError(w, http.StatusInternalServerError, "enqueue failed: "+err.Error())
		return
	}

	thread := Thread{
		ID:        jobID,
		AgentID:   req.AgentID,
		Status:    ThreadStatusPending,
		CreatedAt: qReq.CreatedAt,
		UpdatedAt: qReq.UpdatedAt,
	}
	writeJSON(w, http.StatusCreated, thread)
}

// handleGetThread looks up a queue job by its ACP thread ID and returns the
// mapped Thread status.
// TODO: when acp-go-sdk is available, use sdk.Thread as the return type and
//       populate message history from session artifacts.
func (s *Server) handleGetThread(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing thread id")
		return
	}

	qReq, err := s.queue.Get(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("thread %q not found: %v", id, err))
		return
	}

	thread := Thread{
		ID:        qReq.ID,
		AgentID:   qReq.StarterAgent,
		Status:    queueStatusToThread(qReq.Status),
		CreatedAt: qReq.CreatedAt,
		UpdatedAt: qReq.UpdatedAt,
		Error:     qReq.Error,
	}
	writeJSON(w, http.StatusOK, thread)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func queueStatusToThread(s queue.Status) ThreadStatus {
	switch s {
	case queue.StatusPending:
		return ThreadStatusPending
	case queue.StatusRunning, queue.StatusAwaitingApproval:
		return ThreadStatusRunning
	case queue.StatusSucceeded:
		return ThreadStatusCompleted
	case queue.StatusFailed, queue.StatusRejected:
		return ThreadStatusFailed
	case queue.StatusCancelled:
		return ThreadStatusCancelled
	default:
		return ThreadStatusPending
	}
}

func extractPrompt(messages []Message) string {
	parts := make([]string, 0, len(messages))
	for _, m := range messages {
		if m.Role == "user" {
			parts = append(parts, m.Content)
		}
	}
	return strings.Join(parts, "\n")
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("acpserver: encode response: %v", err)
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
