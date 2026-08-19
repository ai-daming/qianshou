package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadV1ContainsOnlyRunnerExecutionTrust(t *testing.T) {
	path := writeConfig(t, `{
  "version": 1,
  "runner": {"id":"runner-1","allowedRoots":["/Users/operator/work","/tmp/qianshou-tests"]},
  "engines": [{"id":"codex","adapter":"codex-cli","command":"codex"}]
}`)
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Runner.ID != "runner-1" || len(got.Runner.AllowedRoots) != 2 || len(got.Engines) != 1 {
		t.Fatalf("Config = %+v", got)
	}
}

func TestLoadExplicitlyRejectsProjectFieldsWithoutFallback(t *testing.T) {
	for _, projectField := range []string{
		`"projects":[]`,
		`"projects":null`,
		`"projects":[{"id":"qianshou","repository":{"provider":"github","slug":"ai-daming/qianshou"},"local":{"path":"/tmp/qianshou"}}]`,
	} {
		body := `{"version":1,"runner":{"id":"runner-1","allowedRoots":["/tmp"]},"engines":[],` + projectField + `}`
		_, err := Load(writeConfig(t, body))
		if err == nil || !strings.Contains(strings.ToLower(err.Error()), "projects") {
			t.Fatalf("legacy Project config error = %v", err)
		}
	}
}

func TestLoadRejectsInvalidRunnerOrEngineTrust(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"missing runner", `{"version":1,"engines":[]}`, "runner"},
		{"missing roots", `{"version":1,"runner":{"id":"r","allowedRoots":[]},"engines":[]}`, "allowedroots"},
		{"relative root", `{"version":1,"runner":{"id":"r","allowedRoots":["relative"]},"engines":[]}`, "absolute"},
		{"duplicate root", `{"version":1,"runner":{"id":"r","allowedRoots":["/tmp","/tmp"]},"engines":[]}`, "duplicate"},
		{"duplicate engine", `{"version":1,"runner":{"id":"r","allowedRoots":["/tmp"]},"engines":[{"id":"codex","adapter":"a","command":"c"},{"id":"CODEX","adapter":"b","command":"d"}]}`, "duplicate"},
		{"duplicate JSON key", `{"version":1,"version":1,"runner":{"id":"r","allowedRoots":["/tmp"]},"engines":[]}`, "duplicate"},
		{"unknown field", `{"version":1,"runner":{"id":"r","allowedRoots":["/tmp"]},"engines":[],"extra":true}`, "unknown"},
		{"wrong version", `{"version":2,"runner":{"id":"r","allowedRoots":["/tmp"]},"engines":[]}`, "version"},
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

func TestDefaultPathUsesQianshouHomeWithoutExposingEnvironment(t *testing.T) {
	t.Setenv("QIANSHOU_HOME", "/tmp/qianshou-test-home")
	if got := DefaultPath(); got != "/tmp/qianshou-test-home/config.json" {
		t.Fatalf("DefaultPath = %q", got)
	}
}
