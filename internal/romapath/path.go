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

// StateDir returns the ROMA state directory for a workspace root.
func StateDir(workDir string) string {
	cleaned := filepath.Clean(strings.TrimSpace(workDir))
	if cleaned == "" || cleaned == "." {
		return filepath.Clean(".roma")
	}
	if filepath.Base(cleaned) == ".roma" {
		return cleaned
	}
	return filepath.Join(cleaned, ".roma")
}

// Join returns a path rooted under the ROMA state directory.
func Join(workDir string, elems ...string) string {
	parts := append([]string{StateDir(workDir)}, elems...)
	return filepath.Join(parts...)
}
