package replay

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/liliang-cn/roma/internal/domain"
	"github.com/liliang-cn/roma/internal/events"
	"github.com/liliang-cn/roma/internal/store"
)

// TaskSnapshot is the replayed task-level view derived from persisted events.
type TaskSnapshot struct {
	ID           string              `json:"id"`
	NodeID       string              `json:"node_id"`
	State        domain.TaskState    `json:"state"`
	Title        string              `json:"title,omitempty"`
	Strategy     domain.TaskStrategy `json:"strategy,omitempty"`
	AgentID      string              `json:"agent_id,omitempty"`
	ArtifactID   string              `json:"artifact_id,omitempty"`
	Dependencies []string            `json:"dependencies,omitempty"`
	UpdatedAt    time.Time           `json:"updated_at"`
}

// SessionSnapshot is the replayed execution view derived from persisted events.
type SessionSnapshot struct {
	SessionID   string          `json:"session_id"`
	TaskID      string          `json:"task_id,omitempty"`
	Status      string          `json:"status,omitempty"`
	StartedAt   time.Time       `json:"started_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
	ArtifactIDs []string        `json:"artifact_ids,omitempty"`
	Tasks       []TaskSnapshot  `json:"tasks,omitempty"`
	Plans       []PlanSnapshot  `json:"plans,omitempty"`
	Events      []events.Record `json:"events"`
}

type PlanSnapshot struct {
	ArtifactID     string    `json:"artifact_id,omitempty"`
	TaskID         string    `json:"task_id,omitempty"`
	EventType      string    `json:"event_type"`
	Reason         string    `json:"reason,omitempty"`
	ChangedPaths   []string  `json:"changed_paths,omitempty"`
	Violations     []string  `json:"violations,omitempty"`
	Conflict       bool      `json:"conflict,omitempty"`
	ConflictDetail string    `json:"conflict_detail,omitempty"`
	OccurredAt     time.Time `json:"occurred_at"`
}

// Service rebuilds replayable execution views from the append-only event log.
type Service struct {
	events store.EventStore
}

// NewService constructs a replay service.
func NewService(eventStore store.EventStore) *Service {
	return &Service{events: eventStore}
}

// ReplaySession rebuilds a session timeline from persisted events only.
func (s *Service) ReplaySession(ctx context.Context, sessionID string) (SessionSnapshot, error) {
	items, err := s.events.ListEvents(ctx, store.EventFilter{SessionID: sessionID})
	if err != nil {
		return SessionSnapshot{}, err
	}
	return RebuildSessionSnapshot(sessionID, items), nil
}

// RebuildSessionSnapshot derives session and task state from ordered events.
func RebuildSessionSnapshot(sessionID string, items []events.Record) SessionSnapshot {
	ordered := append([]events.Record(nil), items...)
	slices.SortFunc(ordered, compareEvents)

	snapshot := SessionSnapshot{
		SessionID: sessionID,
		Events:    ordered,
	}
	taskByID := make(map[string]TaskSnapshot)
	artifactSeen := make(map[string]struct{})
	planSnapshots := make([]PlanSnapshot, 0)

	for _, item := range ordered {
		if snapshot.SessionID == "" {
			snapshot.SessionID = item.SessionID
		}
		if snapshot.TaskID == "" && item.Type == events.TypeSessionCreated && item.TaskID != "" {
			snapshot.TaskID = item.TaskID
		}
		if snapshot.StartedAt.IsZero() || item.OccurredAt.Before(snapshot.StartedAt) {
			snapshot.StartedAt = item.OccurredAt
		}
		if item.OccurredAt.After(snapshot.UpdatedAt) {
			snapshot.UpdatedAt = item.OccurredAt
		}

		switch item.Type {
		case events.TypePlanApplied, events.TypePlanRolledBack, events.TypePlanApplyRejected:
			plan := PlanSnapshot{
				TaskID:     item.TaskID,
				EventType:  string(item.Type),
				Reason:     item.ReasonCode,
				OccurredAt: item.OccurredAt,
			}
			if artifactID, ok := payloadString(item.Payload, "artifact_id"); ok {
				plan.ArtifactID = artifactID
			}
			if changedPaths, ok := payloadStringSlice(item.Payload, "changed_paths"); ok {
				plan.ChangedPaths = changedPaths
			}
			if violations, ok := payloadStringSlice(item.Payload, "violations"); ok {
				plan.Violations = violations
			}
			if detail, ok := payloadString(item.Payload, "conflict_detail"); ok {
				plan.ConflictDetail = detail
			}
			if conflict, ok := payloadBool(item.Payload, "conflict"); ok {
				plan.Conflict = conflict
			}
			planSnapshots = append(planSnapshots, plan)
		case events.TypeSessionStateChanged:
			if item.ReasonCode != "" {
				snapshot.Status = item.ReasonCode
			}
			if ids, ok := payloadStringSlice(item.Payload, "artifact_ids"); ok {
				for _, id := range ids {
					if _, exists := artifactSeen[id]; exists {
						continue
					}
					artifactSeen[id] = struct{}{}
					snapshot.ArtifactIDs = append(snapshot.ArtifactIDs, id)
				}
			}
		case events.TypeArtifactStored:
			if artifactID, ok := payloadString(item.Payload, "artifact_id"); ok {
				if _, exists := artifactSeen[artifactID]; !exists {
					artifactSeen[artifactID] = struct{}{}
					snapshot.ArtifactIDs = append(snapshot.ArtifactIDs, artifactID)
				}
			}
		case events.TypeTaskStateChanged:
			taskID := item.TaskID
			if taskID == "" {
				continue
			}
			task := taskByID[taskID]
			task.ID = taskID
			task.NodeID = nodeIDFromTaskID(snapshot.SessionID, taskID)
			task.State = domain.TaskState(item.ReasonCode)
			task.UpdatedAt = item.OccurredAt
			if title, ok := payloadString(item.Payload, "node_title"); ok {
				task.Title = title
			}
			if strategy, ok := payloadString(item.Payload, "strategy"); ok {
				task.Strategy = domain.TaskStrategy(strategy)
			}
			if agentID, ok := payloadString(item.Payload, "agent_id"); ok {
				task.AgentID = agentID
			}
			if artifactID, ok := payloadString(item.Payload, "artifact_id"); ok {
				task.ArtifactID = artifactID
				if artifactID != "" {
					if _, exists := artifactSeen[artifactID]; !exists {
						artifactSeen[artifactID] = struct{}{}
						snapshot.ArtifactIDs = append(snapshot.ArtifactIDs, artifactID)
					}
				}
			}
			if deps, ok := payloadStringSlice(item.Payload, "dependencies"); ok {
				task.Dependencies = deps
			}
			taskByID[taskID] = task
		}
	}

	snapshot.Tasks = make([]TaskSnapshot, 0, len(taskByID))
	for _, task := range taskByID {
		snapshot.Tasks = append(snapshot.Tasks, task)
	}
	slices.SortFunc(snapshot.Tasks, func(a, b TaskSnapshot) int {
		if !a.UpdatedAt.Equal(b.UpdatedAt) {
			return a.UpdatedAt.Compare(b.UpdatedAt)
		}
		switch {
		case a.ID < b.ID:
			return -1
		case a.ID > b.ID:
			return 1
		default:
			return 0
		}
	})
	snapshot.Plans = planSnapshots
	return snapshot
}

func compareEvents(a, b events.Record) int {
	if !a.OccurredAt.Equal(b.OccurredAt) {
		return a.OccurredAt.Compare(b.OccurredAt)
	}
	switch {
	case a.ID < b.ID:
		return -1
	case a.ID > b.ID:
		return 1
	default:
		return 0
	}
}

func nodeIDFromTaskID(sessionID, taskID string) string {
	prefix := sessionID + "__"
	if sessionID != "" && strings.HasPrefix(taskID, prefix) {
		return strings.TrimPrefix(taskID, prefix)
	}
	return taskID
}

func payloadString(payload map[string]any, key string) (string, bool) {
	if payload == nil {
		return "", false
	}
	value, ok := payload[key]
	if !ok {
		return "", false
	}
	text, ok := value.(string)
	return text, ok
}

func payloadStringSlice(payload map[string]any, key string) ([]string, bool) {
	if payload == nil {
		return nil, false
	}
	value, ok := payload[key]
	if !ok {
		return nil, false
	}
	switch typed := value.(type) {
	case []string:
		return typed, true
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			text, ok := item.(string)
			if !ok {
				continue
			}
			out = append(out, text)
		}
		return out, true
	default:
		return nil, false
	}
}

func payloadBool(payload map[string]any, key string) (bool, bool) {
	if payload == nil {
		return false, false
	}
	value, ok := payload[key]
	if !ok {
		return false, false
	}
	flag, ok := value.(bool)
	return flag, ok
}
