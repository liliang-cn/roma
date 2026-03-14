package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/liliang-cn/roma/internal/domain"
	"github.com/liliang-cn/roma/internal/queue"
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
