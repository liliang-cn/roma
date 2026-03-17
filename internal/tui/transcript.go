package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/liliang-cn/roma/internal/api"
	"github.com/liliang-cn/roma/internal/events"
	"github.com/liliang-cn/roma/internal/queue"
)

func newStreamState(jobID string) streamState {
	return streamState{
		jobID:        strings.TrimSpace(jobID),
		seenEventIDs: map[string]struct{}{},
	}
}

func (m *model) resetStream(jobID string) {
	if m.stream.cancel != nil {
		m.stream.cancel()
	}
	ch := make(chan events.Record, 64)
	ctx, cancel := context.WithCancel(context.Background())
	m.selectedJobID = strings.TrimSpace(jobID)
	m.stream = streamState{
		jobID:        strings.TrimSpace(jobID),
		seenEventIDs: map[string]struct{}{},
		ch:           ch,
		cancel:       cancel,
		ctx:          ctx,
	}
}

// consumeStreamEvent processes a single streamed event record, deduplicating against seenEventIDs.
func (m *model) consumeStreamEvent(record events.Record) {
	if _, ok := m.stream.seenEventIDs[record.ID]; ok {
		return
	}
	m.stream.seenEventIDs[record.ID] = struct{}{}
	for _, entry := range formatEventEntries(record) {
		switch entry.kind {
		case transcriptOutput:
			m.appendOutput(entry.label, entry.text)
		default:
			m.appendTranscript(entry.kind, entry.label, entry.text)
		}
	}
}

func (m *model) appendSystem(text string) {
	m.appendTranscript(transcriptSystem, "ROMA", text)
}

func (m *model) appendUser(text string) {
	m.appendTranscript(transcriptUser, "You", text)
}

func (m *model) appendOutput(agent, text string) {
	label := strings.TrimSpace(agent)
	if label == "" {
		label = "Agent"
	}
	m.appendTranscript(transcriptOutput, label, text)
}

func (m *model) appendTranscript(kind transcriptKind, label, text string) {
	lines := splitTranscriptLines(kind, text)
	for _, line := range lines {
		entry := transcriptEntry{
			kind:  kind,
			label: strings.TrimSpace(label),
			text:  line,
		}
		if len(m.transcript) > 0 {
			last := m.transcript[len(m.transcript)-1]
			if last.kind == entry.kind && last.label == entry.label && last.text == entry.text {
				continue
			}
		}
		m.transcript = append(m.transcript, entry)
	}
	if len(m.transcript) > 400 {
		m.transcript = m.transcript[len(m.transcript)-400:]
	}
}

func (m *model) hasTranscript(kind transcriptKind, label, text string) bool {
	label = strings.TrimSpace(label)
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	for _, entry := range m.transcript {
		if entry.kind == kind && strings.TrimSpace(entry.label) == label && strings.TrimSpace(entry.text) == text {
			return true
		}
	}
	return false
}

func splitTranscriptLines(kind transcriptKind, text string) []string {
	raw := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	lines := make([]string, 0, len(raw))
	for _, line := range raw {
		switch kind {
		case transcriptOutput:
			line = strings.TrimRight(line, "\r\t ")
			if strings.TrimSpace(line) == "" {
				continue
			}
		default:
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
		}
		lines = append(lines, line)
	}
	return lines
}

func (m *model) consumeInspect(resp *api.QueueInspectResponse) {
	if resp == nil || strings.TrimSpace(resp.Job.ID) == "" {
		return
	}
	if m.stream.jobID != resp.Job.ID {
		m.stream = newStreamState(resp.Job.ID)
	}
	for _, entry := range inspectTranscriptEntries(resp.Events, m.stream.seenEventIDs) {
		switch entry.kind {
		case transcriptOutput:
			m.appendOutput(entry.label, entry.text)
		default:
			m.appendTranscript(entry.kind, entry.label, entry.text)
		}
	}

	prev := m.stream.lastStatus
	if resp.Job.Status != prev {
		if line := formatJobStatusLine(prev, resp.Job); line != "" {
			m.appendSystem(line)
		}
		m.stream.lastStatus = resp.Job.Status
	}
}

