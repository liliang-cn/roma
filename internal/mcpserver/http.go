package mcpserver

// Streamable-HTTP transport for the TagIt MCP server.
//
// Stdio is the right transport when the client can spawn `tagit mcp` itself —
// Claude Code and Codex both run on the same machine as the daemon. It is the
// wrong one the moment the client lives somewhere else: an OpenClaw gateway on
// a VM (or three, when it fails over) cannot spawn a process on the laptop that
// holds the coding-agent logins. That case wants one endpoint the cluster dials
// out to, not a shell on the laptop for every node.
//
// This endpoint can start coding agents. It is therefore token-gated with no
// unauthenticated mode at all — see ServeStreamableHTTP.

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// DefaultHTTPPath is where the streamable-HTTP endpoint is mounted.
const DefaultHTTPPath = "/mcp"

// httpShutdownGrace bounds the wait for in-flight tool calls when the process
// is asked to stop. A tagit_job_wait can legitimately be parked for minutes, so
// this is a cap on politeness, not on the call.
const httpShutdownGrace = 5 * time.Second

// HTTPOptions configures the streamable-HTTP transport.
type HTTPOptions struct {
	// Addr is the listen address, e.g. ":43821" or "100.64.0.5:43821".
	Addr string
	// Token is the bearer token every request must present. Required.
	Token string
	// Path is the mount point; defaults to DefaultHTTPPath.
	Path string
	// Announce, when set, is called with the resolved listen address once the
	// listener is up — the port is only known after Listen when Addr uses :0.
	Announce func(addr string)
}

// ErrNoToken is returned when an HTTP endpoint is requested without a token.
var ErrNoToken = errors.New("an MCP HTTP endpoint needs a token: it can start coding agents on this machine")

// ServeStreamableHTTP serves srv over streamable HTTP until ctx is cancelled.
//
// There is deliberately no way to run this without a token. The tools behind it
// check out repositories and run coding agents; an open port doing that on a
// developer's laptop is not a mode worth offering, not even on loopback, where
// it would still be reachable by every process on the machine.
func ServeStreamableHTTP(ctx context.Context, srv *mcp.Server, opts HTTPOptions) error {
	token := strings.TrimSpace(opts.Token)
	if token == "" {
		return ErrNoToken
	}
	path := strings.TrimSpace(opts.Path)
	if path == "" {
		path = DefaultHTTPPath
	}

	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil)
	mux := http.NewServeMux()
	mux.Handle(path, requireBearer(token, handler))
	// A liveness probe that needs no token: an operator checking whether the
	// endpoint is up should not have to hold the key that can run code.
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"service":"tagit-mcp","status":"ok"}`))
	})

	listener, err := net.Listen("tcp", opts.Addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", opts.Addr, err)
	}
	if opts.Announce != nil {
		opts.Announce(listener.Addr().String())
	}

	server := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(listener) }()

	select {
	case err := <-serveErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), httpShutdownGrace)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
		return nil
	}
}

// requireBearer rejects any request that does not carry the exact token.
//
// The comparison is constant-time: this endpoint is reachable from the network,
// and a length- or prefix-dependent compare leaks the token to anyone patient
// enough to measure it.
func requireBearer(token string, next http.Handler) http.Handler {
	want := []byte(token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := []byte(strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")))
		if subtle.ConstantTimeCompare(got, want) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="tagit"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
