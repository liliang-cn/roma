package store

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/liliang-cn/roma/internal/events"
	"github.com/liliang-cn/roma/internal/romapath"
)

// FileEventStore persists events as append-only JSONL.
type FileEventStore struct {
	mu   sync.Mutex
	path string
}

// NewFileEventStore constructs a file-backed event store.
func NewFileEventStore(workDir string) *FileEventStore {
	return &FileEventStore{
		path: romapath.Join(workDir, "events", "events.jsonl"),
	}
}

// AppendEvent appends an event to the JSONL log.
func (s *FileEventStore) AppendEvent(ctx context.Context, event events.Record) error {
	_ = ctx

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("create event directory: %w", err)
	}
	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open event log: %w", err)
	}
	defer f.Close()

	raw, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}
	if _, err := f.Write(append(raw, '\n')); err != nil {
		return fmt.Errorf("append event: %w", err)
	}
	return nil
}

// ListEvents scans and filters persisted events.
func (s *FileEventStore) ListEvents(ctx context.Context, filter EventFilter) ([]events.Record, error) {
	_ = ctx

	f, err := os.Open(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("open event log: %w", err)
	}
	defer f.Close()

	out := make([]events.Record, 0)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var event events.Record
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return nil, fmt.Errorf("decode event line: %w", err)
		}
		if filter.SessionID != "" && event.SessionID != filter.SessionID {
			continue
		}
		if filter.TaskID != "" && event.TaskID != filter.TaskID {
			continue
		}
		if filter.Type != "" && event.Type != filter.Type {
			continue
		}
		out = append(out, event)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan event log: %w", err)
	}
	return out, nil
}
