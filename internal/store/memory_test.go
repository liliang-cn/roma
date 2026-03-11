package store

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/liliang/roma/internal/domain"
)

func TestMemoryStoreSessionLifecycle(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := NewMemoryStore()

	record := domain.SessionRecord{
		ID:        "sess_1",
		State:     domain.SessionStatePending,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := s.CreateSession(ctx, record); err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}

	if err := s.UpdateSessionState(ctx, SessionStateUpdate{
		SessionID: "sess_1",
		State:     domain.SessionStateRunning,
	}); err != nil {
		t.Fatalf("UpdateSessionState() error = %v", err)
	}

	got, err := s.GetSession(ctx, "sess_1")
	if err != nil {
		t.Fatalf("GetSession() error = %v", err)
	}
	if got.State != domain.SessionStateRunning {
		t.Fatalf("GetSession() state = %s, want %s", got.State, domain.SessionStateRunning)
	}
}

func TestMemoryStoreBlobRoundTrip(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := NewMemoryStore()

	if err := s.PutBlob(ctx, "blob/test", bytes.NewBufferString("roma")); err != nil {
		t.Fatalf("PutBlob() error = %v", err)
	}

	rc, err := s.OpenBlob(ctx, "blob/test")
	if err != nil {
		t.Fatalf("OpenBlob() error = %v", err)
	}
	defer rc.Close()

	buf := new(bytes.Buffer)
	if _, err := buf.ReadFrom(rc); err != nil {
		t.Fatalf("ReadFrom() error = %v", err)
	}
	if got := buf.String(); got != "roma" {
		t.Fatalf("blob content = %q, want %q", got, "roma")
	}
}
