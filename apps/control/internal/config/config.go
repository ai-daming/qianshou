// Package config loads Runner-local execution trust. Project identity and
// checkout bindings belong to the central SQLite ledger, never this file.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ai-daming/qianshou/apps/control/internal/strictjson"
)

var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

type Config struct {
	Version int      `json:"version"`
	Runner  Runner   `json:"runner"`
	Engines []Engine `json:"engines"`
}

type Runner struct {
	ID           string   `json:"id"`
	AllowedRoots []string `json:"allowedRoots"`
}

type Engine struct {
	ID      string `json:"id"`
	Adapter string `json:"adapter"`
	Command string `json:"command"`
}

func DefaultHome() string {
	if home := strings.TrimSpace(os.Getenv("QIANSHOU_HOME")); home != "" {
		return filepath.Clean(home)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		fallback, absErr := filepath.Abs(".qianshou")
		if absErr != nil {
			return filepath.Join(string(filepath.Separator), ".qianshou")
		}
		return fallback
	}
	return filepath.Join(home, ".qianshou")
}

func DefaultPath() string {
	return filepath.Join(DefaultHome(), "config.json")
}

func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read Qianshou config: %w", err)
	}
	var raw struct {
		Version *int      `json:"version"`
		Runner  *Runner   `json:"runner"`
		Engines *[]Engine `json:"engines"`
	}
	if err := strictjson.Decode(data, &raw, true); err != nil {
		return Config{}, fmt.Errorf("invalid config: %w", err)
	}
	if raw.Version == nil {
		return Config{}, fmt.Errorf("config version is required")
	}
	if raw.Runner == nil {
		return Config{}, fmt.Errorf("config runner is required")
	}
	if raw.Engines == nil {
		return Config{}, fmt.Errorf("config engines must be an array")
	}
	cfg := Config{Version: *raw.Version, Runner: *raw.Runner, Engines: *raw.Engines}
	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (cfg Config) validate() error {
	if cfg.Version != 1 {
		return fmt.Errorf("unsupported config version %d; want version 1", cfg.Version)
	}
	if !idPattern.MatchString(cfg.Runner.ID) {
		return fmt.Errorf("runner.id is missing or invalid")
	}
	if len(cfg.Runner.AllowedRoots) == 0 {
		return fmt.Errorf("runner.allowedRoots must contain at least one absolute path")
	}
	roots := make(map[string]struct{}, len(cfg.Runner.AllowedRoots))
	for i, raw := range cfg.Runner.AllowedRoots {
		root := filepath.Clean(strings.TrimSpace(raw))
		if !filepath.IsAbs(root) {
			return fmt.Errorf("runner.allowedRoots[%d] must be absolute", i)
		}
		if _, exists := roots[root]; exists {
			return fmt.Errorf("duplicate runner allowed root %q", root)
		}
		roots[root] = struct{}{}
		cfg.Runner.AllowedRoots[i] = root
	}
	engineIDs := make(map[string]struct{}, len(cfg.Engines))
	for i, engine := range cfg.Engines {
		if !idPattern.MatchString(engine.ID) {
			return fmt.Errorf("engines[%d].id is missing or invalid", i)
		}
		if strings.TrimSpace(engine.Adapter) == "" || strings.TrimSpace(engine.Command) == "" {
			return fmt.Errorf("engine %q requires adapter and command", engine.ID)
		}
		switch strings.ToLower(strings.TrimSpace(engine.Adapter)) {
		case "codex-cli":
			return fmt.Errorf("engine %q adapter %q was renamed; update it to %q", engine.ID, engine.Adapter, "codex")
		case "claude-code-cli":
			return fmt.Errorf("engine %q adapter %q was renamed; update it to %q", engine.ID, engine.Adapter, "claude")
		}
		key := strings.ToLower(engine.ID)
		if _, exists := engineIDs[key]; exists {
			return fmt.Errorf("duplicate engine id %q", engine.ID)
		}
		engineIDs[key] = struct{}{}
	}
	return nil
}