func inspectTranscriptEntries(records []events.Record, seen map[string]struct{}) []transcriptEntry {
	if seen == nil {
		seen = map[string]struct{}{}
	}
	lines := make([]transcriptEntry, 0)
	for _, record := range records {
		if _, ok := seen[record.ID]; ok {
			continue
		}
		seen[record.ID] = struct{}{}
		lines = append(lines, formatEventEntries(record)...)
	}
	return lines
}

func formatEventEntries(record events.Record) []transcriptEntry {
	switch record.Type {
	case events.TypeRuntimeStdoutCaptured:
		return formatOutputEntries(record)
	case events.TypeRelayNodeStarted:
		return []transcriptEntry{{
			kind:  transcriptSystem,
			label: "ROMA",
			text:  fmt.Sprintf("task %s started with %s", fallbackTask(record.TaskID), fallbackAgent(payloadString(record.Payload, "agent"))),
		}}
	case events.TypeRelayNodeCompleted:
		text := fmt.Sprintf("task %s completed", fallbackTask(record.TaskID))
		if artifactID := strings.TrimSpace(payloadString(record.Payload, "artifact_id")); artifactID != "" {
			text += " -> " + artifactID
		}
		return []transcriptEntry{{kind: transcriptSystem, label: "ROMA", text: text}}
	case events.TypeWorkspacePrepared:
		dir := strings.TrimSpace(payloadString(record.Payload, "effective_dir"))
		if dir == "" {
			dir = strings.TrimSpace(payloadString(record.Payload, "workspace"))
		}
		if dir == "" {
			dir = "(unknown workspace)"
		}
		return []transcriptEntry{{kind: transcriptSystem, label: "ROMA", text: "workspace ready: " + dir}}
	case events.TypeRuntimeStarted:
		text := fmt.Sprintf("%s started", fallbackAgent(payloadString(record.Payload, "agent")))
		if pid := payloadInt(record.Payload, "pid"); pid > 0 {
			text += " (pid " + strconv.Itoa(pid) + ")"
		}
		return []transcriptEntry{{kind: transcriptSystem, label: "ROMA", text: text}}
	case events.TypeRuntimeExited:
		text := fmt.Sprintf("%s finished", fallbackAgent(payloadString(record.Payload, "agent")))
		if reason := strings.TrimSpace(record.ReasonCode); reason != "" {
			text += " (" + reason + ")"
		}
		return []transcriptEntry{{kind: transcriptSystem, label: "ROMA", text: text}}
	case events.TypeApprovalRequested:
		return []transcriptEntry{{kind: transcriptSystem, label: "ROMA", text: formatSignalLine("approval requested", record)}}
	case events.TypeDangerousCommandDetected:
		return []transcriptEntry{{kind: transcriptSystem, label: "ROMA", text: formatSignalLine("dangerous command detected", record)}}
	case events.TypeHighRiskChangeDetected:
		return []transcriptEntry{{kind: transcriptSystem, label: "ROMA", text: formatSignalLine("high-risk change detected", record)}}
	case events.TypeDelegationRequested:
		return []transcriptEntry{{kind: transcriptSystem, label: "ROMA", text: formatSignalLine("delegation requested", record)}}
	case events.TypeExecutionCompletedDetected:
		return []transcriptEntry{{kind: transcriptSystem, label: "ROMA", text: formatSignalLine("execution completed", record)}}
	case events.TypeParseWarning:
		return []transcriptEntry{{kind: transcriptSystem, label: "ROMA", text: formatSignalLine("parse warning", record)}}
	case events.TypeSemanticReportProduced:
		return []transcriptEntry{{kind: transcriptSystem, label: "ROMA", text: formatSemanticLine("semantic report", record)}}
	case events.TypeSemanticApprovalRecommended:
		return []transcriptEntry{{kind: transcriptSystem, label: "ROMA", text: formatSemanticLine("approval recommended", record)}}
	case events.TypeCuriaPromotionRecommended:
		return []transcriptEntry{{kind: transcriptSystem, label: "ROMA", text: formatSemanticLine("curia recommended", record)}}
	case events.TypePlanApplied, events.TypePlanRolledBack, events.TypePlanApplyRejected:
		text := strings.ToLower(queueTailEventLabel(record.Type))
		if reason := strings.TrimSpace(record.ReasonCode); reason != "" {
			text += ": " + reason
		}
		return []transcriptEntry{{kind: transcriptSystem, label: "ROMA", text: text}}
	case events.TypeTaskStateChanged:
		state := strings.TrimSpace(record.ReasonCode)
		if state == "" {
			return nil
		}
		return []transcriptEntry{{kind: transcriptSystem, label: "ROMA", text: fmt.Sprintf("task %s -> %s", fallbackTask(record.TaskID), state)}}
	case events.TypeQueueCancelled:
		return []transcriptEntry{{kind: transcriptSystem, label: "ROMA", text: "job cancelled"}}
	default:
		return nil
	}
}

