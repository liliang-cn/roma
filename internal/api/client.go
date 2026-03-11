package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/liliang-cn/roma/internal/curia"
	"github.com/liliang-cn/roma/internal/domain"
	"github.com/liliang-cn/roma/internal/events"
	"github.com/liliang-cn/roma/internal/history"
	"github.com/liliang-cn/roma/internal/queue"
	"github.com/liliang-cn/roma/internal/romapath"
	"github.com/liliang-cn/roma/internal/scheduler"
	"github.com/liliang-cn/roma/internal/workspace"
)

// Client talks to romad over a Unix domain socket.
type Client struct {
	metaPaths []string
}

var healthCheckFn = checkHealth

// NewClient constructs a UDS API client.
func NewClient(workDir string) *Client {
	return &Client{metaPaths: candidateMetaPaths(workDir)}
}

// Available reports whether the daemon socket exists.
func (c *Client) Available() bool {
	_, _, err := c.httpClient()
	return err == nil
}

// Status returns daemon-owned workspace counters.
func (c *Client) Status(ctx context.Context) (StatusResponse, error) {
	httpClient, baseURL, err := c.httpClient()
	if err != nil {
		return StatusResponse{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/status", nil)
	if err != nil {
		return StatusResponse{}, fmt.Errorf("create status request: %w", err)
	}
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return StatusResponse{}, fmt.Errorf("status request: %w", err)
	}
	defer resp.Body.Close()

	var out StatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return StatusResponse{}, fmt.Errorf("decode status response: %w", err)
	}
	return out, nil
}

// Submit enqueues a job through romad.
func (c *Client) Submit(ctx context.Context, req SubmitRequest) (SubmitResponse, error) {
	httpClient, baseURL, err := c.httpClient()
	if err != nil {
		return SubmitResponse{}, err
	}
	raw, err := json.Marshal(req)
	if err != nil {
		return SubmitResponse{}, fmt.Errorf("marshal submit request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/submit", bytes.NewReader(raw))
	if err != nil {
		return SubmitResponse{}, fmt.Errorf("create submit request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return SubmitResponse{}, fmt.Errorf("submit request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		return SubmitResponse{}, fmt.Errorf("submit request returned %s", resp.Status)
	}
	var out SubmitResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return SubmitResponse{}, fmt.Errorf("decode submit response: %w", err)
	}
	return out, nil
}

// QueueList returns daemon queue items.
func (c *Client) QueueList(ctx context.Context) ([]queue.Request, error) {
	httpClient, baseURL, err := c.httpClient()
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/queue", nil)
	if err != nil {
		return nil, fmt.Errorf("create queue request: %w", err)
	}
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("queue request: %w", err)
	}
	defer resp.Body.Close()

	var out QueueListResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode queue response: %w", err)
	}
	return out.Items, nil
}

// QueueGet returns one queue item from the daemon.
func (c *Client) QueueGet(ctx context.Context, id string) (queue.Request, error) {
	httpClient, baseURL, err := c.httpClient()
	if err != nil {
		return queue.Request{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/queue/"+id, nil)
	if err != nil {
		return queue.Request{}, fmt.Errorf("create queue get request: %w", err)
	}
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return queue.Request{}, fmt.Errorf("queue get request: %w", err)
	}
	defer resp.Body.Close()

	var out queue.Request
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return queue.Request{}, fmt.Errorf("decode queue get response: %w", err)
	}
	return out, nil
}

// QueueInspect returns a queue job with expanded execution records.
func (c *Client) QueueInspect(ctx context.Context, id string) (QueueInspectResponse, error) {
	httpClient, baseURL, err := c.httpClient()
	if err != nil {
		return QueueInspectResponse{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/queue-inspect/"+id, nil)
	if err != nil {
		return QueueInspectResponse{}, fmt.Errorf("create queue inspect request: %w", err)
	}
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return QueueInspectResponse{}, fmt.Errorf("queue inspect request: %w", err)
	}
	defer resp.Body.Close()

	var out QueueInspectResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return QueueInspectResponse{}, fmt.Errorf("decode queue inspect response: %w", err)
	}
	return out, nil
}

func (c *Client) CuriaReputation(ctx context.Context, reviewerID string) ([]curia.ReputationRecord, error) {
	httpClient, baseURL, err := c.httpClient()
	if err != nil {
		return nil, err
	}
	url := baseURL + "/curia/reputation"
	if reviewerID != "" {
		url += "?reviewer=" + reviewerID
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create curia reputation request: %w", err)
	}
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("curia reputation request: %w", err)
	}
	defer resp.Body.Close()

	var out CuriaReputationResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode curia reputation response: %w", err)
	}
	return out.Items, nil
}

