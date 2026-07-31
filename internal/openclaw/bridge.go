package openclaw

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Event is one entry from OpenClaw's live bridge queue. Only `message` events
// carry chat traffic; approval events (`exec_approval_requested`, …) also flow
// through the same queue and are ignored by the bot.
type Event struct {
	Cursor       int64         `json:"cursor"`
	Type         string        `json:"type"`
	SessionKey   string        `json:"sessionKey"`
	MessageID    string        `json:"messageId"`
	MessageSeq   int64         `json:"messageSeq"`
	Role         string        `json:"role"`
	Text         string        `json:"text"`
	Conversation *Conversation `json:"conversation"`
}

// Conversation is the channel route OpenClaw recorded for a session.
type Conversation struct {
	SessionKey  string `json:"sessionKey"`
	Channel     string `json:"channel"`
	To          string `json:"to"`
	AccountID   string `json:"accountId"`
	DisplayName string `json:"displayName"`
}

// TranscriptMessage is one durable message read back from a conversation, with
// its content blocks already flattened to text.
type TranscriptMessage struct {
	Role string
	Text string
}

// Bridge is the slice of OpenClaw's MCP surface TagIt uses. It exists so the
// bot can be tested without spawning `openclaw mcp serve`.
type Bridge interface {
	// WaitEvent long-polls for the next event after cursor. A nil event with a
	// nil error means the wait timed out, which is normal and not an error.
	WaitEvent(ctx context.Context, afterCursor int64, timeout time.Duration) (*Event, error)
	// PollEvents reads queued events after cursor and returns the next cursor.
	PollEvents(ctx context.Context, afterCursor int64, limit int) ([]Event, int64, error)
	// SendMessage replies through the route already recorded on the session.
	SendMessage(ctx context.Context, sessionKey, text string) error
	// ReadMessages reads recent durable transcript messages for a conversation.
	ReadMessages(ctx context.Context, sessionKey string, limit int) ([]TranscriptMessage, error)
	Close() error
}

// Dial starts `openclaw mcp serve` (per cfg) and speaks MCP to it over stdio.
// The caller owns the returned Bridge and must Close it.
func Dial(ctx context.Context, cfg *Config) (Bridge, error) {
	cmd := exec.Command(cfg.Command, cfg.Args...)
	client := mcp.NewClient(&mcp.Implementation{Name: "tagit", Version: "dev"}, nil)
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		return nil, fmt.Errorf("connect to %q: %w", cfg.Command, err)
	}
	return newBridge(session), nil
}

// newBridge wraps an established MCP session. Split out from Dial so tests can
// drive it against an in-process stand-in for the real bridge.
func newBridge(session *mcp.ClientSession) Bridge { return &mcpBridge{session: session} }

type mcpBridge struct{ session *mcp.ClientSession }

func (b *mcpBridge) Close() error { return b.session.Close() }

func (b *mcpBridge) WaitEvent(ctx context.Context, afterCursor int64, timeout time.Duration) (*Event, error) {
	timeoutMS := timeout.Milliseconds()
	if timeoutMS < 1 {
		timeoutMS = 1
	}
	var out struct {
		Event *Event `json:"event"`
	}
	if err := b.call(ctx, "events_wait", map[string]any{
		"after_cursor": afterCursor,
		"timeout_ms":   timeoutMS,
	}, &out); err != nil {
		return nil, err
	}
	return out.Event, nil
}

func (b *mcpBridge) PollEvents(ctx context.Context, afterCursor int64, limit int) ([]Event, int64, error) {
	var out struct {
		Events     []Event `json:"events"`
		NextCursor int64   `json:"next_cursor"`
	}
	if err := b.call(ctx, "events_poll", map[string]any{
		"after_cursor": afterCursor,
		"limit":        limit,
	}, &out); err != nil {
		return nil, afterCursor, err
	}
	return out.Events, out.NextCursor, nil
}

func (b *mcpBridge) SendMessage(ctx context.Context, sessionKey, text string) error {
	return b.call(ctx, "messages_send", map[string]any{
		"session_key": sessionKey,
		"text":        text,
	}, nil)
}

func (b *mcpBridge) ReadMessages(ctx context.Context, sessionKey string, limit int) ([]TranscriptMessage, error) {
	var out struct {
		Messages []struct {
			Role    string `json:"role"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"messages"`
	}
	if err := b.call(ctx, "messages_read", map[string]any{
		"session_key": sessionKey,
		"limit":       limit,
	}, &out); err != nil {
		return nil, err
	}
	msgs := make([]TranscriptMessage, 0, len(out.Messages))
	for _, m := range out.Messages {
		var text strings.Builder
		for _, block := range m.Content {
			if block.Type == "text" && block.Text != "" {
				if text.Len() > 0 {
					text.WriteString("\n")
				}
				text.WriteString(block.Text)
			}
		}
		msgs = append(msgs, TranscriptMessage{Role: m.Role, Text: text.String()})
	}
	return msgs, nil
}

// call invokes one OpenClaw tool and decodes its structured output into out
// (which may be nil when the caller only cares about success).
func (b *mcpBridge) call(ctx context.Context, tool string, args map[string]any, out any) error {
	res, err := b.session.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		return fmt.Errorf("openclaw %s: %w", tool, err)
	}
	if res.IsError {
		return fmt.Errorf("openclaw %s: %s", tool, toolErrorText(res))
	}
	if out == nil {
		return nil
	}
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		return fmt.Errorf("openclaw %s: encode result: %w", tool, err)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("openclaw %s: decode result: %w", tool, err)
	}
	return nil
}

func toolErrorText(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	if b.Len() == 0 {
		return "tool reported an error"
	}
	return b.String()
}
