package scheduler

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/liliang-cn/roma/internal/artifacts"
	"github.com/liliang-cn/roma/internal/curia"
	"github.com/liliang-cn/roma/internal/domain"
	"github.com/liliang-cn/roma/internal/events"
	"github.com/liliang-cn/roma/internal/policy"
	"github.com/liliang-cn/roma/internal/runtime"
	"github.com/liliang-cn/roma/internal/store"
	workspacepkg "github.com/liliang-cn/roma/internal/workspace"
)

// Dispatcher owns ready-batch dispatch for relay/direct graph execution.
type Dispatcher struct {
	supervisor *runtime.Supervisor
	artifacts  *artifacts.Service
	events     store.EventStore
	lifecycle  *GraphLifecycle
	leases     *LeaseStore
	workspaces *workspacepkg.Manager
	ownerID    string
	now        func() time.Time
}

// ApprovalPendingError indicates one or more task nodes are waiting for human approval.
type ApprovalPendingError struct {
	TaskIDs []string
}

func (e *ApprovalPendingError) Error() string {
	return fmt.Sprintf("scheduler is awaiting approval for %d task(s)", len(e.TaskIDs))
}

// NewDispatcher constructs a scheduler-owned dispatcher.
func NewDispatcher(workDir string, supervisor *runtime.Supervisor, eventStore store.EventStore, taskStore store.TaskStore) *Dispatcher {
	return NewDispatcherWithControlDir(workDir, workDir, supervisor, eventStore, taskStore)
}

// NewDispatcherWithControlDir constructs a dispatcher with an explicit control-plane root.
func NewDispatcherWithControlDir(workDir, controlDir string, supervisor *runtime.Supervisor, eventStore store.EventStore, taskStore store.TaskStore) *Dispatcher {
	var lifecycle *GraphLifecycle
	if taskStore != nil {
		lifecycle = NewGraphLifecycle(taskStore, eventStore)
	}
	var leases *LeaseStore
	if controlDir != "" {
		if store, err := NewLeaseStore(controlDir); err == nil {
			leases = store
		}
	}
	now := time.Now().UTC
	workspaceRoot := controlDir
	if workspaceRoot == "" {
		workspaceRoot = workDir
	}
	return &Dispatcher{
		supervisor: supervisor,
		artifacts:  artifacts.NewService(),
		events:     eventStore,
		lifecycle:  lifecycle,
		leases:     leases,
		workspaces: workspacepkg.NewManager(workspaceRoot, eventStore),
		ownerID:    fmt.Sprintf("lease_%d", now().UnixNano()),
		now:        now,
	}
}

// Execute registers tasks then runs ready batches until completion.
func (d *Dispatcher) Execute(ctx context.Context, sessionID, workDir, basePrompt string, assignments []NodeAssignment) (DispatchResult, error) {
	return d.execute(ctx, sessionID, workDir, basePrompt, assignments, nil, true)
}

// Resume continues a persisted session using existing successful artifacts.
func (d *Dispatcher) Resume(ctx context.Context, sessionID, workDir, basePrompt string, assignments []NodeAssignment, existing map[string]domain.ArtifactEnvelope) (DispatchResult, error) {
	return d.execute(ctx, sessionID, workDir, basePrompt, assignments, existing, false)
}

