package gateway

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/liliang-cn/tagit/internal/domain"
)

// Config is ~/.tagit/gateway.json: the outbound endpoints this daemon notifies.
type Config struct {
	Endpoints []EndpointConfig `json:"endpoints"`
}

// EndpointConfig is one configured endpoint plus its notification filter.
type EndpointConfig struct {
	ID       string   `json:"id"`
	Type     string   `json:"type,omitempty"`     // default "webhook"
	Target   string   `json:"target"`             // URL to POST to
	Secret   string   `json:"secret,omitempty"`   // literal, or "env:NAME"
	Events   []string `json:"events,omitempty"`   // empty = every event type
	Severity string   `json:"severity,omitempty"` // low|medium|high, default low
	Sessions []string `json:"sessions,omitempty"` // empty = every session
	Actions  []string `json:"actions,omitempty"`  // remote commands this endpoint may send back
	Disabled bool     `json:"disabled,omitempty"`
}

// Registration is one endpoint paired with its subscription, ready to hand to
// Service.RegisterEndpoint.
type Registration struct {
	Endpoint     domain.GatewayEndpoint
	Subscription domain.RemoteSubscription
}

// Load reads the gateway config. A missing file is not an error: it means no
// endpoints are configured, which is the default state.
func Load(path string) (Config, bool, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Config{}, false, nil
	}
	if err != nil {
		return Config{}, false, fmt.Errorf("read %s: %w", path, err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, false, fmt.Errorf("parse %s: %w", path, err)
	}
	return cfg, len(cfg.Endpoints) > 0, nil
}

// Registrations converts the config into registrations, rejecting entries that
// could not be delivered to. A bad entry fails the whole load rather than being
// skipped: an endpoint silently dropped is a notification silently lost, and
// the operator would only find out by not being paged.
func (c Config) Registrations() ([]Registration, error) {
	out := make([]Registration, 0, len(c.Endpoints))
	seen := make(map[string]struct{}, len(c.Endpoints))

	for i, ep := range c.Endpoints {
		id := strings.TrimSpace(ep.ID)
		if id == "" {
			return nil, fmt.Errorf("endpoints[%d]: id is required", i)
		}
		if _, dup := seen[id]; dup {
			return nil, fmt.Errorf("endpoints[%d]: duplicate id %q", i, id)
		}
		seen[id] = struct{}{}

		if strings.TrimSpace(ep.Target) == "" {
			return nil, fmt.Errorf("endpoint %s: target is required", id)
		}
		endpointType := domain.GatewayEndpointType(strings.TrimSpace(ep.Type))
		if endpointType == "" {
			endpointType = domain.GatewayEndpointTypeWebhook
		}

		severity, err := parseSeverity(ep.Severity)
		if err != nil {
			return nil, fmt.Errorf("endpoint %s: %w", id, err)
		}
		actions, err := parseActions(ep.Actions)
		if err != nil {
			return nil, fmt.Errorf("endpoint %s: %w", id, err)
		}

		endpoint := domain.GatewayEndpoint{
			ID:             id,
			Type:           endpointType,
			Enabled:        !ep.Disabled,
			Target:         strings.TrimSpace(ep.Target),
			SecretRef:      strings.TrimSpace(ep.Secret),
			AllowedActions: actions,
		}
		if err := domain.ValidateGatewayEndpoint(endpoint); err != nil {
			return nil, fmt.Errorf("endpoint %s: %w", id, err)
		}

		out = append(out, Registration{
			Endpoint: endpoint,
			Subscription: domain.RemoteSubscription{
				EndpointID:          id,
				EventTypes:          trimAll(ep.Events),
				SessionFilter:       trimAll(ep.Sessions),
				SeverityThreshold:   severity,
				SummaryMode:         "compact",
				IncludeArtifactRefs: true,
			},
		})
	}
	return out, nil
}

func parseSeverity(raw string) (domain.NotificationSeverity, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "low":
		return domain.NotificationSeverityLow, nil
	case "medium":
		return domain.NotificationSeverityMedium, nil
	case "high":
		return domain.NotificationSeverityHigh, nil
	default:
		return "", fmt.Errorf("unknown severity %q (want low|medium|high)", raw)
	}
}

func parseActions(raw []string) ([]domain.RemoteCommandAction, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	allowed := map[string]domain.RemoteCommandAction{
		"approve":      domain.RemoteCommandActionApprove,
		"reject":       domain.RemoteCommandActionReject,
		"pause":        domain.RemoteCommandActionPause,
		"resume":       domain.RemoteCommandActionResume,
		"cancel":       domain.RemoteCommandActionCancel,
		"retry":        domain.RemoteCommandActionRetry,
		"plan_approve": domain.RemoteCommandActionPlanApprove,
		"plan_reject":  domain.RemoteCommandActionPlanReject,
	}
	out := make([]domain.RemoteCommandAction, 0, len(raw))
	for _, name := range raw {
		action, ok := allowed[strings.ToLower(strings.TrimSpace(name))]
		if !ok {
			return nil, fmt.Errorf("unknown action %q", name)
		}
		out = append(out, action)
	}
	return out, nil
}

func trimAll(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
