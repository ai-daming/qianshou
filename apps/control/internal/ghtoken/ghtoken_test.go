package ghtoken

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolvePrefersEnvironment(t *testing.T) {
	t.Setenv("GH_TOKEN", "env-token")
	t.Setenv("GITHUB_TOKEN", "env-token-2")
	got, err := Resolve(context.Background())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != "env-token" {
		t.Fatalf("token = %q, want GH_TOKEN precedence", got)
	}
}

func TestResolveFallsBackToGhCLI(t *testing.T) {
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	dir := t.TempDir()
	script := filepath.Join(dir, "gh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf 'cli-token\\n'\n"), 0o755); err != nil {
		t.Fatalf("write fake gh: %v", err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	got, err := Resolve(context.Background())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != "cli-token" {
		t.Fatalf("token = %q, want gh auth token output", got)
	}
}

func TestResolveFailsClosedWhenGhCLIFails(t *testing.T) {
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	dir := t.TempDir()
	script := filepath.Join(dir, "gh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho 'not logged in' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatalf("write fake gh: %v", err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	_, err := Resolve(context.Background())
	if err == nil || !strings.Contains(err.Error(), "gh auth token") {
		t.Fatalf("expected gh failure surfaced, got: %v", err)
	}
}

func TestResolveFailsClosedWithoutAnySource(t *testing.T) {
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("PATH", t.TempDir()) // no gh binary
	_, err := Resolve(context.Background())
	if err == nil {
		t.Fatalf("expected missing-credentials error")
	}
	for _, want := range []string{"GH_TOKEN", "GITHUB_TOKEN", "gh auth token"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not mention %q", err.Error(), want)
		}
	}
}

func TestResolveRejectsTokenWithInternalWhitespace(t *testing.T) {
	t.Setenv("GH_TOKEN", "abc def")
	t.Setenv("GITHUB_TOKEN", "")
	if _, err := Resolve(context.Background()); err == nil {
		t.Fatalf("token with internal whitespace must be rejected, not sent as a header")
	}
}

func TestResolveRejectsControlCharactersInToken(t *testing.T) {
	for _, dirty := range []string{"abc\vdef", "abc\fdef", "abc\x01def", "abc\x7fdef"} {
		t.Setenv("GH_TOKEN", dirty)
		t.Setenv("GITHUB_TOKEN", "")
		if _, err := Resolve(context.Background()); err == nil {
			t.Fatalf("token %q accepted", dirty)
		}
	}
}
