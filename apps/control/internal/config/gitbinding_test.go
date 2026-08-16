package config

import (
	"os/exec"
	"strings"
	"testing"
)

func gitAvailable(t *testing.T) bool {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
		return false
	}
	return true
}

func initRepoWithRemote(t *testing.T, remote string) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	run("remote", "add", "origin", remote)
	return dir
}

func TestVerifyGitBindingAcceptsHTTPSAndSSHRemotes(t *testing.T) {
	if !gitAvailable(t) {
		return
	}
	for _, remote := range []string{
		"https://github.com/ai-daming/qianshou.git",
		"git@github.com:ai-daming/qianshou.git",
		"ssh://git@github.com/ai-daming/qianshou.git",
	} {
		dir := initRepoWithRemote(t, remote)
		if err := VerifyGitBinding(dir, "ai-daming/qianshou"); err != nil {
			t.Fatalf("remote %s rejected: %v", remote, err)
		}
	}
}

func TestVerifyGitBindingRejectsMismatchAndMissingRemote(t *testing.T) {
	if !gitAvailable(t) {
		return
	}
	dir := initRepoWithRemote(t, "https://github.com/ai-daming/mamamate.git")
	err := VerifyGitBinding(dir, "ai-daming/qianshou")
	if err == nil || !strings.Contains(err.Error(), "ai-daming/mamamate") {
		t.Fatalf("mismatched remote accepted or error lacks detail: %v", err)
	}

	bare := t.TempDir()
	if out, err := exec.Command("git", "-C", bare, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	err = VerifyGitBinding(bare, "ai-daming/qianshou")
	if err == nil || !strings.Contains(err.Error(), "origin") {
		t.Fatalf("missing remote accepted or error lacks detail: %v", err)
	}

	err = VerifyGitBinding(t.TempDir()+"/does-not-exist", "ai-daming/qianshou")
	if err == nil {
		t.Fatalf("missing path accepted")
	}
}
