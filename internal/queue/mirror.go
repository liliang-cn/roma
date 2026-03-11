package queue

import "context"

// MirrorStore mirrors queue writes into multiple backends.
type MirrorStore struct {
	backends []Backend
}

// NewMirrorStore constructs a mirrored queue backend.
func NewMirrorStore(backends ...Backend) *MirrorStore {
	out := make([]Backend, 0, len(backends))
	for _, item := range backends {
		if item != nil {
			out = append(out, item)
		}
	}
	return &MirrorStore{backends: out}
}

// Enqueue persists to every configured backend.
func (s *MirrorStore) Enqueue(ctx context.Context, req Request) error {
	for _, item := range s.backends {
		if err := item.Enqueue(ctx, req); err != nil {
			return err
		}
	}
	return nil
}

// Update mirrors the queue update.
func (s *MirrorStore) Update(ctx context.Context, req Request) error {
	for _, item := range s.backends {
		if err := item.Update(ctx, req); err != nil {
			return err
		}
	}
	return nil
}

// Get loads from the first backend.
func (s *MirrorStore) Get(ctx context.Context, id string) (Request, error) {
	if len(s.backends) == 0 {
		return Request{}, nil
	}
	var lastErr error
	for _, item := range s.backends {
		record, err := item.Get(ctx, id)
		if err == nil {
			return record, nil
		}
		lastErr = err
	}
	return Request{}, lastErr
}

// List loads from the first backend.
func (s *MirrorStore) List(ctx context.Context) ([]Request, error) {
	if len(s.backends) == 0 {
		return nil, nil
	}
	var lastErr error
	for _, item := range s.backends {
		records, err := item.List(ctx)
		if err == nil && len(records) > 0 {
			return records, nil
		}
		if err == nil {
			lastErr = nil
			continue
		}
		lastErr = err
	}
	return nil, lastErr
}

// NextPending loads from the first backend.
func (s *MirrorStore) NextPending(ctx context.Context) (Request, bool, error) {
	if len(s.backends) == 0 {
		return Request{}, false, nil
	}
	var lastErr error
	for _, item := range s.backends {
		record, ok, err := item.NextPending(ctx)
		if err == nil && ok {
			return record, true, nil
		}
		if err == nil {
			lastErr = nil
			continue
		}
		lastErr = err
	}
	return Request{}, false, lastErr
}

// RecoverInterrupted updates every configured backend.
func (s *MirrorStore) RecoverInterrupted(ctx context.Context) error {
	for _, item := range s.backends {
		if err := item.RecoverInterrupted(ctx); err != nil {
			return err
		}
	}
	return nil
}
