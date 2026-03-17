package tui

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/liliang-cn/roma/internal/api"
	"github.com/liliang-cn/roma/internal/artifacts"
	"github.com/liliang-cn/roma/internal/domain"
	"github.com/liliang-cn/roma/internal/events"
)

func (m model) tickCmd() tea.Cmd {
	delay := 2 * time.Second
	if !m.ready {
		delay = 250 * time.Millisecond
	}
	return tea.Tick(delay, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m model) refreshCmd() tea.Cmd {
	selectedJobID := m.selectedJobID
	client := m.client
	return func() tea.Msg {
		if !client.Available() {
			return commandMsg{text: "waiting for embedded romad..."}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		status, err := client.Status(ctx)
		if err != nil {
			return commandMsg{err: err}
		}
		items, err := client.QueueList(ctx)
		if err != nil {
			return commandMsg{err: err}
		}
		sortQueue(items)
		var resp *api.QueueInspectResponse
		if selectedJobID != "" {
			inspect, err := client.QueueInspect(ctx, selectedJobID, true)
			if err == nil {
				resp = &inspect
			}
		}
		return snapshotMsg{snapshot: snapshot{status: status, queue: items, resp: resp}}
	}
}

func (m model) commandCmd(line string) tea.Cmd {
	cmd, err := parseCommand(line)
	if err != nil {
		return func() tea.Msg { return commandMsg{err: err} }
	}
	switch cmd.name {
	case "help":
		return func() tea.Msg { return commandMsg{text: strings.Join(m.helpText, "  ")} }
	case "status":
		return func() tea.Msg {
			return commandMsg{text: fmt.Sprintf("queue=%d sessions=%d artifacts=%d events=%d", m.status.QueueItems, m.status.Sessions, m.status.Artifacts, m.status.Events)}
		}
	case "jobs":
		if len(m.queue) == 0 {
			return func() tea.Msg { return commandMsg{text: "no jobs"} }
		}
		var lines []string
		for i, item := range m.queue {
			if i >= 10 {
				break
			}
			summary := compactQueueSummary(item)
			lines = append(lines, fmt.Sprintf("%s %s %s", item.Status, item.ID, summary))
		}
		return func() tea.Msg { return commandMsg{text: strings.Join(lines, "\n")} }
	case "theme":
		if len(cmd.args) == 0 {
			return func() tea.Msg { return commandMsg{text: "theme is " + m.themeName} }
		}
		theme := strings.ToLower(strings.TrimSpace(cmd.args[0]))
		if theme != "light" && theme != "dark" {
			return func() tea.Msg { return commandMsg{err: fmt.Errorf("usage: /theme <light|dark>")} }
		}
		return func() tea.Msg {
			return commandMsg{text: "theme set to " + theme, themeName: theme}
		}
	case "agent":
		if len(cmd.args) == 0 || (len(cmd.args) == 1 && cmd.args[0] == "list") {
			ids := make([]string, 0)
			for _, profile := range m.registry.WithResolvedAvailability(context.Background()) {
				ids = append(ids, fmt.Sprintf("%s(%s)", profile.ID, profile.Availability))
			}
			if len(ids) == 0 {
				return func() tea.Msg { return commandMsg{text: "no agents configured; use /agent add <id> <path>"} }
			}
			return func() tea.Msg { return commandMsg{text: "agents: " + strings.Join(ids, ", ")} }
		}
		if len(cmd.args) >= 3 && cmd.args[0] == "add" {
			agentID := strings.TrimSpace(cmd.args[1])
			path := strings.TrimSpace(cmd.args[2])
			if agentID == "" || path == "" {
				return func() tea.Msg { return commandMsg{err: fmt.Errorf("usage: /agent add <id> <path>")} }
			}
			registry := m.registry
			return func() tea.Msg {
				profile := domain.AgentProfile{
					ID:                 agentID,
					DisplayName:        agentID,
					Command:            path,
					Availability:       domain.AgentAvailabilityPlanned,
					SupportsMCP:        false,
					SupportsJSONOutput: false,
				}
				if err := registry.Add(profile); err != nil {
					return commandMsg{err: err}
				}
				if err := registry.SaveUserConfig(); err != nil {
					return commandMsg{err: err}
				}
				resolved, _ := registry.Get(agentID)
				return commandMsg{
					text:    fmt.Sprintf("agent added: %s -> %s", agentID, path),
					agentID: resolved.ID,
				}
			}
		}
		if len(cmd.args) >= 2 && cmd.args[0] == "use" {
			cmd.args = cmd.args[1:]
		}
		agentID := cmd.args[len(cmd.args)-1]
		if _, ok := m.registry.Get(agentID); !ok {
			return func() tea.Msg { return commandMsg{err: fmt.Errorf("unknown agent %s", agentID)} }
		}
		return func() tea.Msg { return commandMsg{text: "agent set to " + agentID, agentID: agentID} }
	case "with":
		parts := splitCSV(strings.Join(cmd.args, " "))
		return func() tea.Msg { return commandMsg{text: "with set to " + strings.Join(parts, ","), withIDs: parts} }
	case "open":
		if len(cmd.args) == 0 {
			return func() tea.Msg { return commandMsg{err: fmt.Errorf("usage: /open <job_id>")} }
		}
		return func() tea.Msg { return commandMsg{text: "opened " + cmd.args[0], jobID: cmd.args[0], selectJob: true} }
	case "run", "submit":
		prompt := strings.TrimSpace(strings.Join(cmd.args, " "))
		if prompt == "" {
			return func() tea.Msg { return commandMsg{err: fmt.Errorf("prompt is required")} }
		}
		if strings.TrimSpace(m.selectedAgent) == "" {
			return func() tea.Msg {
				return commandMsg{err: fmt.Errorf("no agent selected; use /agent add <id> <path> first")}
			}
		}
		return m.submitCmd(prompt)
	case "cancel":
		jobID := m.selectedJobID
		if len(cmd.args) > 0 {
			jobID = cmd.args[0]
		}
		if jobID == "" {
			return func() tea.Msg { return commandMsg{err: fmt.Errorf("no job selected")} }
		}
		return m.cancelCmd(jobID)
	case "result":
		sessionID := ""
		if len(cmd.args) > 0 {
			sessionID = cmd.args[0]
		} else if m.inspect != nil && m.inspect.Job.SessionID != "" {
			sessionID = m.inspect.Job.SessionID
		}
		if sessionID == "" {
			return func() tea.Msg { return commandMsg{err: fmt.Errorf("no session selected")} }
		}
		return m.resultCmd(sessionID)
	case "refresh":
		return tea.Batch(m.refreshCmd(), func() tea.Msg { return commandMsg{text: "refreshed"} })
	case "quit", "exit", "q":
		return func() tea.Msg { return commandMsg{quit: true} }
	default:
		return func() tea.Msg { return commandMsg{err: fmt.Errorf("unknown command /%s", cmd.name)} }
	}
}

func (m model) submitCmd(prompt string) tea.Cmd {
	client := m.client
	workingDir := m.workingDir
	starter := m.selectedAgent
	withAgents := slices.Clone(m.withAgents)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		resp, err := client.Submit(ctx, api.SubmitRequest{
			Prompt:       prompt,
			StarterAgent: starter,
			Delegates:    withAgents,
			WorkingDir:   workingDir,
		})
		if err != nil {
			return commandMsg{err: err}
		}
		return commandMsg{
			text:      fmt.Sprintf("submitted %s via %s with=%s", resp.JobID, starter, strings.Join(withAgents, ",")),
			jobID:     resp.JobID,
			selectJob: true,
		}
	}
}

func (m model) cancelCmd(jobID string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		item, err := client.QueueCancel(ctx, jobID)
		if err != nil {
			return commandMsg{err: err}
		}
		return commandMsg{text: fmt.Sprintf("cancelled %s -> %s", jobID, item.Status), jobID: jobID, selectJob: true}
	}
}

