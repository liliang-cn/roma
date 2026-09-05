package chatbot

import (
	"regexp"
	"sort"
	"strings"
)

// leadingMentionRe matches an "@name" token at the very start of a message.
//
// Only a leading token counts as routing. An "@" further in is prose — a
// filename, an email address, someone thanking a colleague — and treating that
// as a route would silently hand the task to the wrong agent.
var leadingMentionRe = regexp.MustCompile(`^\s*@([A-Za-z0-9][A-Za-z0-9_.\-]*)\s*`)

// SplitLeadingMention splits a leading "@name" off text, returning the name
// without its "@" and the remaining text. ok is false when there is none.
//
// This works on the text every adapter already produces. Feishu strips its
// "@_user_N" placeholders and Slack strips the leading "<@U…>" token, both of
// which are the *platform* mention of the TagIt bot itself; a plain "@dev-agent"
// typed as ordinary text survives both, and OpenClaw has no mention syntax at
// all. So routing needs no per-platform support and no extra bot users: you
// @mention TagIt the normal way, then name the agent you want.
func SplitLeadingMention(text string) (mention, rest string, ok bool) {
	loc := leadingMentionRe.FindStringSubmatchIndex(text)
	if loc == nil {
		return "", text, false
	}
	return text[loc[2]:loc[3]], text[loc[1]:], true
}

// Routes maps a mention name to an agent id, e.g. "dev-agent" -> "claude".
// Keys are stored lowercased; lookups are case-insensitive.
type Routes map[string]string

// NormalizeMention lowercases a mention and drops a leading "@" so that
// "@Dev-Agent", "Dev-Agent" and "dev-agent" are one key.
func NormalizeMention(mention string) string {
	return strings.ToLower(strings.TrimPrefix(strings.TrimSpace(mention), "@"))
}

// Lookup resolves a mention to an agent id.
func (r Routes) Lookup(mention string) (string, bool) {
	if len(r) == 0 {
		return "", false
	}
	agent, ok := r[NormalizeMention(mention)]
	return agent, ok
}

// Set adds or replaces a route. It returns a non-nil map so a zero-valued
// Binding can be routed without the caller allocating first.
func (r Routes) Set(mention, agent string) Routes {
	if r == nil {
		r = make(Routes, 1)
	}
	r[NormalizeMention(mention)] = strings.TrimSpace(agent)
	return r
}

// Delete removes a route and reports whether it existed.
func (r Routes) Delete(mention string) bool {
	key := NormalizeMention(mention)
	if _, ok := r[key]; !ok {
		return false
	}
	delete(r, key)
	return true
}

// Mentions returns the configured mention names, sorted, for stable listings.
func (r Routes) Mentions() []string {
	out := make([]string, 0, len(r))
	for mention := range r {
		out = append(out, mention)
	}
	sort.Strings(out)
	return out
}

// ResolveAgent picks the agent for a message and returns the prompt with a
// matched routing token removed.
//
// An unmatched "@name" is left in the prompt untouched: it is far more likely
// to be a human being addressed than a typo'd route, and quietly deleting a
// word from someone's request is worse than passing it through.
func ResolveAgent(b Binding, text string) (agent, prompt string) {
	mention, rest, ok := SplitLeadingMention(text)
	if !ok {
		return b.Agent, text
	}
	routed, found := b.Routes.Lookup(mention)
	if !found {
		return b.Agent, text
	}
	return routed, rest
}
