package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/key"
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

func (m model) transcriptPrefixStyle(kind transcriptKind) lipgloss.Style {
	style := lipgloss.NewStyle().Bold(true)
	switch kind {
	case transcriptUser:
		return style.Foreground(m.palette().accentCool)
	case transcriptOutput:
		return style.Foreground(m.palette().muted)
	default:
		return style.Foreground(m.palette().accentWarm)
	}
}

func (m model) transcriptTextStyle(kind transcriptKind) lipgloss.Style {
	switch kind {
	case transcriptOutput:
		return lipgloss.NewStyle().Foreground(m.palette().text)
	case transcriptUser:
		return lipgloss.NewStyle().Foreground(m.palette().textSoft)
	default:
		return lipgloss.NewStyle().Foreground(m.palette().text)
	}
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
	m.commandList.SetDelegate(newCommandListDelegate(p))
}

func (m model) shortHelp() []key.Binding {
	return []key.Binding{
		key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "commands")),
		key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "complete")),
		key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "send")),
		key.NewBinding(key.WithKeys("↑/↓"), key.WithHelp("↑/↓", "select")),
		key.NewBinding(key.WithKeys("pgup/pgdn"), key.WithHelp("pgup/pgdn", "scroll")),
		key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "clear")),
		key.NewBinding(key.WithKeys("ctrl+c"), key.WithHelp("ctrl+c", "quit")),
	}
}
