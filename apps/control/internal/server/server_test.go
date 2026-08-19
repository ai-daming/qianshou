package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ai-daming/qianshou/apps/control/internal/config"
	"github.com/ai-daming/qianshou/apps/control/internal/githubapi"
)

func TestHTTPServerHasBoundedConnectionTimeouts(t *testing.T) {
	if githubFactsTimeout != 90*time.Second {
		t.Fatalf("githubFactsTimeout = %s, want 90s", githubFactsTimeout)
	}
	server := newHTTPServer(http.NotFoundHandler())
	if server.ReadHeaderTimeout != 10*time.Second {
		t.Fatalf("ReadHeaderTimeout = %s, want 10s", server.ReadHeaderTimeout)
	}
	if server.WriteTimeout != 120*time.Second {
		t.Fatalf("WriteTimeout = %s, want 120s", server.WriteTimeout)
	}
	if server.IdleTimeout != 60*time.Second {
		t.Fatalf("IdleTimeout = %s, want 60s", server.IdleTimeout)
	}
}

type blockingFacts struct{}

func (blockingFacts) ListMilestones(ctx context.Context, _ string) ([]githubapi.Milestone, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (blockingFacts) ListMilestoneIssues(ctx context.Context, _ string, _ int) ([]githubapi.Issue, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (blockingFacts) GetIssue(ctx context.Context, _ string, _ int) (githubapi.Issue, error) {
	<-ctx.Done()
	return githubapi.Issue{}, ctx.Err()
}

func TestGitHubFactsDeadlineReturnsExistingStructuredError(t *testing.T) {
	h := handlerWithFactsTimeout(testConfig(), blockingFacts{}, 20*time.Millisecond)
	for _, path := range []string{
		"/api/v1/projects/qianshou/milestones",
		"/api/v1/projects/qianshou/milestones/1/issues",
		"/api/v1/projects/qianshou/issues/36",
	} {
		t.Run(path, func(t *testing.T) {
			started := time.Now()
			rr := getJSON(t, h, path, nil)
			if elapsed := time.Since(started); elapsed >= time.Second {
				t.Fatalf("deadline response took %s, test must not wait for the production timeout", elapsed)
			}
			if rr.Code != http.StatusBadGateway || !strings.Contains(rr.Body.String(), `"code":"GITHUB_FACTS_UNAVAILABLE"`) {
				t.Fatalf("GET %s = %d %s", path, rr.Code, rr.Body.String())
			}
			if !strings.Contains(rr.Body.String(), `"message":"Current GitHub facts could not be read completely before the request deadline."`) {
				t.Fatalf("GET %s did not identify the deadline: %s", path, rr.Body.String())
			}
		})
	}
}

func TestHealthz(t *testing.T) {
	ts := httptest.NewServer(handler(config.Config{}, &fakeFacts{}))
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("healthz status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if got := resp.Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("content-type = %q, want application/json", got)
	}
}

type fakeFacts struct {
	milestones []githubapi.Milestone
	issues     []githubapi.Issue
	issue      githubapi.Issue
	err        error
	calls      int
	repos      []string
}

func (f *fakeFacts) ListMilestones(_ context.Context, repo string) ([]githubapi.Milestone, error) {
	f.calls++
	f.repos = append(f.repos, repo)
	return f.milestones, f.err
}

func (f *fakeFacts) ListMilestoneIssues(_ context.Context, repo string, _ int) ([]githubapi.Issue, error) {
	f.calls++
	f.repos = append(f.repos, repo)
	return f.issues, f.err
}

func (f *fakeFacts) GetIssue(_ context.Context, repo string, _ int) (githubapi.Issue, error) {
	f.calls++
	f.repos = append(f.repos, repo)
	return f.issue, f.err
}

func testConfig() config.Config {
	return config.Config{
		Version: 1,
		Engines: []config.Engine{{ID: "codex", Adapter: "codex-cli", Command: "secret-command --token hidden"}},
		Projects: []config.Project{{
			ID:         "qianshou",
			Repository: config.Repository{Provider: "github", Slug: "ai-daming/qianshou"},
			Local:      config.Local{Path: "/private/local/checkout"},
		}},
	}
}

func getJSON(t *testing.T, h http.Handler, path string, target any) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if target != nil {
		if err := json.Unmarshal(rr.Body.Bytes(), target); err != nil {
			t.Fatalf("decode %s: %v\n%s", path, err, rr.Body.String())
		}
	}
	return rr
}

func TestProjectsEndpointDoesNotExposeMachineOrEngineSecrets(t *testing.T) {
	h := handler(testConfig(), &fakeFacts{})
	rr := getJSON(t, h, "/api/v1/projects", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q", rr.Header().Get("Cache-Control"))
	}
	body := rr.Body.String()
	for _, forbidden := range []string{"/private/local/checkout", "secret-command", "hidden", "engines"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("response leaks %q: %s", forbidden, body)
		}
	}
	if !strings.Contains(body, `"id":"qianshou"`) || !strings.Contains(body, `"slug":"ai-daming/qianshou"`) {
		t.Fatalf("public project locator missing: %s", body)
	}
}

