package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestClientFallsBackToGlobalDaemonHome(t *testing.T) {
	workDir := t.TempDir()
	globalHome := t.TempDir()
	t.Setenv("ROMA_HOME", globalHome)

	if err := os.MkdirAll(filepath.Join(workDir, ".roma", "run"), 0o755); err != nil {
		t.Fatalf("mkdir local run dir: %v", err)
	}
	stale := map[string]string{
		"network": "unix",
		"address": filepath.Join(workDir, ".roma", "run", "missing.sock"),
	}
	raw, err := json.Marshal(stale)
	if err != nil {
		t.Fatalf("marshal stale meta: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workDir, ".roma", "run", "api.json"), raw, 0o644); err != nil {
		t.Fatalf("write stale meta: %v", err)
	}

	if err := os.MkdirAll(filepath.Join(globalHome, ".roma", "run"), 0o755); err != nil {
		t.Fatalf("mkdir global run dir: %v", err)
	}
	globalMeta := map[string]string{
		"network": "tcp",
		"address": "global-daemon",
	}
	raw, err = json.Marshal(globalMeta)
	if err != nil {
		t.Fatalf("marshal global meta: %v", err)
	}
	if err := os.WriteFile(filepath.Join(globalHome, ".roma", "run", "api.json"), raw, 0o644); err != nil {
		t.Fatalf("write global meta: %v", err)
	}

	previousHealthCheck := healthCheckFn
	healthCheckFn = func(_ *http.Client, baseURL string) error {
		if baseURL == "http://global-daemon" {
			return nil
		}
		return fmt.Errorf("unavailable")
	}
	defer func() {
		healthCheckFn = previousHealthCheck
	}()

	client := NewClient(workDir)
	if !client.Available() {
		t.Fatal("Available() = false, want true")
	}
	if _, _, err := client.httpClient(); err != nil {
		t.Fatalf("httpClient() error = %v", err)
	}
}
