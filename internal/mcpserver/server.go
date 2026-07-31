// Package mcpserver exposes TagIt to other agents over the Model Context
// Protocol. It is a thin, transport-agnostic facade over the tagitd HTTP API:
// every tool is a call the `tagit` CLI could already make, so the daemon stays
// the single control plane and the scheduler stays the only writer of task
// execution state.
//
// The tool surface deliberately omits the policy-override escape hatch. An MCP
// caller can submit work and watch it, but it can never bypass the policy
// broker the way `tagit run --policy-override` can.
package mcpserver

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/liliang-cn/tagit/internal/api"
	"github.com/liliang-cn/tagit/internal/memory"
	"github.com/liliang-cn/tagit/internal/queue"
)

// Daemon is the slice of the tagitd API the MCP tools need. *api.Client
// satisfies it; tests supply a fake.
type Daemon interface {
	Submit(ctx context.Context, req api.SubmitRequest) (api.SubmitResponse, error)
	QueueInspect(ctx context.Context, id string, raw bool) (api.QueueInspectResponse, error)
	QueueList(ctx context.Context) ([]queue.Request, error)
	QueueCancel(ctx context.Context, id string) (queue.Request, error)
	ResultShow(ctx context.Context, sessionID string) (api.ResultShowResponse, error)
}

// Options configures the MCP server.
type Options struct {
	// Daemon talks to tagitd. Required.
	Daemon Daemon
	// Memory is optional; when nil the memory tools are not exposed.
	Memory memory.Memory
	// ReadOnly hides every tool that mutates state (submit, cancel, note).
	ReadOnly bool
	// DefaultWorkingDir is the repo used when a call omits working_dir/repo.
	DefaultWorkingDir string
	// Version is reported to the MCP client.
	Version string
	// PollInterval is how often tagit_job_wait re-inspects a job.
	PollInterval time.Duration
}

const (
	defaultPollInterval = 2 * time.Second
	defaultWaitTimeout  = 5 * time.Minute
	maxWaitTimeout      = time.Hour
	defaultJobListLimit = 20
	maxJobListLimit     = 100
)

const serverInstructions = `TagIt orchestrates interactive coding-agent CLIs (claude, codex, gemini, ...)
in isolated git worktrees under a single daemon.

Typical flow: tagit_submit to enqueue work against a repo, tagit_job_wait to
block until it finishes, tagit_result to read the final outcome. Runs are
asynchronous — a submit returns a job id immediately.

Every run is subject to TagIt's policy broker; risky work may stop in
awaiting_approval and needs a human to approve it with the tagit CLI.`

