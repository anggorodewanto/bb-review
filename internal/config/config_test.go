package config

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeConfig(t *testing.T, body string, mode os.FileMode) string {
	t.Helper()
	// Keep an ambient BB_REVIEW_TOKEN from changing token-precedence results.
	t.Setenv(TokenEnv, "")
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("chmod config: %v", err)
	}
	return path
}

func bufferLogger() (*slog.Logger, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	return slog.New(slog.NewTextHandler(buf, nil)), buf
}

const validConfig = `{
  "email": "me@example.com",
  "token": "file-token",
  "workspace": "ws",
  "repos": ["repo-a", "other/repo-b"],
  "allowed_hosts": ["box.tail1234.ts.net"]
}`

func TestLoadValidConfig(t *testing.T) {
	path := writeConfig(t, validConfig, 0o600)
	logger, buf := bufferLogger()

	cfg, err := Load(path, logger)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Email != "me@example.com" || cfg.Token != "file-token" || cfg.Workspace != "ws" {
		t.Fatalf("cfg = %+v", cfg)
	}
	if cfg.Listen != DefaultListen {
		t.Fatalf("listen = %q, want default %q", cfg.Listen, DefaultListen)
	}
	if len(cfg.AllowedHosts) != 1 || cfg.AllowedHosts[0] != "box.tail1234.ts.net" {
		t.Fatalf("allowed hosts = %+v", cfg.AllowedHosts)
	}

	repos := cfg.RepoRefs()
	if len(repos) != 2 {
		t.Fatalf("repos = %+v", repos)
	}
	if repos[0].Workspace != "ws" || repos[0].Repo != "repo-a" {
		t.Fatalf("repo 0 = %+v", repos[0])
	}
	if repos[1].Workspace != "other" || repos[1].Repo != "repo-b" {
		t.Fatalf("repo 1 = %+v", repos[1])
	}
	if strings.Contains(buf.String(), "permissions") {
		t.Fatalf("unexpected permissions warning: %s", buf.String())
	}
}

func TestLoadEnvTokenWins(t *testing.T) {
	path := writeConfig(t, validConfig, 0o600)
	t.Setenv(TokenEnv, "env-token")
	logger, _ := bufferLogger()

	cfg, err := Load(path, logger)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Token != "env-token" {
		t.Fatalf("token = %q, want the env override", cfg.Token)
	}
}

func TestLoadMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.json")
	logger, _ := bufferLogger()
	_, err := Load(path, logger)
	if err == nil {
		t.Fatal("expected an error for a missing config")
	}
	if !strings.Contains(err.Error(), "not found") || !strings.Contains(err.Error(), path) {
		t.Fatalf("error = %v", err)
	}
}

func TestLoadMissingFields(t *testing.T) {
	path := writeConfig(t, `{"email":"me@example.com"}`, 0o600)
	logger, _ := bufferLogger()

	_, err := Load(path, logger)
	if err == nil {
		t.Fatal("expected a validation error")
	}
	for _, field := range []string{"token", "workspace"} {
		if !strings.Contains(err.Error(), field) {
			t.Fatalf("error %q does not mention %q", err, field)
		}
	}
}

func TestLoadDoesNotLeakToken(t *testing.T) {
	path := writeConfig(t, `{"token":"super-secret-token"}`, 0o600)
	logger, _ := bufferLogger()

	_, err := Load(path, logger)
	if err == nil {
		t.Fatal("expected a validation error")
	}
	if strings.Contains(err.Error(), "super-secret-token") {
		t.Fatalf("error leaks the token: %v", err)
	}
}

func TestLoosePermissionsWarn(t *testing.T) {
	path := writeConfig(t, validConfig, 0o644)
	logger, buf := bufferLogger()

	if _, err := Load(path, logger); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !strings.Contains(buf.String(), "0600") {
		t.Fatalf("expected a permissions warning, got %q", buf.String())
	}
}

func TestBadRepoEntry(t *testing.T) {
	path := writeConfig(t, `{"email":"me@example.com","token":"t","workspace":"ws","repos":["a/b/c"]}`, 0o600)
	logger, _ := bufferLogger()
	if _, err := Load(path, logger); err == nil {
		t.Fatal("expected an error for a bad repos entry")
	}
}

