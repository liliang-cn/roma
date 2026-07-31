package openclaw

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// fakeOpenClaw is an in-process stand-in for `openclaw mcp serve`. It mirrors
// the real bridge's tool names and structured-output shapes byte for byte, so
// the decoding under test is exercised against the contract we actually ship
// against rather than against a Go struct we control.
type fakeOpenClaw struct {
	waitResult  any
	pollResult  any
	readResult  any
	sendCalls   []map[string]any
	waitCalls   []map[string]any
	pollCalls   []map[string]any
	toolFailure string
}

func (f *fakeOpenClaw) serve(t *testing.T) *mcp.ClientSession {
	t.Helper()
	server := mcp.NewServer(&mcp.Implementation{Name: "openclaw", Version: "test"}, nil)

	add := func(name string, fn func(args map[string]any) any) {
		server.AddTool(
			&mcp.Tool{Name: name, InputSchema: map[string]any{"type": "object"}},
			func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				if f.toolFailure != "" {
					return &mcp.CallToolResult{
						IsError: true,
						Content: []mcp.Content{&mcp.TextContent{Text: f.toolFailure}},
					}, nil
				}
				args := map[string]any{}
				_ = json.Unmarshal(req.Params.Arguments, &args)
				return &mcp.CallToolResult{
					Content:           []mcp.Content{&mcp.TextContent{Text: "ok"}},
					StructuredContent: fn(args),
				}, nil
			},
		)
	}

	add("events_wait", func(args map[string]any) any {
		f.waitCalls = append(f.waitCalls, args)
		return f.waitResult
	})
	add("events_poll", func(args map[string]any) any {
		f.pollCalls = append(f.pollCalls, args)
		return f.pollResult
	})
	add("messages_send", func(args map[string]any) any {
		f.sendCalls = append(f.sendCalls, args)
		return map[string]any{"result": "sent"}
	})
	add("messages_read", func(map[string]any) any { return f.readResult })

	ctx := context.Background()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("fake openclaw connect: %v", err)
	}
	clientSession, err := mcp.NewClient(&mcp.Implementation{Name: "tagit", Version: "test"}, nil).
		Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() {
		_ = clientSession.Close()
		_ = serverSession.Wait()
	})
	return clientSession
}

func TestBridgeWaitEventDecodesAMessageEvent(t *testing.T) {
	fake := &fakeOpenClaw{waitResult: map[string]any{"event": map[string]any{
		"cursor":     7,
		"type":       "message",
		"sessionKey": "agent:main:telegram:direct:1669479669",
		"messageId":  "msg_1",
		"role":       "user",
		"text":       "add a healthcheck endpoint",
		"conversation": map[string]any{
			"channel": "telegram",
			"to":      "telegram:1669479669",
		},
	}}}
	bridge := newBridge(fake.serve(t))

	ev, err := bridge.WaitEvent(context.Background(), 0, 50*time.Millisecond)
	if err != nil {
		t.Fatalf("WaitEvent() error = %v", err)
	}
	if ev == nil {
		t.Fatal("WaitEvent() = nil, want an event")
	}
	if ev.Cursor != 7 || ev.Type != "message" || ev.Role != "user" {
		t.Errorf("event = %+v", ev)
	}
	if ev.SessionKey != "agent:main:telegram:direct:1669479669" {
		t.Errorf("sessionKey = %q", ev.SessionKey)
	}
	if ev.Text != "add a healthcheck endpoint" {
		t.Errorf("text = %q", ev.Text)
	}
	if ev.Conversation == nil || ev.Conversation.Channel != "telegram" {
		t.Errorf("conversation = %+v", ev.Conversation)
	}
}

func TestBridgeWaitEventReturnsNilOnTimeout(t *testing.T) {
	// The real bridge answers a timed-out wait with {"event": null}.
	fake := &fakeOpenClaw{waitResult: map[string]any{"event": nil}}
	bridge := newBridge(fake.serve(t))

	ev, err := bridge.WaitEvent(context.Background(), 0, 50*time.Millisecond)
	if err != nil {
		t.Fatalf("WaitEvent() error = %v", err)
	}
	if ev != nil {
		t.Errorf("WaitEvent() = %+v, want nil on timeout", ev)
	}
}

func TestBridgeWaitEventPassesTheCursorAndTimeout(t *testing.T) {
	fake := &fakeOpenClaw{waitResult: map[string]any{"event": nil}}
	bridge := newBridge(fake.serve(t))

	if _, err := bridge.WaitEvent(context.Background(), 42, 3*time.Second); err != nil {
		t.Fatalf("WaitEvent() error = %v", err)
	}
	if len(fake.waitCalls) != 1 {
		t.Fatalf("events_wait called %d times, want 1", len(fake.waitCalls))
	}
	args := fake.waitCalls[0]
	if got, ok := args["after_cursor"].(float64); !ok || int64(got) != 42 {
		t.Errorf("after_cursor = %v, want 42", args["after_cursor"])
	}
	if got, ok := args["timeout_ms"].(float64); !ok || int64(got) != 3000 {
		t.Errorf("timeout_ms = %v, want 3000", args["timeout_ms"])
	}
}

