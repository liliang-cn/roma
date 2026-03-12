package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/liliang-cn/roma/internal/agents"
	"github.com/liliang-cn/roma/internal/api"
	"github.com/liliang-cn/roma/internal/artifacts"
	"github.com/liliang-cn/roma/internal/domain"
	"github.com/liliang-cn/roma/internal/events"
	"github.com/liliang-cn/roma/internal/gateway"
	"github.com/liliang-cn/roma/internal/history"
	"github.com/liliang-cn/roma/internal/plans"
	"github.com/liliang-cn/roma/internal/queue"
	"github.com/liliang-cn/roma/internal/romapath"
	"github.com/liliang-cn/roma/internal/run"
	"github.com/liliang-cn/roma/internal/scheduler"
	"github.com/liliang-cn/roma/internal/sessions"
	"github.com/liliang-cn/roma/internal/store"
	"github.com/liliang-cn/roma/internal/syncdb"
	"github.com/liliang-cn/roma/internal/taskstore"
	workspacepkg "github.com/liliang-cn/roma/internal/workspace"
)

// Daemon is the bootstrap romad process.
type Daemon struct {
	api       *api.Server
	store     *store.MemoryStore
	gateway   *gateway.Service
	history   history.Backend
	queue     queue.Backend
	runner    *run.Service
	sessions  *sessions.Service
	scheduler *scheduler.Service
	mu        sync.Mutex
	running   map[string]context.CancelFunc
	canceled  map[string]bool
}

// NewDaemon constructs the bootstrap daemon.
func NewDaemon() (*Daemon, error) {
	mem := store.NewMemoryStore()
	wd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("get working directory: %w", err)
	}
	controlDir := romapath.HomeDir()
	registry, err := agents.DefaultRegistry()
	if err != nil {
		return nil, fmt.Errorf("load registry: %w", err)
	}
	registry.SetUserConfigPath(agents.DefaultUserConfigPath())
	if err := registry.LoadUserConfig(registry.UserConfigPath()); err != nil {
		return nil, fmt.Errorf("load user agent config: %w", err)
	}
	runner := run.NewService(registry)
	runner.SetControlDir(controlDir)
	queueBackend := newQueueBackend(controlDir)
	historyBackend := newHistoryBackend(controlDir)
	artifactBackend := newArtifactBackend(controlDir)
	planService := plans.NewService(artifactBackend, workspacepkg.NewManager(wd, mem), mem)
	server := api.NewServer(controlDir, queueBackend, historyBackend)
	daemon := &Daemon{
		api:       server,
		store:     mem,
		gateway:   gateway.NewService(mem, planService, gateway.NewLogAdapter(domain.GatewayEndpointTypeWebhook)),
		history:   historyBackend,
		queue:     queueBackend,
		runner:    runner,
		sessions:  sessions.NewService(mem, mem, mem),
		scheduler: scheduler.NewService(mem, mem, mem),
		running:   make(map[string]context.CancelFunc),
		canceled:  make(map[string]bool),
	}
	server.SetQueueCanceler(daemon)
	return daemon, nil
}

func newQueueBackend(workDir string) queue.Backend {
	fileStore := queue.NewStore(workDir)
	sqliteStore, err := queue.NewSQLiteStore(workDir)
	if err != nil {
		return fileStore
	}
	return queue.NewMirrorStore(sqliteStore, fileStore)
}

func newHistoryBackend(workDir string) history.Backend {
	fileStore := history.NewStore(workDir)
	sqliteStore, err := history.NewSQLiteStore(workDir)
	if err != nil {
		return fileStore
	}
	return history.NewMirrorStore(fileStore, sqliteStore)
}

func newEventBackend(workDir string) store.EventStore {
	fileStore := store.NewFileEventStore(workDir)
	sqliteStore, err := store.NewSQLiteEventStore(workDir)
	if err != nil {
		return fileStore
	}
	return store.NewMultiEventStore(fileStore, sqliteStore)
}

