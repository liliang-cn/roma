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

	"github.com/liliang-cn/roma/internal/artifacts"
	"github.com/liliang-cn/roma/internal/curia"
	"github.com/liliang-cn/roma/internal/domain"
	"github.com/liliang-cn/roma/internal/events"
	"github.com/liliang-cn/roma/internal/history"
	"github.com/liliang-cn/roma/internal/plans"
	"github.com/liliang-cn/roma/internal/policy"
	"github.com/liliang-cn/roma/internal/queue"
	"github.com/liliang-cn/roma/internal/romapath"
	"github.com/liliang-cn/roma/internal/scheduler"
	"github.com/liliang-cn/roma/internal/sqliteutil"
	"github.com/liliang-cn/roma/internal/store"
	"github.com/liliang-cn/roma/internal/syncdb"
	"github.com/liliang-cn/roma/internal/taskstore"
	workspacepkg "github.com/liliang-cn/roma/internal/workspace"
)

// ErrUnavailable indicates the current environment does not permit local listeners.
var ErrUnavailable = errors.New("local api transport unavailable")

// QueueCanceler interrupts a daemon-managed queue job.
type QueueCanceler interface {
	CancelQueueJob(ctx context.Context, id string) (queue.Request, error)
}

// Server exposes a minimal JSON API over a Unix domain socket.
type Server struct {
	httpServer   *http.Server
	metaPath     string
	network      string
	address      string
	socketPath   string
	queueStore   queue.Backend
	sessionStore history.Backend
	canceler     QueueCanceler
}

