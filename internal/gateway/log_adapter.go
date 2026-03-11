package gateway

import (
	"context"
	"log"

	"github.com/liliang-cn/roma/internal/domain"
)

// LogAdapter is a bootstrap adapter that logs outbound notifications.
type LogAdapter struct {
	endpointType domain.GatewayEndpointType
}

// NewLogAdapter constructs a logging adapter for a specific endpoint type.
func NewLogAdapter(endpointType domain.GatewayEndpointType) *LogAdapter {
	return &LogAdapter{endpointType: endpointType}
}

// Type returns the endpoint type handled by this adapter.
func (a *LogAdapter) Type() domain.GatewayEndpointType {
	return a.endpointType
}

// Deliver logs the outbound notification for bootstrap verification.
func (a *LogAdapter) Deliver(_ context.Context, endpoint domain.GatewayEndpoint, notification domain.NotificationEnvelope) error {
	log.Printf("gateway notify endpoint=%s type=%s notification=%s severity=%s title=%q", endpoint.ID, endpoint.Type, notification.ID, notification.Severity, notification.Title)
	return nil
}
