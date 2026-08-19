package githubapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

func newTestClient(server *httptest.Server) *Client {
	return NewClient("test-token", server.URL, server.URL+"/graphql", server.Client())
}

func TestListMilestonesFollowsNextLink(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Fatalf("authorization header not set")
		}
		requests.Add(1)
		if r.URL.Query().Get("page") == "2" {
			fmt.Fprintf(w, `[{"url":%q,"number":2,"title":"M2","state":"open"}]`, requestOrigin(r)+"/repos/ai-daming/qianshou/milestones/2")
			return
		}
		w.Header().Set("Link", fmt.Sprintf(`<%s/repos/ai-daming/qianshou/milestones?state=all&per_page=100&page=2>; rel="next"`, requestOrigin(r)))
		fmt.Fprintf(w, `[{"url":%q,"number":1,"title":"M1","state":"open"}]`, requestOrigin(r)+"/repos/ai-daming/qianshou/milestones/1")
	}))
	defer srv.Close()

	got, err := newTestClient(srv).ListMilestones(context.Background(), "ai-daming/qianshou")
	if err != nil {
		t.Fatalf("ListMilestones: %v", err)
	}
	if len(got) != 2 || got[0].Number != 1 || got[1].Number != 2 || requests.Load() != 2 {
		t.Fatalf("milestones = %+v, requests = %d", got, requests.Load())
	}
}

func TestListMilestonesRejectsUntrustworthyPagination(t *testing.T) {
	cases := []struct {
		name string
		link func(*http.Request) string
	}{
		{"malformed", func(*http.Request) string { return `<broken; rel="next"` }},
		{"ambiguous next", func(r *http.Request) string {
			base := requestOrigin(r) + r.URL.Path
			return `<` + base + `?page=2>; rel="next", <` + base + `?page=3>; rel="next"`
		}},
		{"cross origin", func(*http.Request) string {
			return `<https://evil.example/repos/ai-daming/qianshou/milestones?page=2>; rel="next"`
		}},
		{"cross path", func(r *http.Request) string {
			return `<` + requestOrigin(r) + `/repos/other/repo/issues?page=2>; rel="next"`
		}},
		{"scope query changed", func(r *http.Request) string {
			return `<` + requestOrigin(r) + r.URL.Path + `?state=all&per_page=100&milestone=999&page=2>; rel="next"`
		}},
		{"duplicate page selector", func(r *http.Request) string {
			return `<` + requestOrigin(r) + r.URL.Path + `?state=all&per_page=100&page=2&page=3>; rel="next"`
		}},
		{"loop", func(r *http.Request) string { return `<` + requestOrigin(r) + r.URL.RequestURI() + `>; rel="next"` }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Link", tc.link(r))
				fmt.Fprint(w, `[]`)
			}))
			defer srv.Close()
			if _, err := newTestClient(srv).ListMilestones(context.Background(), "ai-daming/qianshou"); err == nil {
				t.Fatalf("unsafe pagination accepted")
			}
		})
	}
}

func TestListEndpointsEnforcePaginationPageLimit(t *testing.T) {
	type endpointCase struct {
		name string
		path string
		call func(*Client) error
	}
	endpoints := []endpointCase{
		{
			name: "milestones",
			path: "/repos/ai-daming/qianshou/milestones",
			call: func(client *Client) error {
				_, err := client.ListMilestones(context.Background(), "ai-daming/qianshou")
				return err
			},
		},
		{
			name: "milestone issues",
			path: "/repos/ai-daming/qianshou/issues",
			call: func(client *Client) error {
				_, err := client.ListMilestoneIssues(context.Background(), "ai-daming/qianshou", 1)
				return err
			},
		},
	}
	for _, endpoint := range endpoints {
		for _, scenario := range []struct {
			name     string
			lastPage int
			wantErr  bool
		}{
			{name: "one hundred pages succeed", lastPage: 100},
			{name: "page one hundred and one is rejected", lastPage: 101, wantErr: true},
		} {
			t.Run(endpoint.name+"/"+scenario.name, func(t *testing.T) {
				var requests atomic.Int32
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path != endpoint.path {
						http.NotFound(w, r)
						return
					}
					requests.Add(1)
					page := 1
					if raw := r.URL.Query().Get("page"); raw != "" {
						var err error
						page, err = strconv.Atoi(raw)
						if err != nil {
							t.Fatalf("page query = %q: %v", raw, err)
						}
					}
					if page < scenario.lastPage {
						query := r.URL.Query()
						query.Set("page", strconv.Itoa(page+1))
						w.Header().Set("Link", fmt.Sprintf("<%s%s?%s>; rel=\"next\"", requestOrigin(r), r.URL.Path, query.Encode()))
					}
					if endpoint.name == "milestones" {
						fmt.Fprintf(w, `[{"url":%q,"number":%d,"title":"M%d","state":"open"}]`, requestOrigin(r)+"/repos/ai-daming/qianshou/milestones/"+strconv.Itoa(page), page, page)
						return
					}
					fmt.Fprint(w, `[]`)
				}))
				defer srv.Close()

				err := endpoint.call(newTestClient(srv))
				if scenario.wantErr && err == nil {
					t.Fatal("pagination beyond page 100 was accepted")
				}
				if !scenario.wantErr && err != nil {
					t.Fatalf("100-page response rejected: %v", err)
				}
				if got := requests.Load(); got != 100 {
					t.Fatalf("requests = %d, want exactly 100", got)
				}
			})
		}
	}
}

