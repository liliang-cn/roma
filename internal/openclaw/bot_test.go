package openclaw

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/liliang-cn/tagit/internal/chatbot"
)

// ---------------------------------------------------------------------------
// Fake bridge
// ---------------------------------------------------------------------------

type fakeBridge struct {
	mu sync.Mutex

	// waits/polls are consumed in order; the last entry repeats.
	waits []waitStep
	polls []pollStep

	waitCursors []int64
	pollCursors []int64
	sent        []sentMessage
	read        []TranscriptMessage
	readErr     error

	waitCalls int
	pollCalls int
	done      chan struct{}
	stopAfter int
}

type waitStep struct {
	event *Event
	err   error
}

type pollStep struct {
	events []Event
	next   int64
	err    error
}

type sentMessage struct{ sessionKey, text string }

func (f *fakeBridge) WaitEvent(_ context.Context, afterCursor int64, _ time.Duration) (*Event, error) {
	f.mu.Lock()
	f.waitCursors = append(f.waitCursors, afterCursor)
	f.waitCalls++
	step := f.step(f.waits, f.waitCalls)
	stop := f.stopAfter > 0 && f.waitCalls > f.stopAfter
	f.mu.Unlock()
	if stop {
		f.finish()
		time.Sleep(10 * time.Millisecond)
		return nil, nil
	}
	return step.event, step.err
}

func (f *fakeBridge) step(steps []waitStep, call int) waitStep {
	if len(steps) == 0 {
		return waitStep{}
	}
	if call > len(steps) {
		return steps[len(steps)-1]
	}
	return steps[call-1]
}

func (f *fakeBridge) PollEvents(_ context.Context, afterCursor int64, _ int) ([]Event, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pollCursors = append(f.pollCursors, afterCursor)
	f.pollCalls++
	if len(f.polls) == 0 {
		return nil, afterCursor, nil
	}
	idx := f.pollCalls - 1
	if idx >= len(f.polls) {
		idx = len(f.polls) - 1
	}
	s := f.polls[idx]
	return s.events, s.next, s.err
}

func (f *fakeBridge) SendMessage(_ context.Context, sessionKey, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, sentMessage{sessionKey, text})
	return nil
}

func (f *fakeBridge) ReadMessages(context.Context, string, int) ([]TranscriptMessage, error) {
	return f.read, f.readErr
}

func (f *fakeBridge) Close() error { return nil }

func (f *fakeBridge) finish() {
	f.mu.Lock()
	defer f.mu.Unlock()
	select {
	case <-f.done:
	default:
		close(f.done)
	}
}

func (f *fakeBridge) sentTexts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.sent))
	for _, m := range f.sent {
		out = append(out, m.text)
	}
	return out
}

// ---------------------------------------------------------------------------
// Fake enqueuer + binding store
// ---------------------------------------------------------------------------

type fakeEnqueuer struct {
	mu   sync.Mutex
	args []chatbot.SubmitArgs
	err  error
}

func (e *fakeEnqueuer) Submit(_ context.Context, args chatbot.SubmitArgs) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.args = append(e.args, args)
	if e.err != nil {
		return "", e.err
	}
	return "job_1", nil
}

func (e *fakeEnqueuer) submitted() []chatbot.SubmitArgs {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]chatbot.SubmitArgs(nil), e.args...)
}

type fakeStore struct{ binding chatbot.Binding }

func (s fakeStore) For(sessionKey string) (chatbot.Binding, bool) {
	if s.binding.ChatID != sessionKey {
		return chatbot.Binding{}, false
	}
	return s.binding, true
}
func (s fakeStore) Set(chatbot.Binding) error { return nil }
func (s fakeStore) Delete(string) error       { return nil }

// ---------------------------------------------------------------------------
// Harness
// ---------------------------------------------------------------------------

const testSessionKey = "agent:main:telegram:direct:1669479669"

func runBot(t *testing.T, bridge *fakeBridge, enq *fakeEnqueuer) {
	t.Helper()
	bridge.done = make(chan struct{})
	if bridge.stopAfter == 0 {
		bridge.stopAfter = 2
	}
	store := fakeStore{binding: chatbot.Binding{ChatID: testSessionKey, Repo: "/repo", Agent: "claude", Mode: "rage"}}
	bot := newBotWithBridge(bridge, store, enq)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = bot.Start(ctx) }()

	select {
	case <-bridge.done:
	case <-time.After(3 * time.Second):
		t.Fatal("bot loop did not reach the stop point")
	}
	// Let the last dispatch settle.
	time.Sleep(50 * time.Millisecond)
}

