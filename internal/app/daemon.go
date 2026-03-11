package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/liliang/roma/internal/agents"
	"github.com/liliang/roma/internal/api"
	"github.com/liliang/roma/internal/domain"
	"github.com/liliang/roma/internal/events"
	"github.com/liliang/roma/internal/gateway"
	"github.com/liliang/roma/internal/history"
	"github.com/liliang/roma/internal/queue"
	"github.com/liliang/roma/internal/run"
	"github.com/liliang/roma/internal/scheduler"
	"github.com/liliang/roma/internal/sessions"
	"github.com/liliang/roma/internal/store"
	"github.com/liliang/roma/internal/syncdb"
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
}

// NewDaemon constructs the bootstrap daemon.
func NewDaemon() (*Daemon, error) {
	mem := store.NewMemoryStore()
	wd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("get working directory: %w", err)
	}
	registry, err := agents.DefaultRegistry()
	if err != nil {
		return nil, fmt.Errorf("load registry: %w", err)
	}
	queueBackend := newQueueBackend(wd)
	historyBackend := newHistoryBackend(wd)
	return &Daemon{
		api:       api.NewServer(wd, queueBackend, historyBackend),
		store:     mem,
		gateway:   gateway.NewService(mem, gateway.NewLogAdapter(domain.GatewayEndpointTypeWebhook)),
		history:   historyBackend,
		queue:     queueBackend,
		runner:    run.NewService(registry),
		sessions:  sessions.NewService(mem, mem, mem),
		scheduler: scheduler.NewService(mem, mem, mem),
	}, nil
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

// Run starts the daemon lifecycle and initializes bootstrap state.
func (d *Daemon) Run(ctx context.Context) error {
	wd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	if err := syncdb.NewWorkspace(wd).Run(ctx); err != nil {
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
	if err := scheduler.RecoverInterruptedLeases(ctx, wd); err != nil {
		return fmt.Errorf("recover interrupted scheduler leases: %w", err)
	}
	if err := scheduler.NormalizeInterruptedTasks(ctx, wd); err != nil {
		return fmt.Errorf("normalize interrupted tasks: %w", err)
	}
	if recovered, err := scheduler.RecoverableSessions(ctx, wd); err == nil && len(recovered) > 0 {
		log.Printf("romad recovered %d session(s) with runnable tasks from sqlite metadata", len(recovered))
	}
	if err := scheduler.ResumeRecoverableSessions(ctx, wd, d.queue, d.runner); err != nil {
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
	var runErr error
	var runResult run.Result
	if req.GraphFile == "" {
		if req.Graph == nil {
			runResult, runErr = d.runner.RunWithResult(ctx, run.Request{
				Prompt:         req.Prompt,
				StarterAgent:   req.StarterAgent,
				WorkingDir:     req.WorkingDir,
				Delegates:      req.Delegates,
				SessionID:      req.SessionID,
				TaskID:         req.TaskID,
				PolicyOverride: req.PolicyOverride,
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
					ID:           node.ID,
					Title:        node.Title,
					Agent:        node.Agent,
					Strategy:     domain.TaskStrategy(node.Strategy),
					Dependencies: node.Dependencies,
				})
			}
			if runErr = run.ValidateGraphRequest(graphReq); runErr == nil {
				graphReq.SessionID = req.SessionID
				graphReq.TaskID = req.TaskID
				graphReq.PolicyOverride = req.PolicyOverride
				graphReq.Continuous = req.Continuous
				graphReq.MaxRounds = req.MaxRounds
				runResult, runErr = d.runner.RunGraphWithResult(ctx, graphReq, os.Stdout)
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
			graphReq.Continuous = req.Continuous
			graphReq.MaxRounds = req.MaxRounds
			runResult, runErr = d.runner.RunGraphWithResult(ctx, graphReq, os.Stdout)
		}
	}
	req.SessionID = runResult.SessionID
	req.TaskID = runResult.TaskID
	req.ArtifactIDs = runResult.ArtifactIDs
	if runErr != nil {
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
	}
	if err := d.queue.Update(ctx, req); err != nil {
		return fmt.Errorf("finalize queue request: %w", err)
	}
	d.deliverQueueNotification(ctx, req)
	return nil
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