func (d *Dispatcher) execute(ctx context.Context, sessionID, workDir, basePrompt string, assignments []NodeAssignment, existing map[string]domain.ArtifactEnvelope, register bool) (DispatchResult, error) {
	completedOrder := make([]string, 0, len(assignments))
	shouldReleaseLease := true
	if d.leases != nil {
		if err := d.leases.Acquire(ctx, sessionID, d.ownerID); err == nil {
			d.appendLeaseEvent(ctx, sessionID, LeaseStatusActive, nil, nil, nil, nil)
		}
		defer func() {
			if !shouldReleaseLease {
				return
			}
			if err := d.leases.Release(context.Background(), sessionID, d.ownerID, completedOrder); err == nil {
				d.appendLeaseEvent(context.Background(), sessionID, LeaseStatusReleased, nil, nil, nil, completedOrder)
			}
		}()
	}

	graph := domain.TaskGraph{Nodes: make([]domain.TaskNodeSpec, 0, len(assignments))}
	for _, assignment := range assignments {
		graph.Nodes = append(graph.Nodes, assignment.Node)
		if d.lifecycle != nil && register {
			_ = d.lifecycle.RegisterTask(ctx, sessionID, assignment.Node, assignment.Profile.ID)
		}
	}
	if err := graph.Validate(); err != nil {
		return DispatchResult{}, err
	}

	artifactsByNode := make(map[string]domain.ArtifactEnvelope, len(assignments))
	relatedArtifacts := make(map[string][]domain.ArtifactEnvelope, len(assignments))
	order := make([]string, 0, len(assignments))
	for nodeID, artifact := range existing {
		artifactsByNode[nodeID] = artifact
		order = append(order, nodeID)
	}
	completedOrder = append(completedOrder, order...)

	remaining := make([]NodeAssignment, 0, len(assignments))
	for _, assignment := range assignments {
		if _, ok := artifactsByNode[assignment.Node.ID]; ok {
			continue
		}
		remaining = append(remaining, assignment)
	}

	for len(remaining) > 0 {
		progressed := false
		next := make([]NodeAssignment, 0, len(remaining))
		readyByID := make(map[string]struct{}, len(remaining))
		pendingApprovals := make([]string, 0)
		if d.lifecycle != nil {
			readyTasks, err := d.lifecycle.ReadyTasks(ctx, sessionID)
			if err != nil {
				return DispatchResult{Artifacts: artifactsByNode, RelatedArtifacts: relatedArtifacts, Order: order}, err
			}
			for _, task := range readyTasks {
				readyByID[strings.TrimPrefix(task.ID, sessionID+"__")] = struct{}{}
			}
		}

		batch := make([]NodeAssignment, 0, len(remaining))
		preparedWorkspaces := make([]workspacepkg.Prepared, 0, len(remaining))
		workspaceByNode := make(map[string]workspacepkg.Prepared, len(remaining))
		for _, assignment := range remaining {
			if d.lifecycle != nil {
				if _, ok := readyByID[assignment.Node.ID]; !ok {
					next = append(next, assignment)
					continue
				}
			} else if !dependenciesReady(assignment.Node, artifactsByNode) {
				next = append(next, assignment)
				continue
			}
			prepared, err := d.workspaces.Prepare(ctx, sessionID, assignment.Node.ID, workDir, assignment.Node.Strategy)
			if err != nil {
				return DispatchResult{Artifacts: artifactsByNode, RelatedArtifacts: relatedArtifacts, Order: order}, err
			}
			decision, err := policy.NewSimpleBroker(d.events).Evaluate(ctx, policy.Request{
				SessionID:    sessionID,
				TaskID:       assignment.Node.ID,
				Mode:         "node",
				Prompt:       buildNodePrompt(basePrompt, assignment, artifactsByNode),
				WorkingDir:   workDir,
				EffectiveDir: prepared.EffectiveDir,
				PathHints:    []string{prepared.BaseDir, prepared.EffectiveDir},
				StarterAgent: assignment.Profile.ID,
				NodeCount:    1,
			})
			if err != nil {
				return DispatchResult{Artifacts: artifactsByNode, RelatedArtifacts: relatedArtifacts, Order: order}, err
			}
			if decision.Kind == policy.DecisionBlock {
				return DispatchResult{Artifacts: artifactsByNode, RelatedArtifacts: relatedArtifacts, Order: order}, fmt.Errorf("policy blocked node %s: %s", assignment.Node.ID, decision.Reason)
			}
			if decision.Kind == policy.DecisionWarn && d.lifecycle != nil {
				taskRecord, getErr := d.lifecycle.tasks.GetTask(ctx, taskRecordID(sessionID, assignment.Node.ID))
				if getErr != nil {
					return DispatchResult{Artifacts: artifactsByNode, RelatedArtifacts: relatedArtifacts, Order: order}, getErr
				}
				if !taskRecord.ApprovalGranted {
					if err := d.lifecycle.MarkAwaitingApproval(ctx, sessionID, assignment.Node.ID); err != nil {
						return DispatchResult{Artifacts: artifactsByNode, RelatedArtifacts: relatedArtifacts, Order: order}, err
					}
					preparedWorkspaces = append(preparedWorkspaces, prepared)
					workspaceByNode[assignment.Node.ID] = prepared
					pendingApprovals = append(pendingApprovals, taskRecord.ID)
					next = append(next, assignment)
					continue
				}
			}
			preparedWorkspaces = append(preparedWorkspaces, prepared)
			workspaceByNode[assignment.Node.ID] = prepared
			batch = append(batch, assignment)
		}

		if len(batch) > 0 {
			if d.leases != nil {
				readyIDs := make([]string, 0, len(batch))
				workspaceRefs := make([]WorkspaceRef, 0, len(preparedWorkspaces))
				for _, assignment := range batch {
					readyIDs = append(readyIDs, assignment.Node.ID)
				}
				for _, prepared := range preparedWorkspaces {
					workspaceRefs = append(workspaceRefs, WorkspaceRef{
						TaskID:        prepared.TaskID,
						EffectiveDir:  prepared.EffectiveDir,
						Provider:      prepared.Provider,
						EffectiveMode: string(prepared.Effective),
					})
				}
				if err := d.leases.Renew(ctx, sessionID, d.ownerID, readyIDs, workspaceRefs, nil, order); err == nil {
					d.appendLeaseEvent(ctx, sessionID, LeaseStatusActive, readyIDs, workspaceRefs, nil, order)
				}
			}
			results, err := d.executeBatch(ctx, sessionID, workDir, basePrompt, artifactsByNode, batch, workspaceByNode)
			for _, item := range results {
				artifactsByNode[item.assignment.Node.ID] = item.report
				if len(item.related) > 0 {
					relatedArtifacts[item.assignment.Node.ID] = append([]domain.ArtifactEnvelope(nil), item.related...)
				}
				order = append(order, item.assignment.Node.ID)
				completedOrder = append(completedOrder, item.assignment.Node.ID)
			}
			if err != nil {
				return DispatchResult{Artifacts: artifactsByNode, RelatedArtifacts: relatedArtifacts, Order: order}, err
			}
			progressed = true
		}

		if !progressed {
			if len(pendingApprovals) > 0 {
				if d.leases != nil {
					if err := d.leases.Renew(ctx, sessionID, d.ownerID, nil, nil, pendingApprovals, order); err == nil {
						d.appendLeaseEvent(ctx, sessionID, LeaseStatusActive, nil, nil, pendingApprovals, order)
					}
				}
				shouldReleaseLease = false
				return DispatchResult{Artifacts: artifactsByNode, RelatedArtifacts: relatedArtifacts, Order: order}, &ApprovalPendingError{TaskIDs: pendingApprovals}
			}
			return DispatchResult{Artifacts: artifactsByNode, RelatedArtifacts: relatedArtifacts, Order: order}, fmt.Errorf("scheduler dispatcher made no progress; dependency cycle or missing artifact")
		}
		remaining = next
	}

	return DispatchResult{Artifacts: artifactsByNode, RelatedArtifacts: relatedArtifacts, Order: order}, nil
}

