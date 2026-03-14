package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/lipgloss"
)

func (m model) palette() themePalette {
	if strings.EqualFold(m.themeName, "dark") {
		return darkPalette
	}
	return lightPalette
}

func (m model) titleStyle() lipgloss.Style {
	return lipgloss.NewStyle().Bold(true).Foreground(m.palette().accentCool)
}

func (m model) subtitleStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(m.palette().muted)
}

func (m model) panelTitleStyle() lipgloss.Style {
	return lipgloss.NewStyle().Bold(true).Foreground(m.palette().accentCool)
}

func (m model) inputPanelStyle() lipgloss.Style {
	return lipgloss.NewStyle().Padding(0, 0)
}

func (m model) labelStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(m.palette().muted)
}

func (m model) valueStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(m.palette().text)
}

func (m model) logLineStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(m.palette().textSoft)
}

func (m model) helpLineStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(m.palette().muted).Italic(true)
}

func (m model) queueMutedStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(m.palette().muted)
}

func (m model) bootTitleStyle() lipgloss.Style {
	return lipgloss.NewStyle().Bold(true).Foreground(m.palette().accentCool)
}

func (m model) footerHintStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(m.palette().muted)
}

func (m model) appStyle() lipgloss.Style {
	return lipgloss.NewStyle().Background(m.palette().appBg).Foreground(m.palette().text).Padding(1, 1, 0, 1)
}

func (m *model) refreshTheme() {
	p := m.palette()
	m.input.PromptStyle = lipgloss.NewStyle().Foreground(p.accentCool).Bold(true)
	m.input.TextStyle = lipgloss.NewStyle().Foreground(p.text)
	m.input.PlaceholderStyle = lipgloss.NewStyle().Foreground(p.muted)
	m.input.Cursor.Style = lipgloss.NewStyle().Foreground(p.chipText).Background(p.accent)
	m.input.Cursor.TextStyle = lipgloss.NewStyle().Foreground(p.chipText).Background(p.accent)
	m.detailViewport.Style = lipgloss.NewStyle().Foreground(p.text)
	m.jobList.SetDelegate(newJobListDelegate(p))
	m.commandList.SetDelegate(newCommandListDelegate(p))
	listStyles := list.DefaultStyles()
	listStyles.Title = listStyles.Title.Foreground(p.accentCool).Bold(true)
	listStyles.TitleBar = listStyles.TitleBar.Foreground(p.accentCool)
	listStyles.NoItems = listStyles.NoItems.Foreground(p.muted)
	listStyles.HelpStyle = listStyles.HelpStyle.Foreground(p.muted)
	listStyles.FilterPrompt = listStyles.FilterPrompt.Foreground(p.accentCool)
	listStyles.FilterCursor = listStyles.FilterCursor.Foreground(p.chipText).Background(p.accent)
	listStyles.ActivePaginationDot = listStyles.ActivePaginationDot.Foreground(p.accentCool)
	listStyles.InactivePaginationDot = listStyles.InactivePaginationDot.Foreground(p.muted)
	listStyles.StatusBar = listStyles.StatusBar.Foreground(p.muted)
	listStyles.StatusEmpty = listStyles.StatusEmpty.Foreground(p.muted)
	listStyles.DividerDot = listStyles.DividerDot.Foreground(p.panelBorder)
	m.jobList.Styles = listStyles
	m.commandList.Styles = listStyles
}

func (m model) shortHelp() []key.Binding {
	return []key.Binding{
		key.NewBinding(key.WithKeys("i"), key.WithHelp("i", "input")),
		key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "commands")),
		key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "complete")),
		key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "send")),
		key.NewBinding(key.WithKeys("↑/↓"), key.WithHelp("↑/↓", "select")),
		key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "browse")),
		key.NewBinding(key.WithKeys("ctrl+c"), key.WithHelp("ctrl+c", "quit")),
	}
}
