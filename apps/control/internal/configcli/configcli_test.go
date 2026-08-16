package configcli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ai-daming/qianshou/apps/control/internal/config"
)

const v0Fixture = `{
  "projects": [
    {
      "id": "demo-m1",
      "repository": {"slug": "example/demo", "path": "/tmp/demo"},
      "milestone": {"number": 3},
      "integration": {"branch": "codex/demo", "worktree": "/tmp/demo.wt", "baseBranch": "main"},
      "refreshSeconds": 30,
      "defaults": {"developerEngine": "codex", "reviewerEngine": "claude"}
    }
  ]
}`

func writeV0(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "projects.json")
	if err := os.WriteFile(path, []byte(v0Fixture), 0o644); err != nil {
		t.Fatalf("write v0 fixture: %v", err)
	}
	return path
}

func TestMigrateThenCheckRoundTrip(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	source := writeV0(t, dir)

	if err := Run([]string{"migrate", "--source", source, "--home", home}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cfg, err := config.Load(home)
	if err != nil {
		t.Fatalf("load after migrate: %v", err)
	}
	if cfg.Projects[0].ID != "demo" || cfg.Projects[0].Scopes[0].ID != "m3" {
		t.Fatalf("unexpected migration result: %+v", cfg.Projects[0])
	}

	if err := Run([]string{"check", "--home", home, "--skip-git-binding"}); err != nil {
		t.Fatalf("check after migrate: %v", err)
	}
}

func TestMigrateRefusesOverwriteWithoutForce(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	source := writeV0(t, dir)

	if err := Run([]string{"migrate", "--source", source, "--home", home}); err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	err := Run([]string{"migrate", "--source", source, "--home", home})
	if err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("second migrate must refuse and mention --force, got: %v", err)
	}
	if err := Run([]string{"migrate", "--source", source, "--home", home, "--force"}); err != nil {
		t.Fatalf("forced migrate: %v", err)
	}
}

func TestMigrateRejectsUnknownHomeOutsideOverride(t *testing.T) {
	dir := t.TempDir()
	source := writeV0(t, dir)
	// The V0 fixture deliberately contains no git repository; the migrate
	// command must still write configuration because Git binding is a
	// separate check-time fact.
	if err := Run([]string{"migrate", "--source", source, "--home", filepath.Join(dir, "h2")}); err != nil {
		t.Fatalf("migrate without git binding: %v", err)
	}
}

func TestCheckFailsClosedOnMissingHome(t *testing.T) {
	err := Run([]string{"check", "--home", filepath.Join(t.TempDir(), "absent")})
	if err == nil {
		t.Fatalf("check on missing home must fail")
	}
}
