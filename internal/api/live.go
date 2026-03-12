package api

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/liliang-cn/roma/internal/domain"
	"github.com/liliang-cn/roma/internal/events"
	"github.com/liliang-cn/roma/internal/scheduler"
	"github.com/liliang-cn/roma/internal/workspace"
)

// RuntimeLiveSummary captures best-effort live execution state for a running session.
type RuntimeLiveSummary struct {
	State             string     `json:"state,omitempty"`
	CurrentTaskID     string     `json:"current_task_id,omitempty"`
	CurrentTaskTitle  string     `json:"current_task_title,omitempty"`
	CurrentTaskState  string     `json:"current_task_state,omitempty"`
	CurrentAgentID    string     `json:"current_agent_id,omitempty"`
	ExecutionID       string     `json:"execution_id,omitempty"`
	ProcessPID        int        `json:"process_pid,omitempty"`
	WorkspacePath     string     `json:"workspace_path,omitempty"`
	WorkspaceProvider string     `json:"workspace_provider,omitempty"`
	WorkspaceStatus   string     `json:"workspace_status,omitempty"`
	StartedAt         *time.Time `json:"started_at,omitempty"`
	LastOutputAt      *time.Time `json:"last_output_at,omitempty"`
	LastEventAt       *time.Time `json:"last_event_at,omitempty"`
	LastHeartbeatAt   *time.Time `json:"last_heartbeat_at,omitempty"`
	LastEventType     string     `json:"last_event_type,omitempty"`
	LastOutputPreview string     `json:"last_output_preview,omitempty"`
}

// SummarizeRuntimeLive derives a best-effort running summary from persisted session data.
func SummarizeRuntimeLive(sessionStatus string, tasks []domain.TaskRecord, records []events.Record, workspaces []workspace.Prepared, lease *scheduler.LeaseRecord, heartbeatAt time.Time) *RuntimeLiveSummary {
	if sessionStatus == "" && len(tasks) == 0 && len(records) == 0 && len(workspaces) == 0 && lease == nil && heartbeatAt.IsZero() {
		return nil
	}

	summary := &RuntimeLiveSummary{State: sessionStatus}
	currentTask := selectCurrentTask(tasks, sessionStatus)
	if currentTask != nil {
		summary.CurrentTaskID = currentTask.ID
		summary.CurrentTaskTitle = currentTask.Title
		summary.CurrentTaskState = string(currentTask.State)
		summary.CurrentAgentID = currentTask.AgentID
	}

	runtimeStarted, runtimeOutput, latestEvent := selectRuntimeEvents(summary.CurrentTaskID, records)
	if runtimeStarted != nil {
		summary.ExecutionID = payloadString(runtimeStarted.Payload, "execution_id")
		summary.ProcessPID = payloadInt(runtimeStarted.Payload, "pid")
		if agent := payloadString(runtimeStarted.Payload, "agent"); agent != "" {
			summary.CurrentAgentID = agent
		}
		summary.StartedAt = timePtr(runtimeStarted.OccurredAt)
	}
	if runtimeOutput != nil {
		summary.LastOutputAt = timePtr(runtimeOutput.OccurredAt)
		summary.LastOutputPreview = compactOutputPreview(payloadString(runtimeOutput.Payload, "stdout"))
		if summary.CurrentAgentID == "" {
			summary.CurrentAgentID = payloadString(runtimeOutput.Payload, "agent")
		}
	}
	if latestEvent != nil {
		summary.LastEventAt = timePtr(latestEvent.OccurredAt)
		summary.LastEventType = string(latestEvent.Type)
	}

	if summary.CurrentTaskID == "" {
		if taskID, title, state, agent := selectTaskFromEvents(records); taskID != "" {
			summary.CurrentTaskID = taskID
			summary.CurrentTaskTitle = title
			summary.CurrentTaskState = state
			if summary.CurrentAgentID == "" {
				summary.CurrentAgentID = agent
			}
		}
	}

	if prepared := selectWorkspace(summary.CurrentTaskID, workspaces, lease); prepared != nil {
		summary.WorkspacePath = prepared.EffectiveDir
		summary.WorkspaceProvider = prepared.Provider
		summary.WorkspaceStatus = prepared.Status
	}

	summary.LastHeartbeatAt = selectHeartbeat(heartbeatAt, lease)
	approvalBlocked := hasAwaitingApproval(tasks, lease) || sessionStatus == "awaiting_approval"
	switch {
	case summary.ExecutionID != "" || hasRunningTask(tasks):
		summary.State = "running"
	case approvalBlocked:
		summary.State = "awaiting_approval"
	case summary.State == "":
		summary.State = "idle"
	}

	if summary.CurrentTaskID == "" && summary.ExecutionID == "" && summary.LastEventAt == nil && summary.WorkspacePath == "" && summary.LastHeartbeatAt == nil {
		return nil
	}
	return summary
}