// NewServer builds the TagIt MCP server. Mount it on any transport, typically
// mcp.StdioTransport for `tagit mcp`.
func NewServer(opts Options) *mcp.Server {
	version := strings.TrimSpace(opts.Version)
	if version == "" {
		version = "dev"
	}
	server := mcp.NewServer(
		&mcp.Implementation{Name: "tagit", Title: "TagIt", Version: version},
		&mcp.ServerOptions{Instructions: serverInstructions},
	)

	h := &handlers{
		daemon:       opts.Daemon,
		mem:          opts.Memory,
		defaultRepo:  strings.TrimSpace(opts.DefaultWorkingDir),
		pollInterval: opts.PollInterval,
	}
	if h.pollInterval <= 0 {
		h.pollInterval = defaultPollInterval
	}

	readOnly := &mcp.ToolAnnotations{ReadOnlyHint: true}

	if !opts.ReadOnly {
		mcp.AddTool(server, &mcp.Tool{
			Name:  "tagit_submit",
			Title: "Submit a coding task",
			Description: "Enqueue a coding task for a TagIt agent to run in an isolated git worktree of the " +
				"given repository. Returns immediately with a job id; use tagit_job_wait to block until it " +
				"finishes and tagit_result to read the outcome.",
		}, h.submit)
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "tagit_job_status",
		Title:       "Inspect a job",
		Description: "Return the current status of one queued or running TagIt job.",
		Annotations: readOnly,
	}, h.jobStatus)
	mcp.AddTool(server, &mcp.Tool{
		Name:  "tagit_job_wait",
		Title: "Wait for a job",
		Description: "Block until a TagIt job reaches a terminal state (succeeded, failed, rejected or " +
			"cancelled), or until the timeout expires. Returns the last known status either way.",
		Annotations: readOnly,
	}, h.jobWait)
	if !opts.ReadOnly {
		mcp.AddTool(server, &mcp.Tool{
			Name:        "tagit_job_cancel",
			Title:       "Cancel a job",
			Description: "Cancel a queued or running TagIt job.",
		}, h.jobCancel)
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "tagit_job_list",
		Title:       "List recent jobs",
		Description: "List the most recent TagIt jobs, newest first.",
		Annotations: readOnly,
	}, h.jobList)
	mcp.AddTool(server, &mcp.Tool{
		Name:  "tagit_result",
		Title: "Read a run result",
		Description: "Return the final, user-facing outcome of a finished TagIt run. Accepts either a " +
			"session id or the job id returned by tagit_submit.",
		Annotations: readOnly,
	}, h.result)

	if opts.Memory != nil {
		mcp.AddTool(server, &mcp.Tool{
			Name:  "tagit_memory_recall",
			Title: "Recall repo memory",
			Description: "Recall what TagIt agents have previously learned about a repository: past runs and " +
				"durable notes. Advisory context, never authoritative.",
			Annotations: readOnly,
		}, h.memoryRecall)
		if !opts.ReadOnly {
			mcp.AddTool(server, &mcp.Tool{
				Name:        "tagit_memory_note",
				Title:       "Record a repo fact",
				Description: "Store a durable fact about a repository so future TagIt runs can recall it.",
			}, h.memoryNote)
		}
	}

	return server
}

type handlers struct {
	daemon       Daemon
	mem          memory.Memory
	defaultRepo  string
	pollInterval time.Duration
}

// ---------------------------------------------------------------------------
// tagit_submit
// ---------------------------------------------------------------------------

// SubmitInput is the tagit_submit argument set. It intentionally has no
// policy-override field: MCP callers cannot bypass the policy broker.
type SubmitInput struct {
	Prompt       string   `json:"prompt" jsonschema:"what the agent should do, in plain language"`
	WorkingDir   string   `json:"working_dir,omitempty" jsonschema:"absolute path of the target repository; defaults to the server's configured repo"`
	Mode         string   `json:"mode,omitempty" jsonschema:"run mode: rage (single agent), collab (starter plus delegates) or senate (multi-agent vote); omit to let TagIt choose"`
	StarterAgent string   `json:"starter_agent,omitempty" jsonschema:"registered agent id to lead the run, e.g. claude or codex"`
	Delegates    []string `json:"delegates,omitempty" jsonschema:"registered agent ids to delegate to"`
	MaxRounds    int      `json:"max_rounds,omitempty" jsonschema:"maximum worker/foreman rounds before the run stops"`
}

// SubmitOutput is what tagit_submit returns.
type SubmitOutput struct {
	JobID      string `json:"job_id"`
	WorkingDir string `json:"working_dir"`
	Mode       string `json:"mode,omitempty"`
}

func (h *handlers) submit(ctx context.Context, _ *mcp.CallToolRequest, in SubmitInput) (*mcp.CallToolResult, SubmitOutput, error) {
	prompt := strings.TrimSpace(in.Prompt)
	if prompt == "" {
		return nil, SubmitOutput{}, fmt.Errorf("prompt is required")
	}
	workingDir, err := h.repoOf(in.WorkingDir)
	if err != nil {
		return nil, SubmitOutput{}, err
	}
	resp, err := h.daemon.Submit(ctx, api.SubmitRequest{
		Prompt:       prompt,
		WorkingDir:   workingDir,
		Mode:         strings.TrimSpace(in.Mode),
		StarterAgent: strings.TrimSpace(in.StarterAgent),
		Delegates:    in.Delegates,
		MaxRounds:    in.MaxRounds,
	})
	if err != nil {
		return nil, SubmitOutput{}, fmt.Errorf("submit to tagitd: %w", err)
	}
	return nil, SubmitOutput{JobID: resp.JobID, WorkingDir: workingDir, Mode: strings.TrimSpace(in.Mode)}, nil
}

