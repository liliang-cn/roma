package openclaw

import (
	"context"
	"strings"

	"github.com/liliang-cn/tagit/internal/chatbot"
)

const contextMaxChars = 3000

type sender struct{ bridge Bridge }

// NewSender adapts the bridge to chatbot.Sender. OpenClaw's messages_send
// replies through the route already stored on the session, so there is no
// thread to attach to: rootMessageID is not used.
func NewSender(bridge Bridge) chatbot.Sender { return &sender{bridge: bridge} }

func (s *sender) Reply(ctx context.Context, chatID, _ string, text string) error {
	return s.bridge.SendMessage(ctx, chatID, text)
}

type contextProvider struct{ bridge Bridge }

// newContextProvider builds a chatbot.ContextProvider over the durable
// transcript. OpenClaw conversations have no threads, so threadID is ignored.
func newContextProvider(bridge Bridge) chatbot.ContextProvider {
	return &contextProvider{bridge: bridge}
}

// RecentContext returns a plain-text transcript (oldest→newest). Best-effort:
// any bridge error returns "".
func (c *contextProvider) RecentContext(ctx context.Context, chatID, _, _ string) string {
	msgs, err := c.bridge.ReadMessages(ctx, chatID, 20)
	if err != nil {
		return ""
	}
	lines := make([]string, 0, len(msgs))
	for _, m := range msgs {
		if text := strings.TrimSpace(m.Text); text != "" {
			lines = append(lines, text)
		}
	}
	if len(lines) == 0 {
		return ""
	}
	// Drop the oldest lines until the transcript fits.
	for {
		out := strings.Join(lines, "\n")
		if len(out) <= contextMaxChars || len(lines) == 1 {
			return out
		}
		lines = lines[1:]
	}
}
