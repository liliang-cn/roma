package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/liliang-cn/roma/internal/agents"
	"github.com/liliang-cn/roma/internal/api"
	"github.com/liliang-cn/roma/internal/app"
	"github.com/liliang-cn/roma/internal/domain"
	"github.com/liliang-cn/roma/internal/policy"
	"github.com/liliang-cn/roma/internal/queue"
	"github.com/liliang-cn/roma/internal/romapath"
	runsvc "github.com/liliang-cn/roma/internal/run"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type App struct {
	mu              sync.Mutex
	ctx             context.Context
	client          *api.Client
	workingDir      string
	embedded        *embeddedDaemon
	lastDaemonError string
}

type embeddedDaemon struct {
	cancel     context.CancelFunc
	errCh      chan error
	workingDir string
}

type BootstrapResponse struct {
	WorkingDir      string                `json:"working_dir"`
	DaemonAvailable bool                  `json:"daemon_available"`
	EmbeddedDaemon  bool                  `json:"embedded_daemon"`
	LastDaemonError string                `json:"last_daemon_error,omitempty"`
	AgentConfigPath string                `json:"agent_config_path"`
	Agents          []domain.AgentProfile `json:"agents"`
	Status          api.StatusResponse    `json:"status"`
	Queue           []queue.Request       `json:"queue"`
	ACP             api.ACPStatusResponse `json:"acp"`
}

type SnapshotResponse struct {
	WorkingDir      string                `json:"working_dir"`
	DaemonAvailable bool                  `json:"daemon_available"`
	EmbeddedDaemon  bool                  `json:"embedded_daemon"`
	LastDaemonError string                `json:"last_daemon_error,omitempty"`
	Status          api.StatusResponse    `json:"status"`
	Queue           []queue.Request       `json:"queue"`
	ACP             api.ACPStatusResponse `json:"acp"`
}

type RunSubmitRequest struct {
	Prompt         string   `json:"prompt"`
	Mode           string   `json:"mode"`
	StarterAgent   string   `json:"starter_agent"`
	Delegates      []string `json:"delegates"`
	WorkingDir     string   `json:"working_dir"`
	Continuous     bool     `json:"continuous"`
	MaxRounds      int      `json:"max_rounds"`
	PolicyOverride bool     `json:"policy_override"`
}

type PlanPreviewRequest struct {
	SessionID      string `json:"session_id"`
	TaskID         string `json:"task_id"`
	ArtifactID     string `json:"artifact_id"`
	PolicyOverride bool   `json:"policy_override"`
}

func NewApp() *App {
	wd, err := os.Getwd()
	if err != nil || strings.TrimSpace(wd) == "" {
		wd = filepath.Clean(os.Getenv("HOME"))
		if strings.TrimSpace(wd) == "" {
			wd = "."
		}
	}
	return &App{workingDir: wd}
}

func (a *App) startup(ctx context.Context) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.ctx = ctx
	a.client = api.NewClientForControlDir(a.workingDir, romapath.HomeDir())
}

