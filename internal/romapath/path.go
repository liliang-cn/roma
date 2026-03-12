package romapath

import (
	"os"
	"path/filepath"
	"strings"
)

// HomeDir returns the canonical ROMA home directory.
func HomeDir() string {
	if override := strings.TrimSpace(os.Getenv("ROMA_HOME")); override != "" {
		return filepath.Clean(override)
	}
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return filepath.Clean(".roma")
	}
	return filepath.Join(home, ".roma")
}

// ControlDir returns the canonical ROMA control-plane directory.
func ControlDir() string {
	return HomeDir()
}

// ControlJoin returns a path rooted under the canonical ROMA control-plane directory.
func ControlJoin(elems ...string) string {
	parts := append([]string{ControlDir()}, elems...)
	return filepath.Join(parts...)
}

// WorkspaceStateDir returns the workspace-scoped ROMA execution directory.
func WorkspaceStateDir(workDir string) string {
	cleaned := filepath.Clean(strings.TrimSpace(workDir))
	if cleaned == "" || cleaned == "." {
		return filepath.Clean(".roma")
	}
	if filepath.Base(cleaned) == ".roma" {
		return cleaned
	}
	return filepath.Join(cleaned, ".roma")
}

// WorkspaceJoin returns a path rooted under the workspace-scoped execution directory.
func WorkspaceJoin(workDir string, elems ...string) string {
	parts := append([]string{WorkspaceStateDir(workDir)}, elems...)
	return filepath.Join(parts...)
}

// StateDir is retained as an alias for workspace-scoped execution state.
func StateDir(workDir string) string {
	return WorkspaceStateDir(workDir)
}

// Join is retained as an alias for workspace-scoped execution paths.
func Join(workDir string, elems ...string) string {
	parts := append([]string{WorkspaceStateDir(workDir)}, elems...)
	return filepath.Join(parts...)
}
