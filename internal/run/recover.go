package run

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/liliang-cn/roma/internal/domain"
	"github.com/liliang-cn/roma/internal/runtime"
	"github.com/liliang-cn/roma/internal/scheduler"
)

// ResumeSessionWithResult resumes a persisted relay session from SQLite-backed state.
func (s *Service) ResumeSessionWithResult(ctx context.Context, workDir, sessionID string, stdout io.Writer) (Result, error) {
	if strings.TrimSpace(sessionID) == "" {
		return Result{}, fmt.Errorf("session id is required")
	}
	if strings.TrimSpace(workDir) == "" {
		return Result{}, fmt.Errorf("working directory is required")
	}

	s.history = newHistoryBackend(workDir)
	s.events = newEventBackend(workDir)
	s.store = newArtifactBackend(workDir)
	s.tasks = newTaskBackend(workDir)
	s.supervisor = runtime.NewDefaultSupervisorWithEvents(s.events)

	record, err := s.history.Get(ctx, sessionID)
	if err != nil {
		return Result{}, fmt.Errorf("load recovery session: %w", err)
	}
	tasks, err := s.tasks.ListTasksBySession(ctx, sessionID)
	if err != nil {
		return Result{}, fmt.Errorf("list recovery tasks: %w", err)
	}
	if len(tasks) == 0 {
		return Result{}, fmt.Errorf("session %s has no persisted tasks", sessionID)
	}

	assignments := make([]scheduler.NodeAssignment, 0, len(tasks))
	for _, task := range tasks {
		profile, ok := s.registry.Resolve(ctx, task.AgentID)
		if !ok {
			return Result{}, fmt.Errorf("unknown agent %q for recovery task %q", task.AgentID, task.ID)
		}
		if profile.Availability != domain.AgentAvailabilityAvailable {
			return Result{}, fmt.Errorf("agent %q is not available on PATH", profile.ID)
		}
		assignments = append(assignments, scheduler.NodeAssignment{
			Node: domain.TaskNodeSpec{
				ID:            trimTaskRecordID(sessionID, task.ID),
				Title:         task.Title,
				Strategy:      task.Strategy,
				Dependencies:  task.Dependencies,
				SchemaVersion: "v1",
			},
			Profile: profile,
		})
	}
	slices.SortFunc(assignments, func(a, b scheduler.NodeAssignment) int {
		return strings.Compare(a.Node.ID, b.Node.ID)
	})

	existing := make(map[string]domain.ArtifactEnvelope, len(tasks))
	for _, task := range tasks {
		if task.ArtifactID == "" || task.State != domain.TaskStateSucceeded {
			continue
		}
		artifact, err := s.store.Get(ctx, task.ArtifactID)
		if err != nil {
			return Result{}, fmt.Errorf("load recovery artifact %s: %w", task.ArtifactID, err)
		}
		existing[trimTaskRecordID(sessionID, task.ID)] = artifact
	}

	record.Status = "running"
	record.UpdatedAt = time.Now().UTC()
	if err := s.history.Save(ctx, record); err != nil {
		return Result{}, fmt.Errorf("save resumed session: %w", err)
	}
	s.appendSessionStateEvent(ctx, record)

	dispatcher := scheduler.NewDispatcher(workDir, s.supervisor, s.events, s.tasks)
	execResult, runErr := dispatcher.Resume(ctx, sessionID, record.WorkingDir, record.Prompt, assignments, existing)
	writeRelayResult(stdout, assignments, execResult)

	for _, nodeID := range execResult.Order {
		if _, ok := existing[nodeID]; ok {
			continue
		}
		artifact := execResult.Artifacts[nodeID]
		if artifact.ID == "" {
			continue
		}
		if err := s.store.Save(ctx, artifact); err != nil {
			return Result{}, fmt.Errorf("save recovery artifact %s: %w", artifact.ID, err)
		}
		s.appendArtifactStoredEvent(ctx, artifact)
	}

	record.ArtifactIDs = collectRelayArtifactIDs(execResult)
	record.UpdatedAt = time.Now().UTC()
	if runErr != nil {
		var approvalErr *scheduler.ApprovalPendingError
		if errors.As(runErr, &approvalErr) {
			record.Status = "awaiting_approval"
			runErr = nil
		} else {
			record.Status = "failed"
		}
	} else {
		record.Status = "succeeded"
	}
	if err := s.history.Save(ctx, record); err != nil {
		return Result{}, fmt.Errorf("save recovered session result: %w", err)
	}
	s.appendSessionStateEvent(ctx, record)
	_, _ = fmt.Fprintf(stdout, "session=%s task=%s status=%s\n", record.ID, record.TaskID, record.Status)
	return Result{
		SessionID:   record.ID,
		TaskID:      record.TaskID,
		Status:      record.Status,
		ArtifactIDs: record.ArtifactIDs,
	}, runErr
}

// ResumeSession resumes a persisted session and discards the detailed result.
func (s *Service) ResumeSession(ctx context.Context, workDir, sessionID string, stdout io.Writer) error {
	_, err := s.ResumeSessionWithResult(ctx, workDir, sessionID, stdout)
	return err
}

func trimTaskRecordID(sessionID, taskID string) string {
	prefix := sessionID + "__"
	return strings.TrimPrefix(taskID, prefix)
}
