package gateway

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/liliang/roma/internal/domain"
	"github.com/liliang/roma/internal/events"
	"github.com/liliang/roma/internal/plans"
	"github.com/liliang/roma/internal/store"
)

// Adapter delivers normalized notifications to a remote endpoint.
type Adapter interface {
	Type() domain.GatewayEndpointType
	Deliver(ctx context.Context, endpoint domain.GatewayEndpoint, notification domain.NotificationEnvelope) error
}

// Service is the in-process gateway control surface.
type Service struct {
	eventStore store.EventStore
	planSvc    *plans.Service

	mu            sync.RWMutex
	endpoints     map[string]domain.GatewayEndpoint
	subscriptions map[string]domain.RemoteSubscription
	adapters      map[domain.GatewayEndpointType]Adapter
	now           func() time.Time
}

// NewService constructs a gateway service.
func NewService(eventStore store.EventStore, planSvc *plans.Service, adapters ...Adapter) *Service {
	adapterMap := make(map[domain.GatewayEndpointType]Adapter, len(adapters))
	for _, adapter := range adapters {
		adapterMap[adapter.Type()] = adapter
	}

	return &Service{
		eventStore:    eventStore,
		planSvc:       planSvc,
		endpoints:     make(map[string]domain.GatewayEndpoint),
		subscriptions: make(map[string]domain.RemoteSubscription),
		adapters:      adapterMap,
		now:           func() time.Time { return time.Now().UTC() },
	}
}

// RegisterEndpoint registers an endpoint and its subscription.
func (s *Service) RegisterEndpoint(ctx context.Context, endpoint domain.GatewayEndpoint, subscription domain.RemoteSubscription) error {
	if err := domain.ValidateGatewayEndpoint(endpoint); err != nil {
		return fmt.Errorf("validate endpoint: %w", err)
	}
	if endpoint.ID != subscription.EndpointID {
		return fmt.Errorf("subscription endpoint mismatch")
	}

	s.mu.Lock()
	s.endpoints[endpoint.ID] = endpoint
	s.subscriptions[endpoint.ID] = subscription
	s.mu.Unlock()

	return s.eventStore.AppendEvent(ctx, events.Record{
		ID:         "evt_gateway_endpoint_" + endpoint.ID,
		Type:       events.TypeGatewayEndpointRegistered,
		ActorType:  events.ActorTypeSystem,
		OccurredAt: s.now(),
		Payload: map[string]any{
			"endpoint_id": endpoint.ID,
			"type":        endpoint.Type,
			"enabled":     endpoint.Enabled,
		},
	})
}

// Deliver pushes a notification to eligible endpoints.
func (s *Service) Deliver(ctx context.Context, notification domain.NotificationEnvelope) error {
	if err := domain.ValidateNotificationEnvelope(notification); err != nil {
		return fmt.Errorf("validate notification: %w", err)
	}

	s.mu.RLock()
	endpoints := make([]domain.GatewayEndpoint, 0, len(s.endpoints))
	for _, endpoint := range s.endpoints {
		endpoints = append(endpoints, endpoint)
	}
	s.mu.RUnlock()

	var deliverErr error
	for _, endpoint := range endpoints {
		if !endpoint.Enabled {
			continue
		}
		subscription := s.subscriptionFor(endpoint.ID)
		if !matchesSubscription(notification, subscription) {
			continue
		}

		adapter, ok := s.adapters[endpoint.Type]
		if !ok {
			if err := s.appendDeliveryEvent(ctx, endpoint.ID, notification, false, "adapter_missing"); err != nil {
				return err
			}
			deliverErr = errorsJoin(deliverErr, fmt.Errorf("missing adapter for endpoint type %s", endpoint.Type))
			continue
		}

		if err := adapter.Deliver(ctx, endpoint, notification); err != nil {
			if appendErr := s.appendDeliveryEvent(ctx, endpoint.ID, notification, false, "delivery_failed"); appendErr != nil {
				return appendErr
			}
			deliverErr = errorsJoin(deliverErr, fmt.Errorf("deliver to endpoint %s: %w", endpoint.ID, err))
			continue
		}

		if err := s.appendDeliveryEvent(ctx, endpoint.ID, notification, true, "delivered"); err != nil {
			return err
		}
	}

	return deliverErr
}

