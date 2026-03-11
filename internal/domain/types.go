package domain

import "time"

// Confidence represents model or classifier confidence.
type Confidence string

const (
	ConfidenceHigh   Confidence = "high"
	ConfidenceMedium Confidence = "medium"
	ConfidenceLow    Confidence = "low"
)

// ProducerRole describes the logical producer of an artifact.
type ProducerRole string

const (
	ProducerRoleExecutor   ProducerRole = "executor"
	ProducerRolePlanner    ProducerRole = "planner"
	ProducerRoleReviewer   ProducerRole = "reviewer"
	ProducerRoleSenator    ProducerRole = "senator"
	ProducerRoleArbitrator ProducerRole = "arbitrator"
	ProducerRoleSystem     ProducerRole = "system"
	ProducerRoleHuman      ProducerRole = "human"
)

// TaskStrategy defines how a task node executes.
type TaskStrategy string

const (
	TaskStrategyDirect TaskStrategy = "direct"
	TaskStrategyRelay  TaskStrategy = "relay"
	TaskStrategyCuria  TaskStrategy = "curia"
)

// SessionState captures persisted session lifecycle.
type SessionState string

const (
	SessionStatePending           SessionState = "Pending"
	SessionStateRunning           SessionState = "Running"
	SessionStateAwaitingApproval  SessionState = "AwaitingApproval"
	SessionStateBlockedByPolicy   SessionState = "BlockedByPolicy"
	SessionStatePaused            SessionState = "Paused"
	SessionStateSucceeded         SessionState = "Succeeded"
	SessionStateFailedRecoverable SessionState = "FailedRecoverable"
	SessionStateFailedTerminal    SessionState = "FailedTerminal"
	SessionStateCancelled         SessionState = "Cancelled"
)

// TaskState captures task node lifecycle.
type TaskState string

const (
	TaskStatePending           TaskState = "Pending"
	TaskStateReady             TaskState = "Ready"
	TaskStateRunning           TaskState = "Running"
	TaskStateAwaitingQuorum    TaskState = "AwaitingQuorum"
	TaskStateUnderReview       TaskState = "UnderReview"
	TaskStateUnderArbitration  TaskState = "UnderArbitration"
	TaskStateAwaitingApproval  TaskState = "AwaitingApproval"
	TaskStateBlockedByPolicy   TaskState = "BlockedByPolicy"
	TaskStateSucceeded         TaskState = "Succeeded"
	TaskStateFailedRecoverable TaskState = "FailedRecoverable"
	TaskStateFailedTerminal    TaskState = "FailedTerminal"
	TaskStateCancelled         TaskState = "Cancelled"
)

// ArtifactKind is the envelope-level artifact type.
type ArtifactKind string

const (
	ArtifactKindProposal      ArtifactKind = "proposal"
	ArtifactKindBallot        ArtifactKind = "ballot"
	ArtifactKindDebateLog     ArtifactKind = "debate_log"
	ArtifactKindDecisionPack  ArtifactKind = "decision_pack"
	ArtifactKindExecutionPlan ArtifactKind = "execution_plan"
	ArtifactKindReport        ArtifactKind = "report"
)

// Producer identifies the artifact producer.
type Producer struct {
	AgentID string       `json:"agent_id"`
	Role    ProducerRole `json:"role"`
	RunID   string       `json:"run_id,omitempty"`
}

// AttachmentRef points to a persisted blob.
type AttachmentRef struct {
	Name      string `json:"name"`
	MediaType string `json:"media_type"`
	BlobRef   string `json:"blob_ref"`
	SizeBytes int64  `json:"size_bytes"`
	Checksum  string `json:"checksum"`
	Purpose   string `json:"purpose"`
}

// ArtifactEnvelope wraps all structured outputs.
type ArtifactEnvelope struct {
	ID            string          `json:"id"`
	Kind          ArtifactKind    `json:"kind"`
	SchemaVersion string          `json:"schema_version"`
	Producer      Producer        `json:"producer"`
	SessionID     string          `json:"session_id"`
	TaskID        string          `json:"task_id"`
	CreatedAt     time.Time       `json:"created_at"`
	PayloadSchema string          `json:"payload_schema"`
	Payload       any             `json:"payload"`
	Attachments   []AttachmentRef `json:"attachments,omitempty"`
	Checksum      string          `json:"checksum"`
}

// TaskNodeSpec describes a scheduler node contract.
type TaskNodeSpec struct {
	ID              string       `json:"id"`
	Title           string       `json:"title"`
	Strategy        TaskStrategy `json:"strategy"`
	Dependencies    []string     `json:"dependencies,omitempty"`
	Senators        []string     `json:"senators,omitempty"`
	Quorum          int          `json:"quorum,omitempty"`
	ArbitrationMode string       `json:"arbitration_mode,omitempty"`
	Arbitrator      string       `json:"arbitrator,omitempty"`
	SchemaVersion   string       `json:"schema_version"`
	ExpectedOutputs []string     `json:"expected_outputs,omitempty"`
}

// SessionRecord stores persisted session metadata.
type SessionRecord struct {
	ID          string       `json:"id"`
	State       SessionState `json:"state"`
	CreatedAt   time.Time    `json:"created_at"`
	UpdatedAt   time.Time    `json:"updated_at"`
	Description string       `json:"description,omitempty"`
}

// TaskRecord stores persisted task metadata.
type TaskRecord struct {
	ID              string       `json:"id"`
	SessionID       string       `json:"session_id"`
	Title           string       `json:"title"`
	Strategy        TaskStrategy `json:"strategy"`
	State           TaskState    `json:"state"`
	AgentID         string       `json:"agent_id,omitempty"`
	ApprovalGranted bool         `json:"approval_granted,omitempty"`
	Dependencies    []string     `json:"dependencies,omitempty"`
	ArtifactID      string       `json:"artifact_id,omitempty"`
	CreatedAt       time.Time    `json:"created_at"`
	UpdatedAt       time.Time    `json:"updated_at"`
}
