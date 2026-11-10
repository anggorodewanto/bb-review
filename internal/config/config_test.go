package config

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, body string, mode os.FileMode) string {
	t.Helper()
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
	t.Setenv(TokenEnv, "env-token")
	path := writeConfig(t, validConfig, 0o600)
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
	for _, field := range []string{"token", "workspace", "repos"} {
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
