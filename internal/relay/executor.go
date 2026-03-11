package relay

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/liliang/roma/internal/artifacts"
	"github.com/liliang/roma/internal/domain"
	"github.com/liliang/roma/internal/events"
	"github.com/liliang/roma/internal/runtime"
	"github.com/liliang/roma/internal/store"
)

// NodeAssignment binds a task node to an agent profile and prompt strategy.
type NodeAssignment struct {
	Node       domain.TaskNodeSpec
	Profile    domain.AgentProfile
	Continuous bool
	MaxRounds  int
}

// Result captures relay execution results.
type Result struct {
	Artifacts map[string]domain.ArtifactEnvelope
	Order     []string
}

// Executor runs a task graph in relay order.
type Executor struct {
	supervisor *runtime.Supervisor
	artifacts  *artifacts.Service
	events     store.EventStore
	lifecycle  TaskLifecycle
	now        func() time.Time
}

// TaskLifecycle owns persisted task progression for relay nodes.
type TaskLifecycle interface {
	RegisterTask(ctx context.Context, sessionID string, node domain.TaskNodeSpec, agentID string) error
	ReadyTasks(ctx context.Context, sessionID string) ([]domain.TaskRecord, error)
	MarkRunning(ctx context.Context, sessionID, nodeID string) error
	MarkFinished(ctx context.Context, sessionID, nodeID, artifactID string, runErr error) error
}

// NewExecutor constructs a relay executor.
func NewExecutor(supervisor *runtime.Supervisor, eventStore store.EventStore) *Executor {
	return NewExecutorWithLifecycle(supervisor, eventStore, nil)
}

// NewExecutorWithTaskStore constructs a relay executor with persistent task state sink.
func NewExecutorWithTaskStore(supervisor *runtime.Supervisor, eventStore store.EventStore, taskStore store.TaskStore) *Executor {
	if taskStore == nil {
		return NewExecutorWithLifecycle(supervisor, eventStore, nil)
	}
	return NewExecutorWithLifecycle(supervisor, eventStore, &taskStoreLifecycle{
		tasks: taskStore,
		now:   func() time.Time { return time.Now().UTC() },
	})
}

// NewExecutorWithLifecycle constructs a relay executor with task lifecycle control.
func NewExecutorWithLifecycle(supervisor *runtime.Supervisor, eventStore store.EventStore, lifecycle TaskLifecycle) *Executor {
	return &Executor{
		supervisor: supervisor,
		artifacts:  artifacts.NewService(),
		events:     eventStore,
		lifecycle:  lifecycle,
		now:        func() time.Time { return time.Now().UTC() },
	}
}

// Execute runs each node after its dependencies, passing structured summaries forward.
func (e *Executor) Execute(ctx context.Context, sessionID, workDir, basePrompt string, assignments []NodeAssignment) (Result, error) {
	return e.execute(ctx, sessionID, workDir, basePrompt, assignments, nil, true)
}

// Resume continues a previously persisted relay session from existing successful artifacts.
func (e *Executor) Resume(ctx context.Context, sessionID, workDir, basePrompt string, assignments []NodeAssignment, existing map[string]domain.ArtifactEnvelope) (Result, error) {
	return e.execute(ctx, sessionID, workDir, basePrompt, assignments, existing, false)
}