func TestListenAndStatePathFromConfig(t *testing.T) {
	path := writeConfig(t, `{
	  "email": "me@example.com", "token": "t", "workspace": "ws", "repos": ["repo-a"],
	  "listen": "127.0.0.1:9999", "state_path": "~/bb-review-state.json"
	}`, 0o600)
	logger, _ := bufferLogger()

	cfg, err := Load(path, logger)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Listen != "127.0.0.1:9999" {
		t.Fatalf("listen = %q", cfg.Listen)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home dir: %v", err)
	}
	if want := filepath.Join(home, "bb-review-state.json"); cfg.StateFile() != want {
		t.Fatalf("state file = %q, want %q", cfg.StateFile(), want)
	}
}

func TestDefaultStateFile(t *testing.T) {
	path := writeConfig(t, validConfig, 0o600)
	logger, _ := bufferLogger()
	cfg, err := Load(path, logger)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !strings.Contains(cfg.StateFile(), filepath.Join(".local", "share", "bb-review", "state.json")) {
		t.Fatalf("state file = %q", cfg.StateFile())
	}
}

func TestReposAreOptional(t *testing.T) {
	path := writeConfig(t, `{"email":"me@example.com","token":"t","workspace":"ws"}`, 0o600)
	logger, _ := bufferLogger()

	cfg, err := Load(path, logger)
	if err != nil {
		t.Fatalf("Load without repos: %v", err)
	}
	if got := cfg.RepoRefs(); len(got) != 0 {
		t.Fatalf("repos = %+v, want none", got)
	}
}

func TestTokenPrecedence(t *testing.T) {
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte("  file-token\n"), 0o600); err != nil {
		t.Fatalf("write token file: %v", err)
	}

	tests := []struct {
		name   string
		config string
		env    string
		want   string
	}{
		{
			name:   "env beats token and token_file",
			config: `{"email":"me@example.com","token":"file-token-value","token_file":"` + tokenFile + `","workspace":"ws"}`,
			env:    "env-token",
			want:   "env-token",
		},
		{
			name:   "token beats token_file",
			config: `{"email":"me@example.com","token":"file-token-value","token_file":"` + tokenFile + `","workspace":"ws"}`,
			want:   "file-token-value",
		},
		{
			name:   "token_file is trimmed",
			config: `{"email":"me@example.com","token_file":"` + tokenFile + `","workspace":"ws"}`,
			want:   "file-token",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := writeConfig(t, tc.config, 0o600)
			t.Setenv(TokenEnv, tc.env)
			logger, _ := bufferLogger()

			cfg, err := Load(path, logger)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.Token != tc.want {
				t.Fatalf("token = %q, want %q", cfg.Token, tc.want)
			}
		})
	}
}

func TestTokenFileMissingIsAnError(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope")
	path := writeConfig(t, `{"email":"me@example.com","token_file":"`+missing+`","workspace":"ws"}`, 0o600)
	logger, _ := bufferLogger()

	_, err := Load(path, logger)
	if err == nil {
		t.Fatal("expected an error for a missing token_file")
	}
	if !strings.Contains(err.Error(), "token_file") {
		t.Fatalf("error = %v", err)
	}
}

func TestTokenFileLoosePermissionsWarn(t *testing.T) {
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte("file-token"), 0o644); err != nil {
		t.Fatalf("write token file: %v", err)
	}
	if err := os.Chmod(tokenFile, 0o644); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	path := writeConfig(t, `{"email":"me@example.com","token_file":"`+tokenFile+`","workspace":"ws"}`, 0o600)
	logger, buf := bufferLogger()

	cfg, err := Load(path, logger)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Token != "file-token" {
		t.Fatalf("token = %q", cfg.Token)
	}
	if !strings.Contains(buf.String(), "token_file permissions") {
		t.Fatalf("expected a token_file warning, got %q", buf.String())
	}
}

func TestActiveDaysAndDiscoveryTTL(t *testing.T) {
	path := writeConfig(t, validConfig, 0o600)
	logger, _ := bufferLogger()
	cfg, err := Load(path, logger)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.ActiveDaysOrDefault(); got != DefaultActiveDays {
		t.Fatalf("active days = %d, want default %d", got, DefaultActiveDays)
	}
	if got := cfg.DiscoveryTTLOrDefault(); got != DefaultDiscoveryTTL {
		t.Fatalf("discovery ttl = %v, want default %v", got, DefaultDiscoveryTTL)
	}

	path = writeConfig(t, `{"email":"me@example.com","token":"t","workspace":"ws","active_days":3,"discovery_ttl":"90s"}`, 0o600)
	cfg, err = Load(path, logger)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.ActiveDaysOrDefault(); got != 3 {
		t.Fatalf("active days = %d, want 3", got)
	}
	if got := cfg.DiscoveryTTLOrDefault(); got != 90*time.Second {
		t.Fatalf("discovery ttl = %v, want 90s", got)
	}
}

