package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/liliang-cn/roma/internal/queue"
)

func (m model) renderMain() string {
	sections := []string{m.renderHeader()}
	if queueSection := m.renderQueue(); queueSection != "" {
		sections = append(sections, "", queueSection)
	}
	if m.inspect != nil && m.inspect.Job.ID != "" {
		sections = append(sections, "", m.renderDetail())
	}
	if logSection := m.renderMessages(); logSection != "" {
		sections = append(sections, "", logSection)
	}
	return strings.Join(sections, "\n\n")
}

func (m model) renderHeader() string {
	lines := []string{
		m.titleStyle().Render("ROMA"),
		m.subtitleStyle().Render("local multi-agent orchestration"),
		m.helpLineStyle().Render(
			fmt.Sprintf(
				"agent %s • with %s • theme %s • queue %d • session %d • artifact %d • event %d",
				fallbackAgent(m.selectedAgent),
				fallbackWith(m.withAgents),
				m.themeName,
				m.status.QueueItems,
				m.status.Sessions,
				m.status.Artifacts,
				m.status.Events,
			),
		),
	}
	return strings.Join(lines, "\n")
}

func (m model) renderQueue() string {
	lines := []string{m.sectionTitle("Recent Jobs")}
	if len(m.queue) == 0 {
		lines = append(lines, m.queueMutedStyle().Render("No jobs yet. Use /run <prompt> or /submit <prompt>."))
		return strings.Join(lines, "\n")
	}
	for i, item := range m.queue {
		if i >= 8 {
			lines = append(lines, m.queueMutedStyle().Render(fmt.Sprintf("... %d more job(s)", len(m.queue)-i)))
			break
		}
		lines = append(lines, m.renderQueueItem(item))
	}
	return strings.Join(lines, "\n")
}

func (m model) renderDetail() string {
	if m.inspect == nil || m.inspect.Job.ID == "" {
		return m.sectionTitle("Details") + "\n\n" + m.queueMutedStyle().Render("Use /open <job_id> to inspect a job.")
	}
	lines := []string{
		m.sectionTitle("Details"),
		m.kvLine("job", m.inspect.Job.ID),
		m.kvLine("status", string(m.inspect.Job.Status)),
	}
	if m.inspect.Job.SessionID != "" {
		lines = append(lines, m.kvLine("session", m.inspect.Job.SessionID))
	}
	if m.inspect.Live != nil {
		live := m.inspect.Live
		lines = append(lines, "", m.sectionTitle("Live"))
		if live.State != "" {
			lines = append(lines, m.kvLine("state", live.State))
		}
		if live.Phase != "" {
			lines = append(lines, m.kvLine("phase", live.Phase))
		}
		if live.CurrentRound > 0 {
			lines = append(lines, m.kvLine("round", fmt.Sprintf("%d", live.CurrentRound)))
		}
		if live.ParticipantCount > 0 {
			lines = append(lines, m.kvLine("agents", fmt.Sprintf("%d", live.ParticipantCount)))
		}
		if live.CurrentTaskID != "" {
			lines = append(lines, m.kvLine("task", live.CurrentTaskID))
		}
		if live.CurrentAgentID != "" {
			lines = append(lines, m.kvLine("agent", live.CurrentAgentID))
		}
		if live.ProcessPID > 0 {
			lines = append(lines, m.kvLine("pid", fmt.Sprintf("%d", live.ProcessPID)))
		}
		if live.WorkspacePath != "" {
			lines = append(lines, m.kvLine("workspace", trimLine(live.WorkspacePath, 120)))
		}
		if live.LastOutputPreview != "" {
			lines = append(lines, m.kvLine("output", trimLine(live.LastOutputPreview, 120)))
		}
	}
	if m.inspect.Semantic != nil {
		lines = append(lines, "", m.sectionTitle("Semantic"))
		if m.inspect.Semantic.Intent != "" {
			lines = append(lines, m.kvLine("intent", m.inspect.Semantic.Intent))
		}
		if m.inspect.Semantic.Summary != "" {
			lines = append(lines, m.kvLine("summary", trimLine(m.inspect.Semantic.Summary, 120)))
		}
		if m.inspect.Semantic.NeedsApproval {
			lines = append(lines, m.kvLine("needs_approval", "true"))
		}
		if m.inspect.Semantic.RecommendCuria {
			lines = append(lines, m.kvLine("recommend_curia", "true"))
		}
	}
	if m.inspect.Curia != nil {
		lines = append(lines, "", m.sectionTitle("Curia"))
		lines = append(lines, m.kvLine("mode", m.inspect.Curia.WinningMode))
		if m.inspect.Curia.DisputeClass != "" {
			lines = append(lines, m.kvLine("dispute", m.inspect.Curia.DisputeClass))
		}
		if m.inspect.Curia.ArbitrationStrategy != "" {
			lines = append(lines, m.kvLine("strategy", m.inspect.Curia.ArbitrationStrategy))
		}
	}
	lines = append(lines, "", m.sectionTitle("Artifacts"))
	lines = append(lines, m.kvLine("counts", fmt.Sprintf("%d artifact(s) • %d event(s)", m.inspect.ArtifactCount, m.inspect.EventCount)))
	return strings.Join(lines, "\n")
}