func formatOutputEntries(record events.Record) []transcriptEntry {
	chunk := strings.ReplaceAll(payloadString(record.Payload, "stdout"), "\r\n", "\n")
	if strings.TrimSpace(chunk) == "" {
		return nil
	}
	agent := fallbackAgent(payloadString(record.Payload, "agent"))
	lines := make([]transcriptEntry, 0)
	for _, line := range strings.Split(chunk, "\n") {
		line = strings.TrimRight(line, "\r\t ")
		if strings.TrimSpace(line) == "" {
			continue
		}
		lines = append(lines, transcriptEntry{
			kind:  transcriptOutput,
			label: agent,
			text:  line,
		})
	}
	return lines
}

func formatSignalLine(prefix string, record events.Record) string {
	text := strings.TrimSpace(payloadString(record.Payload, "text"))
	if text != "" {
		return prefix + ": " + text
	}
	if reason := strings.TrimSpace(record.ReasonCode); reason != "" {
		return prefix + ": " + reason
	}
	return prefix
}

func formatSemanticLine(prefix string, record events.Record) string {
	parts := []string{prefix}
	if summary := strings.TrimSpace(payloadString(record.Payload, "summary")); summary != "" {
		parts = append(parts, summary)
	} else if reason := strings.TrimSpace(record.ReasonCode); reason != "" {
		parts = append(parts, reason)
	}
	return strings.Join(parts, ": ")
}

func formatJobStatusLine(previous queue.Status, job queue.Request) string {
	switch job.Status {
	case queue.StatusPending:
		return ""
	case queue.StatusRunning:
		if previous == "" {
			return ""
		}
		return "job " + job.ID + " running"
	case queue.StatusAwaitingApproval:
		return "job " + job.ID + " awaiting approval"
	case queue.StatusSucceeded:
		return "job " + job.ID + " succeeded"
	case queue.StatusFailed, queue.StatusCancelled, queue.StatusRejected:
		text := fmt.Sprintf("job %s %s", job.ID, job.Status)
		if reason := strings.TrimSpace(job.Error); reason != "" {
			text += ": " + trimLine(reason, 120)
		}
		return text
	default:
		return "job " + job.ID + " " + string(job.Status)
	}
}

func queueTailEventLabel(typ events.Type) string {
	switch typ {
	case events.TypePlanApplied:
		return "plan applied"
	case events.TypePlanRolledBack:
		return "plan rolled back"
	case events.TypePlanApplyRejected:
		return "plan rejected"
	default:
		return string(typ)
	}
}

func payloadString(payload map[string]any, key string) string {
	if payload == nil {
		return ""
	}
	value, ok := payload[key]
	if !ok || value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	case fmt.Stringer:
		return typed.String()
	default:
		return fmt.Sprintf("%v", value)
	}
}

func payloadInt(payload map[string]any, key string) int {
	if payload == nil {
		return 0
	}
	value, ok := payload[key]
	if !ok || value == nil {
		return 0
	}
	switch typed := value.(type) {
	case int:
		return typed
	case int32:
		return int(typed)
	case int64:
		return int(typed)
	case float32:
		return int(typed)
	case float64:
		return int(typed)
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(typed))
		if err == nil {
			return n
		}
	}
	return 0
}

func fallbackTask(taskID string) string {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return "(task)"
	}
	return taskID
}