func (d *Dispatcher) appendLeaseEvent(ctx context.Context, sessionID string, status LeaseStatus, readyIDs []string, workspaceRefs []WorkspaceRef, pendingApprovalTaskIDs []string, completedIDs []string) {
	if d.events == nil {
		return
	}
	_ = d.events.AppendEvent(ctx, events.Record{
		ID:         fmt.Sprintf("evt_%s_lease_%d", sessionID, d.now().UnixNano()),
		SessionID:  sessionID,
		Type:       events.TypeSchedulerLeaseRecorded,
		ActorType:  events.ActorTypeScheduler,
		OccurredAt: d.now(),
		ReasonCode: string(status),
		Payload: map[string]any{
			"owner_id":                  d.ownerID,
			"status":                    status,
			"ready_task_ids":            readyIDs,
			"workspace_refs":            workspaceRefs,
			"pending_approval_task_ids": pendingApprovalTaskIDs,
			"completed_task_ids":        completedIDs,
		},
	})
}

type dispatchBatchResult struct {
	assignment NodeAssignment
	workspace  workspacepkg.Prepared
	report     domain.ArtifactEnvelope
	related    []domain.ArtifactEnvelope
	runErr     error
	reportErr  error
}

func (r dispatchBatchResult) err() error {
	if r.reportErr != nil {
		return r.reportErr
	}
	return r.runErr
}

