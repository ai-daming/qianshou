// Package config loads the machine-local Qianshou configuration.
package config

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/ai-daming/qianshou/apps/control/internal/strictjson"
)

const gitCommandTimeout = 5 * time.Second

var (
	idPattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)
	slugPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
)

type Config struct {
	Version  int       `json:"version"`
	Engines  []Engine  `json:"engines"`
	Projects []Project `json:"projects"`
}

type Engine struct {
	ID      string `json:"id"`
	Adapter string `json:"adapter"`
	Command string `json:"command"`
}

type Project struct {
	ID         string     `json:"id"`
	Repository Repository `json:"repository"`
	Local      Local      `json:"local"`
}

type Repository struct {
	Provider string `json:"provider"`
	Slug     string `json:"slug"`
}

type Local struct {
	Path string `json:"path"`
}

func DefaultPath() string {
	if home := strings.TrimSpace(os.Getenv("QIANSHOU_HOME")); home != "" {
		return filepath.Join(home, "config.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".qianshou", "config.json")
	}
	return filepath.Join(home, ".qianshou", "config.json")
}

func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read Qianshou config: %w", err)
	}
	var raw struct {
		Version  *int       `json:"version"`
		Engines  *[]Engine  `json:"engines"`
		Projects *[]Project `json:"projects"`
	}
	if err := strictjson.Decode(data, &raw, true); err != nil {
		return Config{}, fmt.Errorf("invalid config: %w", err)
	}
	if raw.Version == nil {
		return Config{}, fmt.Errorf("config version is required")
	}
	if raw.Engines == nil {
		return Config{}, fmt.Errorf("config engines must be an array")
	}
	if raw.Projects == nil {
		return Config{}, fmt.Errorf("config projects must be an array")
	}
	cfg := Config{Version: *raw.Version, Engines: *raw.Engines, Projects: *raw.Projects}
	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (cfg Config) validate() error {
	if cfg.Version != 1 {
		return fmt.Errorf("unsupported config version %d; want version 1", cfg.Version)
	}
	engineIDs := make(map[string]struct{}, len(cfg.Engines))
	for i, engine := range cfg.Engines {
		if !idPattern.MatchString(engine.ID) {
			return fmt.Errorf("engines[%d].id is missing or invalid", i)
		}
		if strings.TrimSpace(engine.Adapter) == "" || strings.TrimSpace(engine.Command) == "" {
			return fmt.Errorf("engine %q requires adapter and command", engine.ID)
		}
		key := strings.ToLower(engine.ID)
		if _, exists := engineIDs[key]; exists {
			return fmt.Errorf("duplicate engine id %q", engine.ID)
		}
		engineIDs[key] = struct{}{}
	}

	projectIDs := make(map[string]struct{}, len(cfg.Projects))
	repositories := make(map[string]struct{}, len(cfg.Projects))
	for i, project := range cfg.Projects {
		if !idPattern.MatchString(project.ID) {
			return fmt.Errorf("projects[%d].id is missing or invalid", i)
		}
		idKey := strings.ToLower(project.ID)
		if _, exists := projectIDs[idKey]; exists {
			return fmt.Errorf("duplicate project id %q", project.ID)
		}
		projectIDs[idKey] = struct{}{}
		if project.Repository.Provider != "github" {
			return fmt.Errorf("project %q repository.provider must be github", project.ID)
		}
		if !slugPattern.MatchString(project.Repository.Slug) {
			return fmt.Errorf("project %q repository.slug must be owner/repo", project.ID)
		}
		repoKey := strings.ToLower(project.Repository.Slug)
		if _, exists := repositories[repoKey]; exists {
			return fmt.Errorf("duplicate repository slug %q", project.Repository.Slug)
		}
		repositories[repoKey] = struct{}{}
		if !filepath.IsAbs(project.Local.Path) {
			return fmt.Errorf("project %q local.path must be absolute", project.ID)
		}
		if err := verifyMainCheckout(project.Local.Path, project.Repository.Slug); err != nil {
			return fmt.Errorf("project %q: %w", project.ID, err)
		}
	}
	return nil
}

func verifyMainCheckout(path, wantSlug string) error {
	return verifyMainCheckoutWithTimeout(path, wantSlug, gitCommandTimeout)
}

func verifyMainCheckoutWithTimeout(path, wantSlug string, timeout time.Duration) error {
	gitDir, err := os.Stat(filepath.Join(path, ".git"))
	if err != nil || !gitDir.IsDir() {
		return fmt.Errorf("local.path is not a main Git checkout")
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "-C", path, "remote", "get-url", "origin").Output()
	if err != nil {
		return fmt.Errorf("cannot read origin from local checkout")
	}
	gotSlug, err := githubSlugFromRemote(strings.TrimSpace(string(out)))
	if err != nil {
		return err
	}
	if !strings.EqualFold(gotSlug, wantSlug) {
		return fmt.Errorf("local checkout repository %q does not match configured %q", gotSlug, wantSlug)
	}
	return nil
}

func githubSlugFromRemote(remote string) (string, error) {
	var slug string
	switch {
	case strings.HasPrefix(remote, "https://github.com/"):
		slug = strings.TrimPrefix(remote, "https://github.com/")
	case strings.HasPrefix(remote, "ssh://git@github.com/"):
		slug = strings.TrimPrefix(remote, "ssh://git@github.com/")
	case strings.HasPrefix(remote, "git@github.com:"):
		slug = strings.TrimPrefix(remote, "git@github.com:")
	default:
		return "", fmt.Errorf("origin is not a supported github.com URL")
	}
	slug = strings.TrimSuffix(slug, ".git")
	if !slugPattern.MatchString(slug) {
		return "", fmt.Errorf("origin does not identify one github.com owner/repo")
	}
	return slug, nil
}
