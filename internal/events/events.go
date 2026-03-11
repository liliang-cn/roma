package events

import "time"

// Type identifies a persisted event category.
type Type string

const (
	TypeSessionCreated              Type = "SessionCreated"
	TypeTaskGraphSubmitted          Type = "TaskGraphSubmitted"
	TypeTaskStateChanged            Type = "TaskStateChanged"
	TypeSessionStateChanged         Type = "SessionStateChanged"
	TypeRuntimeStarted              Type = "RuntimeStarted"
	TypeRuntimeStdoutCaptured       Type = "RuntimeStdoutCaptured"
	TypeRuntimeExited               Type = "RuntimeExited"
	TypeArtifactStored              Type = "ArtifactStored"
	TypeRelayNodeStarted            Type = "RelayNodeStarted"
	TypeRelayNodeCompleted          Type = "RelayNodeCompleted"
	TypePolicyDecisionRecorded      Type = "PolicyDecisionRecorded"
	TypeSchedulerCheckpointRecorded Type = "SchedulerCheckpointRecorded"
	TypeSchedulerLeaseRecorded      Type = "SchedulerLeaseRecorded"
	TypeWorkspacePrepared           Type = "WorkspacePrepared"
	TypeWorkspaceReleased           Type = "WorkspaceReleased"
	TypeWorkspaceReclaimed          Type = "WorkspaceReclaimed"
	TypePlanApplied                 Type = "PlanApplied"
	TypePlanRolledBack              Type = "PlanRolledBack"
	TypePlanApplyRejected           Type = "PlanApplyRejected"
	TypePlanApproved                Type = "PlanApproved"
	TypePlanRejected                Type = "PlanRejected"
	TypeGatewayEndpointRegistered   Type = "GatewayEndpointRegistered"
	TypeGatewayDeliveryRecorded     Type = "GatewayDeliveryRecorded"
	TypeRemoteCommandRecorded       Type = "RemoteCommandRecorded"
)

// ActorType identifies who produced an event.
type ActorType string

const (
	ActorTypeSystem    ActorType = "system"
	ActorTypeScheduler ActorType = "scheduler"
	ActorTypePolicy    ActorType = "policy"
	ActorTypeAgent     ActorType = "agent"
	ActorTypeHuman     ActorType = "human"
	ActorTypeGateway   ActorType = "gateway"
)

// Record is the persisted event envelope.
type Record struct {
	ID         string         `json:"id"`
	SessionID  string         `json:"session_id,omitempty"`
	TaskID     string         `json:"task_id,omitempty"`
	Type       Type           `json:"type"`
	ActorType  ActorType      `json:"actor_type"`
	OccurredAt time.Time      `json:"occurred_at"`
	ReasonCode string         `json:"reason_code,omitempty"`
	Payload    map[string]any `json:"payload,omitempty"`
}