func (e *Executor) execute(ctx context.Context, sessionID, workDir, basePrompt string, assignments []NodeAssignment, existing map[string]domain.ArtifactEnvelope, register bool) (Result, error) {
	graph := domain.TaskGraph{Nodes: make([]domain.TaskNodeSpec, 0, len(assignments))}
	for _, assignment := range assignments {
		graph.Nodes = append(graph.Nodes, assignment.Node)
		if e.lifecycle != nil && register {
			_ = e.lifecycle.RegisterTask(ctx, sessionID, assignment.Node, assignment.Profile.ID)
		}
	}
	if err := graph.Validate(); err != nil {
		return Result{}, err
	}

	artifactsByNode := make(map[string]domain.ArtifactEnvelope, len(assignments))
	order := make([]string, 0, len(assignments))
	for nodeID, artifact := range existing {
		artifactsByNode[nodeID] = artifact
		order = append(order, nodeID)
	}
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
		if e.lifecycle != nil {
			readyTasks, err := e.lifecycle.ReadyTasks(ctx, sessionID)
			if err != nil {
				return Result{Artifacts: artifactsByNode, Order: order}, err
			}
			for _, task := range readyTasks {
				readyByID[strings.TrimPrefix(task.ID, sessionID+"__")] = struct{}{}
			}
		}
		batch := make([]NodeAssignment, 0, len(remaining))
		for _, assignment := range remaining {
			if e.lifecycle != nil {
				if _, ok := readyByID[assignment.Node.ID]; !ok {
					next = append(next, assignment)
					continue
				}
			} else if !dependenciesReady(assignment.Node, artifactsByNode) {
				next = append(next, assignment)
				continue
			}
			batch = append(batch, assignment)
		}
		if len(batch) > 0 {
			results, err := e.executeBatch(ctx, sessionID, workDir, basePrompt, artifactsByNode, batch)
			if err != nil {
				return Result{Artifacts: artifactsByNode, Order: order}, err
			}
			for _, item := range results {
				artifactsByNode[item.assignment.Node.ID] = item.report
				order = append(order, item.assignment.Node.ID)
				if item.runErr != nil {
					return Result{Artifacts: artifactsByNode, Order: order}, item.runErr
				}
			}
			progressed = true
		}
		if !progressed {
			return Result{Artifacts: artifactsByNode, Order: order}, fmt.Errorf("relay executor made no progress; dependency cycle or missing artifact")
		}
		remaining = next
	}

	return Result{Artifacts: artifactsByNode, Order: order}, nil
}

type batchResult struct {
	assignment NodeAssignment
	report     domain.ArtifactEnvelope
	runErr     error
	reportErr  error
}

func (e *Executor) executeBatch(
	ctx context.Context,
	sessionID, workDir, basePrompt string,
	artifactsByNode map[string]domain.ArtifactEnvelope,
	batch []NodeAssignment,
) ([]batchResult, error) {
	results := make([]batchResult, len(batch))
	var wg sync.WaitGroup
	for i, assignment := range batch {
		wg.Add(1)
		go func(i int, assignment NodeAssignment) {
			defer wg.Done()
			if e.lifecycle != nil {
				_ = e.lifecycle.MarkRunning(ctx, sessionID, assignment.Node.ID)
			}

			prompt := e.buildNodePrompt(basePrompt, assignment.Node, artifactsByNode)
			_ = e.events.AppendEvent(ctx, events.Record{
				ID:         fmt.Sprintf("evt_%s_started", assignment.Node.ID),
				SessionID:  sessionID,
				TaskID:     assignment.Node.ID,
				Type:       events.TypeRelayNodeStarted,
				ActorType:  events.ActorTypeScheduler,
				OccurredAt: e.now(),
				Payload: map[string]any{
					"node_id": assignment.Node.ID,
					"agent":   assignment.Profile.ID,
				},
			})

			runResult, runErr := e.supervisor.RunCaptured(ctx, runtime.StartRequest{
				ExecutionID: "exec_" + assignment.Node.ID,
				SessionID:   sessionID,
				TaskID:      assignment.Node.ID,
				Profile:     assignment.Profile,
				Prompt:      prompt,
				WorkingDir:  workDir,
				Continuous:  assignment.Continuous,
				MaxRounds:   assignment.MaxRounds,
			})
			report, reportErr := e.artifacts.BuildReport(ctx, artifacts.BuildReportRequest{
				SessionID: sessionID,
				TaskID:    assignment.Node.ID,
				RunID:     assignment.Node.ID,
				Agent:     assignment.Profile,
				Result:    label(runErr),
				Output:    runResult.Stdout,
				Stderr:    runResult.Stderr,
			})
			results[i] = batchResult{
				assignment: assignment,
				report:     report,
				runErr:     runErr,
				reportErr:  reportErr,
			}
		}(i, assignment)
	}
	wg.Wait()

	for _, item := range results {
		if item.reportErr != nil {
			return nil, item.reportErr
		}
		if e.lifecycle != nil {
			_ = e.lifecycle.MarkFinished(ctx, sessionID, item.assignment.Node.ID, item.report.ID, item.runErr)
		}
		_ = e.events.AppendEvent(ctx, events.Record{
			ID:         fmt.Sprintf("evt_%s_completed", item.assignment.Node.ID),
			SessionID:  sessionID,
			TaskID:     item.assignment.Node.ID,
			Type:       events.TypeRelayNodeCompleted,
			ActorType:  events.ActorTypeScheduler,
			OccurredAt: e.now(),
			ReasonCode: label(item.runErr),
			Payload: map[string]any{
				"node_id":     item.assignment.Node.ID,
				"artifact_id": item.report.ID,
				"agent":       item.assignment.Profile.ID,
			},
		})
	}
	return results, nil
}

