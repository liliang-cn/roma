package gateway

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/liliang/roma/internal/domain"
	"github.com/liliang/roma/internal/store"
)

type fakeAdapter struct {
	typ       domain.GatewayEndpointType
	delivered []string
	fail      bool
}

func (f *fakeAdapter) Type() domain.GatewayEndpointType {
	return f.typ
}

func (f *fakeAdapter) Deliver(_ context.Context, _ domain.GatewayEndpoint, notification domain.NotificationEnvelope) error {
	if f.fail {
		return fmt.Errorf("delivery failed")
	}
	f.delivered = append(f.delivered, notification.ID)
	return nil
}

func TestServiceDeliverAndAudit(t *testing.T) {
	t.Parallel()

	mem := store.NewMemoryStore()
	adapter := &fakeAdapter{typ: domain.GatewayEndpointTypeTelegram}
	svc := NewService(mem, adapter)
	ctx := context.Background()

	err := svc.RegisterEndpoint(ctx, domain.GatewayEndpoint{
		ID:             "gw_1",
		Type:           domain.GatewayEndpointTypeTelegram,
		Enabled:        true,
		Target:         "chat:1",
		AllowedActions: []domain.RemoteCommandAction{domain.RemoteCommandActionApprove},
	}, domain.RemoteSubscription{
		EndpointID:        "gw_1",
		EventTypes:        []string{"approval_required"},
		SeverityThreshold: domain.NotificationSeverityMedium,
	})
	if err != nil {
		t.Fatalf("RegisterEndpoint() error = %v", err)
	}

	err = svc.Deliver(ctx, domain.NotificationEnvelope{
		ID:        "notif_1",
		Type:      "approval_required",
		Severity:  domain.NotificationSeverityHigh,
		SessionID: "sess_1",
		Title:     "Approval",
		Summary:   "Need approval",
		Actions:   []domain.RemoteCommandAction{domain.RemoteCommandActionApprove},
		CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("Deliver() error = %v", err)
	}

	if len(adapter.delivered) != 1 {
		t.Fatalf("delivered count = %d, want 1", len(adapter.delivered))
	}

	events, err := mem.ListEvents(ctx, store.EventFilter{SessionID: "sess_1"})
	if err != nil {
		t.Fatalf("ListEvents() error = %v", err)
	}
	if len(events) == 0 {
		t.Fatal("ListEvents() returned no events")
	}
}

func TestServiceRejectsDisallowedRemoteCommand(t *testing.T) {
	t.Parallel()

	mem := store.NewMemoryStore()
	svc := NewService(mem)
	ctx := context.Background()

	err := svc.RegisterEndpoint(ctx, domain.GatewayEndpoint{
		ID:             "gw_1",
		Type:           domain.GatewayEndpointTypeTelegram,
		Enabled:        true,
		Target:         "chat:1",
		AllowedActions: []domain.RemoteCommandAction{domain.RemoteCommandActionApprove},
	}, domain.RemoteSubscription{
		EndpointID:        "gw_1",
		SeverityThreshold: domain.NotificationSeverityLow,
	})
	if err != nil {
		t.Fatalf("RegisterEndpoint() error = %v", err)
	}

	err = svc.SubmitRemoteCommand(ctx, domain.RemoteCommand{
		CommandID:        "rcmd_1",
		SourceEndpointID: "gw_1",
		Actor:            "user:leo",
		SessionID:        "sess_1",
		Action:           domain.RemoteCommandActionCancel,
		IssuedAt:         time.Now().UTC(),
	})
	if err == nil {
		t.Fatal("SubmitRemoteCommand() error = nil, want error")
	}
}
