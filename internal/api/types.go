package api

import (
	"github.com/liliang/roma/internal/artifacts"
	"github.com/liliang/roma/internal/curia"
	"github.com/liliang/roma/internal/domain"
	"github.com/liliang/roma/internal/events"
	"github.com/liliang/roma/internal/history"
	"github.com/liliang/roma/internal/plans"
	"github.com/liliang/roma/internal/queue"
	"github.com/liliang/roma/internal/scheduler"
	"github.com/liliang/roma/internal/workspace"
)

// SubmitRequest is the daemon API payload for queue submission.
type SubmitRequest struct {
	GraphFile           string              `json:"graph_file,omitempty"`
	Graph               *GraphSubmitRequest `json:"graph,omitempty"`
	Prompt              string              `json:"prompt"`
	StarterAgent        string              `json:"starter_agent"`
	Delegates           []string            `json:"delegates,omitempty"`
	WorkingDir          string              `json:"working_dir"`
	PolicyOverride      bool                `json:"policy_override,omitempty"`
	PolicyOverrideActor string              `json:"policy_override_actor,omitempty"`
	Continuous          bool                `json:"continuous,omitempty"`
	MaxRounds           int                 `json:"max_rounds,omitempty"`
}

// GraphSubmitNode is one node in the inline graph submit payload.
type GraphSubmitNode struct {
	ID              string   `json:"id"`
	Title           string   `json:"title"`
	Agent           string   `json:"agent"`
	Strategy        string   `json:"strategy"`
	Dependencies    []string `json:"dependencies,omitempty"`
	Senators        []string `json:"senators,omitempty"`
	Quorum          int      `json:"quorum,omitempty"`
	ArbitrationMode string   `json:"arbitration_mode,omitempty"`
	Arbitrator      string   `json:"arbitrator,omitempty"`
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

// RecoveryListResponse lists daemon recovery snapshots.
type RecoveryListResponse struct {
	Items []scheduler.RecoverySnapshot `json:"items"`
}

// StatusResponse reports daemon-owned workspace status counters.
type StatusResponse struct {
	QueueItems           int    `json:"queue_items"`
	Sessions             int    `json:"sessions"`
	Artifacts            int    `json:"artifacts"`
	Events               int    `json:"events"`
	ActiveLeases         int    `json:"active_leases"`
	ReleasedLeases       int    `json:"released_leases"`
	RecoveredLeases      int    `json:"recovered_leases"`
	PendingApprovalTasks int    `json:"pending_approval_tasks"`
	RecoverableSessions  int    `json:"recoverable_sessions"`
	PreparedWorkspaces   int    `json:"prepared_workspaces"`
	ReleasedWorkspaces   int    `json:"released_workspaces"`
	ReclaimedWorkspaces  int    `json:"reclaimed_workspaces"`
	MergedWorkspaces     int    `json:"merged_workspaces"`
	SQLiteEnabled        bool   `json:"sqlite_enabled"`
	SQLitePath           string `json:"sqlite_path"`
	SQLiteBytes          int64  `json:"sqlite_bytes"`
}

// PlanActionSummary condenses execution-plan audit events into one inspectable view.
type PlanActionSummary struct {
	ArtifactID      string                      `json:"artifact_id"`
	TaskID          string                      `json:"task_id,omitempty"`
	EventType       string                      `json:"event_type"`
	Reason          string                      `json:"reason,omitempty"`
	ChangedPaths    []string                    `json:"changed_paths,omitempty"`
	Violations      []string                    `json:"violations,omitempty"`
	Conflict        bool                        `json:"conflict,omitempty"`
	ConflictDetail  string                      `json:"conflict_detail,omitempty"`
	ConflictPaths   []string                    `json:"conflict_paths,omitempty"`
	ConflictContext []workspace.ConflictSnippet `json:"conflict_context,omitempty"`
	RequiredChecks  []string                    `json:"required_checks,omitempty"`
	OccurredAt      string                      `json:"occurred_at"`
}

type CuriaScoreSummary struct {
	ProposalID    string `json:"proposal_id"`
	RawScore      int    `json:"raw_score"`
	WeightedScore int    `json:"weighted_score"`
	VetoCount     int    `json:"veto_count"`
	ReviewerCount int    `json:"reviewer_count"`
}

type CuriaReviewerSummary struct {
	ReviewerID       string `json:"reviewer_id"`
	EffectiveWeight  int    `json:"effective_weight"`
	ReviewCount      int    `json:"review_count,omitempty"`
	AlignmentCount   int    `json:"alignment_count,omitempty"`
	VetoCount        int    `json:"veto_count,omitempty"`
	ArbitrationCount int    `json:"arbitration_count,omitempty"`
}

type CuriaSummary struct {
	Dispute             bool                                `json:"dispute"`
	DisputeClass        string                              `json:"dispute_class,omitempty"`
	Arbitrated          bool                                `json:"arbitrated,omitempty"`
	ArbitratorID        string                              `json:"arbitrator_id,omitempty"`
	CriticalVeto        bool                                `json:"critical_veto"`
	TopScoreGap         int                                 `json:"top_score_gap"`
	DisputeReasons      []string                            `json:"dispute_reasons,omitempty"`
	WinningMode         string                              `json:"winning_mode,omitempty"`
	SelectedProposalIDs []string                            `json:"selected_proposal_ids,omitempty"`
	RiskFlags           []string                            `json:"risk_flags,omitempty"`
	ReviewQuestions     []string                            `json:"review_questions,omitempty"`
	CandidateSummaries  []artifacts.CuriaCandidateSummary   `json:"candidate_summaries,omitempty"`
	ReviewerBreakdown   []artifacts.CuriaReviewContribution `json:"reviewer_breakdown,omitempty"`
	ReviewerWeights     []CuriaReviewerSummary              `json:"reviewer_weights,omitempty"`
	Scoreboard          []CuriaScoreSummary                 `json:"scoreboard,omitempty"`
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
	Plans                  []PlanActionSummary       `json:"plans,omitempty"`
	Curia                  *CuriaSummary             `json:"curia,omitempty"`
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
	Plans                  []PlanActionSummary       `json:"plans,omitempty"`
	Curia                  *CuriaSummary             `json:"curia,omitempty"`
}

type PlanApplyRequest struct {
	SessionID           string `json:"session_id,omitempty"`
	TaskID              string `json:"task_id,omitempty"`
	ArtifactID          string `json:"artifact_id"`
	DryRun              bool   `json:"dry_run,omitempty"`
	PolicyOverride      bool   `json:"policy_override,omitempty"`
	PolicyOverrideActor string `json:"policy_override_actor,omitempty"`
}

type PlanInspectResponse struct {
	Artifact domain.ArtifactEnvelope `json:"artifact"`
}

type PlanInboxEntry struct {
	ArtifactID            string                      `json:"artifact_id"`
	SessionID             string                      `json:"session_id"`
	TaskID                string                      `json:"task_id"`
	Goal                  string                      `json:"goal,omitempty"`
	Status                string                      `json:"status"`
	HumanApprovalRequired bool                        `json:"human_approval_required"`
	ExpectedFiles         []string                    `json:"expected_files,omitempty"`
	ForbiddenPaths        []string                    `json:"forbidden_paths,omitempty"`
	LastEventType         string                      `json:"last_event_type,omitempty"`
	LastReason            string                      `json:"last_reason,omitempty"`
	LastOccurredAt        string                      `json:"last_occurred_at,omitempty"`
	Violations            []string                    `json:"violations,omitempty"`
	Conflict              bool                        `json:"conflict,omitempty"`
	ConflictDetail        string                      `json:"conflict_detail,omitempty"`
	ConflictPaths         []string                    `json:"conflict_paths,omitempty"`
	ConflictContext       []workspace.ConflictSnippet `json:"conflict_context,omitempty"`
	RemediationHint       string                      `json:"remediation_hint,omitempty"`
}

type PlanInboxResponse struct {
	Items []PlanInboxEntry `json:"items"`
}

type CuriaReputationResponse struct {
	Items []curia.ReputationRecord `json:"items"`
}

type PlanDecisionRequest struct {
	Actor string `json:"actor,omitempty"`
}

type PlanApplyResponse = plans.ApplyResult
