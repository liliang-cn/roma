package gateway

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/liliang-cn/tagit/internal/domain"
)

// WebhookAdapter POSTs a notification to the endpoint's target URL.
//
// The body is the NotificationEnvelope as JSON. When the endpoint carries a
// secret, the request is signed so the receiver can tell a real delivery from
// anyone who guessed the URL:
//
//	X-TagIt-Timestamp: <unix seconds>
//	X-TagIt-Signature: sha256=<hex HMAC-SHA256 of "<timestamp>.<body>">
//
// The timestamp is inside the signed string on purpose. Signing the body alone
// lets anyone who once observed a delivery replay it forever; with the
// timestamp signed, a receiver can reject anything older than its own window.
type WebhookAdapter struct {
	client   *http.Client
	attempts int
	backoff  time.Duration
	now      func() time.Time
}

// WebhookOption configures a WebhookAdapter.
type WebhookOption func(*WebhookAdapter)

// WithHTTPClient overrides the HTTP client (tests, proxies, custom TLS).
func WithHTTPClient(c *http.Client) WebhookOption {
	return func(a *WebhookAdapter) {
		if c != nil {
			a.client = c
		}
	}
}

// WithAttempts sets how many times one delivery is tried before giving up.
func WithAttempts(n int) WebhookOption {
	return func(a *WebhookAdapter) {
		if n > 0 {
			a.attempts = n
		}
	}
}

// WithBackoff sets the pause before the second attempt; it doubles after each.
func WithBackoff(d time.Duration) WebhookOption {
	return func(a *WebhookAdapter) {
		if d >= 0 {
			a.backoff = d
		}
	}
}

// NewWebhookAdapter constructs a webhook adapter with sane defaults.
func NewWebhookAdapter(opts ...WebhookOption) *WebhookAdapter {
	a := &WebhookAdapter{
		client:   &http.Client{Timeout: 10 * time.Second},
		attempts: 3,
		backoff:  500 * time.Millisecond,
		now:      func() time.Time { return time.Now().UTC() },
	}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// Type returns the endpoint type this adapter handles.
func (a *WebhookAdapter) Type() domain.GatewayEndpointType {
	return domain.GatewayEndpointTypeWebhook
}

// Deliver POSTs the notification, retrying transient failures.
func (a *WebhookAdapter) Deliver(ctx context.Context, endpoint domain.GatewayEndpoint, notification domain.NotificationEnvelope) error {
	target := strings.TrimSpace(endpoint.Target)
	if target == "" {
		return fmt.Errorf("endpoint %s has no target", endpoint.ID)
	}

	body, err := json.Marshal(notification)
	if err != nil {
		return fmt.Errorf("marshal notification: %w", err)
	}
	secret := resolveSecret(endpoint.SecretRef)

	var lastErr error
	wait := a.backoff
	for attempt := 1; attempt <= a.attempts; attempt++ {
		if attempt > 1 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(wait):
			}
			wait *= 2
		}

		retryable, err := a.post(ctx, target, secret, body, notification)
		if err == nil {
			return nil
		}
		lastErr = err
		if !retryable {
			return err
		}
	}
	return fmt.Errorf("webhook %s: %d attempts failed: %w", endpoint.ID, a.attempts, lastErr)
}

// post makes one delivery attempt and reports whether a failure is worth
// retrying. A 4xx other than 429 means the receiver understood us and said no,
// so repeating it only makes the same complaint again.
func (a *WebhookAdapter) post(ctx context.Context, target, secret string, body []byte, notification domain.NotificationEnvelope) (retryable bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return false, fmt.Errorf("build request: %w", err)
	}
	timestamp := strconv.FormatInt(a.now().Unix(), 10)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "tagit-gateway/1")
	req.Header.Set("X-TagIt-Event", notification.Type)
	req.Header.Set("X-TagIt-Notification-Id", notification.ID)
	req.Header.Set("X-TagIt-Timestamp", timestamp)
	if secret != "" {
		req.Header.Set("X-TagIt-Signature", "sha256="+sign(secret, timestamp, body))
	}

	resp, err := a.client.Do(req)
	if err != nil {
		return true, fmt.Errorf("post %s: %w", target, err)
	}
	defer resp.Body.Close()
	// Drain so the connection can be reused; the response body is not part of
	// the contract, only the status is.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return false, nil
	}
	retryable = resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
	return retryable, fmt.Errorf("post %s: status %d", target, resp.StatusCode)
}

// sign computes the hex HMAC-SHA256 of "<timestamp>.<body>".
func sign(secret, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// resolveSecret reads a secret reference. "env:NAME" reads that environment
// variable, so a shared config file need not hold the secret itself; anything
// else is the secret verbatim, which is what ~/.tagit/gateway.json normally
// holds since it sits beside the other credential files.
func resolveSecret(ref string) string {
	ref = strings.TrimSpace(ref)
	if name, ok := strings.CutPrefix(ref, "env:"); ok {
		return os.Getenv(strings.TrimSpace(name))
	}
	return ref
}
