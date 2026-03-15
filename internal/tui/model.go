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

	input       textinput.Model
	commandList list.Model

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
	help           help.Model
	themeName      string

	transcript []transcriptEntry
	stream     streamState
	messages   []string
	helpText   []string

	mainContent string

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
		m.refreshTheme()
		m.resizeViewports()
		m.syncViewports()
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "pgup":
			m.detailViewport.HalfViewUp()
			return m, nil
		case "pgdown":
			m.detailViewport.HalfViewDown()
			return m, nil
		case "esc":
			if m.commandMenuVisible() {
				m.input.SetValue("")
				m.syncCommandList()
				m.syncViewports()
				return m, nil
			}
			return m, nil
		}

		if !m.input.Focused() {
			m.input.Focus()
		}
		if m.input.Focused() {
			switch msg.String() {
			case "up", "ctrl+p":
				if m.commandMenuVisible() {
					var cmd tea.Cmd
					m.commandList, cmd = m.commandList.Update(msg)
					return m, cmd
				}
			case "down", "ctrl+n":
				if m.commandMenuVisible() {
					var cmd tea.Cmd
					m.commandList, cmd = m.commandList.Update(msg)
					return m, cmd
				}
			case "tab":
				if m.commandMenuVisible() {
					m.acceptCommandSuggestion()
					return m, nil
				}
			case "enter":
				if m.shouldCompleteCommand() {
					m.acceptCommandSuggestion()
					return m, nil
				}
				line := strings.TrimSpace(m.input.Value())
				if line == "" {
					return m, nil
				}
				m.appendUser(line)
				m.input.SetValue("")
				m.input.Focus()
				m.syncCommandList()
				m.syncViewports()
				return m, m.commandCmd(line)
			}
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			m.syncCommandList()
			m.syncViewports()
			return m, cmd
		}

		var cmd tea.Cmd
		m.detailViewport, cmd = m.detailViewport.Update(msg)
		return m, cmd

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
		if !m.hasTranscript(transcriptSystem, "ROMA", "embedded romad ready") {
			m.appendSystem("embedded romad ready")
		}
		m.status = msg.snapshot.status
		m.queue = msg.snapshot.queue
		if m.selectedJobID != "" && msg.snapshot.resp != nil && msg.snapshot.resp.Job.ID == m.selectedJobID {
			m.inspect = msg.snapshot.resp
			m.consumeInspect(msg.snapshot.resp)
		}
		m.syncViewports()
		return m, nil

	case commandMsg:
		if msg.err != nil {
			if m.ready {
				m.appendSystem("error: " + msg.err.Error())
			} else {
				m.appendMessage("error: " + msg.err.Error())
			}
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
				m.appendMessage(msg.text)
			} else {
				m.appendSystem(msg.text)
			}
			m.syncViewports()
		}
		if msg.selectJob && msg.jobID != "" {
			if m.selectedJobID != msg.jobID {
				m.resetStream(msg.jobID)
			}
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
	return m, cmd
}

func (m model) View() string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}
	ld := m.layoutDims()

	if !m.ready {
		lines := []string{m.bootTitleStyle().Render("ROMA TUI"), "", m.boot}
		if len(m.messages) > 0 {
			lines = append(lines, "")
			lines = append(lines, m.messages...)
		}
		return m.appStyle().Width(ld.appW).Render(strings.Join(lines, "\n"))
	}

	body := lipgloss.NewStyle().Width(ld.mainW).Render(m.detailViewport.View())
	input := m.inputPanelStyle().Width(ld.inputPanelW).Render(m.renderInput())
	footer := m.footerHintStyle().Width(ld.footerW).Render(m.help.ShortHelpView(m.shortHelp()))

	return m.appStyle().Width(ld.appW).Render(lipgloss.JoinVertical(
		lipgloss.Left,
		body,
		input,
		footer,
	))
}

func (m *model) focusInput(initial string) {
	m.input.Focus()
	if initial != "" && strings.TrimSpace(m.input.Value()) == "" {
		m.input.SetValue(initial)
	}
	m.syncCommandList()
	m.syncViewports()
}

func (m *model) blurInput() {
	m.input.Blur()
	m.input.SetValue("")
	m.commandList.SetItems(nil)
	m.syncViewports()
}

func (m *model) syncCommandList() {
	ld := m.layoutDims()
	items := filterCommandItems(m.commandQuery())
	if !m.input.Focused() || !m.commandMenuActive() {
		items = nil
	}
	m.commandList.SetItems(items)
	menuH := min(6, max(0, len(items)))
	if menuH > 0 {
		menuH += 2
	}
	m.commandList.SetSize(max(24, ld.inputPanelW-4), menuH)
	if len(items) > 0 {
		m.commandList.Select(0)
	}
	m.input.Width = max(20, ld.inputPanelW-6)
}

func (m model) commandQuery() string {
	value := strings.TrimLeft(m.input.Value(), " \t")
	if !m.commandMenuActive() {
		return ""
	}
	value = strings.TrimPrefix(value, "/")
	return strings.TrimSpace(value)
}

func (m model) commandMenuVisible() bool {
	return m.input.Focused() && len(m.commandList.Items()) > 0 && m.commandMenuActive()
}

func (m model) commandMenuActive() bool {
	value := strings.TrimLeft(m.input.Value(), " \t")
	if !strings.HasPrefix(value, "/") {
		return false
	}
	value = strings.TrimPrefix(value, "/")
	return !strings.ContainsAny(value, " \t")
}

func (m model) selectedCommandSuggestion() (commandItem, bool) {
	item, ok := m.commandList.SelectedItem().(commandItem)
	return item, ok
}

func (m model) shouldCompleteCommand() bool {
	item, ok := m.selectedCommandSuggestion()
	if !ok {
		return false
	}
	current := strings.TrimSpace(m.input.Value())
	if !strings.HasPrefix(current, "/") {
		return false
	}
	if strings.Contains(strings.TrimPrefix(current, "/"), " ") {
		return false
	}
	return strings.TrimSpace(item.insert) != current
}

func (m *model) acceptCommandSuggestion() {
	item, ok := m.selectedCommandSuggestion()
	if !ok {
		return
	}
	m.input.SetValue(item.insert)
	m.syncCommandList()
	m.syncViewports()
}
