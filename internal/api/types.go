package api

import (
	"github.com/liliang/roma/internal/domain"
	"github.com/liliang/roma/internal/events"
	"github.com/liliang/roma/internal/history"
	"github.com/liliang/roma/internal/queue"
	"github.com/liliang/roma/internal/scheduler"
	"github.com/liliang/roma/internal/workspace"
)

// SubmitRequest is the daemon API payload for queue submission.
type SubmitRequest struct {
	GraphFile    string              `json:"graph_file,omitempty"`
	Graph        *GraphSubmitRequest `json:"graph,omitempty"`
	Prompt       string              `json:"prompt"`
	StarterAgent string              `json:"starter_agent"`
	Delegates    []string            `json:"delegates,omitempty"`
	WorkingDir   string              `json:"working_dir"`
	Continuous   bool                `json:"continuous,omitempty"`
	MaxRounds    int                 `json:"max_rounds,omitempty"`
}

// GraphSubmitNode is one node in the inline graph submit payload.
type GraphSubmitNode struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	Agent        string   `json:"agent"`
	Strategy     string   `json:"strategy"`
	Dependencies []string `json:"dependencies,omitempty"`
}

// GraphSubmitRequest is the first-class graph submit payload.
type GraphSubmitRequest struct {
	Prompt string            `json:"prompt"`
	Nodes  []GraphSubmitNode `json:"nodes"`
}

// SubmitResponse is returned after a queued submission is accepted.
type SubmitResponse struct {
	JobID string `json:"job_id"`
}

// QueueListResponse lists queued jobs.
type QueueListResponse struct {
	Items []queue.Request `json:"items"`
}

// SessionListResponse lists persisted sessions.
type SessionListResponse struct {
	Items []history.SessionRecord `json:"items"`
}

// TaskListResponse lists persisted task records.
type TaskListResponse struct {
	Items []domain.TaskRecord `json:"items"`
}

// EventListResponse lists persisted event records.
type EventListResponse struct {
	Items []events.Record `json:"items"`
}

// QueueInspectResponse expands a queued job into its execution records.
type QueueInspectResponse struct {
	Job                    queue.Request             `json:"job"`
	Session                *history.SessionRecord    `json:"session,omitempty"`
	Lease                  *scheduler.LeaseRecord    `json:"lease,omitempty"`
	PendingApprovalTaskIDs []string                  `json:"pending_approval_task_ids,omitempty"`
	ApprovalResumeReady    bool                      `json:"approval_resume_ready"`
	Tasks                  []domain.TaskRecord       `json:"tasks,omitempty"`
	Artifacts              []domain.ArtifactEnvelope `json:"artifacts,omitempty"`
	Events                 []events.Record           `json:"events,omitempty"`
	Workspaces             []workspace.Prepared      `json:"workspaces,omitempty"`
}

// WorkspaceListResponse lists persisted workspace records.
type WorkspaceListResponse struct {
	Items []workspace.Prepared `json:"items"`
}

// SessionInspectResponse expands a session into its execution records.
type SessionInspectResponse struct {
	Session                history.SessionRecord     `json:"session"`
	Lease                  *scheduler.LeaseRecord    `json:"lease,omitempty"`
	PendingApprovalTaskIDs []string                  `json:"pending_approval_task_ids,omitempty"`
	ApprovalResumeReady    bool                      `json:"approval_resume_ready"`
	Tasks                  []domain.TaskRecord       `json:"tasks,omitempty"`
	Artifacts              []domain.ArtifactEnvelope `json:"artifacts,omitempty"`
	Events                 []events.Record           `json:"events,omitempty"`
	Workspaces             []workspace.Prepared      `json:"workspaces,omitempty"`
}
