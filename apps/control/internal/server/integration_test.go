package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ai-daming/qianshou/apps/control/internal/deps"
	"github.com/ai-daming/qianshou/apps/control/internal/githubapi"
	"github.com/ai-daming/qianshou/apps/control/internal/ledger"
)

func TestOneServerReadsTwoLiveGitHubProjects(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	token, err := deps.ResolveToken(ctx)
	if err != nil {
		t.Skipf("no GitHub credentials for live server test: %v", err)
	}
	client := githubapi.New(token)
	catalog := testCatalog(t)
	for _, item := range []struct{ id, slug string }{{"qianshou", "ai-daming/qianshou"}, {"mamamate", "ai-daming/mamamate"}} {
		repository, err := client.ResolveRepository(ctx, item.slug)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := catalog.CreateProject(ctx, ledger.NewProject{ID: item.id, RepositoryID: repository.ID, CreationSlug: repository.NameWithOwner}); err != nil {
			t.Fatal(err)
		}
	}
	server := httptest.NewServer(handler(catalog, client))
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
