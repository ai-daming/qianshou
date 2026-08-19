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

	"github.com/ai-daming/qianshou/apps/control/internal/githubapi"
	"github.com/ai-daming/qianshou/apps/control/internal/ledger"
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

func (blockingFacts) ResolveRepository(_ context.Context, _ string) (githubapi.Repository, error) {
	return githubapi.Repository{ID: 101, NameWithOwner: "ai-daming/qianshou"}, nil
}

func (blockingFacts) GetRepositoryByID(_ context.Context, id int64) (githubapi.Repository, error) {
	return githubapi.Repository{ID: id, NameWithOwner: "ai-daming/qianshou"}, nil
}

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
	catalog := testCatalog(t)
	addProject(t, catalog, "qianshou", 101, "ai-daming/qianshou")
	h := handlerWithFactsTimeout(catalog, blockingFacts{}, 20*time.Millisecond)
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

type blockingIdentityFacts struct{ blockingFacts }

func (blockingIdentityFacts) ResolveRepository(ctx context.Context, _ string) (githubapi.Repository, error) {
	<-ctx.Done()
	return githubapi.Repository{}, ctx.Err()
}

func (blockingIdentityFacts) GetRepositoryByID(ctx context.Context, _ int64) (githubapi.Repository, error) {
	<-ctx.Done()
	return githubapi.Repository{}, ctx.Err()
}

func TestGitHubRepositoryIdentityDeadlinesAreBounded(t *testing.T) {
	catalog := testCatalog(t)
	addProject(t, catalog, "qianshou", 101, "ai-daming/qianshou")
	h := handlerWithFactsTimeout(catalog, blockingIdentityFacts{}, 20*time.Millisecond)

	tests := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{name: "resolve repository", method: http.MethodPost, path: "/api/v1/projects", body: `{"id":"other","repositorySlug":"ai-daming/other"}`},
		{name: "refresh repository identity", method: http.MethodGet, path: "/api/v1/projects/qianshou/milestones"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			rr := httptest.NewRecorder()
			started := time.Now()
			h.ServeHTTP(rr, req)
			if elapsed := time.Since(started); elapsed >= time.Second {
				t.Fatalf("deadline response took %s", elapsed)
			}
			if rr.Code != http.StatusBadGateway || !strings.Contains(rr.Body.String(), githubFactsDeadlineMessage) {
				t.Fatalf("%s %s = %d %s", tc.method, tc.path, rr.Code, rr.Body.String())
			}
		})
	}
}

func TestGitHubFactsBodyReadDeadlineUsesDeadlineMessage(t *testing.T) {
	const issuesPath = "/repos/ai-daming/qianshou/issues"
	cases := []struct {
		name        string
		path        string
		stalledPath string
	}{
		{
			name:        "REST response body",
			path:        "/api/v1/projects/qianshou/milestones",
			stalledPath: "/repos/ai-daming/qianshou/milestones",
		},
		{
			name:        "GraphQL dependency response body",
			path:        "/api/v1/projects/qianshou/milestones/1/issues",
			stalledPath: "/graphql",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			catalog := testCatalog(t)
			addProject(t, catalog, "qianshou", 101, "ai-daming/qianshou")
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == "/repositories/101":
					if err := json.NewEncoder(w).Encode(map[string]any{
						"id": 101, "full_name": "ai-daming/qianshou", "url": "http://" + r.Host + "/repos/ai-daming/qianshou",
					}); err != nil {
						t.Errorf("write repository response: %v", err)
					}
				case r.URL.Path == tc.stalledPath:
					w.WriteHeader(http.StatusOK)
					flusher, ok := w.(http.Flusher)
					if !ok {
						t.Error("test server response does not support flushing")
						return
					}
					flusher.Flush()
					<-r.Context().Done()
				case tc.stalledPath == "/graphql" && r.URL.Path == issuesPath:
					err := json.NewEncoder(w).Encode([]map[string]any{{
						"repository_url": "http://" + r.Host + "/repos/ai-daming/qianshou",
						"number":         36,
						"title":          "Issue",
						"state":          "open",
						"labels":         []any{},
						"milestone":      map[string]any{"number": 1},
					}})
					if err != nil {
						t.Errorf("write Issue response: %v", err)
					}
				default:
					t.Errorf("unexpected upstream request: %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()

			httpClient := *srv.Client()
			httpClient.Timeout = 50 * time.Millisecond
			facts := githubapi.NewClient("test-token", srv.URL, srv.URL+"/graphql", &httpClient)
			h := handlerWithFactsTimeout(catalog, facts, 5*time.Second)

			started := time.Now()
			rr := getJSON(t, h, tc.path, nil)
			if elapsed := time.Since(started); elapsed >= 2*time.Second {
				t.Fatalf("response took %s; the child request deadline did not fire first", elapsed)
			}
			if rr.Code != http.StatusBadGateway || !strings.Contains(rr.Body.String(), `"code":"GITHUB_FACTS_UNAVAILABLE"`) {
				t.Fatalf("GET %s = %d %s", tc.path, rr.Code, rr.Body.String())
			}
			if !strings.Contains(rr.Body.String(), `"message":"Current GitHub facts could not be read completely before the request deadline."`) {
				t.Fatalf("GET %s mislabeled a body-read deadline: %s", tc.path, rr.Body.String())
			}
		})
	}
}