func (c *Client) PlanInspect(ctx context.Context, id string) (domain.ArtifactEnvelope, error) {
	httpClient, baseURL, err := c.httpClient()
	if err != nil {
		return domain.ArtifactEnvelope{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/plans/"+id, nil)
	if err != nil {
		return domain.ArtifactEnvelope{}, fmt.Errorf("create plan inspect request: %w", err)
	}
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return domain.ArtifactEnvelope{}, fmt.Errorf("plan inspect request: %w", err)
	}
	defer resp.Body.Close()
	var out PlanInspectResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return domain.ArtifactEnvelope{}, fmt.Errorf("decode plan inspect response: %w", err)
	}
	return out.Artifact, nil
}

func (c *Client) PlanInbox(ctx context.Context, sessionID string) ([]PlanInboxEntry, error) {
	httpClient, baseURL, err := c.httpClient()
	if err != nil {
		return nil, err
	}
	url := baseURL + "/plans/inbox"
	if sessionID != "" {
		url += "?session=" + sessionID
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create plan inbox request: %w", err)
	}
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("plan inbox request: %w", err)
	}
	defer resp.Body.Close()
	var out PlanInboxResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode plan inbox response: %w", err)
	}
	return out.Items, nil
}

func (c *Client) PlanApprove(ctx context.Context, artifactID, actor string) error {
	return c.planDecision(ctx, artifactID, "approve", actor)
}

func (c *Client) PlanReject(ctx context.Context, artifactID, actor string) error {
	return c.planDecision(ctx, artifactID, "reject", actor)
}

func (c *Client) PlanApply(ctx context.Context, req PlanApplyRequest) (PlanApplyResponse, error) {
	return c.planAction(ctx, "/plans/apply", req)
}

func (c *Client) PlanPreview(ctx context.Context, req PlanApplyRequest) (PlanApplyResponse, error) {
	return c.planAction(ctx, "/plans/preview", req)
}

func (c *Client) PlanRollback(ctx context.Context, req PlanApplyRequest) (PlanApplyResponse, error) {
	return c.planAction(ctx, "/plans/rollback", req)
}

