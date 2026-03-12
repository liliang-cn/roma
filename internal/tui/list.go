package tui

import "github.com/charmbracelet/bubbles/list"

type jobItem struct {
	id          string
	title       string
	description string
}

func (i jobItem) Title() string       { return i.title }
func (i jobItem) Description() string { return i.description }
func (i jobItem) FilterValue() string { return i.id + " " + i.title + " " + i.description }

func newJobListDelegate(p themePalette) list.DefaultDelegate {
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
