package mcpserver

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func okHandler() (http.Handler, *bool) {
	reached := false
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	}), &reached
}

// The endpoint can start coding agents on the machine that serves it, so every
// way of arriving without the exact token has to be a 401 — and the wrapped
// handler must never run.
func TestBearerGateRejectsEveryWrongCredential(t *testing.T) {
	for name, header := range map[string]string{
		"no header":       "",
		"empty bearer":    "Bearer ",
		"wrong token":     "Bearer nope",
		"token prefix":    "Bearer secre",
		"no bearer word":  "secret-extra",
		"different token": "Bearer secret-extra",
	} {
		t.Run(name, func(t *testing.T) {
			next, reached := okHandler()
			req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
			if header != "" {
				req.Header.Set("Authorization", header)
			}
			rec := httptest.NewRecorder()

			requireBearer("secret", next).ServeHTTP(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Errorf("want 401, got %d", rec.Code)
			}
			if *reached {
				t.Error("the MCP handler ran for an unauthorized request")
			}
			if got := rec.Header().Get("WWW-Authenticate"); !strings.Contains(got, "Bearer") {
				t.Errorf("want a Bearer challenge, got %q", got)
			}
		})
	}
}

func TestBearerGatePassesTheRightToken(t *testing.T) {
	next, reached := okHandler()
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()

	requireBearer("secret", next).ServeHTTP(rec, req)

	if !*reached || rec.Code != http.StatusOK {
		t.Fatalf("authorized request did not reach the handler: code=%d reached=%v", rec.Code, *reached)
	}
}

// Serving this without a token is not a supported mode, not even on loopback:
// every process on the host could reach it.
func TestServingWithoutATokenIsRefused(t *testing.T) {
	err := ServeStreamableHTTP(context.Background(), NewServer(Options{Daemon: nil}), HTTPOptions{Addr: "127.0.0.1:0"})
	if !errors.Is(err, ErrNoToken) {
		t.Fatalf("want ErrNoToken, got %v", err)
	}
}

// End to end on a real socket: health needs no token, /mcp does, and cancelling
// the context stops the listener.
func TestEndpointServesHealthUnauthedAndMcpGated(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	addrCh := make(chan string, 1)
	done := make(chan error, 1)
	go func() {
		done <- ServeStreamableHTTP(ctx, NewServer(Options{Daemon: nil}), HTTPOptions{
			Addr:     "127.0.0.1:0",
			Token:    "secret",
			Announce: func(addr string) { addrCh <- addr },
		})
	}()

	var addr string
	select {
	case addr = <-addrCh:
	case <-time.After(5 * time.Second):
		t.Fatal("listener never announced its address")
	}

	resp, err := http.Get("http://" + addr + "/healthz")
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"status":"ok"`) {
		t.Errorf("health should answer without a token: %d %s", resp.StatusCode, body)
	}

	resp, err = http.Post("http://"+addr+"/mcp", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("mcp: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("want 401 on an untokened MCP call, got %d", resp.StatusCode)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("shutdown returned %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Error("server did not stop when the context was cancelled")
	}
}