func newTaskBackend(workDir string) store.TaskStore {
	fileStore := taskstore.NewStore(workDir)
	sqliteStore, err := taskstore.NewSQLiteStore(workDir)
	if err != nil {
		return fileStore
	}
	return taskstore.NewMirrorStore(fileStore, sqliteStore)
}

func newArtifactBackend(workDir string) artifacts.Backend {
	fileStore := artifacts.NewFileStore(workDir)
	sqliteStore, err := artifacts.NewSQLiteStore(workDir)
	if err != nil {
		return fileStore
	}
	return artifacts.NewMirrorStore(sqliteStore, fileStore)
}

// Run starts the daemon lifecycle and initializes bootstrap state.
func (d *Daemon) Run(ctx context.Context) error {
	controlDir := romapath.HomeDir()
	if err := syncdb.NewWorkspace(controlDir).Run(ctx); err != nil {
		return fmt.Errorf("sync workspace metadata: %w", err)
	}
	if err := d.api.Start(ctx); err != nil {
		if errors.Is(err, api.ErrUnavailable) {
			log.Printf("romad api disabled: %v", err)
		} else {
			return fmt.Errorf("start daemon api: %w", err)
		}
	}
	if err := d.history.RecoverInterrupted(ctx); err != nil {
		return fmt.Errorf("recover interrupted sessions: %w", err)
	}
	if err := d.queue.RecoverInterrupted(ctx); err != nil {
		return fmt.Errorf("recover interrupted queue items: %w", err)
	}
	if err := scheduler.RecoverInterruptedLeases(ctx, controlDir); err != nil {
		return fmt.Errorf("recover interrupted scheduler leases: %w", err)
	}
	if err := scheduler.ReclaimStaleWorkspaces(ctx, controlDir); err != nil {
		return fmt.Errorf("reclaim stale workspaces: %w", err)
	}
	if err := scheduler.NormalizeInterruptedTasks(ctx, controlDir); err != nil {
		return fmt.Errorf("normalize interrupted tasks: %w", err)
	}
	if recovered, err := scheduler.RecoverableSessions(ctx, controlDir); err == nil && len(recovered) > 0 {
		log.Printf("romad recovered %d session(s) with runnable tasks from sqlite metadata", len(recovered))
	}
	if err := scheduler.ResumeRecoverableSessions(ctx, controlDir, d.queue, d.runner); err != nil {
		return fmt.Errorf("resume recoverable sessions: %w", err)
	}

	session, err := d.sessions.Create(ctx, sessions.CreateSessionRequest{
		ID:          "sess_bootstrap",
		Description: "bootstrap session",
	})
	if err != nil {
		return fmt.Errorf("create bootstrap session: %w", err)
	}

	graph := domain.TaskGraph{
		Nodes: []domain.TaskNodeSpec{
			{
				ID:            "task_bootstrap_direct",
				Title:         "Bootstrap direct task",
				Strategy:      domain.TaskStrategyDirect,
				SchemaVersion: "v1",
			},
		},
	}
	if err := d.sessions.SubmitTaskGraph(ctx, session.ID, graph); err != nil {
		return fmt.Errorf("submit bootstrap graph: %w", err)
	}

	if err := d.scheduler.StartSession(ctx, session.ID); err != nil {
		return fmt.Errorf("start scheduler: %w", err)
	}

	ready, err := d.scheduler.ListReadyTasks(ctx, session.ID)
	if err != nil {
		return fmt.Errorf("list ready tasks: %w", err)
	}

	if err := d.store.AppendEvent(ctx, events.Record{
		ID:         "evt_bootstrap_ready_tasks",
		SessionID:  session.ID,
		Type:       events.TypeTaskStateChanged,
		ActorType:  events.ActorTypeScheduler,
		OccurredAt: time.Now().UTC(),
		Payload: map[string]any{
			"ready_task_count": len(ready),
		},
	}); err != nil {
		return fmt.Errorf("append bootstrap ready event: %w", err)
	}

	if err := d.gateway.RegisterEndpoint(ctx, domain.GatewayEndpoint{
		ID:             "gw_bootstrap_webhook",
		Type:           domain.GatewayEndpointTypeWebhook,
		Enabled:        true,
		Target:         "http://localhost/bootstrap",
		AllowedActions: []domain.RemoteCommandAction{domain.RemoteCommandActionApprove, domain.RemoteCommandActionReject},
	}, domain.RemoteSubscription{
		EndpointID:        "gw_bootstrap_webhook",
		EventTypes:        []string{"session_started", "approval_required", "task_succeeded", "task_failed"},
		SeverityThreshold: domain.NotificationSeverityLow,
		SummaryMode:       "compact",
	}); err != nil {
		return fmt.Errorf("register gateway endpoint: %w", err)
	}

	if err := d.gateway.Deliver(ctx, domain.NotificationEnvelope{
		ID:        "notif_bootstrap_started",
		Type:      "session_started",
		Severity:  domain.NotificationSeverityLow,
		SessionID: session.ID,
		Title:     "ROMA session started",
		Summary:   "Bootstrap session is running and ready for dispatch.",
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		return fmt.Errorf("deliver bootstrap notification: %w", err)
	}

	log.Printf("romad bootstrap started session=%s state=%s ready_tasks=%d", session.ID, domain.SessionStateRunning, len(ready))
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Printf("romad shutting down")
			return nil
		case <-ticker.C:
			if err := d.processNextQueueItem(ctx); err != nil {
				log.Printf("romad queue error: %v", err)
			}
			if err := scheduler.ResumeRecoverableSessions(ctx, controlDir, d.queue, d.runner); err != nil {
				log.Printf("romad recovery error: %v", err)
			}
		}
	}
}

