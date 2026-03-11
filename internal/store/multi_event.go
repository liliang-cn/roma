package store

import (
	"context"

	"github.com/liliang/roma/internal/events"
)

// MultiEventStore mirrors event writes into multiple backends.
type MultiEventStore struct {
	stores []EventStore
}

// NewMultiEventStore constructs a fan-out event store.
func NewMultiEventStore(stores ...EventStore) *MultiEventStore {
	out := make([]EventStore, 0, len(stores))
	for _, item := range stores {
		if item != nil {
			out = append(out, item)
		}
	}
	return &MultiEventStore{stores: out}
}

// AppendEvent writes the event to every configured backend.
func (s *MultiEventStore) AppendEvent(ctx context.Context, event events.Record) error {
	for _, item := range s.stores {
		if err := item.AppendEvent(ctx, event); err != nil {
			return err
		}
	}
	return nil
}

// ListEvents returns events from the first configured backend.
func (s *MultiEventStore) ListEvents(ctx context.Context, filter EventFilter) ([]events.Record, error) {
	if len(s.stores) == 0 {
		return nil, nil
	}
	return s.stores[0].ListEvents(ctx, filter)
}
