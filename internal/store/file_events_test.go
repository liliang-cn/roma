package store

import (
	"context"
	"testing"
	"time"

	"github.com/liliang/roma/internal/events"
)

func TestFileEventStoreAppendAndList(t *testing.T) {
	t.Parallel()

	store := NewFileEventStore(t.TempDir())
	err := store.AppendEvent(context.Background(), events.Record{
		ID:         "evt_1",
		SessionID:  "sess_1",
		Type:       events.TypeSessionCreated,
		ActorType:  events.ActorTypeSystem,
		OccurredAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("AppendEvent() error = %v", err)
	}

	items, err := store.ListEvents(context.Background(), EventFilter{SessionID: "sess_1"})
	if err != nil {
		t.Fatalf("ListEvents() error = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("event count = %d, want 1", len(items))
	}
}