func TestListMilestoneIssuesReturnsFactsAndDependencyJudgment(t *testing.T) {
	var graphqlRequests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/ai-daming/qianshou/issues":
			fmt.Fprintf(w, `[
          {"repository_url":%q,"number":30,"title":"API","state":"open","labels":[{"name":"type:technical"}],"milestone":{"number":1}},
          {"repository_url":%q,"number":31,"title":"UI","state":"closed","labels":[],"milestone":{"number":1}},
          {"repository_url":%q,"number":99,"title":"PR","state":"open","labels":[],"milestone":{"number":1},"pull_request":{"url":%q}}
        ]`, requestOrigin(r)+"/repos/ai-daming/qianshou", requestOrigin(r)+"/repos/ai-daming/qianshou", requestOrigin(r)+"/repos/ai-daming/qianshou", requestOrigin(r)+"/repos/ai-daming/qianshou/pulls/99")
		case "/graphql":
			graphqlRequests.Add(1)
			var request struct {
				Query string `json:"query"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"issue_30: issue(number:30)", "issue_31: issue(number:31)"} {
				if !strings.Contains(request.Query, want) {
					t.Errorf("batch query missing %q: %s", want, request.Query)
				}
			}
			fmt.Fprint(w, `{"data":{"repository":{"nameWithOwner":"ai-daming/qianshou","issue_30":{"number":30,"blockedBy":{"pageInfo":{"hasNextPage":false},"nodes":[]}},"issue_31":{"number":31,"blockedBy":{"pageInfo":{"hasNextPage":false},"nodes":[{"number":30,"state":"OPEN"}]}}}}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	got, err := newTestClient(srv).ListMilestoneIssues(context.Background(), "ai-daming/qianshou", 1)
	if err != nil {
		t.Fatalf("ListMilestoneIssues: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("issues = %+v", got)
	}
	if got[0].Dependency.Status != DependencyReady || got[1].Dependency.Status != DependencyBlocked {
		t.Fatalf("dependency judgments = %+v, %+v", got[0].Dependency, got[1].Dependency)
	}
	if len(got[1].Dependency.BlockedBy) != 1 || got[1].Dependency.BlockedBy[0] != 30 {
		t.Fatalf("blockedBy = %v", got[1].Dependency.BlockedBy)
	}
	if graphqlRequests.Load() != 1 {
		t.Fatalf("GraphQL requests = %d, want one batch", graphqlRequests.Load())
	}
}

func TestListMilestoneIssuesFailsWholeCollectionAndStopsOnRateLimit(t *testing.T) {
	var graphqlRequests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/ai-daming/qianshou/issues":
			fmt.Fprintf(w, `[
				{"repository_url":%q,"number":30,"title":"API","state":"open","labels":[],"milestone":{"number":1}},
				{"repository_url":%q,"number":31,"title":"UI","state":"open","labels":[],"milestone":{"number":1}}
			]`, requestOrigin(r)+"/repos/ai-daming/qianshou", requestOrigin(r)+"/repos/ai-daming/qianshou")
		case "/graphql":
			graphqlRequests.Add(1)
			w.Header().Set("X-RateLimit-Remaining", "0")
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, `{"message":"rate limit exceeded"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	if _, err := newTestClient(srv).ListMilestoneIssues(context.Background(), "ai-daming/qianshou", 1); err == nil {
		t.Fatal("systemic rate limit was flattened into per-Issue ERROR results")
	}
	if graphqlRequests.Load() != 1 {
		t.Fatalf("GraphQL requests after rate limit = %d, want immediate stop", graphqlRequests.Load())
	}
}

func TestListMilestoneIssuesUsesOneDependencyBatchPerHundredIssues(t *testing.T) {
	var graphqlRequests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/ai-daming/qianshou/issues":
			start, end := 1, 100
			if r.URL.Query().Get("page") == "2" {
				start, end = 101, 101
			} else {
				w.Header().Set("Link", fmt.Sprintf(`<%s/repos/ai-daming/qianshou/issues?milestone=1&state=all&per_page=100&page=2>; rel="next"`, requestOrigin(r)))
			}
			items := make([]map[string]any, 0, end-start+1)
			for number := start; number <= end; number++ {
				items = append(items, map[string]any{
					"repository_url": requestOrigin(r) + "/repos/ai-daming/qianshou",
					"number":         number,
					"title":          fmt.Sprintf("Issue %d", number),
					"state":          "open",
					"labels":         []any{},
					"milestone":      map[string]any{"number": 1},
				})
			}
			if err := json.NewEncoder(w).Encode(items); err != nil {
				t.Fatal(err)
			}
		case "/graphql":
			requestNumber := int(graphqlRequests.Add(1))
			var request struct {
				Query string `json:"query"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatal(err)
			}
			start, end := 1, 100
			if requestNumber == 2 {
				start, end = 101, 101
			}
			repository := map[string]any{"nameWithOwner": "ai-daming/qianshou"}
			for number := start; number <= end; number++ {
				alias := fmt.Sprintf("issue_%d", number)
				if !strings.Contains(request.Query, fmt.Sprintf("%s: issue(number:%d)", alias, number)) {
					t.Errorf("batch %d missing Issue #%d", requestNumber, number)
				}
				repository[alias] = map[string]any{
					"number": number,
					"blockedBy": map[string]any{
						"pageInfo": map[string]any{"hasNextPage": false},
						"nodes":    []any{},
					},
				}
			}
			if err := json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"repository": repository}}); err != nil {
				t.Fatal(err)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	issues, err := newTestClient(srv).ListMilestoneIssues(context.Background(), "ai-daming/qianshou", 1)
	if err != nil {
		t.Fatalf("ListMilestoneIssues: %v", err)
	}
	if len(issues) != 101 {
		t.Fatalf("issues = %d, want 101", len(issues))
	}
	if graphqlRequests.Load() != 2 {
		t.Fatalf("GraphQL requests = %d, want two batches for 101 Issues", graphqlRequests.Load())
	}
}

