package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/liliang-cn/roma/internal/queue"
)

func (m model) renderHeader() string {
	brand := lipgloss.NewStyle().
		Bold(true).
		Foreground(m.palette().accentCool).
		Padding(0, 0, 0, 0).
		Render("ROMA")
	meta := lipgloss.JoinHorizontal(lipgloss.Left,
		m.labelStyle().Copy().Foreground(m.palette().accentCool).Render("agent "),
		m.valueStyle().Render(fallbackAgent(m.selectedAgent)),
		m.queueMutedStyle().Render("  •  "),
		m.labelStyle().Copy().Foreground(m.palette().accentWarm).Render("with "),
		m.valueStyle().Render(fallbackWith(m.withAgents)),
		m.queueMutedStyle().Render("  •  "),
		m.labelStyle().Copy().Foreground(m.palette().accent).Render("theme "),
		m.valueStyle().Render(m.themeName),
	)
	stats := lipgloss.JoinHorizontal(lipgloss.Left,
		m.infoChip("queue", fmt.Sprintf("%d", m.status.QueueItems), m.palette().accent),
		" ",
		m.infoChip("session", fmt.Sprintf("%d", m.status.Sessions), m.palette().success),
		" ",
		m.infoChip("artifact", fmt.Sprintf("%d", m.status.Artifacts), m.palette().warning),
		" ",
		m.infoChip("event", fmt.Sprintf("%d", m.status.Events), m.palette().danger),
	)
	subtitle := m.subtitleStyle().Render("embedded daemon • local multi-agent orchestration")
	content := lipgloss.JoinVertical(lipgloss.Left, brand, subtitle, meta, stats)
	return lipgloss.NewStyle().Padding(0, 0, 1, 0).Render(content)
}

func (m model) renderQueue() string {
	lines := []string{m.panelTitleStyle().Render("Queue")}
	if len(m.queue) == 0 {
		lines = append(lines, "")
		lines = append(lines, m.queueMutedStyle().Render("No jobs yet. Use /run <prompt> or /submit <prompt>."))
		return strings.Join(lines, "\n")
	}
	for _, item := range m.queue {
		lines = append(lines, "")
		lines = append(lines, m.renderQueueItem(item))
	}
	return strings.Join(lines, "\n")
}

func (m model) renderDetail() string {
	if m.inspect == nil || m.inspect.Job.ID == "" {
		return m.panelTitleStyle().Render("Details") + "\n\n" + m.queueMutedStyle().Render("Select a job on the left to inspect its live state, semantic summary, Curia outcome, and artifacts.")
	}
	lines := []string{
		m.panelTitleStyle().Render("Details"),
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
	lines := []string{m.panelTitleStyle().Render("Command Log")}
	if len(m.messages) == 0 {
		lines = append(lines, "", m.helpLineStyle().Render("Type /help to list commands. Plain text runs the current agent."))
	} else {
		lines = append(lines, "")
		for _, line := range m.messages {
			lines = append(lines, m.logLineStyle().Render("• "+line))
		}
	}
	return strings.Join(lines, "\n")
}

func (m model) renderInput() string {
	title := m.panelTitleStyle().Render("Command")
	help := m.helpLineStyle().Render("Use /run, /submit, /agent, /with, /open, /cancel, /result, /theme")
	return strings.Join([]string{title, help, "", m.input.View()}, "\n")
}

func (m model) renderQueueItem(item queue.Request) string {
	target := fallbackAgent(item.StarterAgent)
	withAgents := fallbackWith(item.Delegates)
	lines := []string{
		lipgloss.JoinHorizontal(lipgloss.Center, m.statusChip(item.Status), " ", m.valueStyle().Render(trimLine(item.ID, 28))),
		lipgloss.JoinHorizontal(lipgloss.Center,
			m.labelStyle().Render("starter "), m.valueStyle().Render(target),
			"  ",
			m.labelStyle().Render("with "), m.valueStyle().Render(withAgents),
		),
		m.queueMutedStyle().Render(trimLine(compactQueueSummary(item), 52)),
	}
	style := m.queueItemStyle()
	if item.ID == m.selectedJobID {
		style = m.queueActiveStyle()
	}
	ld := computeLayout(m.width, m.height)
	return style.Width(max(18, ld.listW-4)).Render(strings.Join(lines, "\n"))
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
	return lipgloss.NewStyle().
		Bold(true).
		Foreground(m.palette().chipText).
		Background(color).
		Padding(0, 1).
		Render(strings.ToUpper(string(status)))
}

func (m model) infoChip(label, value string, color lipgloss.Color) string {
	return lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(color).
		Foreground(m.palette().text).
		Padding(0, 1).
		Render(m.labelStyle().Copy().Foreground(color).Render(label) + " " + m.valueStyle().Render(value))
}

func (m model) sectionTitle(title string) string {
	return lipgloss.NewStyle().Bold(true).Foreground(m.palette().accentWarm).Render(title)
}

func (m model) kvLine(label, value string) string {
	return m.labelStyle().Render(label+": ") + m.valueStyle().Render(value)
}
