package githubapi

import (
	"context"
	"testing"
	"time"

	"github.com/ai-daming/qianshou/apps/control/internal/deps"
)

func TestQianshouIssue30Live(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	token, err := deps.ResolveToken(ctx)
	if err != nil {
		t.Skipf("no GitHub credentials for live facts test: %v", err)
	}
	issue, err := New(token).GetIssue(ctx, "ai-daming/qianshou", 30)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if issue.Number != 30 || issue.Title == "" || issue.Dependency.Status == DependencyError {
		t.Fatalf("incomplete live Issue: %+v", issue)
	}
}

func TestQianshouMilestoneDependenciesLive(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	token, err := deps.ResolveToken(ctx)
	if err != nil {
		t.Skipf("no GitHub credentials for live facts test: %v", err)
	}
	issues, err := New(token).ListMilestoneIssues(ctx, "ai-daming/qianshou", 1)
	if err != nil {
		t.Fatalf("ListMilestoneIssues: %v", err)
	}
	if len(issues) == 0 {
		t.Fatal("live M1 Milestone unexpectedly has no Issues")
	}
	for _, issue := range issues {
		if issue.Dependency.Status == DependencyError {
			t.Fatalf("live dependency facts unavailable for Issue #%d", issue.Number)
		}
	}
}
