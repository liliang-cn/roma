package gateway

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/liliang-cn/tagit/internal/domain"
)

func testNotification() domain.NotificationEnvelope {
	return domain.NotificationEnvelope{
		ID:        "notif_1",
		Type:      "task_succeeded",
		Severity:  domain.NotificationSeverityLow,
		SessionID: "sess_1",
		TaskID:    "task_1",
		Title:     "TagIt task succeeded",
		Summary:   "Job job_1 completed with 1 artifact(s).",
		CreatedAt: time.Unix(1700000000, 0).UTC(),
	}
}

func TestWebhookDeliverPostsSignedBody(t *testing.T) {
	t.Parallel()

	type captured struct {
		body      []byte
		event     string
		notifID   string
		timestamp string
		signature string
	}
	got := make(chan captured, 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got <- captured{
			body:      body,
			event:     r.Header.Get("X-TagIt-Event"),
			notifID:   r.Header.Get("X-TagIt-Notification-Id"),
			timestamp: r.Header.Get("X-TagIt-Timestamp"),
			signature: r.Header.Get("X-TagIt-Signature"),
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	adapter := NewWebhookAdapter()
	err := adapter.Deliver(context.Background(), domain.GatewayEndpoint{
		ID:        "gw_1",
		Type:      domain.GatewayEndpointTypeWebhook,
		Enabled:   true,
		Target:    srv.URL,
		SecretRef: "s3cret",
	}, testNotification())
	if err != nil {
		t.Fatalf("Deliver() error = %v", err)
	}

	c := <-got
	if c.event != "task_succeeded" || c.notifID != "notif_1" {
		t.Fatalf("headers: event=%q id=%q", c.event, c.notifID)
	}

	var decoded domain.NotificationEnvelope
	if err := json.Unmarshal(c.body, &decoded); err != nil {
		t.Fatalf("body is not a notification envelope: %v", err)
	}
	if decoded.ID != "notif_1" || decoded.Summary == "" {
		t.Fatalf("decoded = %+v", decoded)
	}

	// The signature covers "<timestamp>.<body>", so a receiver can reject a
	// replay by age instead of accepting anything ever captured.
	mac := hmac.New(sha256.New, []byte("s3cret"))
	mac.Write([]byte(c.timestamp))
	mac.Write([]byte("."))
	mac.Write(c.body)
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if c.signature != want {
		t.Fatalf("signature = %q, want %q", c.signature, want)
	}
}

func TestWebhookNoSecretMeansNoSignature(t *testing.T) {
	t.Parallel()

	sig := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sig <- r.Header.Get("X-TagIt-Signature")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	err := NewWebhookAdapter().Deliver(context.Background(), domain.GatewayEndpoint{
		ID: "gw_1", Type: domain.GatewayEndpointTypeWebhook, Enabled: true, Target: srv.URL,
	}, testNotification())
	if err != nil {
		t.Fatalf("Deliver() error = %v", err)
	}
	if s := <-sig; s != "" {
		t.Fatalf("unsigned delivery carried a signature: %q", s)
	}
}

func TestWebhookSecretFromEnv(t *testing.T) {
	sig := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sig <- r.Header.Get("X-TagIt-Signature")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	t.Setenv("TAGIT_TEST_HOOK_SECRET", "from-env")
	err := NewWebhookAdapter().Deliver(context.Background(), domain.GatewayEndpoint{
		ID: "gw_1", Type: domain.GatewayEndpointTypeWebhook, Enabled: true,
		Target: srv.URL, SecretRef: "env:TAGIT_TEST_HOOK_SECRET",
	}, testNotification())
	if err != nil {
		t.Fatalf("Deliver() error = %v", err)
	}
	if s := <-sig; s == "" {
		t.Fatal("env-referenced secret produced no signature")
	}
}

func TestWebhookRetriesServerErrorsThenSucceeds(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	adapter := NewWebhookAdapter(WithBackoff(time.Millisecond))
	err := adapter.Deliver(context.Background(), domain.GatewayEndpoint{
		ID: "gw_1", Type: domain.GatewayEndpointTypeWebhook, Enabled: true, Target: srv.URL,
	}, testNotification())
	if err != nil {
		t.Fatalf("Deliver() error = %v", err)
	}
	if n := calls.Load(); n != 3 {
		t.Fatalf("attempts = %d, want 3", n)
	}
}

func TestWebhookDoesNotRetryClientErrors(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	adapter := NewWebhookAdapter(WithBackoff(time.Millisecond))
	err := adapter.Deliver(context.Background(), domain.GatewayEndpoint{
		ID: "gw_1", Type: domain.GatewayEndpointTypeWebhook, Enabled: true, Target: srv.URL,
	}, testNotification())
	if err == nil {
		t.Fatal("Deliver() error = nil, want failure")
	}
	// 400 means the receiver understood and refused; repeating it just earns
	// the same refusal three times.
	if n := calls.Load(); n != 1 {
		t.Fatalf("attempts = %d, want 1", n)
	}
}

func TestWebhookGivesUpAfterAttempts(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	adapter := NewWebhookAdapter(WithAttempts(2), WithBackoff(time.Millisecond))
	err := adapter.Deliver(context.Background(), domain.GatewayEndpoint{
		ID: "gw_1", Type: domain.GatewayEndpointTypeWebhook, Enabled: true, Target: srv.URL,
	}, testNotification())
	if err == nil {
		t.Fatal("Deliver() error = nil, want failure")
	}
	if n := calls.Load(); n != 2 {
		t.Fatalf("attempts = %d, want 2", n)
	}
}

func TestWebhookEmptyTargetFails(t *testing.T) {
	t.Parallel()

	err := NewWebhookAdapter().Deliver(context.Background(), domain.GatewayEndpoint{
		ID: "gw_1", Type: domain.GatewayEndpointTypeWebhook, Enabled: true,
	}, testNotification())
	if err == nil {
		t.Fatal("Deliver() error = nil, want failure for an empty target")
	}
}

func TestWebhookAdapterTypeIsWebhook(t *testing.T) {
	t.Parallel()

	if got := NewWebhookAdapter().Type(); got != domain.GatewayEndpointTypeWebhook {
		t.Fatalf("Type() = %q", got)
	}
}

func TestWebhookSendsConfiguredHeaders(t *testing.T) {
	got := make(chan http.Header, 4)
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		got <- r.Header.Clone()
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	t.Setenv("TAGIT_TEST_RELAY_AUTH", "Bearer relay-token")
	err := NewWebhookAdapter().Deliver(context.Background(), domain.GatewayEndpoint{
		ID: "gw_1", Type: domain.GatewayEndpointTypeWebhook, Enabled: true, Target: srv.URL,
		SecretRef: "s3cret",
		Headers: map[string]string{
			"Authorization":      "env:TAGIT_TEST_RELAY_AUTH",
			"X-Hookrelay-Source": "tagit",
		},
	}, testNotification())
	if err != nil {
		t.Fatalf("Deliver() error = %v", err)
	}

	h := <-got
	// This is what reaches a relay that authenticates its senders rather than
	// verifying the signature; without it every delivery is a 401.
	if h.Get("Authorization") != "Bearer relay-token" {
		t.Fatalf("Authorization = %q", h.Get("Authorization"))
	}
	if h.Get("X-Hookrelay-Source") != "tagit" {
		t.Fatalf("X-Hookrelay-Source = %q", h.Get("X-Hookrelay-Source"))
	}
	// TagIt's own headers still win, so a config cannot strip the signature.
	if h.Get("X-TagIt-Signature") == "" || h.Get("X-TagIt-Event") != "task_succeeded" {
		t.Fatalf("TagIt headers lost: %v", h)
	}
	// 202 counts as delivered: a relay that queues is the normal case, and
	// retrying it would duplicate every notification hookrelay accepts.
	if n := calls.Load(); n != 1 {
		t.Fatalf("attempts = %d, want 1 (202 must not be retried)", n)
	}
}
