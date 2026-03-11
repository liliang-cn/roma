package store

import (
	"context"
	"testing"
	"time"

	"github.com/liliang/roma/internal/events"
)

func TestSQLiteEventStoreAppendAndList(t *testing.T) {
	t.Parallel()

	s, err := NewSQLiteEventStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewSQLiteEventStore() error = %v", err)
	}
	ctx := context.Background()
	record := events.Record{
		ID:         "evt_1",
		SessionID:  "sess_1",
		TaskID:     "task_1",
		Type:       events.TypeSessionCreated,
		ActorType:  events.ActorTypeSystem,
		OccurredAt: time.Now().UTC(),
		Payload:    map[string]any{"mode": "direct"},
	}
	if err := s.AppendEvent(ctx, record); err != nil {
		t.Fatalf("AppendEvent() error = %v", err)
	}
	items, err := s.ListEvents(ctx, EventFilter{SessionID: "sess_1"})
	if err != nil {
		t.Fatalf("ListEvents() error = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("event count = %d, want 1", len(items))
	}
	if items[0].ID != "evt_1" {
		t.Fatalf("event id = %s, want evt_1", items[0].ID)
	}
}
