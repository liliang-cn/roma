package domain

import "fmt"

// ValidateSessionState checks whether the state is known.
func ValidateSessionState(state SessionState) error {
	switch state {
	case SessionStatePending,
		SessionStateRunning,
		SessionStateAwaitingApproval,
		SessionStateBlockedByPolicy,
		SessionStatePaused,
		SessionStateSucceeded,
		SessionStateFailedRecoverable,
		SessionStateFailedTerminal,
		SessionStateCancelled:
		return nil
	default:
		return fmt.Errorf("unknown session state %q", state)
	}
}

// ValidateTaskState checks whether the state is known.
func ValidateTaskState(state TaskState) error {
	switch state {
	case TaskStatePending,
		TaskStateReady,
		TaskStateRunning,
		TaskStateAwaitingQuorum,
		TaskStateUnderReview,
		TaskStateUnderArbitration,
		TaskStateAwaitingApproval,
		TaskStateBlockedByPolicy,
		TaskStateSucceeded,
		TaskStateFailedRecoverable,
		TaskStateFailedTerminal,
		TaskStateCancelled:
		return nil
	default:
		return fmt.Errorf("unknown task state %q", state)
	}
}

// ValidateSessionRecord checks minimal session invariants.
func ValidateSessionRecord(record SessionRecord) error {
	if record.ID == "" {
		return fmt.Errorf("session id is required")
	}
	if err := ValidateSessionState(record.State); err != nil {
		return err
	}
	return nil
}

// ValidateTaskRecord checks minimal task invariants.
func ValidateTaskRecord(record TaskRecord) error {
	if record.ID == "" {
		return fmt.Errorf("task id is required")
	}
	if record.SessionID == "" {
		return fmt.Errorf("task session id is required")
	}
	if record.Title == "" {
		return fmt.Errorf("task title is required")
	}
	if err := ValidateTaskState(record.State); err != nil {
		return err
	}
	return nil
}