// ---------------------------------------------------------------------------
// tagit_job_status / tagit_job_wait / tagit_job_cancel / tagit_job_list
// ---------------------------------------------------------------------------

// JobInput identifies one job.
type JobInput struct {
	JobID string `json:"job_id" jsonschema:"job id returned by tagit_submit"`
}

// JobWaitInput identifies one job plus how long to wait for it.
type JobWaitInput struct {
	JobID          string `json:"job_id" jsonschema:"job id returned by tagit_submit"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty" jsonschema:"how long to wait before giving up and returning the last known status; defaults to 300"`
}

// JobOutput is the summarized state of one job.
type JobOutput struct {
	JobID                  string   `json:"job_id"`
	Status                 string   `json:"status"`
	Terminal               bool     `json:"terminal"`
	TimedOut               bool     `json:"timed_out,omitempty"`
	SessionID              string   `json:"session_id,omitempty"`
	Mode                   string   `json:"mode,omitempty"`
	StarterAgent           string   `json:"starter_agent,omitempty"`
	WorkingDir             string   `json:"working_dir,omitempty"`
	Prompt                 string   `json:"prompt,omitempty"`
	PendingApprovalTaskIDs []string `json:"pending_approval_task_ids,omitempty"`
	Error                  string   `json:"error,omitempty"`
	UpdatedAt              string   `json:"updated_at,omitempty"`
}

// JobListInput bounds a job listing.
type JobListInput struct {
	Limit int `json:"limit,omitempty" jsonschema:"how many jobs to return, newest first; defaults to 20"`
}

// JobListOutput lists jobs, newest first.
type JobListOutput struct {
	Jobs []JobOutput `json:"jobs"`
}

func (h *handlers) jobStatus(ctx context.Context, _ *mcp.CallToolRequest, in JobInput) (*mcp.CallToolResult, JobOutput, error) {
	jobID := strings.TrimSpace(in.JobID)
	if jobID == "" {
		return nil, JobOutput{}, fmt.Errorf("job_id is required")
	}
	resp, err := h.daemon.QueueInspect(ctx, jobID, false)
	if err != nil {
		return nil, JobOutput{}, fmt.Errorf("inspect job %s: %w", jobID, err)
	}
	return nil, jobOutput(resp), nil
}

func (h *handlers) jobWait(ctx context.Context, _ *mcp.CallToolRequest, in JobWaitInput) (*mcp.CallToolResult, JobOutput, error) {
	jobID := strings.TrimSpace(in.JobID)
	if jobID == "" {
		return nil, JobOutput{}, fmt.Errorf("job_id is required")
	}
	timeout := defaultWaitTimeout
	if in.TimeoutSeconds > 0 {
		timeout = time.Duration(in.TimeoutSeconds) * time.Second
	}
	if timeout > maxWaitTimeout {
		timeout = maxWaitTimeout
	}

	deadline := time.Now().Add(timeout)
	for {
		resp, err := h.daemon.QueueInspect(ctx, jobID, false)
		if err != nil {
			return nil, JobOutput{}, fmt.Errorf("inspect job %s: %w", jobID, err)
		}
		out := jobOutput(resp)
		if out.Terminal {
			return nil, out, nil
		}
		if !time.Now().Before(deadline) {
			out.TimedOut = true
			return nil, out, nil
		}
		timer := time.NewTimer(h.pollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, JobOutput{}, ctx.Err()
		case <-timer.C:
		}
	}
}

func (h *handlers) jobCancel(ctx context.Context, _ *mcp.CallToolRequest, in JobInput) (*mcp.CallToolResult, JobOutput, error) {
	jobID := strings.TrimSpace(in.JobID)
	if jobID == "" {
		return nil, JobOutput{}, fmt.Errorf("job_id is required")
	}
	req, err := h.daemon.QueueCancel(ctx, jobID)
	if err != nil {
		return nil, JobOutput{}, fmt.Errorf("cancel job %s: %w", jobID, err)
	}
	return nil, jobOutputOf(req), nil
}

