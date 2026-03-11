package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/liliang/roma/internal/artifacts"
	"github.com/liliang/roma/internal/events"
	"github.com/liliang/roma/internal/history"
	"github.com/liliang/roma/internal/queue"
	"github.com/liliang/roma/internal/scheduler"
	"github.com/liliang/roma/internal/store"
	"github.com/liliang/roma/internal/taskstore"
)

// ErrUnavailable indicates the current environment does not permit local listeners.
var ErrUnavailable = errors.New("local api transport unavailable")

// Server exposes a minimal JSON API over a Unix domain socket.
type Server struct {
	httpServer   *http.Server
	metaPath     string
	network      string
	address      string
	socketPath   string
	queueStore   queue.Backend
	sessionStore history.Backend
}

// NewServer constructs the API server.
func NewServer(workDir string, queueStore queue.Backend, sessionStore history.Backend) *Server {
	socketPath := filepath.Join(workDir, ".roma", "run", "romad.sock")
	server := &Server{
		metaPath:     filepath.Join(workDir, ".roma", "run", "api.json"),
		socketPath:   socketPath,
		queueStore:   queueStore,
		sessionStore: sessionStore,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", server.handleHealth)
	mux.HandleFunc("/submit", server.handleSubmit)
	mux.HandleFunc("/artifacts", server.handleArtifactList)
	mux.HandleFunc("/artifacts/", server.handleArtifactShow)
	mux.HandleFunc("/events", server.handleEventList)
	mux.HandleFunc("/events/", server.handleEventShow)
	mux.HandleFunc("/queue", server.handleQueueList)
	mux.HandleFunc("/queue/", server.handleQueueShow)
	mux.HandleFunc("/queue-inspect/", server.handleQueueInspect)
	mux.HandleFunc("/sessions", server.handleSessionList)
	mux.HandleFunc("/sessions/", server.handleSessionShow)
	mux.HandleFunc("/tasks", server.handleTaskList)
	mux.HandleFunc("/tasks/", server.handleTaskShow)

	server.httpServer = &http.Server{
		Handler: mux,
	}
	return server
}

// Start begins serving on the Unix domain socket.
func (s *Server) Start(ctx context.Context) error {
	runDir := filepath.Dir(s.socketPath)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return fmt.Errorf("create socket directory: %w", err)
	}

	_ = os.Remove(s.socketPath)
	listener, err := net.Listen("unix", s.socketPath)
	if err == nil {
		s.network = "unix"
		s.address = s.socketPath
	} else {
		listener, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return fmt.Errorf("%w: %v", ErrUnavailable, err)
		}
		s.network = "tcp"
		s.address = listener.Addr().String()
	}
	if err := s.writeMeta(); err != nil {
		_ = listener.Close()
		return err
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.httpServer.Shutdown(shutdownCtx)
		_ = os.Remove(s.socketPath)
		_ = os.Remove(s.metaPath)
	}()

	go func() {
		_ = s.httpServer.Serve(listener)
	}()
	return nil
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) writeMeta() error {
	raw, err := json.MarshalIndent(map[string]string{
		"network": s.network,
		"address": s.address,
	}, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal api metadata: %w", err)
	}
	if err := os.WriteFile(s.metaPath, raw, 0o644); err != nil {
		return fmt.Errorf("write api metadata: %w", err)
	}
	return nil
}

func (s *Server) handleSubmit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req SubmitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	jobID := fmt.Sprintf("job_%d", time.Now().UTC().UnixNano())
	record := queue.Request{
		ID:           jobID,
		GraphFile:    req.GraphFile,
		Graph:        toQueueGraph(req.Graph),
		Prompt:       req.Prompt,
		StarterAgent: req.StarterAgent,
		Delegates:    req.Delegates,
		WorkingDir:   req.WorkingDir,
		Continuous:   req.Continuous,
		MaxRounds:    req.MaxRounds,
	}
	if err := s.queueStore.Enqueue(r.Context(), record); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusAccepted, SubmitResponse{JobID: jobID})
}

func toQueueGraph(in *GraphSubmitRequest) *queue.GraphSpec {
	if in == nil {
		return nil
	}
	nodes := make([]queue.GraphNode, 0, len(in.Nodes))
	for _, node := range in.Nodes {
		nodes = append(nodes, queue.GraphNode{
			ID:           node.ID,
			Title:        node.Title,
			Agent:        node.Agent,
			Strategy:     node.Strategy,
			Dependencies: node.Dependencies,
		})
	}
	return &queue.GraphSpec{
		Prompt: in.Prompt,
		Nodes:  nodes,
	}
}