func TestInvalidDiscoverySettings(t *testing.T) {
	logger, _ := bufferLogger()

	path := writeConfig(t, `{"email":"me@example.com","token":"t","workspace":"ws","discovery_ttl":"soon"}`, 0o600)
	if _, err := Load(path, logger); err == nil {
		t.Fatal("expected an error for a bad discovery_ttl")
	}

	path = writeConfig(t, `{"email":"me@example.com","token":"t","workspace":"ws","active_days":-2}`, 0o600)
	if _, err := Load(path, logger); err == nil {
		t.Fatal("expected an error for a negative active_days")
	}
}

func TestTokenFileFormats(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{name: "bare token", content: "ATATT-bare\n", want: "ATATT-bare"},
		{name: "bare token with whitespace", content: "  ATATT-bare  \n\n", want: "ATATT-bare"},
		{name: "env style", content: "BB_REVIEW_TOKEN=ATATT-env\n", want: "ATATT-env"},
		{name: "export and double quotes", content: "export BB_REVIEW_TOKEN=\"ATATT-double\"\n", want: "ATATT-double"},
		{name: "single quotes", content: "BB_REVIEW_TOKEN='ATATT-single'\n", want: "ATATT-single"},
		{name: "comments and other vars", content: "# my token\n\nOTHER=1\nBB_REVIEW_TOKEN=ATATT-clean\n", want: "ATATT-clean"},
		{name: "crlf", content: "BB_REVIEW_TOKEN=ATATT-crlf\r\n", want: "ATATT-crlf"},
		{name: "spaces around equals", content: "BB_REVIEW_TOKEN = ATATT-spaced \n", want: "ATATT-spaced"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tokenFile := filepath.Join(t.TempDir(), "token")
			if err := os.WriteFile(tokenFile, []byte(tc.content), 0o600); err != nil {
				t.Fatalf("write token file: %v", err)
			}
			path := writeConfig(t, `{"email":"me@example.com","token_file":"`+tokenFile+`","workspace":"ws"}`, 0o600)
			logger, _ := bufferLogger()

			cfg, err := Load(path, logger)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.Token != tc.want {
				t.Fatalf("token = %q, want %q", cfg.Token, tc.want)
			}
		})
	}
}

func TestParseTokenFileIgnoresUnrelatedAssignments(t *testing.T) {
	if got := parseTokenFile("OTHER=1\nBB_REVIEW_TOKEN=right\n"); got != "right" {
		t.Fatalf("parseTokenFile = %q, want right", got)
	}
	if got := parseTokenFile("\n# only comments\n"); got != "" {
		t.Fatalf("parseTokenFile = %q, want empty", got)
	}
}

func TestScanIntervals(t *testing.T) {
	path := writeConfig(t, validConfig, 0o600)
	logger, _ := bufferLogger()
	cfg, err := Load(path, logger)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.ScanIntervalOrDefault(); got != DefaultScanInterval {
		t.Fatalf("scan interval = %v, want %v", got, DefaultScanInterval)
	}
	if got := cfg.FullSweepIntervalOrDefault(); got != DefaultFullSweepInterval {
		t.Fatalf("full sweep interval = %v, want %v", got, DefaultFullSweepInterval)
	}

	path = writeConfig(t, `{"email":"me@example.com","token":"t","workspace":"ws","scan_interval":"30s","full_sweep_interval":"2h"}`, 0o600)
	cfg, err = Load(path, logger)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.ScanIntervalOrDefault(); got != 30*time.Second {
		t.Fatalf("scan interval = %v, want 30s", got)
	}
	if got := cfg.FullSweepIntervalOrDefault(); got != 2*time.Hour {
		t.Fatalf("full sweep interval = %v, want 2h", got)
	}
}

func TestInvalidScanIntervals(t *testing.T) {
	logger, _ := bufferLogger()
	for _, interval := range []string{"soon", "0s", "-5m"} {
		path := writeConfig(t, `{"email":"me@example.com","token":"t","workspace":"ws","scan_interval":"`+interval+`"}`, 0o600)
		if _, err := Load(path, logger); err == nil {
			t.Fatalf("expected an error for scan_interval=%q", interval)
		}
	}
}
