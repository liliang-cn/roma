package tui

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/liliang-cn/roma/internal/agents"
	"github.com/liliang-cn/roma/internal/api"
	"github.com/liliang-cn/roma/internal/queue"
)

type Options struct {
	WorkingDir string
}

type command struct {
	name string
	args []string
	raw  string
}

type model struct {
	workingDir string
	client     *api.Client
	registry   *agents.Registry

	input   textinput.Model
	jobList list.Model

	selectedAgent string
	withAgents    []string
	selectedJobID string

	status  api.StatusResponse
	queue   []queue.Request
	inspect *api.QueueInspectResponse

	width  int
	height int
	ready  bool
	boot   string

	detailViewport viewport.Model
	logViewport    viewport.Model
	help           help.Model
	themeName      string

	messages []string
	helpText []string

	daemonCancel context.CancelFunc
	daemonErrCh  <-chan error
}

func (m model) Init() tea.Cmd {
	return tea.Batch(
		func() tea.Msg { return tea.ClearScreen() },
		m.tickCmd(),
		m.refreshCmd(),
	)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.input.Width = max(20, msg.Width-6)
		m.refreshTheme()
		m.resizeViewports()
		m.syncViewports()
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "esc":
			m.input.SetValue("")
			return m, nil
		case "enter":
			line := strings.TrimSpace(m.input.Value())
			if line == "" {
				return m, nil
			}
			m.input.SetValue("")
			return m, m.commandCmd(line)
		}
		var cmd tea.Cmd
		var listCmd tea.Cmd
		var detailCmd tea.Cmd
		var logCmd tea.Cmd
		if strings.TrimSpace(m.input.Value()) == "" {
			m.jobList, listCmd = m.jobList.Update(msg)
			if selected, ok := m.jobList.SelectedItem().(jobItem); ok && selected.id != "" && selected.id != m.selectedJobID {
				m.selectedJobID = selected.id
				return m, tea.Batch(listCmd, m.refreshCmd())
			}
		}
		m.input, cmd = m.input.Update(msg)
		m.detailViewport, detailCmd = m.detailViewport.Update(msg)
		m.logViewport, logCmd = m.logViewport.Update(msg)
		return m, tea.Batch(cmd, listCmd, detailCmd, logCmd)

	case tickMsg:
		select {
		case err := <-m.daemonErrCh:
			return m, func() tea.Msg { return daemonErrMsg{err: err} }
		default:
		}
		return m, tea.Batch(m.tickCmd(), m.refreshCmd())

	case daemonErrMsg:
		if msg.err == nil || errors.Is(msg.err, context.Canceled) {
			return m, nil
		}
		m.boot = "daemon error: " + msg.err.Error()
		m.appendMessage(m.boot)
		return m, nil

	case snapshotMsg:
		m.ready = true
		m.boot = ""
		m.dropMessagePrefix("waiting for embedded romad")
		if !m.hasMessage("embedded romad ready") {
			m.appendMessage("embedded romad ready")
		}
		m.status = msg.snapshot.status
		m.queue = msg.snapshot.queue
		if m.selectedJobID == "" {
			m.selectedJobID = preferredJobID(m.queue)
		}
		if m.selectedJobID != "" && msg.snapshot.resp != nil && msg.snapshot.resp.Job.ID == m.selectedJobID {
			m.inspect = msg.snapshot.resp
		}
		if m.selectedJobID == "" && len(m.queue) > 0 {
			m.selectedJobID = m.queue[0].ID
		}
		m.syncViewports()
		return m, nil

	case commandMsg:
		if msg.err != nil {
			m.appendMessage("error: " + msg.err.Error())
			return m, nil
		}
		if msg.agentID != "" {
			m.selectedAgent = msg.agentID
		}
		if msg.withIDs != nil {
			m.withAgents = slices.Clone(msg.withIDs)
		}
		if msg.themeName != "" {
			m.themeName = msg.themeName
			m.refreshTheme()
		}
		if msg.text != "" {
			if !m.ready {
				m.boot = msg.text
			}
			m.appendMessage(msg.text)
			m.syncViewports()
		}
		if msg.selectJob && msg.jobID != "" {
			m.selectedJobID = msg.jobID
			m.syncViewports()
			return m, tea.Batch(m.refreshCmd())
		}
		if msg.quit {
			return m, tea.Quit
		}
		m.syncViewports()
		return m, nil
	}
	var cmd tea.Cmd
	m.detailViewport, cmd = m.detailViewport.Update(msg)
	m.logViewport, _ = m.logViewport.Update(msg)
	return m, cmd
}

func (m model) View() string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}
	ld := computeLayout(m.width, m.height)

	if !m.ready {
		lines := []string{m.bootTitleStyle().Render("ROMA TUI"), "", m.boot}
		if len(m.messages) > 0 {
			lines = append(lines, "")
			lines = append(lines, m.messages...)
		}
		return m.appStyle().Width(ld.appW).Render(strings.Join(lines, "\n"))
	}

	header := m.renderHeader()
	body := lipgloss.JoinHorizontal(
		lipgloss.Top,
		m.jobList.View(),
		" ",
		m.panelStyle().Width(ld.rightPanelW).Render(m.detailViewport.View()),
	)
	logs := m.panelStyle().Width(ld.logPanelW).Render(m.logViewport.View())
	input := m.inputPanelStyle().Width(ld.inputPanelW).Render(m.renderInput())
	footer := m.footerHintStyle().Width(ld.footerW).Render(m.help.ShortHelpView(m.shortHelp()))

	return m.appStyle().Width(ld.appW).Render(lipgloss.JoinVertical(
		lipgloss.Left,
		header,
		body,
		logs,
		input,
		footer,
	))
}