func (s *Server) handleArtifactList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	workDir := filepath.Dir(filepath.Dir(filepath.Dir(s.metaPath)))
	store := preferredArtifactStore(workDir)
	items, err := store.List(r.Context(), r.URL.Query().Get("session"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) handleArtifactShow(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/artifacts/")
	if id == "" {
		http.Error(w, "missing artifact id", http.StatusBadRequest)
		return
	}
	workDir := filepath.Dir(filepath.Dir(filepath.Dir(s.metaPath)))
	store := preferredArtifactStore(workDir)
	item, err := store.Get(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) handleQueueList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	items, err := s.queueStore.List(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, QueueListResponse{Items: items})
}

func (s *Server) handleEventList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	workDir := filepath.Dir(filepath.Dir(filepath.Dir(s.metaPath)))
	eventStore := preferredEventStore(workDir)
	items, err := eventStore.ListEvents(r.Context(), store.EventFilter{
		SessionID: r.URL.Query().Get("session"),
		TaskID:    r.URL.Query().Get("task"),
		Type:      events.Type(r.URL.Query().Get("type")),
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, EventListResponse{Items: items})
}

func (s *Server) handleEventShow(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/events/")
	if id == "" {
		http.Error(w, "missing event id", http.StatusBadRequest)
		return
	}
	workDir := filepath.Dir(filepath.Dir(filepath.Dir(s.metaPath)))
	eventStore := preferredEventStore(workDir)
	items, err := eventStore.ListEvents(r.Context(), store.EventFilter{})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	for _, item := range items {
		if item.ID != id {
			continue
		}
		writeJSON(w, http.StatusOK, item)
		return
	}
	http.Error(w, "event not found", http.StatusNotFound)
}

func (s *Server) handleSessionList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	items, err := s.sessionStore.List(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, SessionListResponse{Items: items})
}