func (m model) renderMessages() string {
	lines := []string{m.sectionTitle("Command Log")}
	if len(m.messages) == 0 {
		lines = append(lines, m.helpLineStyle().Render("Type /help to list commands."))
	} else {
		start := max(0, len(m.messages)-12)
		for _, line := range m.messages[start:] {
			lines = append(lines, m.logLineStyle().Render("• "+line))
		}
	}
	return strings.Join(lines, "\n")
}

func (m model) renderInput() string {
	title := m.panelTitleStyle().Render("Command")
	help := m.helpLineStyle().Render("Press i to type. Start with / to open the command menu.")
	lines := []string{title, help}
	if m.commandMenuVisible() {
		lines = append(lines, "", m.renderCommandSuggestions())
	}
	if m.input.Focused() {
		lines = append(lines, "", m.input.View())
	} else {
		lines = append(lines, "", m.helpLineStyle().Render("Press i or / to focus input."))
	}
	return strings.Join(lines, "\n")
}

func (m model) renderQueueItem(item queue.Request) string {
	target := fallbackAgent(item.StarterAgent)
	withAgents := fallbackWith(item.Delegates)
	prefix := "  "
	if item.ID == m.selectedJobID {
		prefix = m.titleStyle().Render("> ")
	}
	headline := prefix + m.statusChip(item.Status) + " " + m.valueStyle().Render(trimLine(item.ID, 32))
	meta := prefix + m.labelStyle().Render("starter ") + m.valueStyle().Render(target) + m.queueMutedStyle().Render(" • ") + m.labelStyle().Render("with ") + m.valueStyle().Render(withAgents)
	summary := prefix + m.queueMutedStyle().Render(trimLine(compactQueueSummary(item), 80))
	return strings.Join([]string{headline, meta, summary}, "\n")
}

func (m model) statusChip(status queue.Status) string {
	color := m.palette().muted
	switch status {
	case queue.StatusRunning:
		color = m.palette().accentCool
	case queue.StatusSucceeded:
		color = m.palette().success
	case queue.StatusAwaitingApproval:
		color = m.palette().warning
	case queue.StatusFailed, queue.StatusRejected, queue.StatusCancelled:
		color = m.palette().danger
	}
	return lipgloss.NewStyle().Bold(true).Foreground(color).Render(strings.ToUpper(string(status)))
}

func (m model) sectionTitle(title string) string {
	return lipgloss.NewStyle().Bold(true).Foreground(m.palette().accentWarm).Render(title)
}

func (m model) kvLine(label, value string) string {
	return m.labelStyle().Render(label+": ") + m.valueStyle().Render(value)
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
