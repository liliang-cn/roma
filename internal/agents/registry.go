package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/liliang-cn/roma/internal/domain"
	"github.com/liliang-cn/roma/internal/romapath"
)

// Registry provides discoverable agent profiles.
type Registry struct {
	builtins  map[string]domain.AgentProfile
	users     map[string]domain.AgentProfile
	userOrder []string
	path      string
}

// DefaultUserConfigPath returns the per-user registry config location.
func DefaultUserConfigPath() string {
	return filepath.Join(romapath.HomeDir(), "agents.json")
}

// NewRegistry constructs a registry from agent profiles.
func NewRegistry(profiles ...domain.AgentProfile) (*Registry, error) {
	registry := &Registry{
		builtins:  make(map[string]domain.AgentProfile, len(profiles)),
		users:     make(map[string]domain.AgentProfile),
		userOrder: make([]string, 0),
	}

	for _, profile := range profiles {
		if err := domain.ValidateAgentProfile(profile); err != nil {
			return nil, err
		}
		if _, exists := registry.builtins[profile.ID]; exists {
			return nil, fmt.Errorf("agent profile %s already exists", profile.ID)
		}
		registry.builtins[profile.ID] = profile
	}

	return registry, nil
}

// DefaultRegistry returns an empty registry backed only by user-provided config.
func DefaultRegistry() (*Registry, error) {
	return NewRegistry()
}

// LoadUserConfig loads user-defined agents from the given path.
func (r *Registry) LoadUserConfig(path string) error {
	if r.path == "" {
		r.path = path
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read agent config: %w", err)
	}

	var profiles []domain.AgentProfile
	if err := json.Unmarshal(data, &profiles); err != nil {
		return fmt.Errorf("unmarshal agent config: %w", err)
	}

	r.users = make(map[string]domain.AgentProfile, len(profiles))
	r.userOrder = make([]string, 0, len(profiles))
	for _, p := range profiles {
		p = normalizeProfile(p)
		if err := domain.ValidateAgentProfile(p); err != nil {
			return fmt.Errorf("validate agent %s: %w", p.ID, err)
		}
		if _, exists := r.users[p.ID]; exists {
			return fmt.Errorf("duplicate agent id %s in config", p.ID)
		}
		r.users[p.ID] = p
		r.userOrder = append(r.userOrder, p.ID)
	}
	return nil
}

// SetUserConfigPath sets the path used by SaveUserConfig.
func (r *Registry) SetUserConfigPath(path string) {
	r.path = path
}

// UserConfigPath returns the current save path.
func (r *Registry) UserConfigPath() string {
	return r.path
}

// IsBuiltin reports whether the profile id is part of the default registry.
func (r *Registry) IsBuiltin(id string) bool {
	_, ok := r.builtins[id]
	return ok
}

// SaveUserConfig saves user-defined agents to the registry's path.
func (r *Registry) SaveUserConfig() error {
	if r.path == "" {
		return fmt.Errorf("no config path set")
	}

	return withUserConfigLock(r.path, func() error {
		return r.saveUserConfigUnlocked()
	})
}

func (r *Registry) saveUserConfigUnlocked() error {
	if r.path == "" {
		return fmt.Errorf("no config path set")
	}

	if err := os.MkdirAll(filepath.Dir(r.path), 0o755); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}

	profiles := make([]domain.AgentProfile, 0, len(r.userOrder))
	for _, id := range r.userOrder {
		profiles = append(profiles, r.users[id])
	}

	data, err := json.MarshalIndent(profiles, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal agent config: %w", err)
	}

	if err := os.WriteFile(r.path, data, 0o644); err != nil {
		return fmt.Errorf("write agent config: %w", err)
	}
	return nil
}

// AddUserProfile performs an atomic read-modify-write update against the user config.
func (r *Registry) AddUserProfile(profile domain.AgentProfile) error {
	profile = normalizeProfile(profile)
	if err := domain.ValidateAgentProfile(profile); err != nil {
		return err
	}
	return r.mutateUserConfig(func() error {
		if _, exists := r.users[profile.ID]; !exists {
			r.userOrder = append(r.userOrder, profile.ID)
		}
		r.users[profile.ID] = profile
		return nil
	})
}

// RemoveUserProfile performs an atomic read-modify-write removal against the user config.
func (r *Registry) RemoveUserProfile(id string) error {
	return r.mutateUserConfig(func() error {
		if _, exists := r.users[id]; !exists {
			return fmt.Errorf("agent %s not found", id)
		}
		delete(r.users, id)
		for i, item := range r.userOrder {
			if item == id {
				r.userOrder = append(r.userOrder[:i], r.userOrder[i+1:]...)
				break
			}
		}
		return nil
	})
}

