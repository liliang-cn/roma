package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/liliang/roma/internal/domain"
	"github.com/liliang/roma/internal/events"
	"github.com/liliang/roma/internal/history"
	"github.com/liliang/roma/internal/queue"
)

// Client talks to romad over a Unix domain socket.
type Client struct {
	metaPath string
}

// NewClient constructs a UDS API client.
func NewClient(workDir string) *Client {
	return &Client{
		metaPath: filepath.Join(workDir, ".roma", "run", "api.json"),
	}
}

// Available reports whether the daemon socket exists.
func (c *Client) Available() bool {
	httpClient, baseURL, err := c.httpClient()
	if err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/health", nil)
	if err != nil {
		return false
	}
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
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

// QueueApprove marks a queue item approved and ready for re-dispatch.
func (c *Client) QueueApprove(ctx context.Context, id string) (queue.Request, error) {
	return c.queueAction(ctx, id, "approve")
}

// QueueReject marks a queue item rejected.
func (c *Client) QueueReject(ctx context.Context, id string) (queue.Request, error) {
	return c.queueAction(ctx, id, "reject")
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
	raw, err := os.ReadFile(c.metaPath)
	if err != nil {
		return nil, "", fmt.Errorf("read api metadata: %w", err)
	}
	var meta struct {
		Network string `json:"network"`
		Address string `json:"address"`
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		return nil, "", fmt.Errorf("unmarshal api metadata: %w", err)
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
		return nil, "", fmt.Errorf("unsupported api network %q", meta.Network)
	}
}