func (d *Daemon) processNextQueueItem(ctx context.Context) error {
	req, ok, err := d.queue.NextPending(ctx)
	if err != nil {
		return fmt.Errorf("get next pending job: %w", err)
	}
	if !ok {
		return nil
	}

	req.Status = queue.StatusRunning
	req.Error = ""
	if req.SessionID == "" {
		req.SessionID = fmt.Sprintf("sess_%d", time.Now().UTC().UnixNano())
	}
	if req.TaskID == "" {
		prefix := "task"
		if req.GraphFile != "" || req.Graph != nil {
			prefix = "task_graph"
		}
		req.TaskID = fmt.Sprintf("%s_%d", prefix, time.Now().UTC().UnixNano())
	}
	if err := d.queue.Update(ctx, req); err != nil {
		return fmt.Errorf("mark queue running: %w", err)
	}

	log.Printf("romad processing queued job id=%s agent=%s", req.ID, req.StarterAgent)
	runCtx, cancel := context.WithCancel(ctx)
	d.trackRunning(req.ID, cancel)
	defer func() {
		cancel()
		d.clearRunning(req.ID)
	}()
	stopHeartbeat := d.startJobHeartbeat(runCtx, req)
	defer stopHeartbeat()
	var runErr error
	var runResult run.Result
	if req.GraphFile == "" {
		if req.Graph == nil {
			runResult, runErr = d.runner.RunWithResult(runCtx, run.Request{
				Prompt:         req.Prompt,
				StarterAgent:   req.StarterAgent,
				WorkingDir:     req.WorkingDir,
				Delegates:      req.Delegates,
				SessionID:      req.SessionID,
				TaskID:         req.TaskID,
				PolicyOverride: req.PolicyOverride,
				OverrideActor:  req.PolicyOverrideActor,
				Continuous:     req.Continuous,
				MaxRounds:      req.MaxRounds,
			})
		} else {
			log.Printf("romad processing inline graph job id=%s nodes=%d", req.ID, len(req.Graph.Nodes))
			graphReq := run.GraphRequest{
				Prompt:     req.Graph.Prompt,
				WorkingDir: req.WorkingDir,
				Nodes:      make([]run.GraphNodeRequest, 0, len(req.Graph.Nodes)),
			}
			for _, node := range req.Graph.Nodes {
				graphReq.Nodes = append(graphReq.Nodes, run.GraphNodeRequest{
					ID:              node.ID,
					Title:           node.Title,
					Agent:           node.Agent,
					Strategy:        domain.TaskStrategy(node.Strategy),
					Dependencies:    node.Dependencies,
					Senators:        node.Senators,
					Quorum:          node.Quorum,
					ArbitrationMode: node.ArbitrationMode,
					Arbitrator:      node.Arbitrator,
				})
			}
			if runErr = run.ValidateGraphRequest(graphReq); runErr == nil {
				graphReq.SessionID = req.SessionID
				graphReq.TaskID = req.TaskID
				graphReq.PolicyOverride = req.PolicyOverride
				graphReq.OverrideActor = req.PolicyOverrideActor
				graphReq.Continuous = req.Continuous
				graphReq.MaxRounds = req.MaxRounds
				runResult, runErr = d.runner.RunGraphWithResult(runCtx, graphReq, os.Stdout)
			}
		}
	} else {
		log.Printf("romad processing graph job id=%s file=%s", req.ID, req.GraphFile)
		graphReq, err := run.LoadGraphRequest(req.GraphFile)
		if err != nil {
			runErr = err
		} else {
			if req.WorkingDir != "" {
				graphReq.WorkingDir = req.WorkingDir
			}
			graphReq.SessionID = req.SessionID
			graphReq.TaskID = req.TaskID
			graphReq.PolicyOverride = req.PolicyOverride
			graphReq.OverrideActor = req.PolicyOverrideActor
			graphReq.Continuous = req.Continuous
			graphReq.MaxRounds = req.MaxRounds
			runResult, runErr = d.runner.RunGraphWithResult(runCtx, graphReq, os.Stdout)
		}
	}
	req.SessionID = runResult.SessionID
	req.TaskID = runResult.TaskID
	req.ArtifactIDs = runResult.ArtifactIDs
	wasCanceled := d.consumeCanceled(req.ID)
	if wasCanceled {
		req.Status = queue.StatusCancelled
		req.Error = "cancelled by user"
		req.PolicyOverride = false
		req.PolicyOverrideActor = ""
		d.syncCancelledState(context.Background(), req)
	} else if runErr != nil {
		req.Status = queue.StatusFailed
		req.Error = runErr.Error()
		req.PolicyOverride = false
	} else if runResult.Status == "awaiting_approval" {
		req.Status = queue.StatusAwaitingApproval
		req.Error = "approval required"
	} else {
		req.Status = queue.StatusSucceeded
		req.Error = ""
		req.PolicyOverride = false
		req.PolicyOverrideActor = ""
	}
	if err := d.queue.Update(ctx, req); err != nil {
		return fmt.Errorf("finalize queue request: %w", err)
	}
	log.Printf("romad finalized job id=%s status=%s session=%s task=%s", req.ID, req.Status, req.SessionID, req.TaskID)
	d.deliverQueueNotification(ctx, req)
	return nil
}

