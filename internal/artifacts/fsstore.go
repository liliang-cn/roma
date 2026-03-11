package artifacts

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/liliang-cn/roma/internal/domain"
)

// FileStore persists artifacts under the workspace .roma directory.
type FileStore struct {
	rootDir string
}

// NewFileStore constructs a file-backed artifact store.
func NewFileStore(workDir string) *FileStore {
	return &FileStore{
		rootDir: filepath.Join(workDir, ".roma", "artifacts"),
	}
}

// Save persists an artifact envelope as JSON.
func (s *FileStore) Save(ctx context.Context, envelope domain.ArtifactEnvelope) error {
	_ = ctx

	dir := filepath.Join(s.rootDir, envelope.SessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create artifact directory: %w", err)
	}

	path := filepath.Join(dir, envelope.ID+".json")
	raw, err := json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal artifact: %w", err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		return fmt.Errorf("write artifact: %w", err)
	}
	return nil
}

// Get loads an artifact by id from the store.
func (s *FileStore) Get(ctx context.Context, artifactID string) (domain.ArtifactEnvelope, error) {
	_ = ctx

	path, err := s.findArtifactPath(artifactID)
	if err != nil {
		return domain.ArtifactEnvelope{}, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return domain.ArtifactEnvelope{}, fmt.Errorf("read artifact: %w", err)
	}

	var envelope domain.ArtifactEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return domain.ArtifactEnvelope{}, fmt.Errorf("unmarshal artifact: %w", err)
	}
	return envelope, nil
}

// List returns stored artifacts, optionally filtered by session id.
func (s *FileStore) List(ctx context.Context, sessionID string) ([]domain.ArtifactEnvelope, error) {
	_ = ctx

	root := s.rootDir
	if sessionID != "" {
		root = filepath.Join(root, sessionID)
	}

	entries := make([]domain.ArtifactEnvelope, 0)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if os.IsNotExist(walkErr) {
				return nil
			}
			return walkErr
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".json") {
			return nil
		}

		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var envelope domain.ArtifactEnvelope
		if err := json.Unmarshal(raw, &envelope); err != nil {
			return err
		}
		entries = append(entries, envelope)
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("walk artifacts: %w", err)
	}

	slices.SortFunc(entries, func(a, b domain.ArtifactEnvelope) int {
		return a.CreatedAt.Compare(b.CreatedAt)
	})
	return entries, nil
}

func (s *FileStore) findArtifactPath(artifactID string) (string, error) {
	var found string
	err := filepath.WalkDir(s.rootDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if os.IsNotExist(walkErr) {
				return nil
			}
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if d.Name() == artifactID+".json" {
			found = path
			return fs.SkipAll
		}
		return nil
	})
	if err != nil && err != fs.SkipAll {
		return "", fmt.Errorf("search artifact: %w", err)
	}
	if found == "" {
		return "", fmt.Errorf("artifact %s not found", artifactID)
	}
	return found, nil
}
