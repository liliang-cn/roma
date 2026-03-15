package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

func (m model) renderMain() string {
	if len(m.transcript) == 0 {
		return strings.Join([]string{
			m.titleStyle().Render("ROMA"),
			m.subtitleStyle().Render("Type a prompt below. Use / to open commands."),
			m.helpLineStyle().Render(
				fmt.Sprintf("agent %s • with %s • queue %d", fallbackAgent(m.selectedAgent), fallbackWith(m.withAgents), m.status.QueueItems),
			),
		}, "\n")
	}

	lines := make([]string, 0, len(m.transcript))
	for _, entry := range m.transcript {
		lines = append(lines, m.renderTranscriptEntry(entry))
	}
	return strings.Join(lines, "\n")
}

func (m model) renderTranscriptEntry(entry transcriptEntry) string {
	text := entry.text
	if strings.TrimSpace(entry.label) != "" {
		prefix := m.transcriptPrefixStyle(entry.kind).Render(entry.label + " > ")
		text = prefix + m.transcriptTextStyle(entry.kind).Render(entry.text)
	} else {
		text = m.transcriptTextStyle(entry.kind).Render(entry.text)
	}
	return lipgloss.NewStyle().Width(max(1, m.layoutDims().mainW)).Render(text)
}

func (m model) renderInput() string {
	lines := []string{
		m.helpLineStyle().Render(
			fmt.Sprintf("/ for commands • agent %s • with %s", fallbackAgent(m.selectedAgent), fallbackWith(m.withAgents)),
		),
	}
	if m.commandMenuVisible() {
		lines = append(lines, "", m.renderCommandSuggestions())
	}
	lines = append(lines, "", m.input.View())
	return strings.Join(lines, "\n")
}

func (m model) renderCommandSuggestions() string {
	items := m.commandList.Items()
	if len(items) == 0 {
		return ""
	}
	selected := m.commandList.Index()
	lines := make([]string, 0, len(items))
	for i, raw := range items {
		item := raw.(commandItem)
		prefix := "  "
		lineStyle := m.helpLineStyle().Copy().Italic(false)
		if i == selected {
			prefix = m.titleStyle().Render("> ")
			lineStyle = m.valueStyle()
		}
		lines = append(lines, prefix+lineStyle.Render(item.insert+"  "+m.queueMutedStyle().Render(item.description)))
	}
	return strings.Join(lines, "\n")
}
