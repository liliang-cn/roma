package relay

import (
	"context"

	"github.com/liliang-cn/roma/internal/domain"
	"github.com/liliang-cn/roma/internal/runtime"
	"github.com/liliang-cn/roma/internal/scheduler"
	"github.com/liliang-cn/roma/internal/store"
)

// NodeAssignment is the compatibility alias for scheduler-owned node execution input.
type NodeAssignment = scheduler.NodeAssignment

// Result is the compatibility alias for scheduler-owned dispatch results.
type Result = scheduler.DispatchResult

// TaskLifecycle is kept only for compatibility with older relay wiring.
// New code should use scheduler.GraphLifecycle directly.
type TaskLifecycle interface {
	RegisterTask(ctx context.Context, sessionID string, node domain.TaskNodeSpec, agentID string) error
	ReadyTasks(ctx context.Context, sessionID string) ([]domain.TaskRecord, error)
	MarkRunning(ctx context.Context, sessionID, nodeID string) error
	MarkFinished(ctx context.Context, sessionID, nodeID, artifactID string, runErr error) error
}

// Executor is a thin compatibility wrapper over scheduler.Dispatcher.
type Executor struct {
	supervisor *runtime.Supervisor
	events     store.EventStore
	tasks      store.TaskStore
}

// NewExecutor constructs a relay executor compatibility wrapper.
func NewExecutor(supervisor *runtime.Supervisor, eventStore store.EventStore) *Executor {
	return &Executor{
		supervisor: supervisor,
		events:     eventStore,
	}
}

// NewExecutorWithTaskStore constructs a relay executor compatibility wrapper with task persistence.
func NewExecutorWithTaskStore(supervisor *runtime.Supervisor, eventStore store.EventStore, taskStore store.TaskStore) *Executor {
	return &Executor{
		supervisor: supervisor,
		events:     eventStore,
		tasks:      taskStore,
	}
}

// NewExecutorWithLifecycle preserves the previous constructor shape but defers lifecycle ownership to scheduler.
func NewExecutorWithLifecycle(supervisor *runtime.Supervisor, eventStore store.EventStore, lifecycle TaskLifecycle) *Executor {
	_ = lifecycle
	return &Executor{
		supervisor: supervisor,
		events:     eventStore,
	}
}

// Execute runs the relay graph through the scheduler-owned dispatcher.
func (e *Executor) Execute(ctx context.Context, sessionID, workDir, basePrompt string, assignments []NodeAssignment) (Result, error) {
	dispatcher := scheduler.NewDispatcher(workDir, e.supervisor, e.events, e.tasks)
	return dispatcher.Execute(ctx, sessionID, workDir, basePrompt, assignments)
}

// Resume continues a relay graph through the scheduler-owned dispatcher.
func (e *Executor) Resume(ctx context.Context, sessionID, workDir, basePrompt string, assignments []NodeAssignment, existing map[string]domain.ArtifactEnvelope) (Result, error) {
	dispatcher := scheduler.NewDispatcher(workDir, e.supervisor, e.events, e.tasks)
	return dispatcher.Resume(ctx, sessionID, workDir, basePrompt, assignments, existing)
}