// CancelQueueJob interrupts a queued or currently running job.
func (d *Daemon) CancelQueueJob(ctx context.Context, id string) (queue.Request, error) {
	req, err := d.queue.Get(ctx, id)
	if err != nil {
		return queue.Request{}, err
	}
	switch req.Status {
	case queue.StatusSucceeded, queue.StatusFailed, queue.StatusRejected, queue.StatusCancelled:
		return req, nil
	}

	req.Status = queue.StatusCancelled
	req.Error = "cancelled by user"
	req.PolicyOverride = false
	req.PolicyOverrideActor = ""
	if err := d.queue.Update(ctx, req); err != nil {
		return queue.Request{}, err
	}
	d.markCanceled(id)
	log.Printf("romad cancelling job id=%s session=%s task=%s", req.ID, req.SessionID, req.TaskID)
	if cancel := d.runningCancel(id); cancel != nil {
		cancel()
	}
	d.syncCancelledState(ctx, req)
	return req, nil
}

func (d *Daemon) trackRunning(id string, cancel context.CancelFunc) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.running[id] = cancel
	delete(d.canceled, id)
}

func (d *Daemon) clearRunning(id string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.running, id)
	delete(d.canceled, id)
}

func (d *Daemon) runningCancel(id string) context.CancelFunc {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.running[id]
}

func (d *Daemon) markCanceled(id string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.canceled[id] = true
}

