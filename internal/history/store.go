package history

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/liliang-cn/roma/internal/romapath"
)

// SessionRecord is the persisted metadata for one local ROMA run.
type SessionRecord struct {
	ID          string    `json:"id"`
	TaskID      string    `json:"task_id"`
	Prompt      string    `json:"prompt"`
	Starter     string    `json:"starter"`
	Delegates   []string  `json:"delegates,omitempty"`
	WorkingDir  string    `json:"working_dir"`
	Status      string    `json:"status"`
	ArtifactIDs []string  `json:"artifact_ids,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Store persists session records under .roma/sessions.
type Store struct {
	rootDir string
}

// NewStore constructs a file-backed session store.
func NewStore(workDir string) *Store {
	return &Store{
		rootDir: romapath.Join(workDir, "sessions"),
	}
}

// Save persists the session record.
func (s *Store) Save(ctx context.Context, record SessionRecord) error {
	_ = ctx

	if err := os.MkdirAll(s.rootDir, 0o755); err != nil {
		return fmt.Errorf("create session directory: %w", err)
	}
	raw, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal session record: %w", err)
	}
	path := filepath.Join(s.rootDir, record.ID+".json")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		return fmt.Errorf("write session record: %w", err)
	}
	return nil
}

// Get loads one session record.
func (s *Store) Get(ctx context.Context, sessionID string) (SessionRecord, error) {
	_ = ctx

	path := filepath.Join(s.rootDir, sessionID+".json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return SessionRecord{}, fmt.Errorf("read session record: %w", err)
	}
	var record SessionRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return SessionRecord{}, fmt.Errorf("unmarshal session record: %w", err)
	}
	return record, nil
}

// List returns all persisted session records.
func (s *Store) List(ctx context.Context) ([]SessionRecord, error) {
	_ = ctx

	out := make([]SessionRecord, 0)
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
		var record SessionRecord
		if err := json.Unmarshal(raw, &record); err != nil {
			return err
		}
		out = append(out, record)
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("walk sessions: %w", err)
	}
	slices.SortFunc(out, func(a, b SessionRecord) int {
		return a.CreatedAt.Compare(b.CreatedAt)
	})
	return out, nil
}

// RecoverInterrupted marks stale running sessions as failed-recoverable.
func (s *Store) RecoverInterrupted(ctx context.Context) error {
	records, err := s.List(ctx)
	if err != nil {
		return err
	}
	for _, record := range records {
		if record.Status != "running" {
			continue
		}
		record.Status = "failed_recoverable"
		record.UpdatedAt = time.Now().UTC()
		if err := s.Save(ctx, record); err != nil {
			return err
		}
	}
	return nil
}