func TestMilestoneIssueFactsRejectMembershipMismatchAndNullPullRequestMarker(t *testing.T) {
	cases := []string{
		`{"repository_url":%q,"number":30,"title":"API","state":"open","labels":[],"milestone":{"number":2}}`,
		`{"repository_url":%q,"number":30,"title":"API","state":"open","labels":[],"milestone":{"number":1},"pull_request":null}`,
	}
	for _, template := range cases {
		t.Run(template, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprintf(w, "["+template+"]", requestOrigin(r)+"/repos/ai-daming/qianshou")
			}))
			defer srv.Close()
			if _, err := newTestClient(srv).ListMilestoneIssues(context.Background(), "ai-daming/qianshou", 1); err == nil {
				t.Fatal("ambiguous or wrong membership accepted")
			}
		})
	}
}

func TestIssueDependencyFailureIsVisibleInsteadOfReady(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/ai-daming/qianshou/issues/30" {
			fmt.Fprintf(w, `{"repository_url":%q,"number":30,"title":"API","state":"open","labels":[]}`, requestOrigin(r)+"/repos/ai-daming/qianshou")
			return
		}
		fmt.Fprint(w, `{"data":{"repository":{"nameWithOwner":"ai-daming/qianshou","issue":{"number":30,"blockedBy":{"pageInfo":{"hasNextPage":true},"nodes":[]}}}}}`)
	}))
	defer srv.Close()

	got, err := newTestClient(srv).GetIssue(context.Background(), "ai-daming/qianshou", 30)
	if err != nil {
		t.Fatalf("GetIssue metadata: %v", err)
	}
	if got.Dependency.Status != DependencyError || got.Dependency.Error == nil || got.Dependency.BlockedBy != nil {
		t.Fatalf("dependency = %+v, want explicit ERROR without fabricated blockers", got.Dependency)
	}
}