// SubmitRemoteCommand audits and accepts or rejects a remote intent.
func (s *Service) SubmitRemoteCommand(ctx context.Context, cmd domain.RemoteCommand) error {
	if err := domain.ValidateRemoteCommand(cmd); err != nil {
		return fmt.Errorf("validate remote command: %w", err)
	}

	endpoint, ok := s.endpointFor(cmd.SourceEndpointID)
	if !ok {
		if err := s.appendRemoteCommandEvent(ctx, cmd, false, "endpoint_not_found"); err != nil {
			return err
		}
		return fmt.Errorf("endpoint %s not found", cmd.SourceEndpointID)
	}
	if !endpoint.Enabled {
		if err := s.appendRemoteCommandEvent(ctx, cmd, false, "endpoint_disabled"); err != nil {
			return err
		}
		return fmt.Errorf("endpoint %s disabled", cmd.SourceEndpointID)
	}
	if len(endpoint.AllowedActions) > 0 && !slices.Contains(endpoint.AllowedActions, cmd.Action) {
		if err := s.appendRemoteCommandEvent(ctx, cmd, false, "action_not_allowed"); err != nil {
			return err
		}
		return fmt.Errorf("action %s not allowed for endpoint %s", cmd.Action, endpoint.ID)
	}

	if err := s.appendRemoteCommandEvent(ctx, cmd, true, "accepted_for_romad_validation"); err != nil {
		return err
	}
	switch cmd.Action {
	case domain.RemoteCommandActionPlanApprove:
		return s.planSvc.Approve(ctx, cmd.TaskID, cmd.Actor)
	case domain.RemoteCommandActionPlanReject:
		return s.planSvc.Reject(ctx, cmd.TaskID, cmd.Actor)
	}
	return nil
}

func (s *Service) endpointFor(endpointID string) (domain.GatewayEndpoint, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	endpoint, ok := s.endpoints[endpointID]
	return endpoint, ok
}

func (s *Service) subscriptionFor(endpointID string) domain.RemoteSubscription {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.subscriptions[endpointID]
}

func matchesSubscription(notification domain.NotificationEnvelope, subscription domain.RemoteSubscription) bool {
	if subscription.EndpointID == "" {
		return false
	}
	if len(subscription.SessionFilter) > 0 && !slices.Contains(subscription.SessionFilter, notification.SessionID) {
		return false
	}
	if subscription.SeverityThreshold != "" && severityRank(notification.Severity) < severityRank(subscription.SeverityThreshold) {
		return false
	}
	if len(subscription.EventTypes) > 0 && !slices.Contains(subscription.EventTypes, notification.Type) {
		return false
	}
	return true
}

func severityRank(severity domain.NotificationSeverity) int {
	switch severity {
	case domain.NotificationSeverityHigh:
		return 3
	case domain.NotificationSeverityMedium:
		return 2
	case domain.NotificationSeverityLow:
		return 1
	default:
		return 0
	}
}

func (s *Service) appendDeliveryEvent(ctx context.Context, endpointID string, notification domain.NotificationEnvelope, success bool, reason string) error {
	return s.eventStore.AppendEvent(ctx, events.Record{
		ID:         "evt_gateway_delivery_" + notification.ID + "_" + endpointID,
		SessionID:  notification.SessionID,
		TaskID:     notification.TaskID,
		Type:       events.TypeGatewayDeliveryRecorded,
		ActorType:  events.ActorTypeGateway,
		OccurredAt: s.now(),
		ReasonCode: reason,
		Payload: map[string]any{
			"endpoint_id":     endpointID,
			"notification_id": notification.ID,
			"success":         success,
			"type":            notification.Type,
		},
	})
}

func (s *Service) appendRemoteCommandEvent(ctx context.Context, cmd domain.RemoteCommand, accepted bool, reason string) error {
	return s.eventStore.AppendEvent(ctx, events.Record{
		ID:         "evt_remote_command_" + cmd.CommandID,
		SessionID:  cmd.SessionID,
		TaskID:     cmd.TaskID,
		Type:       events.TypeRemoteCommandRecorded,
		ActorType:  events.ActorTypeGateway,
		OccurredAt: s.now(),
		ReasonCode: reason,
		Payload: map[string]any{
			"command_id":  cmd.CommandID,
			"endpoint_id": cmd.SourceEndpointID,
			"actor":       cmd.Actor,
			"action":      cmd.Action,
			"accepted":    accepted,
		},
	})
}

func errorsJoin(base error, next error) error {
	if base == nil {
		return next
	}
	return fmt.Errorf("%v; %w", base, next)
}
