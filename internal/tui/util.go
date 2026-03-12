package tui

import (
	"fmt"
	"slices"
	"strings"

	"github.com/liliang-cn/roma/internal/queue"
)

func parseCommand(line string) (command, error) {
	raw := strings.TrimSpace(line)
	if raw == "" {
		return command{}, fmt.Errorf("empty command")
	}
	if !strings.HasPrefix(raw, "/") {
		return command{name: "run", args: []string{raw}, raw: raw}, nil
	}
	fields := strings.Fields(strings.TrimPrefix(raw, "/"))
	if len(fields) == 0 {
		return command{}, fmt.Errorf("empty command")
	}
	name := strings.ToLower(fields[0])
	args := []string{}
	if len(fields) > 1 {
		args = fields[1:]
	}
	return command{name: name, args: args, raw: raw}, nil
}

func sortQueue(items []queue.Request) {
	slices.SortFunc(items, func(a, b queue.Request) int {
		switch {
		case a.Status == queue.StatusRunning && b.Status != queue.StatusRunning:
			return -1
		case b.Status == queue.StatusRunning && a.Status != queue.StatusRunning:
			return 1
		case a.CreatedAt.After(b.CreatedAt):
			return -1
		case b.CreatedAt.After(a.CreatedAt):
			return 1
		default:
			return strings.Compare(a.ID, b.ID)
		}
	})
}

func preferredJobID(items []queue.Request) string {
	for _, item := range items {
		if item.Status == queue.StatusRunning {
			return item.ID
		}
	}
	if len(items) == 0 {
		return ""
	}
	return items[0].ID
}

func splitCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func compactQueueSummary(item queue.Request) string {
	summary := strings.TrimSpace(item.Error)
	if summary == "" {
		summary = string(item.Status)
	}
	return trimLine(summary, 48)
}

func trimLine(text string, maxLen int) string {
	text = strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	if maxLen <= 0 || len(text) <= maxLen {
		return text
	}
	if maxLen <= 3 {
		return text[:maxLen]
	}
	return text[:maxLen-3] + "..."
}

func fallbackAgent(agent string) string {
	agent = strings.TrimSpace(agent)
	if agent == "" {
		return "(unset)"
	}
	return agent
}

func fallbackWith(items []string) string {
	if len(items) == 0 {
		return "(none)"
	}
	return strings.Join(items, ",")
}

func (m *model) appendMessage(line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	m.messages = append(m.messages, line)
	if len(m.messages) > 200 {
		m.messages = m.messages[len(m.messages)-200:]
	}
}

func (m *model) hasMessage(target string) bool {
	target = strings.TrimSpace(target)
	if target == "" {
		return false
	}
	for _, line := range m.messages {
		if strings.TrimSpace(line) == target {
			return true
		}
	}
	return false
}

func (m *model) dropMessagePrefix(prefix string) {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" || len(m.messages) == 0 {
		return
	}
	filtered := m.messages[:0]
	for _, line := range m.messages {
		if strings.HasPrefix(strings.TrimSpace(line), prefix) {
			continue
		}
		filtered = append(filtered, line)
	}
	m.messages = filtered
}

func (m *model) selectOffset(delta int) {
	if len(m.queue) == 0 {
		return
	}
	index := 0
	for i, item := range m.queue {
		if item.ID == m.selectedJobID {
			index = i
			break
		}
	}
	index += delta
	if index < 0 {
		index = 0
	}
	if index >= len(m.queue) {
		index = len(m.queue) - 1
	}
	m.selectedJobID = m.queue[index].ID
}