func (r *Registry) mutateUserConfig(mutate func() error) error {
	if r == nil {
		return fmt.Errorf("registry is nil")
	}
	if r.path == "" {
		return fmt.Errorf("no config path set")
	}
	return withUserConfigLock(r.path, func() error {
		if err := r.LoadUserConfig(r.path); err != nil {
			return err
		}
		if err := mutate(); err != nil {
			return err
		}
		return r.saveUserConfigUnlocked()
	})
}

func withUserConfigLock(path string, fn func() error) error {
	lockPath := path + ".lock"
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if err := os.Mkdir(lockPath, 0o755); err == nil {
			break
		} else if !os.IsExist(err) {
			return fmt.Errorf("acquire user config lock: %w", err)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("acquire user config lock: timeout")
		}
		time.Sleep(10 * time.Millisecond)
	}
	defer os.RemoveAll(lockPath)
	return fn()
}

// Register is kept for compatibility but adds to builtins.
func (r *Registry) Register(profile domain.AgentProfile) error {
	if err := domain.ValidateAgentProfile(profile); err != nil {
		return fmt.Errorf("validate agent profile: %w", err)
	}
	if _, exists := r.builtins[profile.ID]; exists {
		return fmt.Errorf("agent profile %s already exists in built-ins", profile.ID)
	}
	r.builtins[profile.ID] = profile
	return nil
}

// Add adds a user-defined profile.
func (r *Registry) Add(profile domain.AgentProfile) error {
	profile = normalizeProfile(profile)
	if err := domain.ValidateAgentProfile(profile); err != nil {
		return err
	}
	if _, exists := r.users[profile.ID]; !exists {
		r.userOrder = append(r.userOrder, profile.ID)
	}
	r.users[profile.ID] = profile
	return nil
}

// Remove removes a user-defined profile.
func (r *Registry) Remove(id string) error {
	if _, exists := r.users[id]; !exists {
		return fmt.Errorf("agent %s not found", id)
	}
	delete(r.users, id)
	for i, item := range r.userOrder {
		if item == id {
			r.userOrder = append(r.userOrder[:i], r.userOrder[i+1:]...)
			break
		}
	}
	return nil
}

// Get returns an agent by ID or alias.
func (r *Registry) Get(idOrAlias string) (domain.AgentProfile, bool) {
	needle := strings.TrimSpace(strings.ToLower(idOrAlias))

	for _, p := range r.users {
		if matchesProfile(p, needle) {
			return p, true
		}
	}

	for _, p := range r.builtins {
		if matchesProfile(p, needle) {
			return p, true
		}
	}

	return domain.AgentProfile{}, false
}

// List returns sorted agent profiles.
func (r *Registry) List(_ context.Context) []domain.AgentProfile {
	out := make([]domain.AgentProfile, 0, len(r.builtins)+len(r.users))
	for _, id := range r.userOrder {
		if profile, ok := r.users[id]; ok {
			out = append(out, profile)
		}
	}
	builtinIDs := make([]string, 0, len(r.builtins))
	for id := range r.builtins {
		if _, exists := r.users[id]; exists {
			continue
		}
		builtinIDs = append(builtinIDs, id)
	}
	slices.Sort(builtinIDs)
	for _, id := range builtinIDs {
		out = append(out, r.builtins[id])
	}
	return out
}

// DefaultProfile returns the first configured agent.
func (r *Registry) DefaultProfile(ctx context.Context) (domain.AgentProfile, error) {
	profiles := r.List(ctx)
	if len(profiles) == 0 {
		return domain.AgentProfile{}, fmt.Errorf("no agents configured; use roma agent add <id> <name> <path> ...")
	}
	return profiles[0], nil
}

func matchesProfile(profile domain.AgentProfile, needle string) bool {
	if strings.EqualFold(profile.ID, needle) || strings.EqualFold(profile.Command, needle) {
		return true
	}
	for _, alias := range profile.Aliases {
		if strings.EqualFold(alias, needle) {
			return true
		}
	}
	return false
}

func normalizeProfile(profile domain.AgentProfile) domain.AgentProfile {
	command := strings.ToLower(filepath.Base(strings.TrimSpace(profile.Command)))
	if len(profile.HealthcheckArgs) == 0 {
		profile.HealthcheckArgs = []string{"--help"}
	}
	if len(profile.Args) > 0 || command == "" {
		return profile
	}

	switch command {
	case "codex":
		profile.Args = []string{"exec", "--full-auto", "--skip-git-repo-check", "--ephemeral", "-C", "{cwd}", "{prompt}"}
		profile.UsePTY = true
	case "gemini":
		profile.Args = []string{"-p", "{prompt}", "--approval-mode", "auto_edit"}
		profile.UsePTY = true
	case "copilot":
		profile.Args = []string{"-p", "{prompt}", "--allow-all-tools", "--allow-all-paths", "--allow-all-urls", "-s"}
		profile.UsePTY = true
	case "claude":
		profile.Args = []string{"-p", "{prompt}", "--permission-mode", "acceptEdits"}
		profile.UsePTY = true
	}
	return profile
}