func TestBridgePollEventsReturnsEventsAndTheNextCursor(t *testing.T) {
	fake := &fakeOpenClaw{pollResult: map[string]any{
		"events": []any{
			map[string]any{"cursor": 1, "type": "message", "sessionKey": "s", "role": "user", "text": "one"},
			map[string]any{"cursor": 2, "type": "exec_approval_requested", "sessionKey": "s"},
		},
		"next_cursor": 2,
	}}
	bridge := newBridge(fake.serve(t))

	evs, next, err := bridge.PollEvents(context.Background(), 0, 200)
	if err != nil {
		t.Fatalf("PollEvents() error = %v", err)
	}
	if len(evs) != 2 {
		t.Fatalf("got %d events, want 2", len(evs))
	}
	if evs[0].Text != "one" || evs[1].Type != "exec_approval_requested" {
		t.Errorf("events = %+v", evs)
	}
	if next != 2 {
		t.Errorf("next cursor = %d, want 2", next)
	}
}

func TestBridgeSendMessagePassesTheSessionKeyAndText(t *testing.T) {
	fake := &fakeOpenClaw{}
	bridge := newBridge(fake.serve(t))

	if err := bridge.SendMessage(context.Background(), "sess_1", "done ✅"); err != nil {
		t.Fatalf("SendMessage() error = %v", err)
	}
	if len(fake.sendCalls) != 1 {
		t.Fatalf("messages_send called %d times, want 1", len(fake.sendCalls))
	}
	if got := fake.sendCalls[0]["session_key"]; got != "sess_1" {
		t.Errorf("session_key = %v, want sess_1", got)
	}
	if got := fake.sendCalls[0]["text"]; got != "done ✅" {
		t.Errorf("text = %v", got)
	}
}

func TestBridgeReadMessagesExtractsTextBlocks(t *testing.T) {
	fake := &fakeOpenClaw{readResult: map[string]any{"messages": []any{
		map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "text", "text": "first"},
		}},
		map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "image", "url": "x"},
			map[string]any{"type": "text", "text": "second"},
		}},
	}}}
	bridge := newBridge(fake.serve(t))

	msgs, err := bridge.ReadMessages(context.Background(), "sess_1", 20)
	if err != nil {
		t.Fatalf("ReadMessages() error = %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("got %d messages, want 2", len(msgs))
	}
	if msgs[0].Role != "user" || msgs[0].Text != "first" {
		t.Errorf("messages[0] = %+v", msgs[0])
	}
	if msgs[1].Role != "assistant" || msgs[1].Text != "second" {
		t.Errorf("messages[1] = %+v (must skip non-text blocks)", msgs[1])
	}
}

func TestBridgeSurfacesToolErrors(t *testing.T) {
	fake := &fakeOpenClaw{toolFailure: "gateway unreachable"}
	bridge := newBridge(fake.serve(t))

	if _, err := bridge.WaitEvent(context.Background(), 0, time.Second); err == nil {
		t.Fatal("WaitEvent() error = nil, want the tool error surfaced")
	}
	if err := bridge.SendMessage(context.Background(), "s", "hi"); err == nil {
		t.Fatal("SendMessage() error = nil, want the tool error surfaced")
	}
}

// TestDialAgainstRealOpenClaw exercises the whole path against a real
// `openclaw mcp serve` process: spawn, MCP handshake, tool call, decode. It is
// read-only (it never sends a message) and opt-in, because it needs OpenClaw
// installed and its Gateway reachable.
//
//	TAGIT_OPENCLAW_E2E=1 go test -run TestDialAgainstRealOpenClaw ./internal/openclaw/
func TestDialAgainstRealOpenClaw(t *testing.T) {
	if os.Getenv("TAGIT_OPENCLAW_E2E") != "1" {
		t.Skip("set TAGIT_OPENCLAW_E2E=1 to run against a real openclaw install")
	}
	if _, err := exec.LookPath("openclaw"); err != nil {
		t.Skipf("openclaw not on PATH: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cfg := &Config{}
	cfg.applyDefaults()
	bridge, err := Dial(ctx, cfg)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer func() { _ = bridge.Close() }()

	events, next, err := bridge.PollEvents(ctx, 0, 5)
	if err != nil {
		t.Fatalf("PollEvents() error = %v", err)
	}
	if next < 0 {
		t.Errorf("next cursor = %d, want >= 0", next)
	}
	t.Logf("real bridge returned %d queued events, next cursor %d", len(events), next)
}
