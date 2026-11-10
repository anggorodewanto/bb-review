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
	"time"

	"github.com/anggorodewanto/bb-review/internal/bitbucket"
	"github.com/anggorodewanto/bb-review/internal/store"
)

// TokenEnv is the environment variable that overrides the config token.
const TokenEnv = "BB_REVIEW_TOKEN"

// DefaultListen is the default bind address (loopback only).
const DefaultListen = "127.0.0.1:7777"

// DefaultActiveDays is the repo-discovery window: repos updated within this many
// days are considered active.
const DefaultActiveDays = 14

// DefaultDiscoveryTTL is how long the discovered repo list is cached.
const DefaultDiscoveryTTL = 10 * time.Minute

// Config is ~/.config/bb-review/config.json.
type Config struct {
	Email string `json:"email"`
	Token string `json:"token"`
	// TokenFile is an alternative to token: a file holding the API token.
	// Precedence: BB_REVIEW_TOKEN > token > token_file.
	TokenFile    string   `json:"token_file,omitempty"`
	Workspace    string   `json:"workspace"`
	Repos        []string `json:"repos"` // optional "always check" list; empty = discovery only
	Listen       string   `json:"listen"`
	AllowedHosts []string `json:"allowed_hosts"`
	// ActiveDays is the repo-discovery window in days (default 14).
	ActiveDays int `json:"active_days,omitempty"`
	// DiscoveryTTL is how long the discovered repo list is cached, e.g. "10m".
	DiscoveryTTL string `json:"discovery_ttl,omitempty"`
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

	if err := cfg.resolveToken(log); err != nil {
		return nil, fmt.Errorf("invalid config %s: %w", path, err)
	}

	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("invalid config %s: %w", path, err)
	}
	return &cfg, nil
}

// resolveToken applies the token precedence env > token > token_file and trims
// the result. Token values are never logged or echoed in errors.
func (c *Config) resolveToken(log *slog.Logger) error {
	if envToken := strings.TrimSpace(os.Getenv(TokenEnv)); envToken != "" {
		c.Token = envToken
		return nil
	}
	if token := strings.TrimSpace(c.Token); token != "" {
		c.Token = token
		return nil
	}

	path := strings.TrimSpace(c.TokenFile)
	if path == "" {
		return nil
	}
	path = ExpandHome(path)

	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("read token_file %s: %w", path, err)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		log.Warn("token_file permissions are looser than 0600", "path", path, "mode", perm.String())
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read token_file %s: %w", path, err)
	}
	c.Token = strings.TrimSpace(string(raw))
	return nil
}

// validate checks the required fields and repo entries. Values are never echoed,
// so the token cannot leak through an error message.
func (c *Config) validate() error {
	missing := make([]string, 0, 3)
	if strings.TrimSpace(c.Email) == "" {
		missing = append(missing, "email")
	}
	if strings.TrimSpace(c.Token) == "" {
		missing = append(missing, "token (set token, token_file or BB_REVIEW_TOKEN)")
	}
	if strings.TrimSpace(c.Workspace) == "" {
		missing = append(missing, "workspace")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required field(s): %s", strings.Join(missing, ", "))
	}

	for _, entry := range c.Repos {
		if _, err := c.parseRepo(entry); err != nil {
			return err
		}
	}

	if c.ActiveDays < 0 {
		return fmt.Errorf("active_days must not be negative")
	}
	if ttl := strings.TrimSpace(c.DiscoveryTTL); ttl != "" {
		if _, err := time.ParseDuration(ttl); err != nil {
			return fmt.Errorf("discovery_ttl must be a duration such as \"10m\": %w", err)
		}
	}

	if strings.TrimSpace(c.Listen) == "" {
		c.Listen = DefaultListen
	}
	return nil
}

// ActiveDaysOrDefault returns the repo-discovery window in days.
func (c *Config) ActiveDaysOrDefault() int {
	if c.ActiveDays <= 0 {
		return DefaultActiveDays
	}
	return c.ActiveDays
}

// DiscoveryTTLOrDefault returns how long the discovered repo list is cached.
func (c *Config) DiscoveryTTLOrDefault() time.Duration {
	if strings.TrimSpace(c.DiscoveryTTL) == "" {
		return DefaultDiscoveryTTL
	}
	ttl, err := time.ParseDuration(c.DiscoveryTTL)
	if err != nil || ttl <= 0 {
		return DefaultDiscoveryTTL
	}
	return ttl
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
