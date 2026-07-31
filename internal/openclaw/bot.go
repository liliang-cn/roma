package openclaw

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/liliang-cn/tagit/internal/api"
	"github.com/liliang-cn/tagit/internal/chatbot"
)

const (
	// waitTimeout is how long one events_wait long-poll blocks. OpenClaw caps
	// this at 300s; a shorter value keeps the loop responsive to ctx cancel.
	waitTimeout = 30 * time.Second
	// drainLimit is how many queued events one events_poll drains. OpenClaw
	// caps it at 200.
	drainLimit = 200
	// retryDelay backs off after a bridge error so a dead gateway does not spin.
	retryDelay = 2 * time.Second
)

// Bot pumps OpenClaw's live event queue into the shared chatbot handler, so any
// channel OpenClaw carries (WeChat, Telegram, iMessage, …) can drive TagIt.
type Bot struct {
	bridge  Bridge
	handler *chatbot.Handler
}

// Serve dials the OpenClaw MCP bridge and runs the bot until ctx is cancelled
// or the bridge process dies. This is the entry point the daemon uses.
func Serve(ctx context.Context, cfg *Config, path string, apiClient *api.Client) error {
	bridge, err := Dial(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() { _ = bridge.Close() }()
	return NewBot(bridge, path, apiClient).Start(ctx)
}

// NewBot wires the OpenClaw sender + shared handler over TagIt's api.Client.
// path is the openclaw.json file backing the persistent binding store.
func NewBot(bridge Bridge, path string, apiClient *api.Client) *Bot {
	snd := NewSender(bridge)
	return newBot(
		bridge,
		NewConfigStore(path),
		chatbot.NewAPIEnqueuer(apiClient),
		snd,
		chatbot.NewProgressFunc(apiClient, snd),
	)
}

// newBotWithBridge is the seam tests use: the same wiring with a fake bridge
// and enqueuer, and no daemon.
func newBotWithBridge(bridge Bridge, store chatbot.BindingStore, enq chatbot.Enqueuer) *Bot {
	return newBot(bridge, store, enq, NewSender(bridge), nil)
}

func newBot(bridge Bridge, store chatbot.BindingStore, enq chatbot.Enqueuer, snd chatbot.Sender, progress chatbot.ProgressFunc) *Bot {
	handler := chatbot.NewHandler(store, enq, snd, progress)
	handler.SetContextProvider(newContextProvider(bridge))
	return &Bot{bridge: bridge, handler: handler}
}

// Start runs the event loop until ctx is cancelled.
//
// The queue is live-only — it begins when the bridge process starts — so the
// cursor starts at 0 and nothing historical is replayed. events_wait is used
// purely as a blocking signal; events_poll then drains everything queued at
// that cursor, which keeps a burst of messages from being handled one wait at
// a time. A failed poll deliberately leaves the cursor untouched so the next
// wait re-delivers the same events instead of dropping them.
func (b *Bot) Start(ctx context.Context) error {
	log.Printf("openclaw: starting bridge event loop")
	var cursor int64
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		ev, err := b.bridge.WaitEvent(ctx, cursor, waitTimeout)
		if err != nil {
			log.Printf("openclaw: events_wait failed: %v", err)
			if !sleepCtx(ctx, retryDelay) {
				return ctx.Err()
			}
			continue
		}
		if ev == nil {
			continue // long-poll timed out; normal
		}
		events, next, err := b.bridge.PollEvents(ctx, cursor, drainLimit)
		if err != nil {
			log.Printf("openclaw: events_poll failed: %v", err)
			if !sleepCtx(ctx, retryDelay) {
				return ctx.Err()
			}
			continue
		}
		for _, e := range events {
			b.dispatch(ctx, e)
		}
		if next > cursor {
			cursor = next
		}
	}
}

// dispatch turns one queued event into a handler call. Everything that is not
// an inbound user message — assistant echoes of our own replies, approval
// events — is dropped here.
func (b *Bot) dispatch(ctx context.Context, e Event) {
	if e.Type != "message" || e.Role != "user" || e.SessionKey == "" || e.Text == "" {
		return
	}
	msg := toIncomingMessage(e)
	log.Printf("openclaw: received message session=%s channel=%s text=%q", e.SessionKey, channelOf(e), msg.Text)
	b.handler.Handle(ctx, msg)
}

// toIncomingMessage maps an OpenClaw event onto the shared IncomingMessage.
//
// Two fields need explaining. OpenClaw routes have no @mention concept and no
// threads: the bridge hands us every message on a routed conversation, and the
// per-session binding in openclaw.json is what authorizes work — so Mentioned
// and IsGroup are both true, and ThreadID stays empty (replies go back through
// the session route, not a thread). Role filtering in dispatch is what keeps
// the bot from reacting to its own replies.
func toIncomingMessage(e Event) chatbot.IncomingMessage {
	return chatbot.IncomingMessage{
		MessageID: messageIDOf(e),
		ChatID:    e.SessionKey,
		Text:      e.Text,
		Mentioned: true,
		IsGroup:   true,
	}
}

// messageIDOf returns a stable id for deduplication. OpenClaw may omit
// messageId, in which case the cursor uniquely identifies the event.
func messageIDOf(e Event) string {
	if e.MessageID != "" {
		return e.MessageID
	}
	return fmt.Sprintf("%s#%d", e.SessionKey, e.Cursor)
}

func channelOf(e Event) string {
	if e.Conversation == nil {
		return ""
	}
	return e.Conversation.Channel
}

// sleepCtx sleeps for d, reporting false if ctx was cancelled first.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
