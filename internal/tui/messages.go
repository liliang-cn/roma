package tui

import (
	"time"

	"github.com/liliang-cn/roma/internal/api"
	"github.com/liliang-cn/roma/internal/queue"
)

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
