package config

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func initRepository(t *testing.T, remote string) string {
	t.Helper()
	dir := t.TempDir()
	if out, err := exec.Command("git", "init", "-b", "main", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	if out, err := exec.Command("git", "-C", dir, "remote", "add", "origin", remote).CombinedOutput(); err != nil {
		t.Fatalf("git remote add: %v\n%s", err, out)
	}
	return dir
}

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadV1Projects(t *testing.T) {
	qianshou := initRepository(t, "https://github.com/ai-daming/qianshou.git")
	mamamate := initRepository(t, "git@github.com:ai-daming/mamamate.git")
	path := writeConfig(t, `{
  "version": 1,
  "engines": [{"id":"codex","adapter":"codex-cli","command":"codex"}],
  "projects": [
    {"id":"qianshou","repository":{"provider":"github","slug":"ai-daming/qianshou"},"local":{"path":`+quote(qianshou)+`}},
    {"id":"mamamate","repository":{"provider":"github","slug":"ai-daming/mamamate"},"local":{"path":`+quote(mamamate)+`}}
  ]
}`)

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got.Projects) != 2 || got.Projects[1].Repository.Slug != "ai-daming/mamamate" {
		t.Fatalf("Projects = %+v", got.Projects)
	}
}

func TestVerifyMainCheckoutTimesOutHungGit(t *testing.T) {
	if gitCommandTimeout != 5*time.Second {
		t.Fatalf("gitCommandTimeout = %s, want 5s", gitCommandTimeout)
	}
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	git := filepath.Join(bin, "git")
	if err := os.WriteFile(git, []byte("#!/bin/sh\nexec sleep 30\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	started := time.Now()
	err := verifyMainCheckoutWithTimeout(repo, "ai-daming/qianshou", 20*time.Millisecond)
	if err == nil {
		t.Fatal("hung git command was accepted")
	}
	if elapsed := time.Since(started); elapsed >= time.Second {
		t.Fatalf("hung git command returned after %s, want prompt cancellation", elapsed)
	}
	if !strings.Contains(err.Error(), "cannot read origin from local checkout") {
		t.Fatalf("error = %v, want existing sanitized error", err)
	}
	if strings.Contains(err.Error(), repo) || strings.Contains(err.Error(), "sleep 30") {
		t.Fatalf("error leaked command details: %v", err)
	}
}

func TestLoadRejectsInvalidOrLegacyConfiguration(t *testing.T) {
	repo := initRepository(t, "https://github.com/ai-daming/qianshou.git")
	validProject := `{"id":"qianshou","repository":{"provider":"github","slug":"ai-daming/qianshou"},"local":{"path":` + quote(repo) + `}}`
	cases := []struct {
		name string
		body string
		want string
	}{
		{"missing engines", `{"version":1,"projects":[]}`, "engines"},
		{"null projects", `{"version":1,"engines":[],"projects":null}`, "projects"},
		{"duplicate JSON key", `{"version":1,"version":2,"engines":[],"projects":[]}`, "duplicate"},
		{"unknown top-level field", `{"version":1,"engines":[],"projects":[],"extra":true}`, "unknown"},
		{"legacy scopes", `{"version":1,"engines":[],"projects":[` + strings.TrimSuffix(validProject, "}") + `,"scopes":[]}]}`, "scopes"},
		{"legacy landing", `{"version":1,"engines":[],"projects":[` + strings.TrimSuffix(validProject, "}") + `,"landing":{}}]}`, "landing"},
		{"relative path", `{"version":1,"engines":[],"projects":[{"id":"q","repository":{"provider":"github","slug":"ai-daming/qianshou"},"local":{"path":"relative"}}]}`, "absolute"},
		{"wrong version", `{"version":2,"engines":[],"projects":[]}`, "version"},
		{"duplicate project id", `{"version":1,"engines":[],"projects":[` + validProject + `,` + validProject + `]}`, "duplicate"},
		{"duplicate repository", `{"version":1,"engines":[],"projects":[` + validProject + `,{"id":"other","repository":{"provider":"github","slug":"AI-DAMING/QIANSHOU"},"local":{"path":` + quote(repo) + `}}]}`, "duplicate"},
		{"checkout mismatch", `{"version":1,"engines":[],"projects":[{"id":"q","repository":{"provider":"github","slug":"ai-daming/other"},"local":{"path":` + quote(repo) + `}}]}`, "does not match"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(writeConfig(t, tc.body))
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tc.want)) {
				t.Fatalf("Load error = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestLoadRejectsDuplicateEngineAndMissingRequiredFields(t *testing.T) {
	cases := []string{
		`{"version":1,"engines":[{"id":"codex","adapter":"codex-cli","command":"codex"},{"id":"codex","adapter":"codex-cli","command":"other"}],"projects":[]}`,
		`{"version":1,"engines":[{"id":"","adapter":"codex-cli","command":"codex"}],"projects":[]}`,
		`{"version":1,"engines":[],"projects":[{"id":"q","repository":{"provider":"github","slug":""},"local":{"path":"/tmp"}}]}`,
	}
	for _, body := range cases {
		if _, err := Load(writeConfig(t, body)); err == nil {
			t.Fatalf("invalid config accepted: %s", body)
		}
	}
}

func TestDefaultPathUsesQianshouHomeWithoutExposingEnvironment(t *testing.T) {
	t.Setenv("QIANSHOU_HOME", "/tmp/qianshou-test-home")
	if got := DefaultPath(); got != "/tmp/qianshou-test-home/config.json" {
		t.Fatalf("DefaultPath = %q", got)
	}
}

func quote(value string) string {
	return `"` + strings.ReplaceAll(value, `\`, `\\`) + `"`
}