func TestRESTFactsFailClosedOnMissingContradictoryOrOversizedData(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"missing identity", `[{"number":1,"title":"M1","state":"open"}]`},
		{"missing title", `[{"url":"https://api.github.com/repos/a/b/milestones/1","number":1,"state":"open"}]`},
		{"missing state", `[{"url":"https://api.github.com/repos/a/b/milestones/1","number":1,"title":"M1"}]`},
		{"wrong state", `[{"url":"https://api.github.com/repos/a/b/milestones/1","number":1,"title":"M1","state":"unknown"}]`},
		{"duplicate conclusion key", `[{"url":"https://api.github.com/repos/a/b/milestones/1","number":1,"number":2,"title":"M1","state":"open"}]`},
		{"not an array", `{"number":1}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				fmt.Fprint(w, tc.body)
			}))
			defer srv.Close()
			if _, err := newTestClient(srv).ListMilestones(context.Background(), "ai-daming/qianshou"); err == nil {
				t.Fatalf("untrustworthy response accepted")
			}
		})
	}
}

func TestRESTFactsRejectWrongRepositoryIdentity(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `[{"url":%q,"number":1,"title":"M1","state":"open"}]`, requestOrigin(r)+"/repos/other/repo/milestones/1")
	}))
	defer srv.Close()
	if _, err := newTestClient(srv).ListMilestones(context.Background(), "ai-daming/qianshou"); err == nil {
		t.Fatal("facts for another repository accepted")
	}
}

func TestRESTFactsRejectRedirectedEvidenceSourceEvenWhenEmpty(t *testing.T) {
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `[]`)
	}))
	defer other.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+r.URL.RequestURI(), http.StatusFound)
	}))
	defer srv.Close()
	if _, err := newTestClient(srv).ListMilestones(context.Background(), "ai-daming/qianshou"); err == nil {
		t.Fatal("empty facts from a redirected evidence source accepted")
	}
}

func TestRESTFactsRejectDuplicateIdentityAcrossPages(t *testing.T) {
	var page int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page++
		if page == 1 {
			w.Header().Set("Link", fmt.Sprintf(`<%s%s&page=2>; rel="next"`, requestOrigin(r), r.URL.RequestURI()))
		}
		fmt.Fprintf(w, `[{"url":%q,"number":1,"title":"M1","state":"open"}]`, requestOrigin(r)+"/repos/ai-daming/qianshou/milestones/1")
	}))
	defer srv.Close()
	if _, err := newTestClient(srv).ListMilestones(context.Background(), "ai-daming/qianshou"); err == nil {
		t.Fatal("same milestone appeared twice without contradiction error")
	}
}

func TestRejectsInvalidRepositoryAndNumbers(t *testing.T) {
	c := NewClient("token", "https://api.github.com", "https://api.github.com/graphql", http.DefaultClient)
	if _, err := c.ListMilestones(context.Background(), "bad"); err == nil {
		t.Fatal("bad slug accepted")
	}
	if _, err := c.ListMilestoneIssues(context.Background(), "a/b", 0); err == nil {
		t.Fatal("milestone zero accepted")
	}
	if _, err := c.GetIssue(context.Background(), "a/b", -1); err == nil {
		t.Fatal("negative issue accepted")
	}
}

func requestOrigin(r *http.Request) string {
	return "http://" + r.Host
}

func TestSafeErrorDoesNotEchoSecretsOrRawBodies(t *testing.T) {
	secret := "super-secret-token"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `private upstream body `+secret, http.StatusUnauthorized)
	}))
	defer srv.Close()
	_, err := NewClient(secret, srv.URL, srv.URL+"/graphql", srv.Client()).ListMilestones(context.Background(), "a/b")
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("error leaked token: %v", err)
	}
}

func TestNextLinkMayBeRelative(t *testing.T) {
	var n int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		if n == 1 {
			w.Header().Set("Link", `</repos/a/b/milestones?state=all&per_page=100&page=2>; rel="next"`)
		}
		fmt.Fprint(w, `[]`)
	}))
	defer srv.Close()
	if _, err := newTestClient(srv).ListMilestones(context.Background(), "a/b"); err != nil {
		t.Fatalf("relative same-boundary next rejected: %v", err)
	}
}

func TestParsedNextURLKeepsExpectedCollectionPath(t *testing.T) {
	current, _ := url.Parse("https://api.github.com/repos/a/b/issues?milestone=1")
	next, err := parseNextLink([]string{`<https://api.github.com/repos/a/b/issues?milestone=1&page=2>; rel="next"`}, current)
	if err != nil || next == nil || next.Query().Get("page") != "2" {
		t.Fatalf("parseNextLink = %v, %v", next, err)
	}
}

func TestParsedNextURLRejectsChangedOrAmbiguousSelectorQuery(t *testing.T) {
	current, _ := url.Parse("https://api.github.com/repos/a/b/issues?milestone=1&state=all&per_page=100")
	for _, link := range []string{
		`<https://api.github.com/repos/a/b/issues?milestone=2&state=all&per_page=100&page=2>; rel="next"`,
		`<https://api.github.com/repos/a/b/issues?milestone=1&state=all&per_page=100&page=2&page=3>; rel="next"`,
		`<https://api.github.com/repos/a/b/issues?milestone=1&state=all&per_page=100&page=zero>; rel="next"`,
	} {
		if _, err := parseNextLink([]string{link}, current); err == nil {
			t.Fatalf("unsafe query accepted: %s", link)
		}
	}
}