func (d *Dispatcher) executeBatch(
	ctx context.Context,
	sessionID, workDir, basePrompt string,
	artifactsByNode map[string]domain.ArtifactEnvelope,
	batch []NodeAssignment,
	workspaceByNode map[string]workspacepkg.Prepared,
) ([]dispatchBatchResult, error) {
	batchCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	results := make([]dispatchBatchResult, 0, len(batch))
	resultsCh := make(chan dispatchBatchResult, len(batch))
	var wg sync.WaitGroup
	for _, assignment := range batch {
		wg.Add(1)
		go func(assignment NodeAssignment) {
			defer wg.Done()
			if d.lifecycle != nil {
				_ = d.lifecycle.MarkRunning(batchCtx, sessionID, assignment.Node.ID)
			}

			prompt := buildNodePrompt(basePrompt, assignment, artifactsByNode)
			workspace := workspaceByNode[assignment.Node.ID]
			_ = d.events.AppendEvent(batchCtx, events.Record{
				ID:         fmt.Sprintf("evt_%s_started", assignment.Node.ID),
				SessionID:  sessionID,
				TaskID:     assignment.Node.ID,
				Type:       events.TypeRelayNodeStarted,
				ActorType:  events.ActorTypeScheduler,
				OccurredAt: d.now(),
				Payload: map[string]any{
					"node_id": assignment.Node.ID,
					"agent":   assignment.Profile.ID,
				},
			})

			var (
				report    domain.ArtifactEnvelope
				related   []domain.ArtifactEnvelope
				runErr    error
				reportErr error
			)
			if assignment.Node.Strategy == domain.TaskStrategyCuria {
				senators := assignment.CuriaProfiles
				if len(senators) == 0 {
					senators = []domain.AgentProfile{assignment.Profile}
				}
				curiaResult, err := curia.NewExecutor(workspace.BaseDir, d.supervisor, d.artifacts).Execute(batchCtx, curia.ExecuteRequest{
					SessionID:         sessionID,
					TaskID:            assignment.Node.ID,
					BasePrompt:        prompt,
					WorkingDir:        workspace.EffectiveDir,
					NodeTitle:         assignment.Node.Title,
					Senators:          senators,
					Quorum:            assignment.CuriaQuorum,
					ArbitrationMode:   assignment.CuriaArbitrationMode,
					Arbitrator:        assignment.CuriaArbitrator,
					UpstreamArtifacts: upstreamArtifactsForNode(assignment.Node, artifactsByNode),
				})
				if err != nil {
					runErr = err
				} else {
					report = curiaResult.Primary
					related = curiaResult.RelatedArtifacts
				}
			} else {
				runResult, err := d.supervisor.RunCaptured(batchCtx, runtime.StartRequest{
					ExecutionID:      "exec_" + assignment.Node.ID,
					SessionID:        sessionID,
					TaskID:           assignment.Node.ID,
					Profile:          assignment.Profile,
					SemanticReviewer: chooseSemanticReviewer(assignment),
					Prompt:           prompt,
					WorkingDir:       workspace.EffectiveDir,
					Continuous:       assignment.Continuous,
					MaxRounds:        assignment.MaxRounds,
				})
				runErr = err
				report, reportErr = d.artifacts.BuildReport(batchCtx, artifacts.BuildReportRequest{
					SessionID: sessionID,
					TaskID:    assignment.Node.ID,
					RunID:     assignment.Node.ID,
					Agent:     assignment.Profile,
					Result:    label(runErr),
					Output:    runResult.Stdout,
					Stderr:    runResult.Stderr,
				})
			}
			resultsCh <- dispatchBatchResult{
				assignment: assignment,
				workspace:  workspace,
				report:     report,
				related:    related,
				runErr:     runErr,
				reportErr:  reportErr,
			}
		}(assignment)
	}
	go func() {
		wg.Wait()
		close(resultsCh)
	}()

	var firstErr error
	for item := range resultsCh {
		results = append(results, item)
		terminalErr := item.err()
		if terminalErr != nil && firstErr == nil {
			firstErr = terminalErr
			cancel()
		}
		if d.lifecycle != nil {
			_ = d.lifecycle.MarkFinished(ctx, sessionID, item.assignment.Node.ID, item.report.ID, terminalErr)
		}
		if d.workspaces != nil {
			_ = d.workspaces.Release(ctx, item.workspace, label(terminalErr))
		}
		_ = d.events.AppendEvent(ctx, events.Record{
			ID:         fmt.Sprintf("evt_%s_completed", item.assignment.Node.ID),
			SessionID:  sessionID,
			TaskID:     item.assignment.Node.ID,
			Type:       events.TypeRelayNodeCompleted,
			ActorType:  events.ActorTypeScheduler,
			OccurredAt: d.now(),
			ReasonCode: label(terminalErr),
			Payload: map[string]any{
				"node_id":     item.assignment.Node.ID,
				"artifact_id": item.report.ID,
				"agent":       item.assignment.Profile.ID,
			},
		})
	}
	return results, firstErr
}

