// Package config loads bb-review's JSON config file.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/anggorodewanto/bb-review/internal/bitbucket"
	"github.com/anggorodewanto/bb-review/internal/store"
)

// TokenEnv is the environment variable that overrides the config token.
const TokenEnv = "BB_REVIEW_TOKEN"

// DefaultListen is the default bind address (loopback only).
const DefaultListen = "127.0.0.1:7777"

// Config is ~/.config/bb-review/config.json.
type Config struct {
	Email        string   `json:"email"`
	Token        string   `json:"token"`
	Workspace    string   `json:"workspace"`
	Repos        []string `json:"repos"`
	Listen       string   `json:"listen"`
	AllowedHosts []string `json:"allowed_hosts"`
	// StatePath overrides the local state file (~/.local/share/bb-review/state.json).
	StatePath string `json:"state_path,omitempty"`
	// BaseURL overrides the Bitbucket API root (useful for tests and proxies).
	BaseURL string `json:"base_url,omitempty"`

	// Path is the file the config was loaded from (not part of the schema).
	Path string `json:"-"`
}

// DefaultPath is ~/.config/bb-review/config.json.
func DefaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", "config.json")
	}
	return filepath.Join(home, ".config", "bb-review", "config.json")
}

// Load reads and validates the config file at path. The token may be supplied via
// the BB_REVIEW_TOKEN environment variable, which wins over the file.
func Load(path string, log *slog.Logger) (*Config, error) {
	if log == nil {
		log = slog.New(slog.NewJSONHandler(os.Stderr, nil))
	}

	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("config file not found: %s (create it with mode 0600, see AGENTS.md)", path)
	}
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}

	var cfg Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	cfg.Path = path

	if info, statErr := os.Stat(path); statErr == nil && info.Mode().Perm()&0o077 != 0 {
		log.Warn("config file permissions are looser than 0600", "path", path, "mode", info.Mode().Perm().String())
	}

	if envToken := strings.TrimSpace(os.Getenv(TokenEnv)); envToken != "" {
		cfg.Token = envToken
	}

	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("invalid config %s: %w", path, err)
	}
	return &cfg, nil
}

// validate checks the required fields and repo entries. Values are never echoed,
// so the token cannot leak through an error message.
func (c *Config) validate() error {
	missing := make([]string, 0, 4)
	if strings.TrimSpace(c.Email) == "" {
		missing = append(missing, "email")
	}
	if strings.TrimSpace(c.Token) == "" {
		missing = append(missing, "token (or BB_REVIEW_TOKEN)")
	}
	if strings.TrimSpace(c.Workspace) == "" {
		missing = append(missing, "workspace")
	}
	if len(c.Repos) == 0 {
		missing = append(missing, "repos")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required field(s): %s", strings.Join(missing, ", "))
	}

	for _, entry := range c.Repos {
		if _, err := c.parseRepo(entry); err != nil {
			return err
		}
	}

	if strings.TrimSpace(c.Listen) == "" {
		c.Listen = DefaultListen
	}
	return nil
}

// parseRepo turns a "repo" or "ws/repo" entry into a Repo.
func (c *Config) parseRepo(entry string) (bitbucket.Repo, error) {
	trimmed := strings.TrimSpace(entry)
	if trimmed == "" {
		return bitbucket.Repo{}, errors.New("repos contains an empty entry")
	}
	parts := strings.Split(trimmed, "/")
	switch len(parts) {
	case 1:
		return bitbucket.Repo{Workspace: c.Workspace, Repo: parts[0]}, nil
	case 2:
		if parts[0] == "" || parts[1] == "" {
			return bitbucket.Repo{}, fmt.Errorf("repos entry %q must be \"repo\" or \"workspace/repo\"", trimmed)
		}
		return bitbucket.Repo{Workspace: parts[0], Repo: parts[1]}, nil
	default:
		return bitbucket.Repo{}, fmt.Errorf("repos entry %q must be \"repo\" or \"workspace/repo\"", trimmed)
	}
}

// RepoRefs returns the configured repositories with workspaces resolved.
func (c *Config) RepoRefs() []bitbucket.Repo {
	out := make([]bitbucket.Repo, 0, len(c.Repos))
	for _, entry := range c.Repos {
		repo, err := c.parseRepo(entry)
		if err != nil {
			continue
		}
		out = append(out, repo)
	}
	return out
}

// StateFile returns the resolved local state file path.
func (c *Config) StateFile() string {
	if strings.TrimSpace(c.StatePath) == "" {
		return store.DefaultPath()
	}
	return ExpandHome(c.StatePath)
}

// ExpandHome expands a leading "~/" to the user's home directory.
func ExpandHome(path string) string {
	if !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, path[2:])
}
