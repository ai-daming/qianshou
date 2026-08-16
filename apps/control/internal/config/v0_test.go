package config

import (
	"strings"
	"testing"
)

// mirror of the real config/projects.json on 2026-08-16
const v0Sample = `{
  "projects": [
    {
      "id": "mamamate-m7",
      "repository": {"slug": "ai-daming/mamamate", "path": "/Users/user/work/mamamate/mamamate"},
      "milestone": {"number": 7},
      "integration": {
        "branch": "codex/milestone-7-poster-engine-baseline",
        "worktree": "/Users/user/work/mamamate/mamamate.worktrees/mamamate/milestone-7-poster-engine-baseline",
        "baseBranch": "main"
      },
      "refreshSeconds": 30,
      "defaults": {"developerEngine": "codex", "reviewerEngine": "claude"}
    }
  ]
}`

func TestMigrateV0MapsTargetShape(t *testing.T) {
	cfg, report, err := MigrateV0([]byte(v0Sample))
	if err != nil {
		t.Fatalf("MigrateV0: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("migrated config invalid: %v", err)
	}
	if len(cfg.Projects) != 1 {
		t.Fatalf("projects = %d, want 1", len(cfg.Projects))
	}
	p := cfg.Projects[0]
	if p.ID != "mamamate" {
		t.Fatalf("project id = %q, want repository-derived \"mamamate\" (Project 是仓库，不是里程碑)", p.ID)
	}
	if p.Repository.Slug != "ai-daming/mamamate" || p.Repository.Provider != "github" {
		t.Fatalf("repository not mapped: %+v", p.Repository)
	}
	if p.Local.Path != "/Users/user/work/mamamate/mamamate" {
		t.Fatalf("local path not mapped: %+v", p.Local)
	}
	if len(p.Scopes) != 1 {
		t.Fatalf("scopes = %d, want 1", len(p.Scopes))
	}
	s := p.Scopes[0]
	if s.ID != "m7" {
		t.Fatalf("scope id = %q, want \"m7\"", s.ID)
	}
	if s.Source.Type != "milestone" || s.Source.Number != 7 {
		t.Fatalf("scope source not mapped: %+v", s.Source)
	}
	if s.Landing.Type != "integration-branch" || s.Landing.BaseBranch != "main" ||
		s.Landing.Branch != "codex/milestone-7-poster-engine-baseline" {
		t.Fatalf("landing not mapped: %+v", s.Landing)
	}
	if len(cfg.Engines) != 2 {
		t.Fatalf("engines = %d, want builtin two", len(cfg.Engines))
	}
	if !report.DroppedRefreshSeconds || !report.DroppedEngineDefaults {
		t.Fatalf("migration report must record dropped V0-only fields: %+v", report)
	}
}

func TestMigrateV0MergesSameRepositoryIntoOneProject(t *testing.T) {
	two := `{"projects":[
    {
      "id": "mamamate-m7",
      "repository": {"slug": "ai-daming/mamamate", "path": "/Users/user/work/mamamate/mamamate"},
      "milestone": {"number": 7},
      "integration": {"branch": "codex/m7", "worktree": "/tmp/w7", "baseBranch": "main"},
      "refreshSeconds": 30,
      "defaults": {"developerEngine": "codex", "reviewerEngine": "claude"}
    },
    {
      "id": "mamamate-m8",
      "repository": {"slug": "ai-daming/mamamate", "path": "/Users/user/work/mamamate/mamamate"},
      "milestone": {"number": 8},
      "integration": {"branch": "codex/m8", "worktree": "/tmp/w8", "baseBranch": "main"},
      "refreshSeconds": 30,
      "defaults": {"developerEngine": "codex", "reviewerEngine": "claude"}
    }
  ]}`
	cfg, _, err := MigrateV0([]byte(two))
	if err != nil {
		t.Fatalf("MigrateV0: %v", err)
	}
	if len(cfg.Projects) != 1 {
		t.Fatalf("projects = %d, want 1 repository with two scopes", len(cfg.Projects))
	}
	if len(cfg.Projects[0].Scopes) != 2 {
		t.Fatalf("scopes = %d, want 2", len(cfg.Projects[0].Scopes))
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("merged config invalid: %v", err)
	}
}

func TestMigrateV0FailsClosedOnDrift(t *testing.T) {
	cases := []struct {
		name string
		json string
		want string
	}{
		{
			name: "v0 entry lists issues",
			json: `{"projects":[{"id":"x","repository":{"slug":"o/r","path":"/tmp/p"},"milestone":{"number":1},
				"integration":{"branch":"b","worktree":"/tmp/w","baseBranch":"main"},"issues":[1,2]}]}`,
			want: "issues",
		},
		{
			name: "v0 without milestone selector",
			json: `{"projects":[{"id":"x","repository":{"slug":"o/r","path":"/tmp/p"},
				"integration":{"branch":"b","worktree":"/tmp/w","baseBranch":"main"}}]}`,
			want: "milestone",
		},
		{
			name: "v0 without integration",
			json: `{"projects":[{"id":"x","repository":{"slug":"o/r","path":"/tmp/p"},"milestone":{"number":1}}]}`,
			want: "integration",
		},
		{
			name: "v0 milestone zero",
			json: `{"projects":[{"id":"x","repository":{"slug":"o/r","path":"/tmp/p"},"milestone":{"number":0},
				"integration":{"branch":"b","worktree":"/tmp/w","baseBranch":"main"}}]}`,
			want: "number",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := MigrateV0([]byte(tc.json))
			if err == nil {
				t.Fatalf("expected migration failure, got success")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not mention %q", err.Error(), tc.want)
			}
		})
	}
}

func TestMigrateV0RejectsTrailingSecondDocument(t *testing.T) {
	dirty := strings.TrimRight(v0Sample, "\n") + "\n{\"projects\":[]}\n"
	if _, _, err := MigrateV0([]byte(dirty)); err == nil {
		t.Fatalf("trailing second JSON document must fail closed")
	}
}

func TestMigrateV0RejectsTrailingGarbage(t *testing.T) {
	for _, garbage := range []string{"]", "}", "garbage"} {
		dirty := strings.TrimRight(v0Sample, "\n") + "\n" + garbage + "\n"
		if _, _, err := MigrateV0([]byte(dirty)); err == nil {
			t.Fatalf("trailing %q must fail closed", garbage)
		}
	}
}
