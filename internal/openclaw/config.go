// Package openclaw bridges TagIt to OpenClaw channels (WeChat, Telegram,
// iMessage, ...) over OpenClaw's own MCP stdio bridge (`openclaw mcp serve`).
//
// OpenClaw already exposes every connected chat channel as MCP tools, so TagIt
// speaks that documented interface instead of the Gateway's internal protocol:
// messages_send to reply, events_wait to receive. One adapter therefore covers
// all channels OpenClaw supports.
package openclaw

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"

	"github.com/liliang-cn/tagit/internal/chatbot"
)

// Config is the on-disk OpenClaw bridge configuration (~/.tagit/openclaw.json).
// Command/Args default to `openclaw mcp serve`; bindings map an OpenClaw session
// key (the conversation id) to a repo, exactly like the Feishu/Slack adapters.
type Config struct {
	Command  string           `json:"command"`
	Args     []string         `json:"args"`
	Bindings chatbot.Bindings `json:"bindings"`
}

// DefaultCommand and DefaultArgs launch OpenClaw's MCP channel bridge.
var (
	DefaultCommand = "openclaw"
	DefaultArgs    = []string{"mcp", "serve", "--claude-channel-mode", "off"}
)

// Load reads the config. A missing file means the feature is disabled:
// it returns (nil, false, nil). A present-but-broken file returns an error.
func Load(path string) (*Config, bool, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read openclaw config: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, false, fmt.Errorf("parse openclaw config: %w", err)
	}
	cfg.applyDefaults()
	return &cfg, true, nil
}

func (c *Config) applyDefaults() {
	if c.Command == "" {
		c.Command = DefaultCommand
	}
	if len(c.Args) == 0 {
		c.Args = append([]string(nil), DefaultArgs...)
	}
}

// BindingFor returns the binding for a session key.
func (c *Config) BindingFor(sessionKey string) (chatbot.Binding, bool) {
	return c.Bindings.For(sessionKey)
}

type configStore struct {
	path string
	mu   sync.Mutex
}

// NewConfigStore returns a BindingStore backed by the openclaw.json at path. It
// does read-modify-write of the whole config file, preserving command/args.
func NewConfigStore(path string) chatbot.BindingStore { return &configStore{path: path} }

func (s *configStore) load() (Config, error) {
	var cfg Config
	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return cfg, nil
	}
	if err != nil {
		return cfg, fmt.Errorf("read openclaw config: %w", err)
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("parse openclaw config: %w", err)
	}
	return cfg, nil
}

func (s *configStore) save(cfg Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("encode openclaw config: %w", err)
	}
	if err := os.WriteFile(s.path, data, 0o600); err != nil {
		return fmt.Errorf("write openclaw config: %w", err)
	}
	return nil
}

// For re-reads the file so external edits and restarts are picked up.
func (s *configStore) For(sessionKey string) (chatbot.Binding, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg, err := s.load()
	if err != nil {
		return chatbot.Binding{}, false
	}
	return cfg.Bindings.For(sessionKey)
}

func (s *configStore) Set(b chatbot.Binding) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg, err := s.load()
	if err != nil {
		return err
	}
	found := false
	for i := range cfg.Bindings {
		if cfg.Bindings[i].ChatID == b.ChatID {
			cfg.Bindings[i] = b
			found = true
			break
		}
	}
	if !found {
		cfg.Bindings = append(cfg.Bindings, b)
	}
	return s.save(cfg)
}

func (s *configStore) Delete(sessionKey string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg, err := s.load()
	if err != nil {
		return err
	}
	out := cfg.Bindings[:0]
	for _, b := range cfg.Bindings {
		if b.ChatID != sessionKey {
			out = append(out, b)
		}
	}
	cfg.Bindings = out
	return s.save(cfg)
}