func userEvent(cursor int64, messageID, text string) Event {
	return Event{
		Cursor:     cursor,
		Type:       "message",
		SessionKey: testSessionKey,
		MessageID:  messageID,
		Role:       "user",
		Text:       text,
	}
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestBotSubmitsUserMessagesAgainstTheBoundRepo(t *testing.T) {
	bridge := &fakeBridge{
		waits: []waitStep{{event: &Event{Cursor: 1}}},
		polls: []pollStep{{events: []Event{userEvent(1, "msg_1", "add a healthcheck endpoint")}, next: 1}},
	}
	enq := &fakeEnqueuer{}

	runBot(t, bridge, enq)

	got := enq.submitted()
	if len(got) != 1 {
		t.Fatalf("got %d submits, want 1", len(got))
	}
	if got[0].Repo != "/repo" || got[0].Agent != "claude" || got[0].Mode != "rage" {
		t.Errorf("submit args = %+v", got[0])
	}
	if !strings.Contains(got[0].Prompt, "add a healthcheck endpoint") {
		t.Errorf("prompt = %q, want it to carry the message text", got[0].Prompt)
	}
}

func TestBotAcksIntoTheSameConversation(t *testing.T) {
	bridge := &fakeBridge{
		waits: []waitStep{{event: &Event{Cursor: 1}}},
		polls: []pollStep{{events: []Event{userEvent(1, "msg_1", "do the thing")}, next: 1}},
	}

	runBot(t, bridge, &fakeEnqueuer{})

	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	if len(bridge.sent) == 0 {
		t.Fatal("bot sent nothing back to the conversation")
	}
	if bridge.sent[0].sessionKey != testSessionKey {
		t.Errorf("ack went to %q, want %q", bridge.sent[0].sessionKey, testSessionKey)
	}
}

func TestBotIgnoresItsOwnAssistantMessages(t *testing.T) {
	assistant := userEvent(1, "msg_1", "Got it — one sec… 👀")
	assistant.Role = "assistant"
	bridge := &fakeBridge{
		waits: []waitStep{{event: &Event{Cursor: 1}}},
		polls: []pollStep{{events: []Event{assistant}, next: 1}},
	}
	enq := &fakeEnqueuer{}

	runBot(t, bridge, enq)

	if got := enq.submitted(); len(got) != 0 {
		t.Errorf("assistant echo must not start a run, got %d submits", len(got))
	}
}

func TestBotIgnoresNonMessageEvents(t *testing.T) {
	approval := Event{Cursor: 1, Type: "exec_approval_requested", SessionKey: testSessionKey}
	bridge := &fakeBridge{
		waits: []waitStep{{event: &Event{Cursor: 1}}},
		polls: []pollStep{{events: []Event{approval}, next: 1}},
	}
	enq := &fakeEnqueuer{}

	runBot(t, bridge, enq)

	if got := enq.submitted(); len(got) != 0 {
		t.Errorf("approval events must not start a run, got %d submits", len(got))
	}
}

func TestBotIgnoresUnboundConversations(t *testing.T) {
	stray := userEvent(1, "msg_1", "hello")
	stray.SessionKey = "agent:main:telegram:direct:someone-else"
	bridge := &fakeBridge{
		waits: []waitStep{{event: &Event{Cursor: 1}}},
		polls: []pollStep{{events: []Event{stray}, next: 1}},
	}
	enq := &fakeEnqueuer{}

	runBot(t, bridge, enq)

	if got := enq.submitted(); len(got) != 0 {
		t.Errorf("unbound conversation must not start a run, got %d submits", len(got))
	}
	for _, text := range bridge.sentTexts() {
		if strings.Contains(text, "one sec") {
			t.Error("must not ack into an unbound conversation")
		}
	}
}

func TestBotAdvancesTheCursorPastHandledEvents(t *testing.T) {
	bridge := &fakeBridge{
		stopAfter: 2,
		waits:     []waitStep{{event: &Event{Cursor: 5}}, {event: &Event{Cursor: 9}}},
		polls: []pollStep{
			{events: []Event{userEvent(5, "msg_1", "one")}, next: 5},
			{events: []Event{userEvent(9, "msg_2", "two")}, next: 9},
		},
	}

	runBot(t, bridge, &fakeEnqueuer{})

	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	if len(bridge.pollCursors) < 2 {
		t.Fatalf("poll cursors = %v, want at least 2 polls", bridge.pollCursors)
	}
	if bridge.pollCursors[0] != 0 {
		t.Errorf("first poll cursor = %d, want 0", bridge.pollCursors[0])
	}
	if bridge.pollCursors[1] != 5 {
		t.Errorf("second poll cursor = %d, want 5 (advanced past the handled batch)", bridge.pollCursors[1])
	}
}

func TestBotKeepsTheCursorWhenPollFails(t *testing.T) {
	bridge := &fakeBridge{
		stopAfter: 2,
		waits:     []waitStep{{event: &Event{Cursor: 5}}, {event: &Event{Cursor: 5}}},
		polls: []pollStep{
			{err: errors.New("bridge hiccup")},
			{events: []Event{userEvent(5, "msg_1", "one")}, next: 5},
		},
	}
	enq := &fakeEnqueuer{}

	runBot(t, bridge, enq)

	bridge.mu.Lock()
	cursors := append([]int64(nil), bridge.pollCursors...)
	bridge.mu.Unlock()
	if len(cursors) < 2 {
		t.Fatalf("poll cursors = %v, want a retry", cursors)
	}
	if cursors[1] != 0 {
		t.Errorf("retry poll cursor = %d, want 0 (a failed poll must not advance the cursor)", cursors[1])
	}
	if got := enq.submitted(); len(got) != 1 {
		t.Errorf("got %d submits, want the retried event to be handled once", len(got))
	}
}

func TestBotSurvivesWaitErrors(t *testing.T) {
	bridge := &fakeBridge{
		stopAfter: 3,
		waits: []waitStep{
			{err: errors.New("bridge died")},
			{event: &Event{Cursor: 1}},
		},
		polls: []pollStep{{events: []Event{userEvent(1, "msg_1", "still here")}, next: 1}},
	}
	enq := &fakeEnqueuer{}

	runBot(t, bridge, enq)

	if got := enq.submitted(); len(got) != 1 {
		t.Errorf("got %d submits, want the loop to recover from a wait error", len(got))
	}
}

func TestBotHandlesEventsWithoutAMessageID(t *testing.T) {
	bridge := &fakeBridge{
		waits: []waitStep{{event: &Event{Cursor: 3}}},
		polls: []pollStep{{events: []Event{userEvent(3, "", "no id here")}, next: 3}},
	}
	enq := &fakeEnqueuer{}

	runBot(t, bridge, enq)

	if got := enq.submitted(); len(got) != 1 {
		t.Errorf("got %d submits, want 1 (a missing messageId must not drop the message)", len(got))
	}
}

// ---------------------------------------------------------------------------
// Sender + context provider
// ---------------------------------------------------------------------------

func TestSenderRepliesThroughTheConversationRoute(t *testing.T) {
	bridge := &fakeBridge{}
	snd := NewSender(bridge)

	if err := snd.Reply(context.Background(), "sess_1", "ignored-root", "all done"); err != nil {
		t.Fatalf("Reply() error = %v", err)
	}

	if len(bridge.sent) != 1 {
		t.Fatalf("got %d sends, want 1", len(bridge.sent))
	}
	if bridge.sent[0].sessionKey != "sess_1" || bridge.sent[0].text != "all done" {
		t.Errorf("sent = %+v", bridge.sent[0])
	}
}

func TestRecentContextRendersTheTranscriptOldestFirst(t *testing.T) {
	bridge := &fakeBridge{read: []TranscriptMessage{
		{Role: "user", Text: "first"},
		{Role: "assistant", Text: "second"},
	}}
	provider := newContextProvider(bridge)

	got := provider.RecentContext(context.Background(), "sess_1", "", "")

	if !strings.Contains(got, "first") || !strings.Contains(got, "second") {
		t.Errorf("context = %q, want both messages", got)
	}
	if strings.Index(got, "first") > strings.Index(got, "second") {
		t.Errorf("context = %q, want oldest first", got)
	}
}

func TestRecentContextIsEmptyWhenTheBridgeFails(t *testing.T) {
	bridge := &fakeBridge{readErr: errors.New("nope")}
	provider := newContextProvider(bridge)

	if got := provider.RecentContext(context.Background(), "sess_1", "", ""); got != "" {
		t.Errorf("context = %q, want \"\" on error", got)
	}
}