func selectCurrentTask(tasks []domain.TaskRecord, sessionStatus string) *domain.TaskRecord {
	var selected *domain.TaskRecord
	for i := range tasks {
		task := tasks[i]
		if task.State == domain.TaskStateRunning {
			if selected == nil || task.UpdatedAt.After(selected.UpdatedAt) {
				item := task
				selected = &item
			}
		}
	}
	if selected != nil {
		return selected
	}
	if sessionStatus != "awaiting_approval" {
		return nil
	}
	for i := range tasks {
		task := tasks[i]
		if task.State == domain.TaskStateAwaitingApproval {
			if selected == nil || task.UpdatedAt.After(selected.UpdatedAt) {
				item := task
				selected = &item
			}
		}
	}
	return selected
}

func selectRuntimeEvents(currentTaskID string, records []events.Record) (started *events.Record, output *events.Record, latest *events.Record) {
	starts := make(map[string]events.Record)
	exits := make(map[string]time.Time)
	for i := range records {
		record := records[i]
		if latest == nil || record.OccurredAt.After(latest.OccurredAt) {
			item := record
			latest = &item
		}
		switch record.Type {
		case events.TypeRuntimeStarted:
			execID := payloadString(record.Payload, "execution_id")
			if execID != "" {
				starts[execID] = record
			}
		case events.TypeRuntimeExited:
			execID := payloadString(record.Payload, "execution_id")
			if execID != "" {
				exits[execID] = record.OccurredAt
			}
		case events.TypeRuntimeStdoutCaptured:
			if currentTaskID != "" && record.TaskID != currentTaskID {
				continue
			}
			if output == nil || record.OccurredAt.After(output.OccurredAt) {
				item := record
				output = &item
			}
		}
	}

	for execID, record := range starts {
		if currentTaskID != "" && record.TaskID != currentTaskID {
			continue
		}
		if exitedAt, ok := exits[execID]; ok && !record.OccurredAt.After(exitedAt) {
			continue
		}
		if started == nil || record.OccurredAt.After(started.OccurredAt) {
			item := record
			started = &item
		}
	}
	return started, output, latest
}

func selectTaskFromEvents(records []events.Record) (taskID string, title string, state string, agent string) {
	completed := map[string]struct{}{}
	var current *events.Record
	for i := range records {
		record := records[i]
		switch record.Type {
		case events.TypeRelayNodeCompleted:
			completed[record.TaskID] = struct{}{}
		case events.TypeRelayNodeStarted:
			if _, ok := completed[record.TaskID]; ok {
				continue
			}
			if current == nil || record.OccurredAt.After(current.OccurredAt) {
				item := record
				current = &item
			}
		}
	}
	if current == nil {
		return "", "", "", ""
	}
	return current.TaskID, payloadString(current.Payload, "node_id"), "running", payloadString(current.Payload, "agent")
}

func selectWorkspace(taskID string, workspaces []workspace.Prepared, lease *scheduler.LeaseRecord) *workspace.Prepared {
	for i := range workspaces {
		if taskID != "" && workspaces[i].TaskID == taskID {
			item := workspaces[i]
			return &item
		}
	}
	if lease != nil {
		for _, ref := range lease.WorkspaceRefs {
			if taskID != "" && ref.TaskID == taskID {
				return &workspace.Prepared{
					TaskID:       ref.TaskID,
					EffectiveDir: ref.EffectiveDir,
					Provider:     ref.Provider,
					Status:       "prepared",
				}
			}
		}
	}
	for i := range workspaces {
		if workspaces[i].Status == "prepared" {
			item := workspaces[i]
			return &item
		}
	}
	return nil
}

func selectHeartbeat(heartbeatAt time.Time, lease *scheduler.LeaseRecord) *time.Time {
	if lease != nil && !lease.UpdatedAt.IsZero() && (heartbeatAt.IsZero() || lease.UpdatedAt.After(heartbeatAt)) {
		return timePtr(lease.UpdatedAt)
	}
	if heartbeatAt.IsZero() {
		return nil
	}
	return timePtr(heartbeatAt)
}

func hasRunningTask(tasks []domain.TaskRecord) bool {
	for _, task := range tasks {
		if task.State == domain.TaskStateRunning {
			return true
		}
	}
	return false
}

func hasAwaitingApproval(tasks []domain.TaskRecord, lease *scheduler.LeaseRecord) bool {
	if lease != nil && len(lease.PendingApprovalTaskIDs) > 0 {
		return true
	}
	for _, task := range tasks {
		if task.State == domain.TaskStateAwaitingApproval {
			return true
		}
	}
	return false
}

func payloadString(payload map[string]any, key string) string {
	if payload == nil {
		return ""
	}
	value, ok := payload[key]
	if !ok {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	case fmt.Stringer:
		return typed.String()
	default:
		return fmt.Sprint(typed)
	}
}

func payloadInt(payload map[string]any, key string) int {
	if payload == nil {
		return 0
	}
	value, ok := payload[key]
	if !ok {
		return 0
	}
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case string:
		n, _ := strconv.Atoi(strings.TrimSpace(typed))
		return n
	default:
		n, _ := strconv.Atoi(strings.TrimSpace(fmt.Sprint(typed)))
		return n
	}
}

func compactOutputPreview(stdout string) string {
	stdout = strings.TrimSpace(stdout)
	if stdout == "" {
		return ""
	}
	lines := strings.Split(stdout, "\n")
	last := strings.TrimSpace(lines[len(lines)-1])
	if len(last) > 160 {
		return last[:157] + "..."
	}
	return last
}

func timePtr(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	copy := value
	return &copy
}
