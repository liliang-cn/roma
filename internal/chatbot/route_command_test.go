package chatbot

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// bindChat links a chat to a temp repo so route commands have something to
// attach to.
func bindChat(t *testing.T, h *Handler, snd *fakeSender, chatID string) {
	t.Helper()
	if got := runCmd(t, h, snd, chatID, "/bind "+t.TempDir()); !strings.Contains(got, "✅") {
		t.Fatalf("/bind failed: %q", got)
	}
}

func TestCommandRouteAddListRemove(t *testing.T) {
	snd := &fakeSender{}
	store := newFakeStore()
	h := NewHandler(store, &fakeEnqueuer{}, snd, noopProgress)
	bindChat(t, h, snd, "c1")

	if got := runCmd(t, h, snd, "c1", "/route"); !strings.Contains(got, "No routes yet") {
		t.Fatalf("empty listing = %q", got)
	}

	if got := runCmd(t, h, snd, "c1", "/route @dev-agent codex"); !strings.Contains(got, "@dev-agent") {
		t.Fatalf("/route add = %q", got)
	}
	// The "@" is optional on input.
	if got := runCmd(t, h, snd, "c1", "/route qa-agent gemini"); !strings.Contains(got, "@qa-agent") {
		t.Fatalf("/route add without @ = %q", got)
	}

	b, ok := store.For("c1")
	if !ok {
		t.Fatal("binding vanished")
	}
	if agent, _ := b.Routes.Lookup("dev-agent"); agent != "codex" {
		t.Fatalf("routes = %+v", b.Routes)
	}

	listing := runCmd(t, h, snd, "c1", "/route")
	for _, want := range []string{"@dev-agent", "codex", "@qa-agent", "gemini"} {
		if !strings.Contains(listing, want) {
			t.Fatalf("listing %q missing %q", listing, want)
		}
	}

	if got := runCmd(t, h, snd, "c1", "/route rm @dev-agent"); !strings.Contains(got, "Removed") {
		t.Fatalf("/route rm = %q", got)
	}
	if got := runCmd(t, h, snd, "c1", "/route rm @dev-agent"); !strings.Contains(got, "No route") {
		t.Fatalf("second /route rm = %q", got)
	}
}

func TestCommandRouteUsageAndUnbound(t *testing.T) {
	snd := &fakeSender{}
	h := NewHandler(newFakeStore(), &fakeEnqueuer{}, snd, noopProgress)

	if got := runCmd(t, h, snd, "c1", "/route @dev codex"); !strings.Contains(got, "/bind") {
		t.Fatalf("route before bind = %q", got)
	}

	bindChat(t, h, snd, "c1")
	if got := runCmd(t, h, snd, "c1", "/route @dev"); !strings.Contains(got, "Usage") {
		t.Fatalf("missing agent id = %q", got)
	}
	if got := runCmd(t, h, snd, "c1", "/route rm"); !strings.Contains(got, "Usage") {
		t.Fatalf("rm without name = %q", got)
	}
}

func TestCommandStatusShowsRoutes(t *testing.T) {
	snd := &fakeSender{}
	h := NewHandler(newFakeStore(), &fakeEnqueuer{}, snd, noopProgress)
	bindChat(t, h, snd, "c1")
	runCmd(t, h, snd, "c1", "/route @dev-agent codex")

	got := runCmd(t, h, snd, "c1", "/status")
	if !strings.Contains(got, "@dev-agent") || !strings.Contains(got, "codex") {
		t.Fatalf("/status = %q", got)
	}
}

func TestCommandHelpMentionsRoute(t *testing.T) {
	snd := &fakeSender{}
	h := NewHandler(newFakeStore(), &fakeEnqueuer{}, snd, noopProgress)
	if got := runCmd(t, h, snd, "c1", "/help"); !strings.Contains(got, "/route") {
		t.Fatalf("/help missing /route: %q", got)
	}
}

// lastSubmit returns the most recent Submit args, failing if there were none.
func lastSubmit(t *testing.T, enq *fakeEnqueuer) SubmitArgs {
	t.Helper()
	enq.mu.Lock()
	defer enq.mu.Unlock()
	if len(enq.args) == 0 {
		t.Fatal("nothing was submitted")
	}
	return enq.args[len(enq.args)-1]
}

var routeMsgSeq int

// send delivers a normal (non-command) mention and returns the submitted args.
func send(t *testing.T, h *Handler, chatID, text string) IncomingMessage {
	t.Helper()
	routeMsgSeq++
	msg := IncomingMessage{
		MessageID: fmt.Sprintf("route-msg-%d", routeMsgSeq),
		ChatID:    chatID,
		Text:      text,
		Mentioned: true,
		IsGroup:   true,
	}
	h.Handle(context.Background(), msg)
	return msg
}

func TestHandleRoutesMentionToItsAgent(t *testing.T) {
	snd := &fakeSender{}
	store := newFakeStore()
	enq := &fakeEnqueuer{}
	h := NewHandler(store, enq, snd, noopProgress)
	bindChat(t, h, snd, "c1")
	runCmd(t, h, snd, "c1", "/agent claude")
	runCmd(t, h, snd, "c1", "/route @dev-agent codex")
	runCmd(t, h, snd, "c1", "/route @qa-agent gemini")

	send(t, h, "c1", "@qa-agent run the suite")
	args := lastSubmit(t, enq)
	if args.Agent != "gemini" {
		t.Fatalf("agent = %q, want gemini", args.Agent)
	}
	// The routing token is consumed, not passed to the agent as part of the task.
	if args.Prompt != "run the suite" {
		t.Fatalf("prompt = %q", args.Prompt)
	}

	send(t, h, "c1", "just fix it")
	if args := lastSubmit(t, enq); args.Agent != "claude" || args.Prompt != "just fix it" {
		t.Fatalf("default route = %+v", args)
	}

	send(t, h, "c1", "@bob can you look")
	if args := lastSubmit(t, enq); args.Agent != "claude" || args.Prompt != "@bob can you look" {
		t.Fatalf("unrouted mention = %+v", args)
	}
}

func TestHandleMentionWithoutTaskDoesNotEnqueue(t *testing.T) {
	snd := &fakeSender{}
	enq := &fakeEnqueuer{}
	h := NewHandler(newFakeStore(), enq, snd, noopProgress)
	bindChat(t, h, snd, "c1")
	runCmd(t, h, snd, "c1", "/route @dev-agent codex")

	before := enq.count()
	send(t, h, "c1", "@dev-agent")
	if enq.count() != before {
		t.Fatal("a bare route token must not start a run")
	}
	replies := snd.all()
	if last := replies[len(replies)-1].text; !strings.Contains(last, "tell me what to do") {
		t.Fatalf("reply = %q", last)
	}
}
