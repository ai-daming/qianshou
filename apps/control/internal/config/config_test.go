package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validConfigJSON = `{
  "version": 1,
  "engines": [
    {"id": "codex", "adapter": "codex-cli", "command": "codex"},
    {"id": "claude-code", "adapter": "claude-code-cli", "command": "claude"}
  ],
  "projects": [
    {
      "id": "qianshou",
      "repository": {"provider": "github", "slug": "ai-daming/qianshou"},
      "local": {"path": "/Users/user/work/qianshou"},
      "scopes": [
        {
          "id": "m1",
          "source": {"type": "milestone", "number": 1},
          "landing": {"type": "base-branch", "baseBranch": "main"}
        }
      ]
    }
  ]
}`

func mustParse(t *testing.T, data string) *Config {
	t.Helper()
	cfg, err := parse([]byte(data))
	if err != nil {
		t.Fatalf("parse valid config: %v", err)
	}
	return cfg
}

func TestParseAcceptsTargetShape(t *testing.T) {
	cfg := mustParse(t, validConfigJSON)
	if cfg.Version != 1 {
		t.Fatalf("version = %d, want 1", cfg.Version)
	}
	if len(cfg.Engines) != 2 || len(cfg.Projects) != 1 {
		t.Fatalf("engines = %d, projects = %d; want 2, 1", len(cfg.Engines), len(cfg.Projects))
	}
	p := cfg.Projects[0]
	if p.Repository.Slug != "ai-daming/qianshou" || p.Local.Path != "/Users/user/work/qianshou" {
		t.Fatalf("project locators not round-tripped: %+v", p)
	}
	if p.Scopes[0].Source.Type != "milestone" || p.Scopes[0].Source.Number != 1 {
		t.Fatalf("scope source not round-tripped: %+v", p.Scopes[0].Source)
	}
	if p.Scopes[0].Landing.Type != "base-branch" || p.Scopes[0].Landing.BaseBranch != "main" {
		t.Fatalf("landing not round-tripped: %+v", p.Scopes[0].Landing)
	}
}

func TestParseRejectsUnknownFields(t *testing.T) {
	cases := []struct {
		name string
		json string
		want string
	}{
		{
			name: "project declares engine role bindings",
			json: `{"version":1,"engines":[{"id":"codex","adapter":"a","command":"c"}],
				"projects":[{"id":"p","repository":{"provider":"github","slug":"o/r"},
				"local":{"path":"/tmp/p"},"defaults":{"developerEngine":"codex","reviewerEngine":"claude"},
				"scopes":[{"id":"s","source":{"type":"issue","number":1},"landing":{"type":"base-branch","baseBranch":"main"}}]}]}`,
			want: `defaults`,
		},
		{
			name: "project copies an issue list",
			json: `{"version":1,"engines":[{"id":"codex","adapter":"a","command":"c"}],
				"projects":[{"id":"p","repository":{"provider":"github","slug":"o/r"},
				"local":{"path":"/tmp/p"},"issues":[1,2,3],
				"scopes":[{"id":"s","source":{"type":"issue","number":1},"landing":{"type":"base-branch","baseBranch":"main"}}]}]}`,
			want: `issues`,
		},
		{
			name: "scope copies dependency edges",
			json: `{"version":1,"engines":[{"id":"codex","adapter":"a","command":"c"}],
				"projects":[{"id":"p","repository":{"provider":"github","slug":"o/r"},
				"local":{"path":"/tmp/p"},"scopes":[{"id":"s","source":{"type":"issue","number":1},
				"landing":{"type":"base-branch","baseBranch":"main"},"dependencies":[2]}]}]}`,
			want: `dependencies`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parse([]byte(tc.json))
			if err == nil {
				t.Fatalf("expected unknown-field rejection, got success")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not name the offending field %q", err.Error(), tc.want)
			}
		})
	}
}