func (s *Server) handleQueueShow(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/queue/")
	if strings.HasSuffix(id, "/approve") {
		s.handleQueueApproval(w, r, strings.TrimSuffix(id, "/approve"), true)
		return
	}
	if strings.HasSuffix(id, "/reject") {
		s.handleQueueApproval(w, r, strings.TrimSuffix(id, "/reject"), false)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if id == "" {
		http.Error(w, "missing job id", http.StatusBadRequest)
		return
	}
	item, err := s.queueStore.Get(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) handleQueueApproval(w http.ResponseWriter, r *http.Request, id string, approved bool) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if id == "" {
		http.Error(w, "missing job id", http.StatusBadRequest)
		return
	}
	req, err := s.queueStore.Get(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if approved {
		req.PolicyOverride = true
		req.Status = queue.StatusPending
		req.Error = ""
	} else {
		req.PolicyOverride = false
		req.Status = queue.StatusRejected
		req.Error = "rejected by user"
	}
	if err := s.queueStore.Update(r.Context(), req); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if req.SessionID != "" {
		workDir := filepath.Dir(filepath.Dir(filepath.Dir(s.metaPath)))
		sessionStore := preferredHistoryStore(workDir)
		if session, err := sessionStore.Get(r.Context(), req.SessionID); err == nil {
			if approved {
				session.Status = "pending"
			} else {
				session.Status = "rejected"
			}
			session.UpdatedAt = time.Now().UTC()
			_ = sessionStore.Save(r.Context(), session)
		}
		eventStore := preferredEventStore(workDir)
		reason := "human_approved"
		if !approved {
			reason = "human_rejected"
		}
		_ = eventStore.AppendEvent(r.Context(), events.Record{
			ID:         "evt_" + req.ID + "_" + reason,
			SessionID:  req.SessionID,
			TaskID:     req.TaskID,
			Type:       events.TypePolicyDecisionRecorded,
			ActorType:  events.ActorTypeHuman,
			OccurredAt: time.Now().UTC(),
			ReasonCode: reason,
			Payload: map[string]any{
				"job_id":   req.ID,
				"approved": approved,
			},
		})
	}
	writeJSON(w, http.StatusOK, req)
}

func (s *Server) handleQueueInspect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/queue-inspect/")
	if id == "" {
		http.Error(w, "missing job id", http.StatusBadRequest)
		return
	}
	job, err := s.queueStore.Get(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	resp := QueueInspectResponse{Job: job}

	workDir := filepath.Dir(filepath.Dir(filepath.Dir(s.metaPath)))
	if job.SessionID != "" {
		if session, err := s.sessionStore.Get(r.Context(), job.SessionID); err == nil {
			resp.Session = &session
		}
		taskStore := preferredTaskStore(workDir)
		if items, err := taskStore.ListTasksBySession(r.Context(), job.SessionID); err == nil {
			resp.Tasks = items
		}
		eventStore := preferredEventStore(workDir)
		if items, err := eventStore.ListEvents(r.Context(), store.EventFilter{SessionID: job.SessionID}); err == nil {
			resp.Events = items
		}
		artifactStore := preferredArtifactStore(workDir)
		if items, err := artifactStore.List(r.Context(), job.SessionID); err == nil {
			resp.Artifacts = items
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleSessionShow(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/sessions/")
	if id == "" {
		http.Error(w, "missing session id", http.StatusBadRequest)
		return
	}
	item, err := s.sessionStore.Get(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) handleTaskList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	workDir := filepath.Dir(filepath.Dir(filepath.Dir(s.metaPath)))
	taskStore := preferredTaskStore(workDir)
	items, err := taskStore.ListTasksBySession(r.Context(), r.URL.Query().Get("session"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, TaskListResponse{Items: items})
}

func (s *Server) handleTaskShow(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/tasks/")
	if strings.HasSuffix(id, "/approve") {
		s.handleTaskApproval(w, r, strings.TrimSuffix(id, "/approve"), true)
		return
	}
	if strings.HasSuffix(id, "/reject") {
		s.handleTaskApproval(w, r, strings.TrimSuffix(id, "/reject"), false)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if id == "" {
		http.Error(w, "missing task id", http.StatusBadRequest)
		return
	}
	workDir := filepath.Dir(filepath.Dir(filepath.Dir(s.metaPath)))
	taskStore := preferredTaskStore(workDir)
	item, err := taskStore.GetTask(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) handleTaskApproval(w http.ResponseWriter, r *http.Request, id string, approved bool) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if id == "" {
		http.Error(w, "missing task id", http.StatusBadRequest)
		return
	}
	workDir := filepath.Dir(filepath.Dir(filepath.Dir(s.metaPath)))
	taskStore := preferredTaskStore(workDir)
	eventStore := preferredEventStore(workDir)
	lifecycle := scheduler.NewGraphLifecycle(taskStore, eventStore)
	var err error
	if approved {
		err = lifecycle.ApproveTask(r.Context(), id)
	} else {
		err = lifecycle.RejectTask(r.Context(), id)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	item, err := taskStore.GetTask(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	sessionStore := preferredHistoryStore(workDir)
	if session, err := sessionStore.Get(r.Context(), item.SessionID); err == nil {
		if approved {
			session.Status = "running"
		} else {
			session.Status = "failed"
		}
		session.UpdatedAt = time.Now().UTC()
		_ = sessionStore.Save(r.Context(), session)
	}
	requests, err := s.queueStore.List(r.Context())
	if err == nil {
		for _, req := range requests {
			if req.SessionID != item.SessionID || req.Status != queue.StatusAwaitingApproval {
				continue
			}
			if approved {
				req.Status = queue.StatusPending
				req.Error = ""
			} else {
				req.Status = queue.StatusRejected
				req.Error = "task approval rejected"
			}
			_ = s.queueStore.Update(r.Context(), req)
		}
	}
	writeJSON(w, http.StatusOK, item)
}

func preferredEventStore(workDir string) store.EventStore {
	sqliteStore, err := store.NewSQLiteEventStore(workDir)
	if err == nil {
		return sqliteStore
	}
	return store.NewFileEventStore(workDir)
}

func preferredHistoryStore(workDir string) history.Backend {
	sqliteStore, err := history.NewSQLiteStore(workDir)
	if err == nil {
		return sqliteStore
	}
	return history.NewStore(workDir)
}

func preferredTaskStore(workDir string) store.TaskStore {
	sqliteStore, err := taskstore.NewSQLiteStore(workDir)
	if err == nil {
		return sqliteStore
	}
	return taskstore.NewStore(workDir)
}

func preferredArtifactStore(workDir string) artifacts.Backend {
	sqliteStore, err := artifacts.NewSQLiteStore(workDir)
	if err == nil {
		return sqliteStore
	}
	return artifacts.NewFileStore(workDir)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