func (h *handlers) jobList(ctx context.Context, _ *mcp.CallToolRequest, in JobListInput) (*mcp.CallToolResult, JobListOutput, error) {
	limit := in.Limit
	if limit <= 0 {
		limit = defaultJobListLimit
	}
	if limit > maxJobListLimit {
		limit = maxJobListLimit
	}
	items, err := h.daemon.QueueList(ctx)
	if err != nil {
		return nil, JobListOutput{}, fmt.Errorf("list jobs: %w", err)
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].CreatedAt.After(items[j].CreatedAt) })
	if len(items) > limit {
		items = items[:limit]
	}
	jobs := make([]JobOutput, 0, len(items))
	for _, item := range items {
		jobs = append(jobs, jobOutputOf(item))
	}
	return nil, JobListOutput{Jobs: jobs}, nil
}

func jobOutput(resp api.QueueInspectResponse) JobOutput {
	out := jobOutputOf(resp.Job)
	out.PendingApprovalTaskIDs = resp.PendingApprovalTaskIDs
	return out
}

func jobOutputOf(req queue.Request) JobOutput {
	out := JobOutput{
		JobID:        req.ID,
		Status:       string(req.Status),
		Terminal:     isTerminal(req.Status),
		SessionID:    req.SessionID,
		Mode:         req.Mode,
		StarterAgent: req.StarterAgent,
		WorkingDir:   req.WorkingDir,
		Prompt:       req.Prompt,
		Error:        req.Error,
	}
	if !req.UpdatedAt.IsZero() {
		out.UpdatedAt = req.UpdatedAt.UTC().Format(time.RFC3339)
	}
	return out
}

func isTerminal(status queue.Status) bool {
	switch status {
	case queue.StatusSucceeded, queue.StatusFailed, queue.StatusRejected, queue.StatusCancelled:
		return true
	default:
		return false
	}
}

// ---------------------------------------------------------------------------
// tagit_result
// ---------------------------------------------------------------------------

// ResultInput addresses a finished run by session id or job id.
type ResultInput struct {
	SessionID string `json:"session_id,omitempty" jsonschema:"session id of the run"`
	JobID     string `json:"job_id,omitempty" jsonschema:"job id returned by tagit_submit; its session is resolved automatically"`
}

// ResultOutput is the final outcome of a run.
type ResultOutput struct {
	SessionID  string `json:"session_id"`
	Pending    bool   `json:"pending"`
	Message    string `json:"message,omitempty"`
	ArtifactID string `json:"artifact_id,omitempty"`
}

func (h *handlers) result(ctx context.Context, _ *mcp.CallToolRequest, in ResultInput) (*mcp.CallToolResult, ResultOutput, error) {
	sessionID := strings.TrimSpace(in.SessionID)
	if sessionID == "" {
		jobID := strings.TrimSpace(in.JobID)
		if jobID == "" {
			return nil, ResultOutput{}, fmt.Errorf("one of session_id or job_id is required")
		}
		resp, err := h.daemon.QueueInspect(ctx, jobID, false)
		if err != nil {
			return nil, ResultOutput{}, fmt.Errorf("inspect job %s: %w", jobID, err)
		}
		sessionID = resp.Job.SessionID
		if sessionID == "" {
			return nil, ResultOutput{}, fmt.Errorf("job %s has not started a session yet (status %s)", jobID, resp.Job.Status)
		}
	}
	resp, err := h.daemon.ResultShow(ctx, sessionID)
	if err != nil {
		return nil, ResultOutput{}, fmt.Errorf("read result for %s: %w", sessionID, err)
	}
	return nil, ResultOutput{
		SessionID:  sessionID,
		Pending:    resp.Pending,
		Message:    resp.Message,
		ArtifactID: resp.Artifact.ID,
	}, nil
}