func TestValidateFailClosed(t *testing.T) {
	base := func(mutate func(*Config)) *Config {
		cfg := mustParse(t, validConfigJSON)
		mutate(cfg)
		return cfg
	}
	cases := []struct {
		name string
		cfg  *Config
		want string
	}{
		{"version zero", base(func(c *Config) { c.Version = 0 }), "version"},
		{"no engines", base(func(c *Config) { c.Engines = nil }), "engines"},
		{"no projects", base(func(c *Config) { c.Projects = nil }), "projects"},
		{
			name: "duplicate engine ids",
			cfg: base(func(c *Config) {
				c.Engines = []Engine{{ID: "codex", Adapter: "a", Command: "c"}, {ID: "codex", Adapter: "a", Command: "c"}}
			}),
			want: "codex",
		},
		{
			name: "engine without command",
			cfg:  base(func(c *Config) { c.Engines[0].Command = "" }),
			want: "command",
		},
		{
			name: "provider other than github",
			cfg:  base(func(c *Config) { c.Projects[0].Repository.Provider = "gitlab" }),
			want: "provider",
		},
		{
			name: "malformed slug",
			cfg:  base(func(c *Config) { c.Projects[0].Repository.Slug = "not-a-slug" }),
			want: "slug",
		},
		{
			name: "relative local path",
			cfg:  base(func(c *Config) { c.Projects[0].Local.Path = "relative/path" }),
			want: "path",
		},
		{
			name: "project without scopes",
			cfg:  base(func(c *Config) { c.Projects[0].Scopes = nil }),
			want: "scopes",
		},
		{
			name: "duplicate project ids",
			cfg: base(func(c *Config) {
				c.Projects = append(c.Projects, c.Projects[0])
			}),
			want: "qianshou",
		},
		{
			name: "unknown scope source type",
			cfg: base(func(c *Config) {
				c.Projects[0].Scopes[0].Source.Type = "label"
			}),
			want: "source",
		},
		{
			name: "milestone selector zero",
			cfg: base(func(c *Config) {
				c.Projects[0].Scopes[0].Source.Number = 0
			}),
			want: "number",
		},
		{
			name: "milestone selector with stray root issue",
			cfg: base(func(c *Config) {
				c.Projects[0].Scopes[0].Source.RootIssueNumber = 9
			}),
			want: "rootIssueNumber",
		},
		{
			name: "issue-tree without root issue",
			cfg: base(func(c *Config) {
				c.Projects[0].Scopes[0].Source = Source{Type: "issue-tree"}
			}),
			want: "rootIssueNumber",
		},
		{
			name: "duplicate scope ids",
			cfg: base(func(c *Config) {
				c.Projects[0].Scopes = append(c.Projects[0].Scopes, c.Projects[0].Scopes[0])
			}),
			want: "m1",
		},
		{
			name: "unknown landing type",
			cfg: base(func(c *Config) {
				c.Projects[0].Scopes[0].Landing.Type = "trunk"
			}),
			want: "landing",
		},
		{
			name: "base branch empty",
			cfg: base(func(c *Config) {
				c.Projects[0].Scopes[0].Landing.BaseBranch = ""
			}),
			want: "baseBranch",
		},
		{
			name: "integration landing without worktree",
			cfg: base(func(c *Config) {
				c.Projects[0].Scopes[0].Landing = Landing{Type: "integration-branch", BaseBranch: "main", Branch: "b"}
			}),
			want: "worktree",
		},
		{
			name: "integration landing without branch",
			cfg: base(func(c *Config) {
				c.Projects[0].Scopes[0].Landing = Landing{Type: "integration-branch", BaseBranch: "main", Worktree: "/tmp/w"}
			}),
			want: "branch",
		},
		{
			name: "base landing carrying integration fields",
			cfg: base(func(c *Config) {
				c.Projects[0].Scopes[0].Landing.Branch = "codex/m7"
			}),
			want: "branch",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if err == nil {
				t.Fatalf("expected validation failure, got success")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not mention %q", err.Error(), tc.want)
			}
		})
	}
}

func TestValidateAcceptsUnmodifiedConfig(t *testing.T) {
	if err := mustParse(t, validConfigJSON).Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
}

func TestHomeResolution(t *testing.T) {
	t.Setenv("QIANSHOU_HOME", "/tmp/qs-home-override")
	home, err := DefaultHome()
	if err != nil {
		t.Fatalf("DefaultHome: %v", err)
	}
	if home != "/tmp/qs-home-override" {
		t.Fatalf("home = %q, want env override", home)
	}
	if got := ConfigPath(home); got != filepath.Join("/tmp/qs-home-override", "config.json") {
		t.Fatalf("ConfigPath = %q", got)
	}
}

func TestLoadMissingConfigFailsClosed(t *testing.T) {
	home := t.TempDir()
	if _, err := Load(home); err == nil {
		t.Fatalf("missing config must be an error, not an empty configuration")
	}
}

func TestSaveCreatesHomeWithTightPermissionsAndRoundTrips(t *testing.T) {
	home := filepath.Join(t.TempDir(), "nested", "qianshou")
	cfg := mustParse(t, validConfigJSON)
	if err := Save(home, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	info, err := os.Stat(ConfigPath(home))
	if err != nil {
		t.Fatalf("stat config: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("config file mode = %o, want 0600", perm)
	}
	loaded, err := Load(home)
	if err != nil {
		t.Fatalf("Load after Save: %v", err)
	}
	if err := loaded.Validate(); err != nil {
		t.Fatalf("round-tripped config invalid: %v", err)
	}
	if loaded.Projects[0].Scopes[0].Source.Number != cfg.Projects[0].Scopes[0].Source.Number {
		t.Fatalf("round trip lost scope selector")
	}
}

func TestParseRejectsTrailingSecondDocument(t *testing.T) {
	dirty := strings.TrimRight(validConfigJSON, "\n") + "\n{\"version\":2,\"engines\":[],\"projects\":[]}\n"
	if _, err := parse([]byte(dirty)); err == nil {
		t.Fatalf("trailing second JSON document must fail closed")
	}
}

func TestParseRejectsTrailingGarbage(t *testing.T) {
	for _, garbage := range []string{"]", "}", "garbage", "null"} {
		dirty := strings.TrimRight(validConfigJSON, "\n") + "\n" + garbage + "\n"
		if _, err := parse([]byte(dirty)); err == nil {
			t.Fatalf("trailing %q must fail closed", garbage)
		}
	}
}
