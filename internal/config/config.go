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
//
// Deprecated: the reviewer list is now maintained by the scanner, which re-reads
// discovery on every scan tick. The config key is still accepted and validated.
const DefaultDiscoveryTTL = 10 * time.Minute

// DefaultScanInterval is how often the reviewer scanner runs a tick.
const DefaultScanInterval = 60 * time.Second

// DefaultFullSweepInterval is how often the scanner re-queries every active repo.
const DefaultFullSweepInterval = 6 * time.Hour

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
	// DiscoveryTTL is deprecated: discovery is re-read on every scan tick.
	DiscoveryTTL string `json:"discovery_ttl,omitempty"`
	// ScanInterval is how often the reviewer scanner runs, e.g. "60s".
	ScanInterval string `json:"scan_interval,omitempty"`
	// FullSweepInterval is how often every active repo is re-queried, e.g. "6h".
	FullSweepInterval string `json:"full_sweep_interval,omitempty"`
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
	c.Token = parseTokenFile(string(raw))
	return nil
}

// parseTokenFile accepts either a bare token or env style content such as
// `BB_REVIEW_TOKEN=ATATT...` / `export BB_REVIEW_TOKEN="ATATT..."`. Blank lines and
// `#` comments are ignored, and the value may be single or double quoted.
func parseTokenFile(raw string) string {
	bare := ""
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		key, value, isAssignment := strings.Cut(line, "=")
		if isAssignment {
			if strings.TrimSpace(key) == TokenEnv {
				return unquoteToken(strings.TrimSpace(value))
			}
			continue
		}
		if bare == "" {
			bare = unquoteToken(line)
		}
	}
	return bare
}

// unquoteToken strips one layer of matching single or double quotes.
func unquoteToken(value string) string {
	if len(value) < 2 {
		return value
	}
	first, last := value[0], value[len(value)-1]
	if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
		return value[1 : len(value)-1]
	}
	return value
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
	for field, raw := range map[string]string{
		"discovery_ttl":       c.DiscoveryTTL,
		"scan_interval":       c.ScanInterval,
		"full_sweep_interval": c.FullSweepInterval,
	} {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		d, err := time.ParseDuration(strings.TrimSpace(raw))
		if err != nil {
			return fmt.Errorf("%s must be a duration such as \"10m\": %w", field, err)
		}
		if d <= 0 {
			return fmt.Errorf("%s must be positive", field)
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
//
// Deprecated: the scanner re-reads discovery every tick; kept for config compatibility.
func (c *Config) DiscoveryTTLOrDefault() time.Duration {
	return durationOrDefault(c.DiscoveryTTL, DefaultDiscoveryTTL)
}

// ScanIntervalOrDefault returns how often the reviewer scanner runs.
func (c *Config) ScanIntervalOrDefault() time.Duration {
	return durationOrDefault(c.ScanInterval, DefaultScanInterval)
}

// FullSweepIntervalOrDefault returns how often the scanner re-queries every repo.
func (c *Config) FullSweepIntervalOrDefault() time.Duration {
	return durationOrDefault(c.FullSweepInterval, DefaultFullSweepInterval)
}

// durationOrDefault parses raw, falling back to def for empty or invalid values.
func durationOrDefault(raw string, def time.Duration) time.Duration {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return def
	}
	parsed, err := time.ParseDuration(trimmed)
	if err != nil || parsed <= 0 {
		return def
	}
	return parsed
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
