package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/liliang-cn/roma/internal/domain"
	"github.com/liliang-cn/roma/internal/romapath"
)

// Registry provides discoverable agent profiles.
type Registry struct {
	builtins map[string]domain.AgentProfile
	users    map[string]domain.AgentProfile
	path     string
}

// DefaultUserConfigPath returns the per-user registry config location.
func DefaultUserConfigPath() string {
	return filepath.Join(romapath.HomeDir(), "agents.json")
}

// NewRegistry constructs a registry from agent profiles.
func NewRegistry(profiles ...domain.AgentProfile) (*Registry, error) {
	registry := &Registry{
		builtins: make(map[string]domain.AgentProfile, len(profiles)),
		users:    make(map[string]domain.AgentProfile),
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
	for _, p := range profiles {
		if err := domain.ValidateAgentProfile(p); err != nil {
			return fmt.Errorf("validate agent %s: %w", p.ID, err)
		}
		r.users[p.ID] = p
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

	if err := os.MkdirAll(filepath.Dir(r.path), 0o755); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}

	profiles := make([]domain.AgentProfile, 0, len(r.users))
	for _, id := range r.sortedUserIDs() {
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

func (r *Registry) sortedUserIDs() []string {
	ids := make([]string, 0, len(r.users))
	for id := range r.users {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
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
	if err := domain.ValidateAgentProfile(profile); err != nil {
		return err
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
	for _, profile := range r.builtins {
		out = append(out, profile)
	}
	for _, profile := range r.users {
		out = append(out, profile)
	}
	slices.SortFunc(out, func(a, b domain.AgentProfile) int {
		if a.DisplayName == b.DisplayName {
			switch {
			case a.ID < b.ID:
				return -1
			case a.ID > b.ID:
				return 1
			default:
				return 0
			}
		}
		switch {
		case a.DisplayName < b.DisplayName:
			return -1
		case a.DisplayName > b.DisplayName:
			return 1
		default:
			return 0
		}
	})
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