func (c *Client) planDecision(ctx context.Context, artifactID, action, actor string) error {
	httpClient, baseURL, err := c.httpClient()
	if err != nil {
		return err
	}
	raw, err := json.Marshal(PlanDecisionRequest{Actor: actor})
	if err != nil {
		return fmt.Errorf("marshal plan decision request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/plans/"+artifactID+"/"+action, bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("create plan %s request: %w", action, err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("plan %s request: %w", action, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("plan %s request returned %s", action, resp.Status)
	}
	return nil
}

// QueueApprove marks a queue item approved and ready for re-dispatch.
func (c *Client) QueueApprove(ctx context.Context, id string) (queue.Request, error) {
	return c.queueAction(ctx, id, "approve")
}

// QueueReject marks a queue item rejected.
func (c *Client) QueueReject(ctx context.Context, id string) (queue.Request, error) {
	return c.queueAction(ctx, id, "reject")
}

// QueueCancel cancels a queued or running item.
func (c *Client) QueueCancel(ctx context.Context, id string) (queue.Request, error) {
	return c.queueAction(ctx, id, "cancel")
}

// RecoveryList returns daemon recovery snapshots.
func (c *Client) RecoveryList(ctx context.Context) ([]scheduler.RecoverySnapshot, error) {
	httpClient, baseURL, err := c.httpClient()
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/recovery", nil)
	if err != nil {
		return nil, fmt.Errorf("create recovery request: %w", err)
	}
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("recovery request: %w", err)
	}
	defer resp.Body.Close()

	var out RecoveryListResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode recovery response: %w", err)
	}
	return out.Items, nil
}

func (c *Client) queueAction(ctx context.Context, id, action string) (queue.Request, error) {
	httpClient, baseURL, err := c.httpClient()
	if err != nil {
		return queue.Request{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/queue/"+id+"/"+action, nil)
	if err != nil {
		return queue.Request{}, fmt.Errorf("create queue %s request: %w", action, err)
	}
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return queue.Request{}, fmt.Errorf("queue %s request: %w", action, err)
	}
	defer resp.Body.Close()

	var out queue.Request
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return queue.Request{}, fmt.Errorf("decode queue %s response: %w", action, err)
	}
	return out, nil
}

func (c *Client) planAction(ctx context.Context, path string, req PlanApplyRequest) (PlanApplyResponse, error) {
	httpClient, baseURL, err := c.httpClient()
	if err != nil {
		return PlanApplyResponse{}, err
	}
	raw, err := json.Marshal(req)
	if err != nil {
		return PlanApplyResponse{}, fmt.Errorf("marshal plan request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+path, bytes.NewReader(raw))
	if err != nil {
		return PlanApplyResponse{}, fmt.Errorf("create plan request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return PlanApplyResponse{}, fmt.Errorf("plan request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return PlanApplyResponse{}, fmt.Errorf("plan request returned %s", resp.Status)
	}
	var out PlanApplyResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return PlanApplyResponse{}, fmt.Errorf("decode plan response: %w", err)
	}
	return out, nil
}

// SessionList returns daemon session records.
func (c *Client) SessionList(ctx context.Context) ([]history.SessionRecord, error) {
	httpClient, baseURL, err := c.httpClient()
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/sessions", nil)
	if err != nil {
		return nil, fmt.Errorf("create sessions request: %w", err)
	}
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("sessions request: %w", err)
	}
	defer resp.Body.Close()

	var out SessionListResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode sessions response: %w", err)
	}
	return out.Items, nil
}

// SessionGet returns one session record from the daemon.
func (c *Client) SessionGet(ctx context.Context, id string) (history.SessionRecord, error) {
	httpClient, baseURL, err := c.httpClient()
	if err != nil {
		return history.SessionRecord{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/sessions/"+id, nil)
	if err != nil {
		return history.SessionRecord{}, fmt.Errorf("create session get request: %w", err)
	}
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return history.SessionRecord{}, fmt.Errorf("session get request: %w", err)
	}
	defer resp.Body.Close()

	var out history.SessionRecord
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return history.SessionRecord{}, fmt.Errorf("decode session get response: %w", err)
	}
	return out, nil
}

// SessionInspect returns one session with expanded execution records from the daemon.
func (c *Client) SessionInspect(ctx context.Context, id string) (SessionInspectResponse, error) {
	httpClient, baseURL, err := c.httpClient()
	if err != nil {
		return SessionInspectResponse{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/session-inspect/"+id, nil)
	if err != nil {
		return SessionInspectResponse{}, fmt.Errorf("create session inspect request: %w", err)
	}
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return SessionInspectResponse{}, fmt.Errorf("session inspect request: %w", err)
	}
	defer resp.Body.Close()

	var out SessionInspectResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return SessionInspectResponse{}, fmt.Errorf("decode session inspect response: %w", err)
	}
	return out, nil
}

// TaskList returns persisted task records from the daemon.
func (c *Client) TaskList(ctx context.Context, sessionID string) ([]domain.TaskRecord, error) {
	httpClient, baseURL, err := c.httpClient()
	if err != nil {
		return nil, err
	}
	url := baseURL + "/tasks"
	if sessionID != "" {
		url += "?session=" + sessionID
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create tasks request: %w", err)
	}
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("tasks request: %w", err)
	}
	defer resp.Body.Close()

	var out TaskListResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode tasks response: %w", err)
	}
	return out.Items, nil
}

// TaskGet returns one task record from the daemon.
func (c *Client) TaskGet(ctx context.Context, id string) (domain.TaskRecord, error) {
	httpClient, baseURL, err := c.httpClient()
	if err != nil {
		return domain.TaskRecord{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/tasks/"+id, nil)
	if err != nil {
		return domain.TaskRecord{}, fmt.Errorf("create task get request: %w", err)
	}
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return domain.TaskRecord{}, fmt.Errorf("task get request: %w", err)
	}
	defer resp.Body.Close()

	var out domain.TaskRecord
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return domain.TaskRecord{}, fmt.Errorf("decode task get response: %w", err)
	}
	return out, nil
}

// TaskApprove marks a task ready to resume after approval.
func (c *Client) TaskApprove(ctx context.Context, id string) (domain.TaskRecord, error) {
	return c.taskAction(ctx, id, "approve")
}

// TaskReject marks a task cancelled after approval rejection.
func (c *Client) TaskReject(ctx context.Context, id string) (domain.TaskRecord, error) {
	return c.taskAction(ctx, id, "reject")
}