func buildNodePrompt(basePrompt string, assignment NodeAssignment, artifactsByNode map[string]domain.ArtifactEnvelope) string {
	node := assignment.Node
	var b strings.Builder
	b.WriteString("ROMA relay execution node.\n")
	b.WriteString("Original request:\n")
	b.WriteString(basePrompt)
	b.WriteString("\n\nCurrent node:\n")
	b.WriteString(node.Title)
	b.WriteString(" (")
	b.WriteString(node.ID)
	b.WriteString(")\n")
	if len(node.Dependencies) > 0 {
		b.WriteString("\nUpstream artifact summaries:\n")
		for _, dep := range node.Dependencies {
			artifact := artifactsByNode[dep]
			b.WriteString("- ")
			b.WriteString(dep)
			b.WriteString(": ")
			b.WriteString(artifacts.SummaryFromEnvelope(artifact))
			b.WriteString("\n")
		}
	}
	if strings.TrimSpace(assignment.PromptHint) != "" {
		b.WriteString("\nFollow-up instruction:\n")
		b.WriteString(strings.TrimSpace(assignment.PromptHint))
		b.WriteString("\n")
	}
	b.WriteString("\nProvide the contribution for this node only.")
	return b.String()
}

func dependenciesReady(node domain.TaskNodeSpec, artifacts map[string]domain.ArtifactEnvelope) bool {
	for _, dep := range node.Dependencies {
		if _, ok := artifacts[dep]; !ok {
			return false
		}
	}
	return true
}

func upstreamArtifactsForNode(node domain.TaskNodeSpec, artifactsByNode map[string]domain.ArtifactEnvelope) []domain.ArtifactEnvelope {
	out := make([]domain.ArtifactEnvelope, 0, len(node.Dependencies))
	for _, dep := range node.Dependencies {
		if artifact, ok := artifactsByNode[dep]; ok {
			out = append(out, artifact)
		}
	}
	return out
}

func label(err error) string {
	if err != nil {
		return "failed"
	}
	return "success"
}

func chooseSemanticReviewer(assignment NodeAssignment) domain.AgentProfile {
	if strings.TrimSpace(assignment.SemanticReviewer.ID) != "" {
		return assignment.SemanticReviewer
	}
	return assignment.Profile
}
