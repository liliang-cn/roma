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

func (m model) panelStyle() lipgloss.Style {
	return lipgloss.NewStyle().BorderStyle(lipgloss.RoundedBorder()).BorderForeground(m.palette().panelBorder).Background(m.palette().panelBg).Foreground(m.palette().text).Padding(0, 1)
}

func (m model) headerPanelStyle() lipgloss.Style {
	return lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(m.palette().panelBorder).
		Background(m.palette().panelBg).
		Foreground(m.palette().text).
		Padding(1, 2)
}

func (m model) inputPanelStyle() lipgloss.Style {
	return lipgloss.NewStyle().BorderStyle(lipgloss.RoundedBorder()).BorderForeground(m.palette().inputBorder).Background(m.palette().panelBg).Padding(0, 1)
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

func (m model) queueItemStyle() lipgloss.Style {
	return lipgloss.NewStyle().Padding(0, 1)
}

func (m model) queueActiveStyle() lipgloss.Style {
	return lipgloss.NewStyle().Padding(0, 1).Background(m.palette().selectedBg).BorderLeft(true).BorderForeground(m.palette().accentCool)
}

func (m model) queueMutedStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(m.palette().muted)
}

func (m model) bootTitleStyle() lipgloss.Style {
	return lipgloss.NewStyle().Bold(true).Foreground(m.palette().accentCool)
}

func (m model) bootFrameStyle() lipgloss.Style {
	return lipgloss.NewStyle().BorderStyle(lipgloss.DoubleBorder()).BorderForeground(m.palette().accent).Background(m.palette().panelBg).Padding(1, 2)
}

func (m model) footerHintStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(m.palette().muted)
}

func (m model) appStyle() lipgloss.Style {
	return lipgloss.NewStyle().Background(m.palette().appBg).Foreground(m.palette().text).Padding(0, 1)
}

func (m *model) refreshTheme() {
	p := m.palette()
	m.input.PromptStyle = lipgloss.NewStyle().Foreground(p.accentCool).Bold(true)
	m.input.TextStyle = lipgloss.NewStyle().Foreground(p.text)
	m.input.PlaceholderStyle = lipgloss.NewStyle().Foreground(p.muted)
	m.input.Cursor.Style = lipgloss.NewStyle().Foreground(p.chipText).Background(p.accent)
	m.detailViewport.Style = lipgloss.NewStyle().Background(p.panelBg).Foreground(p.text)
	m.logViewport.Style = lipgloss.NewStyle().Background(p.panelBg).Foreground(p.textSoft)
	m.jobList.SetDelegate(newJobListDelegate(p))
	listStyles := list.DefaultStyles()
	listStyles.Title = listStyles.Title.Foreground(p.accentCool).Bold(true)
	listStyles.TitleBar = listStyles.TitleBar.Background(p.panelBg).Foreground(p.accentCool)
	listStyles.NoItems = listStyles.NoItems.Foreground(p.muted)
	listStyles.HelpStyle = listStyles.HelpStyle.Foreground(p.muted)
	m.jobList.Styles = listStyles
}

func (m model) shortHelp() []key.Binding {
	return []key.Binding{
		key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "send")),
		key.NewBinding(key.WithKeys("↑/↓"), key.WithHelp("↑/↓", "select")),
		key.NewBinding(key.WithKeys("pgup/pgdn"), key.WithHelp("pgup/pgdn", "scroll")),
		key.NewBinding(key.WithKeys("/help"), key.WithHelp("/help", "commands")),
		key.NewBinding(key.WithKeys("ctrl+c"), key.WithHelp("ctrl+c", "quit")),
	}
}
