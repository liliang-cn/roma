package scheduler

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/liliang-cn/roma/internal/domain"
	"github.com/liliang-cn/roma/internal/events"
	"github.com/liliang-cn/roma/internal/store"
)

// GraphLifecycle owns task-record state transitions for graph execution.
type GraphLifecycle struct {
	tasks  store.TaskStore
	events store.EventStore
	now    func() time.Time
}

// NewGraphLifecycle constructs a graph task lifecycle controller.
func NewGraphLifecycle(taskStore store.TaskStore, eventStore store.EventStore) *GraphLifecycle {
	return &GraphLifecycle{
		tasks:  taskStore,
		events: eventStore,
		now:    func() time.Time { return time.Now().UTC() },
	}
}

// RegisterTask persists the initial task record.
func (g *GraphLifecycle) RegisterTask(ctx context.Context, sessionID string, node domain.TaskNodeSpec, agentID string) error {
	createdAt := g.now()
	record := domain.TaskRecord{
		ID:           taskRecordID(sessionID, node.ID),
		SessionID:    sessionID,
		Title:        node.Title,
		Strategy:     node.Strategy,
		State:        domain.TaskStatePending,
		AgentID:      agentID,
		Dependencies: node.Dependencies,
		CreatedAt:    createdAt,
		UpdatedAt:    createdAt,
	}
	if err := g.tasks.UpsertTask(ctx, record); err != nil {
		return err
	}
	return g.appendTaskStateEvent(ctx, record)
}

// MarkRunning transitions a task into running state.
func (g *GraphLifecycle) MarkRunning(ctx context.Context, sessionID, nodeID string) error {
	record, err := g.tasks.GetTask(ctx, taskRecordID(sessionID, nodeID))
	if err != nil {
		return err
	}
	record.State = domain.TaskStateRunning
	record.UpdatedAt = g.now()
	if err := g.tasks.UpsertTask(ctx, record); err != nil {
		return err
	}
	return g.appendTaskStateEvent(ctx, record)
}

// MarkAwaitingApproval transitions a task into approval-required state.
func (g *GraphLifecycle) MarkAwaitingApproval(ctx context.Context, sessionID, nodeID string) error {
	record, err := g.tasks.GetTask(ctx, taskRecordID(sessionID, nodeID))
	if err != nil {
		return err
	}
	record.State = domain.TaskStateAwaitingApproval
	record.UpdatedAt = g.now()
	if err := g.tasks.UpsertTask(ctx, record); err != nil {
		return err
	}
	return g.appendTaskStateEvent(ctx, record)
}

// ApproveTask releases a task from approval gate and marks it ready.
func (g *GraphLifecycle) ApproveTask(ctx context.Context, taskID string) error {
	record, err := g.tasks.GetTask(ctx, taskID)
	if err != nil {
		return err
	}
	record.ApprovalGranted = true
	record.State = domain.TaskStateReady
	record.UpdatedAt = g.now()
	if err := g.tasks.UpsertTask(ctx, record); err != nil {
		return err
	}
	return g.appendTaskStateEvent(ctx, record)
}

// RejectTask marks a task cancelled after approval rejection.
func (g *GraphLifecycle) RejectTask(ctx context.Context, taskID string) error {
	record, err := g.tasks.GetTask(ctx, taskID)
	if err != nil {
		return err
	}
	record.State = domain.TaskStateCancelled
	record.UpdatedAt = g.now()
	if err := g.tasks.UpsertTask(ctx, record); err != nil {
		return err
	}
	return g.appendTaskStateEvent(ctx, record)
}

// ReadyNodeIDs returns node ids currently eligible for dispatch and marks them Ready.
func (g *GraphLifecycle) ReadyNodeIDs(ctx context.Context, sessionID string) ([]string, error) {
	readyTasks, err := g.ReadyTasks(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	ready := make([]string, 0, len(readyTasks))
	for _, task := range readyTasks {
		nodeID := task.ID
		if prefix := sessionID + "__"; strings.HasPrefix(task.ID, prefix) {
			nodeID = strings.TrimPrefix(task.ID, prefix)
		}
		ready = append(ready, nodeID)
	}
	return ready, nil
}

// ReadyTasks returns task records currently eligible for dispatch and marks them Ready.
func (g *GraphLifecycle) ReadyTasks(ctx context.Context, sessionID string) ([]domain.TaskRecord, error) {
	tasks, err := g.tasks.ListTasksBySession(ctx, sessionID)
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
	readyIDs := make([]string, 0, len(tasks))
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
		if !depsReady {
			continue
		}
		if task.State == domain.TaskStatePending {
			task.State = domain.TaskStateReady
			task.UpdatedAt = g.now()
			if err := g.tasks.UpsertTask(ctx, task); err != nil {
				return nil, err
			}
			if err := g.appendTaskStateEvent(ctx, task); err != nil {
				return nil, err
			}
			byNode[nodeID] = task
		}
		ready = append(ready, task)
		readyIDs = append(readyIDs, nodeID)
	}
	if err := g.appendCheckpointEvent(ctx, sessionID, readyIDs); err != nil {
		return nil, err
	}
	return ready, nil
}

// MarkFinished transitions a task into terminal state and records artifact linkage.
func (g *GraphLifecycle) MarkFinished(ctx context.Context, sessionID, nodeID, artifactID string, runErr error) error {
	record, err := g.tasks.GetTask(ctx, taskRecordID(sessionID, nodeID))
	if err != nil {
		return err
	}
	record.ArtifactID = artifactID
	record.UpdatedAt = g.now()
	if runErr != nil {
		record.State = domain.TaskStateFailedTerminal
	} else {
		record.State = domain.TaskStateSucceeded
	}
	if err := g.tasks.UpsertTask(ctx, record); err != nil {
		return err
	}
	return g.appendTaskStateEvent(ctx, record)
}

func (g *GraphLifecycle) appendTaskStateEvent(ctx context.Context, record domain.TaskRecord) error {
	if g.events == nil {
		return nil
	}
	return g.events.AppendEvent(ctx, events.Record{
		ID:         "evt_" + record.ID + "_state_" + string(record.State),
		SessionID:  record.SessionID,
		TaskID:     record.ID,
		Type:       events.TypeTaskStateChanged,
		ActorType:  events.ActorTypeScheduler,
		OccurredAt: record.UpdatedAt,
		ReasonCode: string(record.State),
		Payload: map[string]any{
			"node_title":   record.Title,
			"strategy":     record.Strategy,
			"agent_id":     record.AgentID,
			"artifact_id":  record.ArtifactID,
			"dependencies": record.Dependencies,
		},
	})
}

func (g *GraphLifecycle) appendCheckpointEvent(ctx context.Context, sessionID string, readyIDs []string) error {
	if g.events == nil {
		return nil
	}
	return g.events.AppendEvent(ctx, events.Record{
		ID:         "evt_" + sessionID + "_checkpoint_" + fmt.Sprintf("%d", g.now().UnixNano()),
		SessionID:  sessionID,
		Type:       events.TypeSchedulerCheckpointRecorded,
		ActorType:  events.ActorTypeScheduler,
		OccurredAt: g.now(),
		Payload: map[string]any{
			"ready_task_ids": readyIDs,
			"ready_count":    len(readyIDs),
		},
	})
}

func taskRecordID(sessionID, nodeID string) string {
	return sessionID + "__" + nodeID
}
