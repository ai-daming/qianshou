package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ai-daming/qianshou/apps/control/internal/config"
	"github.com/ai-daming/qianshou/apps/control/internal/deps"
	"github.com/ai-daming/qianshou/apps/control/internal/githubapi"
)

func TestOneServerReadsTwoLiveGitHubProjects(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	token, err := deps.ResolveToken(ctx)
	if err != nil {
		t.Skipf("no GitHub credentials for live server test: %v", err)
	}
	cfg := config.Config{Version: 1, Projects: []config.Project{
		{ID: "qianshou", Repository: config.Repository{Provider: "github", Slug: "ai-daming/qianshou"}},
		{ID: "mamamate", Repository: config.Repository{Provider: "github", Slug: "ai-daming/mamamate"}},
	}}
	server := httptest.NewServer(handler(cfg, githubapi.New(token)))
	defer server.Close()

	for _, projectID := range []string{"qianshou", "mamamate"} {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/v1/projects/"+projectID+"/milestones", nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := server.Client().Do(req)
		if err != nil {
			t.Fatalf("read %s: %v", projectID, err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("read %s status = %d", projectID, response.StatusCode)
		}
	}
}
