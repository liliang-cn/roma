package chatbot

import (
	"context"
	"os"
	"strings"
)

const helpText = "Commands:\n" +
	"/help — show this help\n" +
	"/status — show this chat's repo binding\n" +
	"/bind <repo-path> — link this chat to a repo\n" +
	"/agent <id> — set the default agent for this chat\n" +
	"/route <@name> <agent-id> — send \"@name …\" to that agent\n" +
	"/route rm <@name> — remove a route\n" +
	"/route — list routes\n" +
	"/mode <rage|collab|senate> — set the run mode\n" +
	"/unbind — unlink this chat"

// Command parses and executes a config command for chatID and returns the reply
// text. `text` may be the full "/bind /repo" form (from @mention) or the
// slash-remainder "bind /repo" form (from a native Slack slash command). It
// never enqueues a run.
func (h *Handler) Command(ctx context.Context, chatID, text string) string {
	text = strings.TrimSpace(text)
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return "Unknown command. Try /help."
	}
	word := fields[0]
	arg := strings.TrimSpace(strings.TrimPrefix(text, word))
	cmd := strings.ToLower(strings.TrimPrefix(word, "/"))

	switch cmd {
	case "help":
		return helpText
	case "status":
		return h.cmdStatus(chatID)
	case "bind":
		return h.cmdBind(chatID, arg)
	case "agent":
		return h.cmdAgent(chatID, arg)
	case "route", "routes":
		return h.cmdRoute(chatID, arg)
	case "mode":
		return h.cmdMode(chatID, arg)
	case "unbind":
		return h.cmdUnbind(chatID)
	default:
		return "Unknown command. Try /help."
	}
}

func (h *Handler) cmdStatus(chatID string) string {
	b, ok := h.store.For(chatID)
	if !ok {
		return "This chat isn't linked yet. Use /bind <repo-path> to link it."
	}
	agent := b.Agent
	if agent == "" {
		agent = "(default)"
	}
	mode := b.Mode
	if mode == "" {
		mode = "rage"
	}
	out := "📍 repo: " + b.Repo + "\nagent: " + agent + "\nmode: " + mode
	if len(b.Routes) > 0 {
		out += "\nroutes:"
		for _, mention := range b.Routes.Mentions() {
			out += "\n  @" + mention + " → " + b.Routes[mention]
		}
	}
	return out
}

func (h *Handler) cmdRoute(chatID, arg string) string {
	b, ok := h.store.For(chatID)
	if !ok {
		return "Bind a repo first: /bind <repo-path>."
	}
	fields := strings.Fields(strings.TrimSpace(arg))

	if len(fields) == 0 {
		if len(b.Routes) == 0 {
			return "No routes yet. Add one with /route <@name> <agent-id>."
		}
		out := "Routes:"
		for _, mention := range b.Routes.Mentions() {
			out += "\n@" + mention + " → " + b.Routes[mention]
		}
		return out
	}

	if strings.EqualFold(fields[0], "rm") || strings.EqualFold(fields[0], "remove") {
		if len(fields) < 2 {
			return "Usage: /route rm <@name>"
		}
		mention := NormalizeMention(fields[1])
		if !b.Routes.Delete(mention) {
			return "No route for @" + mention + "."
		}
		if err := h.store.Set(b); err != nil {
			return "Failed to save: " + err.Error()
		}
		return "✅ Removed route @" + mention + "."
	}

	if len(fields) < 2 {
		return "Usage: /route <@name> <agent-id>"
	}
	mention := NormalizeMention(fields[0])
	if mention == "" {
		return "Usage: /route <@name> <agent-id>"
	}
	agent := strings.TrimSpace(fields[1])
	b.Routes = b.Routes.Set(mention, agent)
	if err := h.store.Set(b); err != nil {
		return "Failed to save: " + err.Error()
	}
	return "✅ @" + mention + " → " + agent + "."
}

func (h *Handler) cmdBind(chatID, path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return "Usage: /bind <repo-path>"
	}
	info, err := os.Stat(path)
	if err != nil {
		return "Can't use that path: " + err.Error()
	}
	if !info.IsDir() {
		return "Not a directory: " + path
	}
	b, _ := h.store.For(chatID)
	b.ChatID = chatID
	b.Repo = path
	if err := h.store.Set(b); err != nil {
		return "Failed to save: " + err.Error()
	}
	return "✅ Linked this chat to " + path + "."
}

func (h *Handler) cmdAgent(chatID, id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return "Usage: /agent <id>"
	}
	b, ok := h.store.For(chatID)
	if !ok {
		return "Bind a repo first: /bind <repo-path>."
	}
	b.Agent = id
	if err := h.store.Set(b); err != nil {
		return "Failed to save: " + err.Error()
	}
	return "✅ Agent set to " + id + "."
}

func (h *Handler) cmdMode(chatID, m string) string {
	m = strings.ToLower(strings.TrimSpace(m))
	switch m {
	case "rage", "collab", "senate":
	default:
		return "Usage: /mode <rage|collab|senate>"
	}
	b, ok := h.store.For(chatID)
	if !ok {
		return "Bind a repo first: /bind <repo-path>."
	}
	b.Mode = m
	if err := h.store.Set(b); err != nil {
		return "Failed to save: " + err.Error()
	}
	return "✅ Mode set to " + m + "."
}

func (h *Handler) cmdUnbind(chatID string) string {
	if _, ok := h.store.For(chatID); !ok {
		return "This chat wasn't linked."
	}
	if err := h.store.Delete(chatID); err != nil {
		return "Failed to unlink: " + err.Error()
	}
	return "✅ Unlinked this chat."
}