// streamNextEventCmd waits for the next event from ch and returns a streamEventMsg or streamDoneMsg.
func streamNextEventCmd(ch <-chan events.Record, jobID string) tea.Cmd {
	return func() tea.Msg {
		record, ok := <-ch
		if !ok {
			return streamDoneMsg{jobID: jobID}
		}
		return streamEventMsg{jobID: jobID, record: record}
	}
}

// beginStreamCmd starts the StreamJobEvents goroutine and waits for the first event.
func (m model) beginStreamCmd(jobID string) tea.Cmd {
	ch := m.stream.ch
	ctx := m.stream.ctx
	client := m.client
	return func() tea.Msg {
		go func() {
			defer close(ch)
			_ = client.StreamJobEvents(ctx, jobID, ch)
		}()
		return streamNextEventCmd(ch, jobID)()
	}
}

func (m model) resultCmd(sessionID string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		resp, err := client.ResultShow(ctx, sessionID)
		if err != nil {
			return commandMsg{err: err}
		}
		if resp.Pending {
			return commandMsg{text: fmt.Sprintf("session %s: %s", sessionID, resp.Message)}
		}
		if payload, ok := artifacts.FinalAnswerFromEnvelope(resp.Artifact); ok {
			text := fmt.Sprintf("session %s: %s", sessionID, payload.Summary)
			if strings.TrimSpace(payload.Answer) != "" {
				text += "\n" + strings.TrimSpace(payload.Answer)
			}
			return commandMsg{text: text}
		}
		return commandMsg{text: fmt.Sprintf("session %s: %s", sessionID, resp.Artifact.Kind)}
	}
}