func TestOneServerRoutesTwoConfiguredProjects(t *testing.T) {
	cfg := testConfig()
	cfg.Projects = append(cfg.Projects, config.Project{
		ID:         "mamamate",
		Repository: config.Repository{Provider: "github", Slug: "ai-daming/mamamate"},
		Local:      config.Local{Path: "/another/private/path"},
	})
	facts := &fakeFacts{}
	h := handler(cfg, facts)
	for _, path := range []string{
		"/api/v1/projects/qianshou/milestones",
		"/api/v1/projects/mamamate/milestones",
	} {
		if rr := getJSON(t, h, path, nil); rr.Code != http.StatusOK {
			t.Fatalf("GET %s = %d: %s", path, rr.Code, rr.Body.String())
		}
	}
	if len(facts.repos) != 2 || facts.repos[0] != "ai-daming/qianshou" || facts.repos[1] != "ai-daming/mamamate" {
		t.Fatalf("routed repositories = %v", facts.repos)
	}
}

func TestEmptyCollectionsStayJSONArrays(t *testing.T) {
	h := handler(testConfig(), &fakeFacts{})
	for _, path := range []string{
		"/api/v1/projects/qianshou/milestones",
		"/api/v1/projects/qianshou/milestones/1/issues",
	} {
		rr := getJSON(t, h, path, nil)
		if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `:[]`) || strings.Contains(rr.Body.String(), `:null`) {
			t.Fatalf("GET %s did not preserve an empty array: %d %s", path, rr.Code, rr.Body.String())
		}
	}
}

func TestDynamicScopeEndpointsRefreshFactsOnEveryRequest(t *testing.T) {
	facts := &fakeFacts{
		milestones: []githubapi.Milestone{{Number: 1, Title: "M1", State: "OPEN"}},
		issues: []githubapi.Issue{{
			Number: 30, Title: "API", State: "OPEN",
			Dependency: githubapi.Dependency{Status: githubapi.DependencyReady},
		}},
		issue: githubapi.Issue{Number: 30, Title: "API", State: "OPEN", Dependency: githubapi.Dependency{Status: githubapi.DependencyReady}},
	}
	h := handler(testConfig(), facts)
	for _, path := range []string{
		"/api/v1/projects/qianshou/milestones",
		"/api/v1/projects/qianshou/milestones/1/issues",
		"/api/v1/projects/qianshou/issues/30",
		"/api/v1/projects/qianshou/issues/30",
	} {
		rr := getJSON(t, h, path, nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("GET %s = %d: %s", path, rr.Code, rr.Body.String())
		}
	}
	if facts.calls != 4 {
		t.Fatalf("facts calls = %d, want one fresh call per request", facts.calls)
	}
}

func TestUnknownProjectCannotBecomeRepositoryProxy(t *testing.T) {
	facts := &fakeFacts{}
	h := handler(testConfig(), facts)
	rr := getJSON(t, h, "/api/v1/projects/unconfigured/issues/30", nil)
	if rr.Code != http.StatusNotFound || facts.calls != 0 {
		t.Fatalf("status = %d, facts calls = %d, body = %s", rr.Code, facts.calls, rr.Body.String())
	}
}

func TestCollectionFailureReturnsStructuredError(t *testing.T) {
	h := handler(testConfig(), &fakeFacts{err: context.DeadlineExceeded})
	rr := getJSON(t, h, "/api/v1/projects/qianshou/milestones", nil)
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}
	var got struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Error.Code == "" || got.Error.Message == "" || strings.Contains(got.Error.Message, context.DeadlineExceeded.Error()) {
		t.Fatalf("unsafe or unstructured error: %+v", got)
	}
}

func TestPaginationLimitFailureReturnsExistingStructuredError(t *testing.T) {
	h := handler(testConfig(), &fakeFacts{err: errors.New("GitHub pagination page limit exceeded")})
	for _, path := range []string{
		"/api/v1/projects/qianshou/milestones",
		"/api/v1/projects/qianshou/milestones/1/issues",
	} {
		rr := getJSON(t, h, path, nil)
		if rr.Code != http.StatusBadGateway || !strings.Contains(rr.Body.String(), `"code":"GITHUB_FACTS_UNAVAILABLE"`) {
			t.Fatalf("GET %s = %d %s", path, rr.Code, rr.Body.String())
		}
		if strings.Contains(rr.Body.String(), "request deadline") {
			t.Fatalf("GET %s mislabeled a pagination trust failure as a deadline: %s", path, rr.Body.String())
		}
	}
}

func TestListenAddressMustBeLoopback(t *testing.T) {
	for _, addr := range []string{"127.0.0.1:41727", "[::1]:41727"} {
		if err := validateListenAddress(addr); err != nil {
			t.Fatalf("%s rejected: %v", addr, err)
		}
	}
	for _, addr := range []string{"0.0.0.0:41727", ":41727", "192.168.1.8:41727", "example.com:41727"} {
		if err := validateListenAddress(addr); err == nil {
			t.Fatalf("non-loopback %s accepted", addr)
		}
	}
}
