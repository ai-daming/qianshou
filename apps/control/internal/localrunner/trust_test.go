package localrunner

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ai-daming/qianshou/apps/control/internal/config"
	"github.com/ai-daming/qianshou/apps/control/internal/ledger"
)

func TestResolveDiscussionTargetValidatesTrustBoundary(t *testing.T) {
	root := t.TempDir()
	mainCheckout := filepath.Join(root, "qianshou")
	initRepository(t, mainCheckout, "https://github.com/ai-daming/qianshou.git")
	binding := ledger.RunnerProjectBinding{ID: "binding-1", RunnerID: "runner-1", ProjectID: "qianshou",
		MainCheckoutPath: mainCheckout, RepositoryIDAtBinding: 12345}
	cfg := config.Config{Version: 1, Runner: config.Runner{ID: "runner-1", AllowedRoots: []string{root}},
		Engines: []config.Engine{{ID: "codex", Adapter: "codex", Command: "codex"}}}

	target, err := ResolveDiscussionTarget(context.Background(), cfg, binding, 12345, "ai-daming/qianshou", "codex")
	if err != nil {
		t.Fatalf("valid target rejected: %v", err)
	}
	canonicalCheckout, err := filepath.EvalSymlinks(mainCheckout)
	if err != nil {
		t.Fatal(err)
	}
	if target.CheckoutPath != canonicalCheckout || target.Engine.ID != "codex" {
		t.Fatalf("target = %+v", target)
	}

	tests := []struct {
		name       string
		config     config.Config
		binding    ledger.RunnerProjectBinding
		repository int64
		slug       string
		engine     string
		want       string
	}{
		{name: "engine disabled", config: config.Config{Runner: cfg.Runner}, binding: binding, repository: 12345, slug: "ai-daming/qianshou", engine: "claude", want: "engine"},
		{name: "runner mismatch", config: cfg, binding: withRunner(binding, "other"), repository: 12345, slug: "ai-daming/qianshou", engine: "codex", want: "runner"},
		{name: "repository id mismatch", config: cfg, binding: binding, repository: 999, slug: "ai-daming/qianshou", engine: "codex", want: "repository"},
		{name: "repository remote mismatch", config: cfg, binding: binding, repository: 12345, slug: "ai-daming/other", engine: "codex", want: "repository"},
		{name: "outside allowed root", config: withRoots(cfg, filepath.Join(root, "another")), binding: binding, repository: 12345, slug: "ai-daming/qianshou", engine: "codex", want: "allowed"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ResolveDiscussionTarget(context.Background(), tc.config, tc.binding, tc.repository, tc.slug, tc.engine)
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestResolveDiscussionTargetRejectsLinkedWorktreeAndSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	mainCheckout := filepath.Join(root, "qianshou")
	initRepository(t, mainCheckout, "git@github.com:ai-daming/qianshou.git")
	linked := filepath.Join(root, "linked")
	runGit(t, mainCheckout, "worktree", "add", "-b", "linked", linked)
	cfg := config.Config{Runner: config.Runner{ID: "runner-1", AllowedRoots: []string{root}},
		Engines: []config.Engine{{ID: "codex", Adapter: "codex", Command: "codex"}}}
	binding := ledger.RunnerProjectBinding{ID: "binding-1", RunnerID: "runner-1", ProjectID: "qianshou",
		MainCheckoutPath: linked, RepositoryIDAtBinding: 12345}
	if _, err := ResolveDiscussionTarget(context.Background(), cfg, binding, 12345, "ai-daming/qianshou", "codex"); err == nil || !strings.Contains(strings.ToLower(err.Error()), "main checkout") {
		t.Fatalf("linked worktree error = %v", err)
	}

	outside := t.TempDir()
	outsideRepo := filepath.Join(outside, "outside")
	initRepository(t, outsideRepo, "https://github.com/ai-daming/qianshou.git")
	symlink := filepath.Join(root, "escape")
	if err := os.Symlink(outsideRepo, symlink); err != nil {
		t.Fatal(err)
	}
	binding.MainCheckoutPath = symlink
	if _, err := ResolveDiscussionTarget(context.Background(), cfg, binding, 12345, "ai-daming/qianshou", "codex"); err == nil || !strings.Contains(strings.ToLower(err.Error()), "allowed") {
		t.Fatalf("symlink escape error = %v", err)
	}
}

func TestTrustHelpersRejectMalformedOriginsAndBoundOutput(t *testing.T) {
	tests := []struct {
		remote string
		want   string
	}{
		{remote: "git@github.com:ai-daming/qianshou.git", want: "ai-daming/qianshou"},
		{remote: "ssh://git@github.com/ai-daming/qianshou.git", want: "ai-daming/qianshou"},
		{remote: "git@github.com", want: ""},
		{remote: "https://gitlab.com/ai-daming/qianshou.git", want: ""},
		{remote: "https://github.com/too/many/parts.git", want: ""},
		{remote: "not-a-url", want: ""},
	}
	for _, tc := range tests {
		got, err := githubSlug(tc.remote)
		if tc.want == "" && err == nil {
			t.Fatalf("remote %q unexpectedly resolved to %q", tc.remote, got)
		}
		if tc.want != "" && (err != nil || got != tc.want) {
			t.Fatalf("remote %q = %q, %v", tc.remote, got, err)
		}
	}

	var output boundedBuffer
	large := make([]byte, gitOutputLimit+1)
	written, err := output.Write(large)
	if err != nil || written != len(large) || !output.overflow || output.Len() != gitOutputLimit {
		t.Fatalf("bounded output = written %d len %d overflow %v err %v", written, output.Len(), output.overflow, err)
	}
	written, err = output.Write([]byte("ignored"))
	if err != nil || written != len("ignored") || output.Len() != gitOutputLimit {
		t.Fatalf("overflow retry = written %d len %d err %v", written, output.Len(), err)
	}
}

func TestValidateMainCheckoutRejectsRetiredBindingAndNonRootPath(t *testing.T) {
	root := t.TempDir()
	mainCheckout := filepath.Join(root, "qianshou")
	initRepository(t, mainCheckout, "https://github.com/ai-daming/qianshou.git")
	retiredAt := "2026-08-19T00:00:00Z"
	binding := ledger.RunnerProjectBinding{ID: "binding-1", RunnerID: "runner-1", ProjectID: "qianshou",
		MainCheckoutPath: mainCheckout, RepositoryIDAtBinding: 12345, RetiredAt: &retiredAt}
	cfg := config.Config{Runner: config.Runner{ID: "runner-1", AllowedRoots: []string{root}}}
	if _, err := ValidateMainCheckout(context.Background(), cfg, binding, 12345, "ai-daming/qianshou"); err == nil || !strings.Contains(err.Error(), "retired") {
		t.Fatalf("retired binding error = %v", err)
	}
	binding.RetiredAt = nil
	binding.MainCheckoutPath = filepath.Join(mainCheckout, "nested")
	if err := os.Mkdir(binding.MainCheckoutPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateMainCheckout(context.Background(), cfg, binding, 12345, "ai-daming/qianshou"); err == nil || !strings.Contains(err.Error(), "root") {
		t.Fatalf("nested binding error = %v", err)
	}
}

func withRunner(binding ledger.RunnerProjectBinding, runner string) ledger.RunnerProjectBinding {
	binding.RunnerID = runner
	return binding
}

func withRoots(cfg config.Config, root string) config.Config {
	cfg.Runner.AllowedRoots = []string{root}
	return cfg
}

func initRepository(t *testing.T, path, remote string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, path, "init", "-b", "main")
	runGit(t, path, "config", "user.email", "test@example.com")
	runGit(t, path, "config", "user.name", "Test")
	runGit(t, path, "remote", "add", "origin", remote)
	if err := os.WriteFile(filepath.Join(path, "README.md"), []byte("test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, path, "add", "README.md")
	runGit(t, path, "commit", "-m", "initial")
}

func runGit(t *testing.T, path string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", path}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}