// NewServer constructs the API server.
func NewServer(workDir string, queueStore queue.Backend, sessionStore history.Backend) *Server {
	socketPath := romapath.Join(workDir, "run", "romad.sock")
	server := &Server{
		metaPath:     romapath.Join(workDir, "run", "api.json"),
		socketPath:   socketPath,
		queueStore:   queueStore,
		sessionStore: sessionStore,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", server.handleHealth)
	mux.HandleFunc("/status", server.handleStatus)
	mux.HandleFunc("/submit", server.handleSubmit)
	mux.HandleFunc("/artifacts", server.handleArtifactList)
	mux.HandleFunc("/artifacts/", server.handleArtifactShow)
	mux.HandleFunc("/curia/reputation", server.handleCuriaReputation)
	mux.HandleFunc("/events", server.handleEventList)
	mux.HandleFunc("/events/", server.handleEventShow)
	mux.HandleFunc("/queue", server.handleQueueList)
	mux.HandleFunc("/queue/", server.handleQueueShow)
	mux.HandleFunc("/queue-inspect/", server.handleQueueInspect)
	mux.HandleFunc("/recovery", server.handleRecoveryList)
	mux.HandleFunc("/plans/", server.handlePlanShow)
	mux.HandleFunc("/plans/apply", server.handlePlanApply)
	mux.HandleFunc("/plans/inbox", server.handlePlanInbox)
	mux.HandleFunc("/plans/preview", server.handlePlanPreview)
	mux.HandleFunc("/plans/rollback", server.handlePlanRollback)
	mux.HandleFunc("/sessions", server.handleSessionList)
	mux.HandleFunc("/sessions/", server.handleSessionShow)
	mux.HandleFunc("/session-inspect/", server.handleSessionInspect)
	mux.HandleFunc("/tasks", server.handleTaskList)
	mux.HandleFunc("/tasks/", server.handleTaskShow)
	mux.HandleFunc("/workspaces", server.handleWorkspaceList)
	mux.HandleFunc("/workspaces/", server.handleWorkspaceShow)

	server.httpServer = &http.Server{
		Handler: mux,
	}
	return server
}

// SetQueueCanceler attaches an optional daemon-owned queue canceler.
func (s *Server) SetQueueCanceler(canceler QueueCanceler) {
	s.canceler = canceler
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

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	workDir := filepath.Dir(filepath.Dir(filepath.Dir(s.metaPath)))
	queueItems, _ := s.queueStore.List(r.Context())
	sessionItems, _ := s.sessionStore.List(r.Context())
	artifactItems, _ := preferredArtifactStore(workDir).List(r.Context(), "")
	eventItems, _ := preferredEventStore(workDir).ListEvents(r.Context(), store.EventFilter{})
	activeLeases := 0
	releasedLeases := 0
	recoveredLeases := 0
	pendingApprovalTasks := 0
	if leaseStore, err := scheduler.NewLeaseStore(workDir); err == nil {
		if items, err := leaseStore.ListByStatus(r.Context(), scheduler.LeaseStatusActive); err == nil {
			activeLeases = len(items)
			for _, item := range items {
				pendingApprovalTasks += len(item.PendingApprovalTaskIDs)
			}
		}
		if items, err := leaseStore.ListByStatus(r.Context(), scheduler.LeaseStatusRecovered); err == nil {
			recoveredLeases = len(items)
			for _, item := range items {
				pendingApprovalTasks += len(item.PendingApprovalTaskIDs)
			}
		}
		if items, err := leaseStore.ListByStatus(r.Context(), scheduler.LeaseStatusReleased); err == nil {
			releasedLeases = len(items)
		}
	}
	preparedWorkspaces := 0
	releasedWorkspaces := 0
	reclaimedWorkspaces := 0
	mergedWorkspaces := 0
	if items, err := workspacepkg.NewManager(workDir, nil).List(r.Context()); err == nil {
		for _, item := range items {
			switch item.Status {
			case "prepared":
				preparedWorkspaces++
			case "released":
				releasedWorkspaces++
			case "reclaimed":
				reclaimedWorkspaces++
			case "merged":
				mergedWorkspaces++
			}
		}
	}
	recoverableSessions := 0
	if items, err := scheduler.RecoverableSessions(r.Context(), workDir); err == nil {
		recoverableSessions = len(items)
	}
	sqlitePath := sqliteutil.DBPath(workDir)
	sqliteEnabled := false
	sqliteBytes := int64(0)
	if info, err := os.Stat(sqlitePath); err == nil {
		sqliteEnabled = true
		sqliteBytes = info.Size()
	}
	writeJSON(w, http.StatusOK, StatusResponse{
		QueueItems:           len(queueItems),
		Sessions:             len(sessionItems),
		Artifacts:            len(artifactItems),
		Events:               len(eventItems),
		ActiveLeases:         activeLeases,
		ReleasedLeases:       releasedLeases,
		RecoveredLeases:      recoveredLeases,
		PendingApprovalTasks: pendingApprovalTasks,
		RecoverableSessions:  recoverableSessions,
		PreparedWorkspaces:   preparedWorkspaces,
		ReleasedWorkspaces:   releasedWorkspaces,
		ReclaimedWorkspaces:  reclaimedWorkspaces,
		MergedWorkspaces:     mergedWorkspaces,
		SQLiteEnabled:        sqliteEnabled,
		SQLitePath:           sqlitePath,
		SQLiteBytes:          sqliteBytes,
	})
}

func (s *Server) handleCuriaReputation(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	workDir := filepath.Dir(filepath.Dir(filepath.Dir(s.metaPath)))
	store := curia.NewReputationStore(workDir)
	items, err := store.List(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	reviewer := strings.TrimSpace(r.URL.Query().Get("reviewer"))
	if reviewer != "" {
		filtered := make([]curia.ReputationRecord, 0, 1)
		for _, item := range items {
			if item.AgentID == reviewer {
				filtered = append(filtered, item)
			}
		}
		items = filtered
	}
	writeJSON(w, http.StatusOK, CuriaReputationResponse{Items: items})
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
		ID:                  jobID,
		GraphFile:           req.GraphFile,
		Graph:               toQueueGraph(req.Graph),
		Prompt:              req.Prompt,
		StarterAgent:        req.StarterAgent,
		Delegates:           req.Delegates,
		WorkingDir:          req.WorkingDir,
		PolicyOverride:      req.PolicyOverride,
		PolicyOverrideActor: req.PolicyOverrideActor,
		Continuous:          req.Continuous,
		MaxRounds:           req.MaxRounds,
	}
	if record.PolicyOverride && record.PolicyOverrideActor == "" {
		record.PolicyOverrideActor = policy.OverrideActor()
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
			ID:              node.ID,
			Title:           node.Title,
			Agent:           node.Agent,
			Strategy:        node.Strategy,
			Dependencies:    node.Dependencies,
			Senators:        node.Senators,
			Quorum:          node.Quorum,
			ArbitrationMode: node.ArbitrationMode,
			Arbitrator:      node.Arbitrator,
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

func (s *Server) handleRecoveryList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	workDir := filepath.Dir(filepath.Dir(filepath.Dir(s.metaPath)))
	if err := syncWorkspaceMetadata(r.Context(), workDir); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	items, err := scheduler.RecoverableSessions(r.Context(), workDir)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, RecoveryListResponse{Items: items})
}

func (s *Server) handlePlanShow(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/plans/")
	if strings.HasSuffix(id, "/approve") {
		s.handlePlanDecision(w, r, strings.TrimSuffix(id, "/approve"), true)
		return
	}
	if strings.HasSuffix(id, "/reject") {
		s.handlePlanDecision(w, r, strings.TrimSuffix(id, "/reject"), false)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if id == "" {
		http.Error(w, "missing artifact id", http.StatusBadRequest)
		return
	}
	workDir := filepath.Dir(filepath.Dir(filepath.Dir(s.metaPath)))
	service := plans.NewService(preferredArtifactStore(workDir), workspacepkg.NewManager(workDir, preferredEventStore(workDir)), preferredEventStore(workDir))
	envelope, _, err := service.Inspect(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, PlanInspectResponse{Artifact: envelope})
}

func (s *Server) handlePlanInbox(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	workDir := filepath.Dir(filepath.Dir(filepath.Dir(s.metaPath)))
	service := plans.NewService(preferredArtifactStore(workDir), workspacepkg.NewManager(workDir, preferredEventStore(workDir)), preferredEventStore(workDir))
	items, err := service.Inbox(r.Context(), r.URL.Query().Get("session"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out := make([]PlanInboxEntry, 0, len(items))
	for _, item := range items {
		out = append(out, PlanInboxEntry{
			ArtifactID:            item.ArtifactID,
			SessionID:             item.SessionID,
			TaskID:                item.TaskID,
			Goal:                  item.Goal,
			Status:                item.Status,
			HumanApprovalRequired: item.HumanApprovalRequired,
			ExpectedFiles:         item.ExpectedFiles,
			ForbiddenPaths:        item.ForbiddenPaths,
			LastEventType:         item.LastEventType,
			LastReason:            item.LastReason,
			LastOccurredAt:        item.LastOccurredAt,
			Violations:            item.Violations,
			Conflict:              item.Conflict,
			ConflictDetail:        item.ConflictDetail,
			ConflictPaths:         item.ConflictPaths,
			ConflictContext:       item.ConflictContext,
			RemediationHint:       item.RemediationHint,
		})
	}
	writeJSON(w, http.StatusOK, PlanInboxResponse{Items: out})
}

func (s *Server) handlePlanPreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req PlanApplyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.ArtifactID == "" {
		http.Error(w, "artifact_id is required", http.StatusBadRequest)
		return
	}
	workDir := filepath.Dir(filepath.Dir(filepath.Dir(s.metaPath)))
	service := plans.NewService(preferredArtifactStore(workDir), workspacepkg.NewManager(workDir, preferredEventStore(workDir)), preferredEventStore(workDir))
	result, err := service.Preview(r.Context(), req.SessionID, req.TaskID, req.ArtifactID)
	if err != nil {
		writePlanError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handlePlanDecision(w http.ResponseWriter, r *http.Request, artifactID string, approved bool) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if artifactID == "" {
		http.Error(w, "missing artifact id", http.StatusBadRequest)
		return
	}
	var req PlanDecisionRequest
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.Actor == "" {
		req.Actor = policy.OverrideActor()
	}
	workDir := filepath.Dir(filepath.Dir(filepath.Dir(s.metaPath)))
	service := plans.NewService(preferredArtifactStore(workDir), workspacepkg.NewManager(workDir, preferredEventStore(workDir)), preferredEventStore(workDir))
	var err error
	if approved {
		err = service.Approve(r.Context(), artifactID, req.Actor)
	} else {
		err = service.Reject(r.Context(), artifactID, req.Actor)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"artifact_id": artifactID,
		"approved":    approved,
		"actor":       req.Actor,
	})
}

func (s *Server) handlePlanApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req PlanApplyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.ArtifactID == "" {
		http.Error(w, "artifact_id is required", http.StatusBadRequest)
		return
	}
	if req.PolicyOverride && req.PolicyOverrideActor == "" {
		req.PolicyOverrideActor = policy.OverrideActor()
	}
	workDir := filepath.Dir(filepath.Dir(filepath.Dir(s.metaPath)))
	service := plans.NewService(preferredArtifactStore(workDir), workspacepkg.NewManager(workDir, preferredEventStore(workDir)), preferredEventStore(workDir))
	result, err := service.Apply(r.Context(), req.SessionID, req.TaskID, req.ArtifactID, plans.ApplyOptions{
		DryRun:              req.DryRun,
		PolicyOverride:      req.PolicyOverride,
		PolicyOverrideActor: req.PolicyOverrideActor,
	})
	if err != nil {
		writePlanError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handlePlanRollback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req PlanApplyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.ArtifactID == "" {
		http.Error(w, "artifact_id is required", http.StatusBadRequest)
		return
	}
	workDir := filepath.Dir(filepath.Dir(filepath.Dir(s.metaPath)))
	service := plans.NewService(preferredArtifactStore(workDir), workspacepkg.NewManager(workDir, preferredEventStore(workDir)), preferredEventStore(workDir))
	result, err := service.Rollback(r.Context(), req.SessionID, req.TaskID, req.ArtifactID)
	if err != nil {
		writePlanError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func writePlanError(w http.ResponseWriter, err error) {
	switch {
	case plans.IsApplyErrorKind(err, plans.ErrorKindApprovalRequired):
		http.Error(w, err.Error(), http.StatusConflict)
	case plans.IsApplyErrorKind(err, plans.ErrorKindOverrideForbidden):
		http.Error(w, err.Error(), http.StatusForbidden)
	case plans.IsApplyErrorKind(err, plans.ErrorKindValidation):
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
	case plans.IsApplyErrorKind(err, plans.ErrorKindConflict):
		http.Error(w, err.Error(), http.StatusConflict)
	case plans.IsApplyErrorKind(err, plans.ErrorKindCheckFailed):
		http.Error(w, err.Error(), http.StatusConflict)
	default:
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func summarizePlanActions(items []events.Record) []PlanActionSummary {
	out := make([]PlanActionSummary, 0)
	for _, item := range items {
		switch item.Type {
		case events.TypePlanApplied, events.TypePlanRolledBack, events.TypePlanApplyRejected:
		default:
			continue
		}
		summary := PlanActionSummary{
			EventType:  string(item.Type),
			TaskID:     item.TaskID,
			Reason:     item.ReasonCode,
			OccurredAt: item.OccurredAt.Format(time.RFC3339),
		}
		if value, ok := item.Payload["artifact_id"].(string); ok {
			summary.ArtifactID = value
		}
		if values, ok := stringSlicePayload(item.Payload, "changed_paths"); ok {
			summary.ChangedPaths = values
		}
		if values, ok := stringSlicePayload(item.Payload, "violations"); ok {
			summary.Violations = values
		}
		if values, ok := stringSlicePayload(item.Payload, "required_checks"); ok {
			summary.RequiredChecks = values
		}
		if value, ok := item.Payload["conflict"].(bool); ok {
			summary.Conflict = value
		}
		if value, ok := item.Payload["conflict_detail"].(string); ok {
			summary.ConflictDetail = value
		}
		if values, ok := stringSlicePayload(item.Payload, "conflict_paths"); ok {
			summary.ConflictPaths = values
		}
		if items, ok := conflictContextPayload(item.Payload, "conflict_context"); ok {
			summary.ConflictContext = items
		}
		out = append(out, summary)
	}
	return out
}

func conflictContextPayload(payload map[string]any, key string) ([]workspacepkg.ConflictSnippet, bool) {
	if payload == nil {
		return nil, false
	}
	value, ok := payload[key]
	if !ok {
		return nil, false
	}
	switch typed := value.(type) {
	case []workspacepkg.ConflictSnippet:
		return append([]workspacepkg.ConflictSnippet(nil), typed...), true
	case []any:
		out := make([]workspacepkg.ConflictSnippet, 0, len(typed))
		for _, item := range typed {
			entry, ok := item.(map[string]any)
			if !ok {
				continue
			}
			path, _ := entry["path"].(string)
			snippet, _ := entry["snippet"].(string)
			if path == "" && snippet == "" {
				continue
			}
			out = append(out, workspacepkg.ConflictSnippet{Path: path, Snippet: snippet})
		}
		return out, len(out) > 0
	default:
		return nil, false
	}
}

func stringSlicePayload(payload map[string]any, key string) ([]string, bool) {
	if payload == nil {
		return nil, false
	}
	value, ok := payload[key]
	if !ok {
		return nil, false
	}
	switch typed := value.(type) {
	case []string:
		return typed, true
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			text, ok := item.(string)
			if ok {
				out = append(out, text)
			}
		}
		return out, true
	default:
		return nil, false
	}
}

func summarizeCuriaArtifacts(workDir string, items []domain.ArtifactEnvelope) *CuriaSummary {
	var latestDebate *artifacts.DebateLogPayload
	var latestDecision *artifacts.DecisionPackPayload
	for _, item := range items {
		switch item.Kind {
		case domain.ArtifactKindDebateLog:
			if payload, ok := artifacts.DebateLogFromEnvelope(item); ok {
				value := payload
				latestDebate = &value
			}
		case domain.ArtifactKindDecisionPack:
			if payload, ok := artifacts.DecisionPackFromEnvelope(item); ok {
				value := payload
				latestDecision = &value
			}
		}
	}
	if latestDebate == nil && latestDecision == nil {
		return nil
	}
	out := &CuriaSummary{}
	if latestDebate != nil {
		out.Dispute = latestDebate.DisputeDetected
		out.DisputeClass = latestDebate.DisputeClass
		out.CriticalVeto = latestDebate.CriticalVeto
		out.TopScoreGap = latestDebate.TopScoreGap
		out.DisputeReasons = append([]string(nil), latestDebate.DisputeReasons...)
		for _, item := range latestDebate.Scoreboard {
			out.Scoreboard = append(out.Scoreboard, CuriaScoreSummary{
				ProposalID:    item.ProposalID,
				RawScore:      item.RawScore,
				WeightedScore: item.WeightedScore,
				VetoCount:     item.VetoCount,
				ReviewerCount: item.ReviewerCount,
			})
		}
	}
	if latestDecision != nil {
		out.WinningMode = latestDecision.WinningMode
		out.Arbitrated = latestDecision.Arbitrated
		out.ArbitratorID = latestDecision.ArbitratorID
		out.SelectedProposalIDs = append([]string(nil), latestDecision.SelectedProposalIDs...)
		out.RiskFlags = append([]string(nil), latestDecision.RiskFlags...)
		out.ReviewQuestions = append([]string(nil), latestDecision.ReviewQuestions...)
		out.CandidateSummaries = append([]artifacts.CuriaCandidateSummary(nil), latestDecision.CandidateSummaries...)
		out.ReviewerBreakdown = append([]artifacts.CuriaReviewContribution(nil), latestDecision.ReviewerBreakdown...)
		out.ReviewerWeights = summarizeCuriaReviewerWeights(workDir, latestDecision.ReviewerBreakdown)
		if len(out.Scoreboard) == 0 {
			for _, item := range latestDecision.Scoreboard {
				out.Scoreboard = append(out.Scoreboard, CuriaScoreSummary{
					ProposalID:    item.ProposalID,
					RawScore:      item.RawScore,
					WeightedScore: item.WeightedScore,
					VetoCount:     item.VetoCount,
					ReviewerCount: item.ReviewerCount,
				})
			}
		}
	}
	return out
}

func summarizeCuriaReviewerWeights(workDir string, items []artifacts.CuriaReviewContribution) []CuriaReviewerSummary {
	if workDir == "" || len(items) == 0 {
		return nil
	}
	store := curia.NewReputationStore(workDir)
	if store == nil {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]CuriaReviewerSummary, 0, len(items))
	for _, item := range items {
		if item.ReviewerID == "" {
			continue
		}
		if _, ok := seen[item.ReviewerID]; ok {
			continue
		}
		seen[item.ReviewerID] = struct{}{}
		record, ok, err := store.Get(context.Background(), item.ReviewerID)
		if err != nil {
			continue
		}
		if ok {
			out = append(out, CuriaReviewerSummary{
				ReviewerID:       item.ReviewerID,
				EffectiveWeight:  record.EffectiveWeight,
				ReviewCount:      record.ReviewCount,
				AlignmentCount:   record.AlignmentCount,
				VetoCount:        record.VetoCount,
				ArbitrationCount: record.ArbitrationCount,
			})
			continue
		}
		out = append(out, CuriaReviewerSummary{
			ReviewerID:      item.ReviewerID,
			EffectiveWeight: store.EffectiveWeight(context.Background(), domain.AgentProfile{ID: item.ReviewerID}),
		})
	}
	return out
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
	if strings.HasSuffix(id, "/cancel") {
		s.handleQueueCancel(w, r, strings.TrimSuffix(id, "/cancel"))
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

func (s *Server) handleQueueCancel(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if id == "" {
		http.Error(w, "missing job id", http.StatusBadRequest)
		return
	}
	if s.canceler != nil {
		item, err := s.canceler.CancelQueueJob(r.Context(), id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, item)
		return
	}

	req, err := s.queueStore.Get(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	workDir := filepath.Dir(filepath.Dir(filepath.Dir(s.metaPath)))
	if err := syncWorkspaceMetadata(r.Context(), workDir); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	req.Status = queue.StatusCancelled
	req.Error = "cancelled by user"
	req.PolicyOverride = false
	req.PolicyOverrideActor = ""
	if err := s.queueStore.Update(r.Context(), req); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if req.SessionID != "" {
		_ = s.updateSessionStatus(r.Context(), workDir, req.SessionID, "cancelled")
		eventStore := preferredEventStore(workDir)
		_ = eventStore.AppendEvent(r.Context(), events.Record{
			ID:         "evt_" + req.ID + "_cancelled",
			SessionID:  req.SessionID,
			TaskID:     req.TaskID,
			Type:       events.TypeQueueCancelled,
			ActorType:  events.ActorTypeHuman,
			OccurredAt: time.Now().UTC(),
			ReasonCode: "manual_cancel",
			Payload: map[string]any{
				"job_id": req.ID,
			},
		})
	}
	writeJSON(w, http.StatusOK, req)
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
	workDir := filepath.Dir(filepath.Dir(filepath.Dir(s.metaPath)))
	if err := syncWorkspaceMetadata(r.Context(), workDir); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	actor := policy.OverrideActor()
	taskStore := preferredTaskStore(workDir)
	eventStore := preferredEventStore(workDir)
	if approved {
		if handled, queueReq, err := s.handleQueueTaskApproval(r.Context(), workDir, req, taskStore, eventStore, true); handled {
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			writeJSON(w, http.StatusOK, queueReq)
			return
		}
	} else {
		if handled, queueReq, err := s.handleQueueTaskApproval(r.Context(), workDir, req, taskStore, eventStore, false); handled {
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			writeJSON(w, http.StatusOK, queueReq)
			return
		}
	}
	if approved {
		req.PolicyOverride = true
		req.PolicyOverrideActor = actor
		req.Status = queue.StatusPending
		req.Error = ""
	} else {
		req.PolicyOverride = false
		req.PolicyOverrideActor = ""
		req.Status = queue.StatusRejected
		req.Error = "rejected by user"
	}
	if err := s.queueStore.Update(r.Context(), req); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if req.SessionID != "" {
		if approved {
			_ = s.updateSessionStatus(r.Context(), workDir, req.SessionID, "pending")
		} else {
			_ = s.updateSessionStatus(r.Context(), workDir, req.SessionID, "rejected")
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
				"actor":    actor,
			},
		})
	}
	writeJSON(w, http.StatusOK, req)
}

func (s *Server) handleQueueTaskApproval(ctx context.Context, workDir string, req queue.Request, taskStore store.TaskStore, eventStore store.EventStore, approved bool) (bool, queue.Request, error) {
	if req.SessionID == "" {
		return false, req, nil
	}
	leaseStore, err := scheduler.NewLeaseStore(workDir)
	if err != nil {
		return false, req, nil
	}
	lease, err := leaseStore.Get(ctx, req.SessionID)
	if err != nil || len(lease.PendingApprovalTaskIDs) == 0 {
		return false, req, nil
	}
	lifecycle := scheduler.NewGraphLifecycle(taskStore, eventStore)
	for _, taskID := range lease.PendingApprovalTaskIDs {
		if approved {
			if err := lifecycle.ApproveTask(ctx, taskID); err != nil {
				return true, req, err
			}
		} else {
			if err := lifecycle.RejectTask(ctx, taskID); err != nil {
				return true, req, err
			}
		}
	}
	if err := leaseStore.UpdatePendingApprovalTaskIDs(ctx, req.SessionID, nil); err != nil {
		return true, req, err
	}
	reason := "human_approved"
	if !approved {
		reason = "human_rejected"
	}
	actor := policy.OverrideActor()
	_ = eventStore.AppendEvent(ctx, events.Record{
		ID:         "evt_" + req.ID + "_" + reason,
		SessionID:  req.SessionID,
		TaskID:     req.TaskID,
		Type:       events.TypePolicyDecisionRecorded,
		ActorType:  events.ActorTypeHuman,
		OccurredAt: time.Now().UTC(),
		ReasonCode: reason,
		Payload: map[string]any{
			"job_id":                    req.ID,
			"approved":                  approved,
			"actor":                     actor,
			"pending_approval_task_ids": lease.PendingApprovalTaskIDs,
		},
	})
	_ = eventStore.AppendEvent(ctx, events.Record{
		ID:         "evt_" + req.SessionID + "_lease_" + fmt.Sprintf("%d", time.Now().UTC().UnixNano()),
		SessionID:  req.SessionID,
		Type:       events.TypeSchedulerLeaseRecorded,
		ActorType:  events.ActorTypeScheduler,
		OccurredAt: time.Now().UTC(),
		ReasonCode: string(lease.Status),
		Payload: map[string]any{
			"owner_id":                  lease.OwnerID,
			"status":                    lease.Status,
			"ready_task_ids":            lease.ReadyTaskIDs,
			"workspace_refs":            lease.WorkspaceRefs,
			"pending_approval_task_ids": []string{},
			"completed_task_ids":        lease.CompletedTaskIDs,
		},
	})
	if approved {
		_ = s.updateSessionStatus(ctx, workDir, req.SessionID, "running")
	} else {
		_ = s.updateSessionStatus(ctx, workDir, req.SessionID, "rejected")
	}
	if approved {
		req.Status = queue.StatusPending
		req.Error = ""
	} else {
		req.Status = queue.StatusRejected
		req.Error = "task approval rejected"
	}
	req.PolicyOverride = false
	req.PolicyOverrideActor = ""
	if err := s.queueStore.Update(ctx, req); err != nil {
		return true, req, err
	}
	return true, req, nil
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
	resp := QueueInspectResponse{Job: job, ApprovalResumeReady: true}

	workDir := filepath.Dir(filepath.Dir(filepath.Dir(s.metaPath)))
	if job.SessionID != "" {
		if session, err := s.sessionStore.Get(r.Context(), job.SessionID); err == nil {
			resp.Session = &session
		}
		if leaseStore, err := scheduler.NewLeaseStore(workDir); err == nil {
			if lease, err := leaseStore.Get(r.Context(), job.SessionID); err == nil {
				resp.Lease = &lease
				resp.PendingApprovalTaskIDs = append(resp.PendingApprovalTaskIDs, lease.PendingApprovalTaskIDs...)
				resp.ApprovalResumeReady = len(lease.PendingApprovalTaskIDs) == 0
			}
		}
		taskStore := preferredTaskStore(workDir)
		if items, err := taskStore.ListTasksBySession(r.Context(), job.SessionID); err == nil {
			resp.Tasks = items
		}
		eventStore := preferredEventStore(workDir)
		if items, err := eventStore.ListEvents(r.Context(), store.EventFilter{SessionID: job.SessionID}); err == nil {
			resp.Events = items
			resp.Plans = summarizePlanActions(items)
		}
		artifactStore := preferredArtifactStore(workDir)
		if items, err := artifactStore.List(r.Context(), job.SessionID); err == nil {
			resp.Artifacts = items
			resp.Curia = summarizeCuriaArtifacts(workDir, items)
		}
		if items, err := workspacepkg.NewManager(workDir, nil).List(r.Context()); err == nil {
			for _, item := range items {
				if item.SessionID == job.SessionID {
					resp.Workspaces = append(resp.Workspaces, item)
				}
			}
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

func (s *Server) handleSessionInspect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/session-inspect/")
	if id == "" {
		http.Error(w, "missing session id", http.StatusBadRequest)
		return
	}
	workDir := filepath.Dir(filepath.Dir(filepath.Dir(s.metaPath)))
	session, err := s.sessionStore.Get(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	resp := SessionInspectResponse{Session: session, ApprovalResumeReady: true}
	if leaseStore, err := scheduler.NewLeaseStore(workDir); err == nil {
		if lease, err := leaseStore.Get(r.Context(), id); err == nil {
			resp.Lease = &lease
			resp.PendingApprovalTaskIDs = append(resp.PendingApprovalTaskIDs, lease.PendingApprovalTaskIDs...)
			resp.ApprovalResumeReady = len(lease.PendingApprovalTaskIDs) == 0
		}
	}
	taskStore := preferredTaskStore(workDir)
	if items, err := taskStore.ListTasksBySession(r.Context(), id); err == nil {
		resp.Tasks = items
	}
	eventStore := preferredEventStore(workDir)
	if items, err := eventStore.ListEvents(r.Context(), store.EventFilter{SessionID: id}); err == nil {
		resp.Events = items
		resp.Plans = summarizePlanActions(items)
	}
	artifactStore := preferredArtifactStore(workDir)
	if items, err := artifactStore.List(r.Context(), id); err == nil {
		resp.Artifacts = items
		resp.Curia = summarizeCuriaArtifacts(workDir, items)
	}
	if items, err := workspacepkg.NewManager(workDir, nil).List(r.Context()); err == nil {
		for _, item := range items {
			if item.SessionID == id {
				resp.Workspaces = append(resp.Workspaces, item)
			}
		}
	}
	writeJSON(w, http.StatusOK, resp)
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
	if leaseStore, err := scheduler.NewLeaseStore(workDir); err == nil {
		if lease, err := leaseStore.Get(r.Context(), item.SessionID); err == nil {
			pending := make([]string, 0, len(lease.PendingApprovalTaskIDs))
			for _, taskID := range lease.PendingApprovalTaskIDs {
				if taskID == id {
					continue
				}
				pending = append(pending, taskID)
			}
			if err := leaseStore.UpdatePendingApprovalTaskIDs(r.Context(), item.SessionID, pending); err == nil {
				_ = eventStore.AppendEvent(r.Context(), events.Record{
					ID:         "evt_" + item.SessionID + "_lease_" + fmt.Sprintf("%d", time.Now().UTC().UnixNano()),
					SessionID:  item.SessionID,
					Type:       events.TypeSchedulerLeaseRecorded,
					ActorType:  events.ActorTypeScheduler,
					OccurredAt: time.Now().UTC(),
					ReasonCode: string(lease.Status),
					Payload: map[string]any{
						"owner_id":                  lease.OwnerID,
						"status":                    lease.Status,
						"ready_task_ids":            lease.ReadyTaskIDs,
						"workspace_refs":            lease.WorkspaceRefs,
						"pending_approval_task_ids": pending,
						"completed_task_ids":        lease.CompletedTaskIDs,
					},
				})
			}
		}
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

func (s *Server) handleWorkspaceList(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost && strings.TrimSuffix(r.URL.Path, "/") == "/workspaces" {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	workDir := filepath.Dir(filepath.Dir(filepath.Dir(s.metaPath)))
	items, err := workspacepkg.NewManager(workDir, nil).List(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, WorkspaceListResponse{Items: items})
}

func (s *Server) handleWorkspaceShow(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/workspaces/")
	if rest == "cleanup" {
		s.handleWorkspaceCleanup(w, r)
		return
	}
	if strings.HasSuffix(rest, "/merge") {
		s.handleWorkspaceMerge(w, r, strings.TrimSuffix(rest, "/merge"))
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	parts := strings.Split(rest, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		http.Error(w, "workspace path must be /workspaces/{session}/{task}", http.StatusBadRequest)
		return
	}
	workDir := filepath.Dir(filepath.Dir(filepath.Dir(s.metaPath)))
	item, err := workspacepkg.NewManager(workDir, nil).Get(r.Context(), parts[0], parts[1])
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) handleWorkspaceMerge(w http.ResponseWriter, r *http.Request, rest string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	parts := strings.Split(rest, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		http.Error(w, "workspace path must be /workspaces/{session}/{task}/merge", http.StatusBadRequest)
		return
	}
	workDir := filepath.Dir(filepath.Dir(filepath.Dir(s.metaPath)))
	manager := workspacepkg.NewManager(workDir, preferredEventStore(workDir))
	item, err := manager.Get(r.Context(), parts[0], parts[1])
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if err := manager.MergeBack(r.Context(), item); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	updated, err := manager.Get(r.Context(), parts[0], parts[1])
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleWorkspaceCleanup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	workDir := filepath.Dir(filepath.Dir(filepath.Dir(s.metaPath)))
	manager := workspacepkg.NewManager(workDir, preferredEventStore(workDir))
	if err := manager.ReclaimStale(r.Context()); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	items, err := manager.List(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, WorkspaceListResponse{Items: items})
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

func syncWorkspaceMetadata(ctx context.Context, workDir string) error {
	return syncdb.NewWorkspace(workDir).Run(ctx)
}

func (s *Server) updateSessionStatus(ctx context.Context, workDir, sessionID, status string) error {
	sessionStore := preferredHistoryStore(workDir)
	session, err := sessionStore.Get(ctx, sessionID)
	if err != nil {
		if s.sessionStore == nil {
			return err
		}
		session, err = s.sessionStore.Get(ctx, sessionID)
		if err != nil {
			return err
		}
	}
	session.Status = status
	session.UpdatedAt = time.Now().UTC()
	if err := sessionStore.Save(ctx, session); err != nil {
		if s.sessionStore == nil {
			return err
		}
	}
	if s.sessionStore == nil {
		return nil
	}
	if err := s.sessionStore.Save(ctx, session); err != nil {
		return err
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
