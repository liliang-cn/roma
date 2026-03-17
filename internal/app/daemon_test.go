package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/liliang-cn/roma/internal/acpserver"
	"github.com/liliang-cn/roma/internal/domain"
	"github.com/liliang-cn/roma/internal/queue"
	"github.com/liliang-cn/roma/internal/run"
)

func TestDaemonReloadsUserAgentConfigBeforeProcessingQueueItem(t *testing.T) {
	homeDir := t.TempDir()
	workDir := t.TempDir()
	t.Setenv("ROMA_HOME", homeDir)

	initial := []domain.AgentProfile{
		{
			ID:           "my-codex",
			DisplayName:  "My Codex",
			Command:      "sh",
			Args:         []string{"-c", "printf 'starter\n'"},
			Availability: domain.AgentAvailabilityPlanned,
		},
	}
	writeAgentConfig(t, filepath.Join(homeDir, "agents.json"), initial)

	daemon, err := NewDaemonForWorkingDir(workDir)
	if err != nil {
		t.Fatalf("NewDaemonForWorkingDir() error = %v", err)
	}

	updated := []domain.AgentProfile{
		initial[0],
		{
			ID:           "my-opencode",
			DisplayName:  "My OpenCode",
			Command:      "sh",
			Args:         []string{"-c", "printf 'opencode ok\n'"},
			Availability: domain.AgentAvailabilityPlanned,
		},
	}
	writeAgentConfig(t, filepath.Join(homeDir, "agents.json"), updated)

	job := queue.Request{
		ID:           "job_reload_agent",
		Prompt:       "ignored prompt",
		StarterAgent: "my-opencode",
		WorkingDir:   workDir,
		Status:       queue.StatusPending,
	}
	if err := daemon.queue.Enqueue(context.Background(), job); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}

	if err := daemon.processNextQueueItem(context.Background()); err != nil {
		t.Fatalf("processNextQueueItem() error = %v", err)
	}

	got, err := daemon.queue.Get(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Status != queue.StatusSucceeded {
		t.Fatalf("status = %s, want %s (error=%q)", got.Status, queue.StatusSucceeded, got.Error)
	}
}

func TestFinalizeQueueRequestUsesRunResultStatusFallback(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		runStatus  string
		runErr     error
		canceled   bool
		wantStatus queue.Status
		wantError  string
	}{
		{
			name:       "failed result without error",
			runStatus:  "failed",
			wantStatus: queue.StatusFailed,
			wantError:  "run failed",
		},
		{
			name:       "awaiting approval",
			runStatus:  "awaiting_approval",
			wantStatus: queue.StatusAwaitingApproval,
			wantError:  "approval required",
		},
		{
			name:       "success result",
			runStatus:  "succeeded",
			wantStatus: queue.StatusSucceeded,
			wantError:  "",
		},
		{
			name:       "cancelled overrides result",
			runStatus:  "failed",
			canceled:   true,
			wantStatus: queue.StatusCancelled,
			wantError:  "cancelled by user",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := queue.Request{
				ID:                  "job_1",
				Status:              queue.StatusRunning,
				PolicyOverride:      true,
				PolicyOverrideActor: "tester",
			}
			finalizeQueueRequest(&req, run.Result{Status: tt.runStatus}, tt.runErr, tt.canceled)
			if req.Status != tt.wantStatus {
				t.Fatalf("status = %s, want %s", req.Status, tt.wantStatus)
			}
			if req.Error != tt.wantError {
				t.Fatalf("error = %q, want %q", req.Error, tt.wantError)
			}
		})
	}
}

func TestDaemonStartACPServerWhenConfigured(t *testing.T) {
	homeDir := t.TempDir()
	workDir := t.TempDir()
	t.Setenv("ROMA_HOME", homeDir)

	started := false
	gotPort := 0
	daemon, err := NewDaemonWithOptions(DaemonOptions{
		WorkingDir: workDir,
		ACPPort:    8090,
		newACPServer: func(cfg acpserver.Config) (acpService, error) {
			gotPort = cfg.Port
			return fakeACPService{
				port: cfg.Port,
				start: func(context.Context) error {
					started = true
					return nil
				},
			}, nil
		},
	})
	if err != nil {
		t.Fatalf("NewDaemonWithOptions() error = %v", err)
	}
	if gotPort != 8090 {
		t.Fatalf("ACP port = %d, want %d", gotPort, 8090)
	}
	if err := daemon.startACP(context.Background()); err != nil {
		t.Fatalf("startACP() error = %v", err)
	}
	if !started {
		t.Fatal("startACP() did not start the ACP server")
	}
}

type fakeACPService struct {
	port  int
	start func(context.Context) error
}

func (f fakeACPService) Start(ctx context.Context) error {
	if f.start == nil {
		return nil
	}
	return f.start(ctx)
}

func (f fakeACPService) Port() int {
	return f.port
}

func writeAgentConfig(t *testing.T, path string, profiles []domain.AgentProfile) {
	t.Helper()
	data, err := json.MarshalIndent(profiles, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}