func (c *Client) taskAction(ctx context.Context, id, action string) (domain.TaskRecord, error) {
	httpClient, baseURL, err := c.httpClient()
	if err != nil {
		return domain.TaskRecord{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/tasks/"+id+"/"+action, nil)
	if err != nil {
		return domain.TaskRecord{}, fmt.Errorf("create task %s request: %w", action, err)
	}
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return domain.TaskRecord{}, fmt.Errorf("task %s request: %w", action, err)
	}
	defer resp.Body.Close()

	var out domain.TaskRecord
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return domain.TaskRecord{}, fmt.Errorf("decode task %s response: %w", action, err)
	}
	return out, nil
}

// WorkspaceList returns persisted workspace metadata from the daemon.
func (c *Client) WorkspaceList(ctx context.Context) ([]workspace.Prepared, error) {
	httpClient, baseURL, err := c.httpClient()
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/workspaces", nil)
	if err != nil {
		return nil, fmt.Errorf("create workspaces request: %w", err)
	}
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("workspaces request: %w", err)
	}
	defer resp.Body.Close()

	var out WorkspaceListResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode workspaces response: %w", err)
	}
	return out.Items, nil
}

// WorkspaceGet returns one persisted workspace record from the daemon.
func (c *Client) WorkspaceGet(ctx context.Context, sessionID, taskID string) (workspace.Prepared, error) {
	httpClient, baseURL, err := c.httpClient()
	if err != nil {
		return workspace.Prepared{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/workspaces/"+sessionID+"/"+taskID, nil)
	if err != nil {
		return workspace.Prepared{}, fmt.Errorf("create workspace get request: %w", err)
	}
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return workspace.Prepared{}, fmt.Errorf("workspace get request: %w", err)
	}
	defer resp.Body.Close()

	var out workspace.Prepared
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return workspace.Prepared{}, fmt.Errorf("decode workspace get response: %w", err)
	}
	return out, nil
}

// WorkspaceCleanup reclaims stale workspaces through the daemon.
func (c *Client) WorkspaceCleanup(ctx context.Context) ([]workspace.Prepared, error) {
	httpClient, baseURL, err := c.httpClient()
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/workspaces/cleanup", nil)
	if err != nil {
		return nil, fmt.Errorf("create workspace cleanup request: %w", err)
	}
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("workspace cleanup request: %w", err)
	}
	defer resp.Body.Close()

	var out WorkspaceListResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode workspace cleanup response: %w", err)
	}
	return out.Items, nil
}

// WorkspaceMerge merges one workspace back into the base repository.
func (c *Client) WorkspaceMerge(ctx context.Context, sessionID, taskID string) (workspace.Prepared, error) {
	httpClient, baseURL, err := c.httpClient()
	if err != nil {
		return workspace.Prepared{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/workspaces/"+sessionID+"/"+taskID+"/merge", nil)
	if err != nil {
		return workspace.Prepared{}, fmt.Errorf("create workspace merge request: %w", err)
	}
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return workspace.Prepared{}, fmt.Errorf("workspace merge request: %w", err)
	}
	defer resp.Body.Close()

	var out workspace.Prepared
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return workspace.Prepared{}, fmt.Errorf("decode workspace merge response: %w", err)
	}
	return out, nil
}

// ArtifactList returns persisted artifacts from the daemon.
func (c *Client) ArtifactList(ctx context.Context, sessionID string) ([]domain.ArtifactEnvelope, error) {
	httpClient, baseURL, err := c.httpClient()
	if err != nil {
		return nil, err
	}
	url := baseURL + "/artifacts"
	if sessionID != "" {
		url += "?session=" + sessionID
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create artifacts request: %w", err)
	}
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("artifacts request: %w", err)
	}
	defer resp.Body.Close()

	var out []domain.ArtifactEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode artifacts response: %w", err)
	}
	return out, nil
}

// ArtifactGet returns a persisted artifact from the daemon.
func (c *Client) ArtifactGet(ctx context.Context, id string) (domain.ArtifactEnvelope, error) {
	httpClient, baseURL, err := c.httpClient()
	if err != nil {
		return domain.ArtifactEnvelope{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/artifacts/"+id, nil)
	if err != nil {
		return domain.ArtifactEnvelope{}, fmt.Errorf("create artifact get request: %w", err)
	}
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return domain.ArtifactEnvelope{}, fmt.Errorf("artifact get request: %w", err)
	}
	defer resp.Body.Close()

	var out domain.ArtifactEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return domain.ArtifactEnvelope{}, fmt.Errorf("decode artifact get response: %w", err)
	}
	return out, nil
}