func (d *Daemon) consumeCanceled(id string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	v := d.canceled[id]
	delete(d.canceled, id)
	return v
}

func (d *Daemon) syncCancelledState(ctx context.Context, req queue.Request) {
	if req.WorkingDir == "" {
		return
	}
	if req.SessionID != "" {
		sessionStore := newHistoryBackend(req.WorkingDir)
		if session, err := sessionStore.Get(ctx, req.SessionID); err == nil {
			session.Status = "cancelled"
			session.UpdatedAt = time.Now().UTC()
			_ = sessionStore.Save(ctx, session)
		}
	}
	eventStore := newEventBackend(req.WorkingDir)
	if req.SessionID != "" {
		_ = eventStore.AppendEvent(ctx, events.Record{
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
	taskStore := newTaskBackend(req.WorkingDir)
	if req.SessionID == "" {
		return
	}
	if items, err := taskStore.ListTasksBySession(ctx, req.SessionID); err == nil {
		for _, item := range items {
			switch item.State {
			case domain.TaskStateSucceeded, domain.TaskStateFailedRecoverable, domain.TaskStateFailedTerminal, domain.TaskStateCancelled:
				continue
			default:
				_ = taskStore.UpdateTaskState(ctx, store.TaskStateUpdate{
					TaskID: item.ID,
					State:  domain.TaskStateCancelled,
				})
			}
		}
	}
}

func (d *Daemon) startJobHeartbeat(ctx context.Context, req queue.Request) func() {
	stop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-stop:
				return
			case <-ticker.C:
				current, err := d.queue.Get(context.Background(), req.ID)
				if err == nil && current.Status == queue.StatusRunning {
					current.UpdatedAt = time.Now().UTC()
					_ = d.queue.Update(context.Background(), current)
				}
				log.Printf("romad heartbeat job=%s session=%s task=%s status=running", req.ID, req.SessionID, req.TaskID)
			}
		}
	}()
	return func() {
		close(stop)
	}
}

func (d *Daemon) deliverQueueNotification(ctx context.Context, req queue.Request) {
	if req.SessionID == "" {
		return
	}

	notification := domain.NotificationEnvelope{
		ID:        "notif_" + req.ID + "_" + string(req.Status),
		SessionID: req.SessionID,
		TaskID:    req.TaskID,
		CreatedAt: time.Now().UTC(),
	}
	switch req.Status {
	case queue.StatusAwaitingApproval:
		notification.Type = "approval_required"
		notification.Severity = domain.NotificationSeverityHigh
		notification.Title = "ROMA approval required"
		notification.Summary = fmt.Sprintf("Job %s is waiting for approval before execution continues.", req.ID)
	case queue.StatusSucceeded:
		notification.Type = "task_succeeded"
		notification.Severity = domain.NotificationSeverityLow
		notification.Title = "ROMA task succeeded"
		notification.Summary = fmt.Sprintf("Job %s completed with %d artifact(s).", req.ID, len(req.ArtifactIDs))
	case queue.StatusFailed:
		notification.Type = "task_failed"
		notification.Severity = domain.NotificationSeverityHigh
		notification.Title = "ROMA task failed"
		notification.Summary = fmt.Sprintf("Job %s failed: %s", req.ID, req.Error)
	case queue.StatusRejected:
		notification.Type = "approval_rejected"
		notification.Severity = domain.NotificationSeverityMedium
		notification.Title = "ROMA approval rejected"
		notification.Summary = fmt.Sprintf("Job %s was rejected and will not run.", req.ID)
	case queue.StatusCancelled:
		notification.Type = "task_cancelled"
		notification.Severity = domain.NotificationSeverityMedium
		notification.Title = "ROMA task cancelled"
		notification.Summary = fmt.Sprintf("Job %s was cancelled.", req.ID)
	default:
		return
	}
	for _, artifactID := range req.ArtifactIDs {
		notification.ArtifactRefs = append(notification.ArtifactRefs, "artifact://"+artifactID)
	}
	if err := d.gateway.Deliver(ctx, notification); err != nil {
		log.Printf("romad gateway delivery error for job=%s: %v", req.ID, err)
	}
}
