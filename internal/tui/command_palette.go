package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/list"
)

type focusTarget uint8

const (
	focusQueue focusTarget = iota
	focusInput
)

type commandItem struct {
	title       string
	description string
	insert      string
}

func (i commandItem) Title() string       { return i.title }
func (i commandItem) Description() string { return i.description }
func (i commandItem) FilterValue() string { return i.title + " " + i.description + " " + i.insert }

var commandCatalog = []commandItem{
	{title: "/help", description: "show command reference", insert: "/help"},
	{title: "/status", description: "show control-plane summary", insert: "/status"},
	{title: "/theme light", description: "switch to light theme", insert: "/theme light"},
	{title: "/theme dark", description: "switch to dark theme", insert: "/theme dark"},
	{title: "/agent list", description: "list configured agents", insert: "/agent list"},
	{title: "/agent add ", description: "add a user-provided agent", insert: "/agent add "},
	{title: "/agent ", description: "select an agent by id", insert: "/agent "},
	{title: "/with ", description: "set extra participating agents", insert: "/with "},
	{title: "/run ", description: "run a prompt now", insert: "/run "},
	{title: "/submit ", description: "submit a background job", insert: "/submit "},
	{title: "/open ", description: "open a job by id", insert: "/open "},
	{title: "/cancel ", description: "cancel a job", insert: "/cancel "},
	{title: "/result ", description: "show session result", insert: "/result "},
	{title: "/refresh", description: "refresh queue and status", insert: "/refresh"},
	{title: "/quit", description: "quit the TUI", insert: "/quit"},
}

func newCommandListDelegate(p themePalette) list.DefaultDelegate {
	delegate := list.NewDefaultDelegate()
	delegate.SetHeight(2)
	delegate.ShowDescription = true
	styles := list.NewDefaultItemStyles()
	styles.NormalTitle = styles.NormalTitle.Foreground(p.text).Bold(true)
	styles.NormalDesc = styles.NormalDesc.Foreground(p.muted)
	styles.SelectedTitle = styles.SelectedTitle.Foreground(p.accentCool).Bold(true)
	styles.SelectedDesc = styles.SelectedDesc.Foreground(p.textSoft)
	styles.FilterMatch = styles.FilterMatch.Foreground(p.accentWarm).Bold(true)
	delegate.Styles = styles
	return delegate
}

func filterCommandItems(query string) []list.Item {
	query = strings.ToLower(strings.TrimSpace(query))
	items := make([]list.Item, 0, len(commandCatalog))
	for _, item := range commandCatalog {
		if query == "" ||
			strings.Contains(strings.ToLower(item.title), query) ||
			strings.Contains(strings.ToLower(item.description), query) {
			items = append(items, item)
		}
	}
	return items
}