// EventList returns persisted events from the daemon.
func (c *Client) EventList(ctx context.Context, sessionID, taskID string, eventType events.Type) ([]events.Record, error) {
	httpClient, baseURL, err := c.httpClient()
	if err != nil {
		return nil, err
	}
	url := baseURL + "/events"
	params := make([]string, 0, 3)
	if sessionID != "" {
		params = append(params, "session="+sessionID)
	}
	if taskID != "" {
		params = append(params, "task="+taskID)
	}
	if eventType != "" {
		params = append(params, "type="+string(eventType))
	}
	if len(params) > 0 {
		url += "?" + strings.Join(params, "&")
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create events request: %w", err)
	}
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("events request: %w", err)
	}
	defer resp.Body.Close()

	var out EventListResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode events response: %w", err)
	}
	return out.Items, nil
}

// EventGet returns one event record from the daemon.
func (c *Client) EventGet(ctx context.Context, id string) (events.Record, error) {
	httpClient, baseURL, err := c.httpClient()
	if err != nil {
		return events.Record{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/events/"+id, nil)
	if err != nil {
		return events.Record{}, fmt.Errorf("create event get request: %w", err)
	}
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return events.Record{}, fmt.Errorf("event get request: %w", err)
	}
	defer resp.Body.Close()

	var out events.Record
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return events.Record{}, fmt.Errorf("decode event get response: %w", err)
	}
	return out, nil
}

func (c *Client) httpClient() (*http.Client, string, error) {
	var errs []error
	for _, metaPath := range c.metaPaths {
		httpClient, baseURL, err := clientFromMeta(metaPath)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if err := healthCheckFn(httpClient, baseURL); err != nil {
			errs = append(errs, fmt.Errorf("health check %s: %w", metaPath, err))
			continue
		}
		return httpClient, baseURL, nil
	}
	if len(errs) == 0 {
		return nil, "", fmt.Errorf("no daemon metadata paths configured")
	}
	return nil, "", errors.Join(errs...)
}

func candidateMetaPaths(workDir string) []string {
	paths := make([]string, 0, 3)
	seen := make(map[string]struct{}, 3)
	addPath := func(metaPath string) {
		metaPath = strings.TrimSpace(metaPath)
		if metaPath == "" {
			return
		}
		if _, ok := seen[metaPath]; ok {
			return
		}
		seen[metaPath] = struct{}{}
		paths = append(paths, metaPath)
	}
	addStateRoot := func(root string) {
		root = strings.TrimSpace(root)
		if root == "" {
			return
		}
		addPath(filepath.Join(root, "run", "api.json"))
	}
	addWorkspaceRoot := func(root string) {
		root = strings.TrimSpace(root)
		if root == "" {
			return
		}
		addPath(romapath.Join(root, "run", "api.json"))
	}

	if override := daemonHomeOverride(); override != "" {
		addStateRoot(override)
	}
	addWorkspaceRoot(workDir)
	if fallback := defaultDaemonHome(); fallback != "" {
		addStateRoot(fallback)
	}
	return paths
}

func daemonHomeOverride() string {
	for _, key := range []string{"ROMA_DAEMON_DIR", "ROMA_HOME"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}

func defaultDaemonHome() string {
	return romapath.HomeDir()
}

func clientFromMeta(metaPath string) (*http.Client, string, error) {
	raw, err := os.ReadFile(metaPath)
	if err != nil {
		return nil, "", fmt.Errorf("read api metadata %s: %w", metaPath, err)
	}
	var meta struct {
		Network string `json:"network"`
		Address string `json:"address"`
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		return nil, "", fmt.Errorf("unmarshal api metadata %s: %w", metaPath, err)
	}

	switch meta.Network {
	case "unix":
		transport := &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				dialer := net.Dialer{}
				return dialer.DialContext(ctx, "unix", meta.Address)
			},
		}
		return &http.Client{Transport: transport, Timeout: 5 * time.Second}, "http://romad", nil
	case "tcp":
		return &http.Client{Timeout: 5 * time.Second}, "http://" + meta.Address, nil
	default:
		return nil, "", fmt.Errorf("unsupported api network %q in %s", meta.Network, metaPath)
	}
}

func checkHealth(httpClient *http.Client, baseURL string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/health", nil)
	if err != nil {
		return err
	}
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected health status %s", resp.Status)
	}
	return nil
}