func dependenciesReady(node domain.TaskNodeSpec, artifacts map[string]domain.ArtifactEnvelope) bool {
	for _, dep := range node.Dependencies {
		if _, ok := artifacts[dep]; !ok {
			return false
		}
	}
	return true
}

func (e *Executor) buildNodePrompt(basePrompt string, node domain.TaskNodeSpec, artifactsByNode map[string]domain.ArtifactEnvelope) string {
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
	b.WriteString("\nProvide the contribution for this node only.")
	return b.String()
}

func label(err error) string {
	if err != nil {
		return "failed"
	}
	return "success"
}

type taskStoreLifecycle struct {
	tasks store.TaskStore
	now   func() time.Time
}

func (l *taskStoreLifecycle) RegisterTask(ctx context.Context, sessionID string, node domain.TaskNodeSpec, agentID string) error {
	createdAt := l.now()
	return l.tasks.UpsertTask(ctx, domain.TaskRecord{
		ID:           sessionID + "__" + node.ID,
		SessionID:    sessionID,
		Title:        node.Title,
		Strategy:     node.Strategy,
		State:        domain.TaskStatePending,
		AgentID:      agentID,
		Dependencies: node.Dependencies,
		CreatedAt:    createdAt,
		UpdatedAt:    createdAt,
	})
}

func (l *taskStoreLifecycle) ReadyTasks(ctx context.Context, sessionID string) ([]domain.TaskRecord, error) {
	tasks, err := l.tasks.ListTasksBySession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	byNode := make(map[string]domain.TaskRecord, len(tasks))
	for _, task := range tasks {
		nodeID := task.ID
		if prefix := sessionID + "__"; strings.HasPrefix(task.ID, prefix) {
			nodeID = strings.TrimPrefix(task.ID, prefix)
		}
		byNode[nodeID] = task
	}
	ready := make([]domain.TaskRecord, 0, len(tasks))
	for _, task := range tasks {
		nodeID := task.ID
		if prefix := sessionID + "__"; strings.HasPrefix(task.ID, prefix) {
			nodeID = strings.TrimPrefix(task.ID, prefix)
		}
		task = byNode[nodeID]
		if task.State != domain.TaskStatePending && task.State != domain.TaskStateReady {
			continue
		}
		depsReady := true
		for _, dep := range task.Dependencies {
			depTask, ok := byNode[dep]
			if !ok || depTask.State != domain.TaskStateSucceeded {
				depsReady = false
				break
			}
		}
		if depsReady {
			ready = append(ready, task)
		}
	}
	return ready, nil
}

func (l *taskStoreLifecycle) MarkRunning(ctx context.Context, sessionID, nodeID string) error {
	record, err := l.tasks.GetTask(ctx, sessionID+"__"+nodeID)
	if err != nil {
		return err
	}
	record.State = domain.TaskStateRunning
	record.UpdatedAt = l.now()
	return l.tasks.UpsertTask(ctx, record)
}

func (l *taskStoreLifecycle) MarkFinished(ctx context.Context, sessionID, nodeID, artifactID string, runErr error) error {
	record, err := l.tasks.GetTask(ctx, sessionID+"__"+nodeID)
	if err != nil {
		return err
	}
	record.ArtifactID = artifactID
	record.UpdatedAt = l.now()
	if runErr != nil {
		record.State = domain.TaskStateFailedTerminal
	} else {
		record.State = domain.TaskStateSucceeded
	}
	return l.tasks.UpsertTask(ctx, record)
}