func (a *App) shutdown(ctx context.Context) {
	a.mu.Lock()
	cancel := a.stopEmbeddedLocked()
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (a *App) Bootstrap() (BootstrapResponse, error) {
	if err := a.ensureDaemon(); err != nil {
		return BootstrapResponse{}, err
	}
	registry, err := loadRegistry()
	if err != nil {
		return BootstrapResponse{}, err
	}
	status, queueItems, acp, err := a.fetchSnapshot()
	if err != nil {
		return BootstrapResponse{}, err
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	return BootstrapResponse{
		WorkingDir:      a.workingDir,
		DaemonAvailable: true,
		EmbeddedDaemon:  a.embedded != nil,
		LastDaemonError: a.lastDaemonError,
		AgentConfigPath: agents.DefaultUserConfigPath(),
		Agents:          registry.WithResolvedAvailability(a.requestContextLocked()),
		Status:          status,
		Queue:           queueItems,
		ACP:             acp,
	}, nil
}

func (a *App) Snapshot() (SnapshotResponse, error) {
	if err := a.ensureDaemon(); err != nil {
		return SnapshotResponse{}, err
	}
	status, queueItems, acp, err := a.fetchSnapshot()
	if err != nil {
		return SnapshotResponse{}, err
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	return SnapshotResponse{
		WorkingDir:      a.workingDir,
		DaemonAvailable: true,
		EmbeddedDaemon:  a.embedded != nil,
		LastDaemonError: a.lastDaemonError,
		Status:          status,
		Queue:           queueItems,
		ACP:             acp,
	}, nil
}

func (a *App) PickWorkingDir() (string, error) {
	a.mu.Lock()
	ctx := a.ctx
	current := a.workingDir
	a.mu.Unlock()
	if ctx == nil {
		return "", fmt.Errorf("desktop runtime context is not ready")
	}
	dir, err := runtime.OpenDirectoryDialog(ctx, runtime.OpenDialogOptions{
		Title:            "Select Working Directory",
		DefaultDirectory: current,
	})
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(dir) == "" {
		return current, nil
	}
	return dir, nil
}

func (a *App) SetWorkingDir(dir string) (BootstrapResponse, error) {
	resolved, err := resolveWorkingDir(dir)
	if err != nil {
		return BootstrapResponse{}, err
	}
	a.mu.Lock()
	a.workingDir = resolved
	a.client = api.NewClientForControlDir(a.workingDir, romapath.HomeDir())
	a.mu.Unlock()
	return a.Bootstrap()
}

func (a *App) ListAgents() ([]domain.AgentProfile, error) {
	registry, err := loadRegistry()
	if err != nil {
		return nil, err
	}
	return registry.WithResolvedAvailability(context.Background()), nil
}

func (a *App) SubmitRun(req RunSubmitRequest) (api.SubmitResponse, error) {
	if err := a.ensureDaemon(); err != nil {
		return api.SubmitResponse{}, err
	}
	registry, err := loadRegistry()
	if err != nil {
		return api.SubmitResponse{}, err
	}

	workingDir := strings.TrimSpace(req.WorkingDir)
	if workingDir == "" {
		a.mu.Lock()
		workingDir = a.workingDir
		a.mu.Unlock()
	}
	workingDir, err = resolveWorkingDir(workingDir)
	if err != nil {
		return api.SubmitResponse{}, err
	}

	starterAgent := strings.TrimSpace(req.StarterAgent)
	if starterAgent == "" {
		profile, err := registry.DefaultProfile(context.Background())
		if err != nil {
			return api.SubmitResponse{}, err
		}
		starterAgent = profile.ID
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := a.currentClient()
	return client.Submit(ctx, api.SubmitRequest{
		Prompt:              strings.TrimSpace(req.Prompt),
		Mode:                runsvc.NormalizeMode(req.Mode),
		StarterAgent:        starterAgent,
		Delegates:           normalizeDelegates(req.Delegates, starterAgent),
		WorkingDir:          workingDir,
		Continuous:          req.Continuous,
		MaxRounds:           req.MaxRounds,
		PolicyOverride:      req.PolicyOverride,
		PolicyOverrideActor: policy.OverrideActor(),
	})
}

func (a *App) QueueInspect(jobID string) (api.QueueInspectResponse, error) {
	if err := a.ensureDaemon(); err != nil {
		return api.QueueInspectResponse{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return a.currentClient().QueueInspect(ctx, strings.TrimSpace(jobID), false)
}

func (a *App) SessionInspect(sessionID string) (api.SessionInspectResponse, error) {
	if err := a.ensureDaemon(); err != nil {
		return api.SessionInspectResponse{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return a.currentClient().SessionInspect(ctx, strings.TrimSpace(sessionID))
}

func (a *App) ResultShow(sessionID string) (api.ResultShowResponse, error) {
	if err := a.ensureDaemon(); err != nil {
		return api.ResultShowResponse{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return a.currentClient().ResultShow(ctx, strings.TrimSpace(sessionID))
}

func (a *App) QueueCancel(jobID string) (queue.Request, error) {
	if err := a.ensureDaemon(); err != nil {
		return queue.Request{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return a.currentClient().QueueCancel(ctx, strings.TrimSpace(jobID))
}

func (a *App) PlansInbox(sessionID string) ([]api.PlanInboxEntry, error) {
	if err := a.ensureDaemon(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return a.currentClient().PlanInbox(ctx, strings.TrimSpace(sessionID))
}

func (a *App) PlanApprove(artifactID string) error {
	if err := a.ensureDaemon(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return a.currentClient().PlanApprove(ctx, strings.TrimSpace(artifactID), "desktop_user")
}

func (a *App) PlanReject(artifactID string) error {
	if err := a.ensureDaemon(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return a.currentClient().PlanReject(ctx, strings.TrimSpace(artifactID), "desktop_user")
}

func (a *App) PlanPreview(req PlanPreviewRequest) (api.PlanApplyResponse, error) {
	if err := a.ensureDaemon(); err != nil {
		return api.PlanApplyResponse{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return a.currentClient().PlanPreview(ctx, api.PlanApplyRequest{
		SessionID:           strings.TrimSpace(req.SessionID),
		TaskID:              strings.TrimSpace(req.TaskID),
		ArtifactID:          strings.TrimSpace(req.ArtifactID),
		DryRun:              true,
		PolicyOverride:      req.PolicyOverride,
		PolicyOverrideActor: policy.OverrideActor(),
	})
}

func (a *App) fetchSnapshot() (api.StatusResponse, []queue.Request, api.ACPStatusResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := a.currentClient()

	status, err := client.Status(ctx)
	if err != nil {
		return api.StatusResponse{}, nil, api.ACPStatusResponse{}, err
	}
	queueItems, err := client.QueueList(ctx)
	if err != nil {
		return api.StatusResponse{}, nil, api.ACPStatusResponse{}, err
	}
	slices.SortFunc(queueItems, func(a, b queue.Request) int {
		return b.UpdatedAt.Compare(a.UpdatedAt)
	})

	acp, err := client.AcpStatus(ctx)
	if err != nil {
		acp = api.ACPStatusResponse{}
	}
	return status, queueItems, acp, nil
}

func (a *App) ensureDaemon() error {
	a.mu.Lock()
	if a.client == nil {
		a.client = api.NewClientForControlDir(a.workingDir, romapath.HomeDir())
	}
	a.consumeEmbeddedErrorLocked()
	client := a.client
	if client.Available() {
		a.mu.Unlock()
		return nil
	}
	if a.embedded == nil {
		daemon, err := app.NewDaemonForWorkingDir(a.workingDir)
		if err != nil {
			a.mu.Unlock()
			return err
		}
		daemonCtx, cancel := context.WithCancel(context.Background())
		errCh := make(chan error, 1)
		go func() {
			errCh <- daemon.Run(daemonCtx)
		}()
		a.embedded = &embeddedDaemon{
			cancel:     cancel,
			errCh:      errCh,
			workingDir: a.workingDir,
		}
	}
	a.mu.Unlock()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if client.Available() {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}

	a.mu.Lock()
	a.consumeEmbeddedErrorLocked()
	lastErr := strings.TrimSpace(a.lastDaemonError)
	a.mu.Unlock()
	if lastErr != "" {
		return fmt.Errorf("romad unavailable: %s", lastErr)
	}
	return fmt.Errorf("romad did not become ready within 5s")
}

func (a *App) consumeEmbeddedErrorLocked() {
	if a.embedded == nil {
		return
	}
	select {
	case err := <-a.embedded.errCh:
		if err != nil && !errors.Is(err, context.Canceled) {
			a.lastDaemonError = err.Error()
		}
		a.embedded = nil
	default:
	}
}

func (a *App) stopEmbeddedLocked() context.CancelFunc {
	if a.embedded == nil {
		return nil
	}
	cancel := a.embedded.cancel
	a.embedded = nil
	return cancel
}

func (a *App) currentClient() *api.Client {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.client == nil {
		a.client = api.NewClientForControlDir(a.workingDir, romapath.HomeDir())
	}
	return a.client
}

func (a *App) requestContextLocked() context.Context {
	if a.ctx != nil {
		return a.ctx
	}
	return context.Background()
}

func loadRegistry() (*agents.Registry, error) {
	registry, err := agents.DefaultRegistry()
	if err != nil {
		return nil, fmt.Errorf("load agent registry: %w", err)
	}
	registry.SetUserConfigPath(agents.DefaultUserConfigPath())
	if err := registry.LoadUserConfig(registry.UserConfigPath()); err != nil {
		return nil, fmt.Errorf("load user agent config: %w", err)
	}
	return registry, nil
}

func resolveWorkingDir(dir string) (string, error) {
	resolved, err := filepath.Abs(strings.TrimSpace(dir))
	if err != nil {
		return "", fmt.Errorf("resolve working directory: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("stat working directory: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("working directory is not a directory: %s", resolved)
	}
	return resolved, nil
}

func normalizeDelegates(items []string, starter string) []string {
	out := make([]string, 0, len(items))
	seen := map[string]struct{}{}
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" || item == starter {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	return out
}