// ---------------------------------------------------------------------------
// memory tools
// ---------------------------------------------------------------------------

// RecallInput asks what is known about a repo.
type RecallInput struct {
	Query string `json:"query" jsonschema:"what to recall, in plain language"`
	Repo  string `json:"repo,omitempty" jsonschema:"absolute path of the repository; defaults to the server's configured repo"`
	Limit int    `json:"limit,omitempty" jsonschema:"maximum number of recalled items"`
}

// EpisodeOutput is one recalled past run.
type EpisodeOutput struct {
	Summary    string `json:"summary"`
	Agent      string `json:"agent,omitempty"`
	Mode       string `json:"mode,omitempty"`
	Success    bool   `json:"success"`
	OccurredAt string `json:"occurred_at,omitempty"`
}

// FactOutput is one recalled durable note.
type FactOutput struct {
	Text string   `json:"text"`
	Tags []string `json:"tags,omitempty"`
}

// RecallOutput carries injectable context plus its constituent parts.
type RecallOutput struct {
	Context  string          `json:"context"`
	Episodes []EpisodeOutput `json:"episodes,omitempty"`
	Facts    []FactOutput    `json:"facts,omitempty"`
}

// NoteInput records a durable fact about a repo.
type NoteInput struct {
	Fact string   `json:"fact" jsonschema:"the fact worth remembering"`
	Repo string   `json:"repo,omitempty" jsonschema:"absolute path of the repository; defaults to the server's configured repo"`
	Tags []string `json:"tags,omitempty" jsonschema:"free-form labels for scoped recall"`
}

// NoteOutput acknowledges a stored fact.
type NoteOutput struct {
	OK bool `json:"ok"`
}

func (h *handlers) memoryRecall(ctx context.Context, _ *mcp.CallToolRequest, in RecallInput) (*mcp.CallToolResult, RecallOutput, error) {
	repo, err := h.repoOf(in.Repo)
	if err != nil {
		return nil, RecallOutput{}, err
	}
	rec, err := h.mem.Recall(ctx, memory.Scope{Repo: repo}, strings.TrimSpace(in.Query), in.Limit)
	if err != nil {
		return nil, RecallOutput{}, fmt.Errorf("recall memory for %s: %w", repo, err)
	}
	out := RecallOutput{Context: rec.ContextText}
	for _, ep := range rec.Episodes {
		item := EpisodeOutput{Summary: ep.Summary, Agent: ep.Agent, Mode: ep.Mode, Success: ep.Success}
		if !ep.OccurredAt.IsZero() {
			item.OccurredAt = ep.OccurredAt.UTC().Format(time.RFC3339)
		}
		out.Episodes = append(out.Episodes, item)
	}
	for _, fact := range rec.Knowledge {
		out.Facts = append(out.Facts, FactOutput{Text: fact.Text, Tags: fact.Tags})
	}
	return nil, out, nil
}

func (h *handlers) memoryNote(ctx context.Context, _ *mcp.CallToolRequest, in NoteInput) (*mcp.CallToolResult, NoteOutput, error) {
	fact := strings.TrimSpace(in.Fact)
	if fact == "" {
		return nil, NoteOutput{}, fmt.Errorf("fact is required")
	}
	repo, err := h.repoOf(in.Repo)
	if err != nil {
		return nil, NoteOutput{}, err
	}
	if err := h.mem.Note(ctx, memory.Scope{Repo: repo}, fact, in.Tags); err != nil {
		return nil, NoteOutput{}, fmt.Errorf("record memory for %s: %w", repo, err)
	}
	return nil, NoteOutput{OK: true}, nil
}

// repoOf resolves a caller-supplied repo path against the configured default.
func (h *handlers) repoOf(candidate string) (string, error) {
	if repo := strings.TrimSpace(candidate); repo != "" {
		return repo, nil
	}
	if h.defaultRepo != "" {
		return h.defaultRepo, nil
	}
	return "", fmt.Errorf("no repository: pass an absolute path, or start the server with a default working directory")
}