func TestHealthz(t *testing.T) {
	h := handler(testCatalog(t), &fakeFacts{})
	rr := getJSON(t, h, "/healthz", nil)
	if rr.Code != http.StatusOK || rr.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("healthz = %d, %q", rr.Code, rr.Header().Get("Content-Type"))
	}
}

type fakeFacts struct {
	repositories map[int64]githubapi.Repository
	resolved     githubapi.Repository
	milestones   []githubapi.Milestone
	issues       []githubapi.Issue
	issue        githubapi.Issue
	err          error
	calls        int
	repos        []string
}

func (f *fakeFacts) ResolveRepository(_ context.Context, _ string) (githubapi.Repository, error) {
	if f.err != nil {
		return githubapi.Repository{}, f.err
	}
	return f.resolved, nil
}
func (f *fakeFacts) GetRepositoryByID(_ context.Context, id int64) (githubapi.Repository, error) {
	if repository, ok := f.repositories[id]; ok {
		return repository, nil
	}
	if f.err != nil {
		return githubapi.Repository{}, f.err
	}
	return githubapi.Repository{}, errors.New("missing repository")
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

func testCatalog(t *testing.T) *ledger.Store {
	t.Helper()
	store, err := ledger.Open(context.Background(), t.TempDir()+"/home")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func addProject(t *testing.T, catalog *ledger.Store, id string, repositoryID int64, slug string) {
	t.Helper()
	if _, err := catalog.CreateProject(context.Background(), ledger.NewProject{ID: id, RepositoryID: repositoryID, CreationSlug: slug}); err != nil {
		t.Fatal(err)
	}
}

func testFacts() *fakeFacts {
	return &fakeFacts{repositories: map[int64]githubapi.Repository{
		101: {ID: 101, NameWithOwner: "ai-daming/qianshou"},
		202: {ID: 202, NameWithOwner: "ai-daming/mamamate"},
	}}
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

func TestProjectsEndpointReadsSQLiteAndDoesNotExposeMachineOrEngineSecrets(t *testing.T) {
	catalog := testCatalog(t)
	addProject(t, catalog, "qianshou", 101, "ai-daming/qianshou")
	rr := getJSON(t, handler(catalog, testFacts()), "/api/v1/projects", nil)
	if rr.Code != http.StatusOK || rr.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("response = %d %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, forbidden := range []string{"/private/local/checkout", "secret-command", "engines", `"slug"`} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("response leaks or mislabels %q: %s", forbidden, body)
		}
	}
	for _, required := range []string{`"id":"qianshou"`, `"id":101`, `"creationSlug":"ai-daming/qianshou"`} {
		if !strings.Contains(body, required) {
			t.Fatalf("response missing %q: %s", required, body)
		}
	}
}

func TestCreateProjectResolvesGitHubIdentityBeforePersisting(t *testing.T) {
	catalog := testCatalog(t)
	facts := testFacts()
	facts.resolved = githubapi.Repository{ID: 101, NameWithOwner: "ai-daming/qianshou"}
	h := handler(catalog, facts)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/projects", strings.NewReader(`{"id":"qianshou","repositorySlug":"ai-daming/qianshou"}`))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}
	project, err := catalog.GetProject(context.Background(), "qianshou")
	if err != nil || project.RepositoryID != 101 {
		t.Fatalf("stored project = %+v, %v", project, err)
	}

	facts.resolved = githubapi.Repository{ID: 101, NameWithOwner: "ai-daming/qianshou-renamed"}
	req = httptest.NewRequest(http.MethodPost, "/api/v1/projects", strings.NewReader(`{"id":"other","repositorySlug":"ai-daming/qianshou-renamed"}`))
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusConflict {
		t.Fatalf("repository id reuse status = %d: %s", rr.Code, rr.Body.String())
	}
}

func TestCreateProjectRejectsInvalidIdentityBeforeCallingGitHub(t *testing.T) {
	catalog := testCatalog(t)
	facts := testFacts()
	facts.err = errors.New("must not be reached")
	h := handler(catalog, facts)
	for _, body := range []string{`{"id":"","repositorySlug":"ai-daming/qianshou"}`, `{"id":"qianshou","repositorySlug":"not-a-slug"}`} {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/projects", strings.NewReader(body))
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("body %s status = %d: %s", body, rr.Code, rr.Body.String())
		}
	}
}

func TestOneServerRoutesTwoCentralProjectsByCurrentGitHubSlug(t *testing.T) {
	catalog := testCatalog(t)
	addProject(t, catalog, "qianshou", 101, "ai-daming/qianshou-old")
	addProject(t, catalog, "mamamate", 202, "ai-daming/mamamate")
	facts := testFacts()
	h := handler(catalog, facts)
	for _, path := range []string{"/api/v1/projects/qianshou/milestones", "/api/v1/projects/mamamate/milestones"} {
		if rr := getJSON(t, h, path, nil); rr.Code != http.StatusOK {
			t.Fatalf("GET %s = %d: %s", path, rr.Code, rr.Body.String())
		}
	}
	if len(facts.repos) != 2 || facts.repos[0] != "ai-daming/qianshou" || facts.repos[1] != "ai-daming/mamamate" {
		t.Fatalf("routed repositories = %#v", facts.repos)
	}
}

func TestEmptyCollectionsStayJSONArrays(t *testing.T) {
	catalog := testCatalog(t)
	addProject(t, catalog, "qianshou", 101, "ai-daming/qianshou")
	h := handler(catalog, testFacts())
	for _, path := range []string{"/api/v1/projects/qianshou/milestones", "/api/v1/projects/qianshou/milestones/1/issues"} {
		rr := getJSON(t, h, path, nil)
		if rr.Code != http.StatusOK || (strings.Contains(path, "issues") && !strings.Contains(rr.Body.String(), `"issues":[]`)) ||
			(!strings.Contains(path, "issues") && !strings.Contains(rr.Body.String(), `"milestones":[]`)) {
			t.Fatalf("GET %s = %d %s", path, rr.Code, rr.Body.String())
		}
	}
}

func TestDynamicScopeEndpointsRefreshFactsOnEveryRequest(t *testing.T) {
	catalog := testCatalog(t)
	addProject(t, catalog, "qianshou", 101, "ai-daming/qianshou")
	facts := testFacts()
	h := handler(catalog, facts)
	for _, path := range []string{
		"/api/v1/projects/qianshou/milestones",
		"/api/v1/projects/qianshou/milestones/1/issues",
		"/api/v1/projects/qianshou/issues/30",
		"/api/v1/projects/qianshou/issues/30",
	} {
		if rr := getJSON(t, h, path, nil); rr.Code != http.StatusOK {
			t.Fatalf("GET %s = %d", path, rr.Code)
		}
	}
	if facts.calls != 4 {
		t.Fatalf("fact calls = %d, want 4", facts.calls)
	}
}

func TestUnknownProjectCannotBecomeRepositoryProxy(t *testing.T) {
	facts := testFacts()
	rr := getJSON(t, handler(testCatalog(t), facts), "/api/v1/projects/unconfigured/issues/30", nil)
	if rr.Code != http.StatusNotFound || facts.calls != 0 {
		t.Fatalf("response = %d calls=%d: %s", rr.Code, facts.calls, rr.Body.String())
	}
}

func TestCollectionOrRepositoryIdentityFailureReturnsStructuredError(t *testing.T) {
	catalog := testCatalog(t)
	addProject(t, catalog, "qianshou", 101, "ai-daming/qianshou")
	facts := testFacts()
	facts.err = errors.New("unavailable")
	rr := getJSON(t, handler(catalog, facts), "/api/v1/projects/qianshou/milestones", nil)
	if rr.Code != http.StatusBadGateway || !strings.Contains(rr.Body.String(), "GITHUB_FACTS_UNAVAILABLE") {
		t.Fatalf("response = %d: %s", rr.Code, rr.Body.String())
	}
}

func TestPaginationLimitFailureReturnsExistingStructuredError(t *testing.T) {
	catalog := testCatalog(t)
	addProject(t, catalog, "qianshou", 101, "ai-daming/qianshou")
	facts := testFacts()
	facts.err = errors.New("GitHub pagination page limit exceeded")
	h := handler(catalog, facts)
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
	for _, addr := range []string{"0.0.0.0:41727", "192.168.1.4:41727", "localhost:41727", ":41727", "bad"} {
		if err := validateListenAddress(addr); err == nil {
			t.Fatalf("accepted non-IP loopback address %q", addr)
		}
	}
	for _, addr := range []string{"127.0.0.1:41727", "[::1]:41727"} {
		if err := validateListenAddress(addr); err != nil {
			t.Fatalf("rejected loopback %q: %v", addr, err)
		}
	}
}
