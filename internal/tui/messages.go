package tui

import (
	"time"

	"github.com/liliang-cn/roma/internal/api"
	"github.com/liliang-cn/roma/internal/queue"
)

type transcriptKind uint8

const (
	transcriptSystem transcriptKind = iota
	transcriptUser
	transcriptOutput
)

type transcriptEntry struct {
	kind  transcriptKind
	label string
	text  string
}

type streamState struct {
	jobID        string
	seenEventIDs map[string]struct{}
	lastStatus   queue.Status
}

type snapshot struct {
	status api.StatusResponse
	queue  []queue.Request
	resp   *api.QueueInspectResponse
}

type snapshotMsg struct {
	snapshot snapshot
}

type commandMsg struct {
	text      string
	jobID     string
	selectJob bool
	agentID   string
	withIDs   []string
	themeName string
	quit      bool
	err       error
}

type daemonErrMsg struct {
	err error
}

type tickMsg time.Time
