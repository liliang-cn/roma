package domain

import (
	"fmt"
	"time"
)

// GatewayEndpointType identifies a remote delivery channel.
type GatewayEndpointType string

const (
	GatewayEndpointTypeWSS      GatewayEndpointType = "wss"
	GatewayEndpointTypeWebhook  GatewayEndpointType = "webhook"
	GatewayEndpointTypeTelegram GatewayEndpointType = "telegram"
)

// RemoteCommandAction identifies an allowed remote intent.
type RemoteCommandAction string

const (
	RemoteCommandActionApprove RemoteCommandAction = "approve"
	RemoteCommandActionReject  RemoteCommandAction = "reject"
	RemoteCommandActionPause   RemoteCommandAction = "pause"
	RemoteCommandActionResume  RemoteCommandAction = "resume"
	RemoteCommandActionCancel  RemoteCommandAction = "cancel"
	RemoteCommandActionRetry   RemoteCommandAction = "retry"
)

// NotificationSeverity identifies notification urgency.
type NotificationSeverity string

const (
	NotificationSeverityLow    NotificationSeverity = "low"
	NotificationSeverityMedium NotificationSeverity = "medium"
	NotificationSeverityHigh   NotificationSeverity = "high"
)

// EventLevel groups remote event verbosity.
type EventLevel string

const (
	EventLevelControl  EventLevel = "control"
	EventLevelDecision EventLevel = "decision"
	EventLevelVerbose  EventLevel = "verbose"
)

// GatewayEndpoint describes a remote endpoint.
type GatewayEndpoint struct {
	ID             string                `json:"id"`
	Type           GatewayEndpointType   `json:"type"`
	Enabled        bool                  `json:"enabled"`
	Target         string                `json:"target"`
	SecretRef      string                `json:"secret_ref,omitempty"`
	AllowedActions []RemoteCommandAction `json:"allowed_actions,omitempty"`
}

// RemoteSubscription defines the notification filter for an endpoint.
type RemoteSubscription struct {
	EndpointID          string               `json:"endpoint_id"`
	EventTypes          []string             `json:"event_types,omitempty"`
	SessionFilter       []string             `json:"session_filter,omitempty"`
	SeverityThreshold   NotificationSeverity `json:"severity_threshold"`
	SummaryMode         string               `json:"summary_mode,omitempty"`
	IncludeArtifactRefs bool                 `json:"include_artifact_refs"`
}

// RemoteCommand is an authenticated intent from a remote endpoint.
type RemoteCommand struct {
	CommandID        string              `json:"command_id"`
	SourceEndpointID string              `json:"source_endpoint_id"`
	Actor            string              `json:"actor"`
	SessionID        string              `json:"session_id"`
	TaskID           string              `json:"task_id,omitempty"`
	Action           RemoteCommandAction `json:"action"`
	Args             map[string]any      `json:"args,omitempty"`
	IssuedAt         time.Time           `json:"issued_at"`
	Signature        string              `json:"signature,omitempty"`
}

// NotificationEnvelope is the normalized outbound payload.
type NotificationEnvelope struct {
	ID           string                `json:"id"`
	Type         string                `json:"type"`
	Severity     NotificationSeverity  `json:"severity"`
	SessionID    string                `json:"session_id"`
	TaskID       string                `json:"task_id,omitempty"`
	Title        string                `json:"title"`
	Summary      string                `json:"summary"`
	ArtifactRefs []string              `json:"artifact_refs,omitempty"`
	Actions      []RemoteCommandAction `json:"actions,omitempty"`
	CreatedAt    time.Time             `json:"created_at"`
}

// ValidateGatewayEndpoint checks minimal endpoint invariants.
func ValidateGatewayEndpoint(endpoint GatewayEndpoint) error {
	if endpoint.ID == "" {
		return fmt.Errorf("gateway endpoint id is required")
	}
	switch endpoint.Type {
	case GatewayEndpointTypeWSS, GatewayEndpointTypeWebhook, GatewayEndpointTypeTelegram:
	default:
		return fmt.Errorf("unknown gateway endpoint type %q", endpoint.Type)
	}
	if endpoint.Target == "" {
		return fmt.Errorf("gateway endpoint target is required")
	}
	for _, action := range endpoint.AllowedActions {
		if err := ValidateRemoteCommandAction(action); err != nil {
			return err
		}
	}
	return nil
}

// ValidateRemoteCommandAction checks if the action is supported.
func ValidateRemoteCommandAction(action RemoteCommandAction) error {
	switch action {
	case RemoteCommandActionApprove,
		RemoteCommandActionReject,
		RemoteCommandActionPause,
		RemoteCommandActionResume,
		RemoteCommandActionCancel,
		RemoteCommandActionRetry:
		return nil
	default:
		return fmt.Errorf("unknown remote command action %q", action)
	}
}

// ValidateRemoteCommand checks minimal command invariants.
func ValidateRemoteCommand(cmd RemoteCommand) error {
	if cmd.CommandID == "" {
		return fmt.Errorf("remote command id is required")
	}
	if cmd.SourceEndpointID == "" {
		return fmt.Errorf("remote command source endpoint id is required")
	}
	if cmd.Actor == "" {
		return fmt.Errorf("remote command actor is required")
	}
	if cmd.SessionID == "" {
		return fmt.Errorf("remote command session id is required")
	}
	if err := ValidateRemoteCommandAction(cmd.Action); err != nil {
		return err
	}
	return nil
}

// ValidateNotificationEnvelope checks the outbound notification shape.
func ValidateNotificationEnvelope(notification NotificationEnvelope) error {
	if notification.ID == "" {
		return fmt.Errorf("notification id is required")
	}
	if notification.Type == "" {
		return fmt.Errorf("notification type is required")
	}
	if notification.SessionID == "" {
		return fmt.Errorf("notification session id is required")
	}
	if notification.Title == "" {
		return fmt.Errorf("notification title is required")
	}
	if notification.Summary == "" {
		return fmt.Errorf("notification summary is required")
	}
	for _, action := range notification.Actions {
		if err := ValidateRemoteCommandAction(action); err != nil {
			return err
		}
	}
	return nil
}
