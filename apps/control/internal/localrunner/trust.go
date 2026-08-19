// Package localrunner validates and executes the M1 loopback-only embedded
// Runner. It never trusts a browser-supplied path or executable.
package localrunner

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/ai-daming/qianshou/apps/control/internal/config"
	"github.com/ai-daming/qianshou/apps/control/internal/ledger"
)

const gitOutputLimit = 64 << 10

type DiscussionTarget struct {
	CheckoutPath string
	Engine       config.Engine
}

func ResolveDiscussionTarget(ctx context.Context, cfg config.Config, binding ledger.RunnerProjectBinding, repositoryID int64, repositorySlug, engineID string) (DiscussionTarget, error) {
	checkout, err := ValidateMainCheckout(ctx, cfg, binding, repositoryID, repositorySlug)
	if err != nil {
		return DiscussionTarget{}, err
	}
	engine, ok := enabledEngine(cfg.Engines, engineID)
	if !ok {
		return DiscussionTarget{}, fmt.Errorf("requested engine is not enabled on this runner")
	}
	adapter := strings.ToLower(strings.TrimSpace(engine.Adapter))
	if adapter != "codex" && adapter != "claude" {
		return DiscussionTarget{}, fmt.Errorf("requested engine adapter is unsupported")
	}
	return DiscussionTarget{CheckoutPath: checkout, Engine: engine}, nil
}

func ValidateMainCheckout(ctx context.Context, cfg config.Config, binding ledger.RunnerProjectBinding, repositoryID int64, repositorySlug string) (string, error) {
	if binding.RetiredAt != nil {
		return "", fmt.Errorf("runner project binding is retired")
	}
	if binding.RunnerID != cfg.Runner.ID {
		return "", fmt.Errorf("runner project binding belongs to a different runner")
	}
	if repositoryID <= 0 || binding.RepositoryIDAtBinding != repositoryID {
		return "", fmt.Errorf("runner project binding repository identity does not match")
	}

	checkout, err := filepath.EvalSymlinks(filepath.Clean(binding.MainCheckoutPath))
	if err != nil {
		return "", fmt.Errorf("main checkout path is unavailable: %w", err)
	}
	checkout, err = filepath.Abs(checkout)
	if err != nil {
		return "", fmt.Errorf("main checkout path is invalid: %w", err)
	}
	if !insideAllowedRoot(checkout, cfg.Runner.AllowedRoots) {
		return "", fmt.Errorf("main checkout is outside runner allowed roots")
	}

	top, err := gitText(ctx, checkout, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("main checkout is not a readable Git repository: %w", err)
	}
	resolvedTop, err := filepath.EvalSymlinks(top)
	if err != nil || filepath.Clean(resolvedTop) != checkout {
		return "", fmt.Errorf("binding path is not the Git repository root")
	}
	gitDir, err := gitText(ctx, checkout, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return "", fmt.Errorf("Git directory cannot be verified: %w", err)
	}
	commonDir, err := gitText(ctx, checkout, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", fmt.Errorf("Git common directory cannot be verified: %w", err)
	}
	if filepath.Clean(gitDir) != filepath.Clean(commonDir) {
		return "", fmt.Errorf("binding must point to the main checkout, not a linked worktree")
	}
	remote, err := gitText(ctx, checkout, "remote", "get-url", "origin")
	if err != nil {
		return "", fmt.Errorf("origin repository identity cannot be verified: %w", err)
	}
	slug, err := githubSlug(remote)
	if err != nil || !strings.EqualFold(slug, repositorySlug) {
		return "", fmt.Errorf("origin repository identity does not match the current GitHub repository")
	}
	return checkout, nil
}

func enabledEngine(engines []config.Engine, id string) (config.Engine, bool) {
	for _, engine := range engines {
		if strings.EqualFold(engine.ID, strings.TrimSpace(id)) && strings.TrimSpace(engine.Command) != "" {
			return engine, true
		}
	}
	return config.Engine{}, false
}

func insideAllowedRoot(path string, rawRoots []string) bool {
	for _, rawRoot := range rawRoots {
		root, err := filepath.EvalSymlinks(filepath.Clean(strings.TrimSpace(rawRoot)))
		if err != nil {
			continue
		}
		root, err = filepath.Abs(root)
		if err != nil {
			continue
		}
		relative, err := filepath.Rel(root, path)
		if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative) {
			return true
		}
	}
	return false
}

func githubSlug(remote string) (string, error) {
	remote = strings.TrimSpace(remote)
	var host, path string
	if strings.HasPrefix(remote, "git@") {
		withoutUser := strings.TrimPrefix(remote, "git@")
		var ok bool
		host, path, ok = strings.Cut(withoutUser, ":")
		if !ok {
			return "", fmt.Errorf("invalid SSH remote")
		}
	} else {
		parsed, err := url.Parse(remote)
		if err != nil || parsed.Hostname() == "" {
			return "", fmt.Errorf("invalid repository remote")
		}
		host, path = parsed.Hostname(), parsed.Path
	}
	if !strings.EqualFold(host, "github.com") {
		return "", fmt.Errorf("origin is not GitHub")
	}
	path = strings.TrimSuffix(strings.Trim(strings.TrimSpace(path), "/"), ".git")
	if len(strings.Split(path, "/")) != 2 || strings.ContainsAny(path, "?#") {
		return "", fmt.Errorf("origin repository slug is invalid")
	}
	return path, nil
}

func gitText(ctx context.Context, checkout string, args ...string) (string, error) {
	command := exec.CommandContext(ctx, "git", append([]string{"-C", checkout}, args...)...)
	var output boundedBuffer
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Run(); err != nil {
		return "", fmt.Errorf("git command failed: %w", err)
	}
	if output.overflow {
		return "", fmt.Errorf("git command output exceeded safe limit")
	}
	return strings.TrimSpace(output.String()), nil
}

type boundedBuffer struct {
	bytes.Buffer
	overflow bool
}

func (b *boundedBuffer) Write(value []byte) (int, error) {
	written := len(value)
	remaining := gitOutputLimit - b.Len()
	if remaining <= 0 {
		b.overflow = true
		return written, nil
	}
	if len(value) > remaining {
		value = value[:remaining]
		b.overflow = true
	}
	_, _ = b.Buffer.Write(value)
	return written, nil
}
