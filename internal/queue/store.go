package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"time"
)

// Status is the lifecycle state of a queued run request.
type Status string

const (
	StatusPending          Status = "pending"
	StatusRunning          Status = "running"
	StatusAwaitingApproval Status = "awaiting_approval"
	StatusSucceeded        Status = "succeeded"
	StatusFailed           Status = "failed"
	StatusRejected         Status = "rejected"
)

// Request is a daemon-submittable run request.
type Request struct {
	ID             string     `json:"id"`
	GraphFile      string     `json:"graph_file,omitempty"`
	Graph          *GraphSpec `json:"graph,omitempty"`
	Prompt         string     `json:"prompt"`
	StarterAgent   string     `json:"starter_agent"`
	Delegates      []string   `json:"delegates,omitempty"`
	WorkingDir     string     `json:"working_dir"`
	Continuous     bool       `json:"continuous,omitempty"`
	MaxRounds      int        `json:"max_rounds,omitempty"`
	SessionID      string     `json:"session_id,omitempty"`
	TaskID         string     `json:"task_id,omitempty"`
	ArtifactIDs    []string   `json:"artifact_ids,omitempty"`
	PolicyOverride bool       `json:"policy_override,omitempty"`
	Status         Status     `json:"status"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	Error          string     `json:"error,omitempty"`
}

// GraphNode captures one submitted task-graph node in queue storage.
type GraphNode struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	Agent        string   `json:"agent"`
	Strategy     string   `json:"strategy"`
	Dependencies []string `json:"dependencies,omitempty"`
}

// GraphSpec is the serialized graph execution payload carried by a queued job.
type GraphSpec struct {
	Prompt string      `json:"prompt"`
	Nodes  []GraphNode `json:"nodes"`
}

// Store persists queue requests under .roma/queue.
type Store struct {
	rootDir string
}

// NewStore constructs a file-backed queue store.
func NewStore(workDir string) *Store {
	return &Store{
		rootDir: filepath.Join(workDir, ".roma", "queue"),
	}
}

// Enqueue creates a pending request.
func (s *Store) Enqueue(ctx context.Context, req Request) error {
	_ = ctx
	req.Status = StatusPending
	req.CreatedAt = time.Now().UTC()
	req.UpdatedAt = req.CreatedAt
	return s.save(req)
}

// Update overwrites a request state.
func (s *Store) Update(ctx context.Context, req Request) error {
	_ = ctx
	req.UpdatedAt = time.Now().UTC()
	return s.save(req)
}

// Get loads one request by id.
func (s *Store) Get(ctx context.Context, id string) (Request, error) {
	_ = ctx
	raw, err := os.ReadFile(filepath.Join(s.rootDir, id+".json"))
	if err != nil {
		return Request{}, fmt.Errorf("read queue request: %w", err)
	}
	var req Request
	if err := json.Unmarshal(raw, &req); err != nil {
		return Request{}, fmt.Errorf("unmarshal queue request: %w", err)
	}
	return req, nil
}

// List returns all queue requests.
func (s *Store) List(ctx context.Context) ([]Request, error) {
	_ = ctx
	out := make([]Request, 0)
	err := filepath.WalkDir(s.rootDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if os.IsNotExist(walkErr) {
				return nil
			}
			return walkErr
		}
		if d.IsDir() || filepath.Ext(d.Name()) != ".json" {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var req Request
		if err := json.Unmarshal(raw, &req); err != nil {
			return err
		}
		out = append(out, req)
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("walk queue: %w", err)
	}
	slices.SortFunc(out, func(a, b Request) int {
		return a.CreatedAt.Compare(b.CreatedAt)
	})
	return out, nil
}

// NextPending returns the earliest pending request.
func (s *Store) NextPending(ctx context.Context) (Request, bool, error) {
	requests, err := s.List(ctx)
	if err != nil {
		return Request{}, false, err
	}
	for _, req := range requests {
		if req.Status == StatusPending {
			return req, true, nil
		}
	}
	return Request{}, false, nil
}

// RecoverInterrupted requeues requests that were left in running state.
func (s *Store) RecoverInterrupted(ctx context.Context) error {
	requests, err := s.List(ctx)
	if err != nil {
		return err
	}
	for _, req := range requests {
		if req.Status != StatusRunning {
			continue
		}
		req.Status = StatusPending
		req.Error = "recovered after daemon restart"
		if err := s.Update(ctx, req); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) save(req Request) error {
	if req.ID == "" {
		return fmt.Errorf("queue request id is required")
	}
	if err := os.MkdirAll(s.rootDir, 0o755); err != nil {
		return fmt.Errorf("create queue directory: %w", err)
	}
	raw, err := json.MarshalIndent(req, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal queue request: %w", err)
	}
	if err := os.WriteFile(filepath.Join(s.rootDir, req.ID+".json"), raw, 0o644); err != nil {
		return fmt.Errorf("write queue request: %w", err)
	}
	return nil
}
