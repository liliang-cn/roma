package chatbot

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"
)

// SubmitArgs are the platform-agnostic inputs to a run submission.
type SubmitArgs struct {
	Repo   string
	Prompt string
	Agent  string
	Mode   string
}

// Enqueuer submits a run and returns a job id to track its progress.
type Enqueuer interface {
	Submit(ctx context.Context, args SubmitArgs) (jobID string, err error)
}

// ProgressFunc streams progress for a job back into the chat thread rooted at
// rootMessageID in chatID. It is run in its own goroutine and must not block the
// caller.
type ProgressFunc func(jobID, chatID, rootMessageID string)

// ContextProvider returns a short transcript of recent messages for extra
// context, or "" if unavailable. When threadID is set, it should return that
// thread's transcript; otherwise recent channel messages. Best-effort: errors -> "".
type ContextProvider interface {
	RecentContext(ctx context.Context, chatID, threadID, messageID string) string
}

// Handler turns an @mention in a bound group chat into a TagIt run and acks it.
// Once engaged in a thread it also picks up follow-up replies in that thread
// without requiring another @mention.
type Handler struct {
	store    BindingStore
	enq      Enqueuer
	snd      Sender
	progress ProgressFunc
	ctxProv  ContextProvider

	mu      sync.Mutex
	seen    map[string]struct{}
	engaged map[string]struct{} // thread roots the bot is actively conversing in
}

// NewHandler wires the handler with its dependencies. progress may be nil.
func NewHandler(store BindingStore, enq Enqueuer, snd Sender, progress ProgressFunc) *Handler {
	return &Handler{
		store:    store,
		enq:      enq,
		snd:      snd,
		progress: progress,
		seen:     make(map[string]struct{}),
		engaged:  make(map[string]struct{}),
	}
}

// SetContextProvider sets an optional provider used to prepend recent chat
// context to task prompts. nil-safe.
func (h *Handler) SetContextProvider(p ContextProvider) {
	h.ctxProv = p
}

// Handle processes one incoming message: best-effort, never returns an error.
//
// It acts when the bot is @mentioned, or when the message is a follow-up reply
// in a thread the bot is already engaged in (so users can keep chatting without
// re-mentioning). Messages from bots (including our own replies) are ignored to
// avoid loops.
func (h *Handler) Handle(ctx context.Context, msg IncomingMessage) {
	if !msg.IsGroup || msg.Text == "" || msg.FromBot {
		return
	}
	active := msg.Mentioned || (msg.ThreadID != "" && h.threadActive(msg.ThreadID))
	if !active {
		return
	}
	if !h.markSeen(msg.MessageID) {
		return
	}

	root := msg.ReplyRoot()

	if strings.HasPrefix(strings.TrimSpace(msg.Text), "/") {
		reply := h.Command(ctx, msg.ChatID, msg.Text)
		h.reply(ctx, msg.ChatID, root, reply)
		return
	}

	binding, ok := h.store.For(msg.ChatID)
	if !ok {
		h.reply(ctx, msg.ChatID, root, "This chat isn't linked to a repo yet. Use /bind <repo-path> to link it.")
		return
	}

	// "@dev-agent fix the login bug" runs on the agent bound to dev-agent;
	// anything unrouted runs on the channel's default agent.
	agent, task := ResolveAgent(binding, msg.Text)
	if strings.TrimSpace(task) == "" {
		h.reply(ctx, msg.ChatID, root, "That's just a name — tell me what to do after it.")
		return
	}

	// Engage this thread so later replies in it continue the conversation
	// without another @mention.
	h.engageThread(root)

	// Neutral ack: this fires before the run layer decides whether the message
	// is real work or just conversation, so it must read naturally before either
	// a quick answer or streamed work progress.
	h.reply(ctx, msg.ChatID, root, "Got it — one sec… 👀")

	mode := binding.Mode
	if mode == "" {
		mode = "rage"
	}

	prompt := task
	if h.ctxProv != nil {
		cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		cxt := h.ctxProv.RecentContext(cctx, msg.ChatID, msg.ThreadID, msg.MessageID)
		cancel()
		prompt = composePrompt(cxt, task)
	}

	jobID, err := h.enq.Submit(ctx, SubmitArgs{
		Repo:   binding.Repo,
		Prompt: prompt,
		Agent:  agent,
		Mode:   mode,
	})
	if err != nil {
		h.reply(ctx, msg.ChatID, root, "Failed to start: "+err.Error())
		return
	}

	if h.progress != nil {
		go h.progress(jobID, msg.ChatID, root)
	}
}

// threadActive reports whether the bot is engaged in the given thread root.
func (h *Handler) threadActive(threadID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	_, ok := h.engaged[threadID]
	return ok
}

// engageThread marks a thread root as one the bot is conversing in.
func (h *Handler) engageThread(threadID string) {
	if threadID == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.engaged[threadID] = struct{}{}
}

// markSeen records the message id and reports whether it is new (not a duplicate).
func (h *Handler) markSeen(messageID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.seen[messageID]; ok {
		return false
	}
	h.seen[messageID] = struct{}{}
	return true
}

// composePrompt prepends recent conversation context to the task prompt.
func composePrompt(contextText, task string) string {
	if strings.TrimSpace(contextText) == "" {
		return task
	}
	return "Recent conversation in this chat (for context, latest last):\n" +
		contextText +
		"\n\n---\nThe latest request to act on:\n" + task
}

func (h *Handler) reply(ctx context.Context, chatID, rootMessageID, text string) {
	if err := h.snd.Reply(ctx, chatID, rootMessageID, text); err != nil {
		log.Printf("chatbot: reply failed: %v", err)
	}
}
