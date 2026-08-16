package ghfacts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func asError(err error, target **Error) bool { return errors.As(err, target) }

func newTestClient(t *testing.T, rest http.HandlerFunc, gql http.HandlerFunc) *Client {
	t.Helper()
	restSrv := httptest.NewServer(rest)
	t.Cleanup(restSrv.Close)
	gqlSrv := httptest.NewServer(gql)
	t.Cleanup(gqlSrv.Close)
	c, err := newClient("test-token", restSrv.URL, gqlSrv.URL, restSrv.Client())
	if err != nil {
		t.Fatalf("newClient: %v", err)
	}
	return c
}

func milestonePage(items ...string) string {
	if len(items) == 0 {
		return "[]"
	}
	return "[" + strings.Join(items, ",") + "]"
}

func issueItem(number int, labels ...string) string {
	nameObjs := make([]string, 0, len(labels))
	for _, l := range labels {
		nameObjs = append(nameObjs, fmt.Sprintf(`{"name":%q}`, l))
	}
	return fmt.Sprintf(`{"number":%d,"title":"issue %d","state":"open","labels":[%s]}`,
		number, number, strings.Join(nameObjs, ","))
}

func prItem(number int) string {
	return fmt.Sprintf(`{"number":%d,"title":"pr %d","state":"open","labels":[],"pull_request":{"url":"x"}}`, number, number)
}

func TestListMilestoneIssuesPaginatesAndFiltersPullRequests(t *testing.T) {
	full := make([]string, 100)
	for i := range full {
		full[i] = issueItem(i + 1)
	}
	full[7] = prItem(8) // a PR hidden in page one must be dropped
	var requests int
	c := newTestClient(t,
		func(w http.ResponseWriter, r *http.Request) {
			requests++
			if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
				t.Errorf("Authorization = %q", got)
			}
			if r.URL.Query().Get("milestone") != "1" || r.URL.Query().Get("state") != "all" {
				t.Errorf("unexpected query: %s", r.URL.RawQuery)
			}
			w.Header().Set("Content-Type", "application/json")
			if r.URL.Query().Get("page") == "1" {
				fmt.Fprint(w, milestonePage(full...))
				return
			}
			fmt.Fprint(w, milestonePage(issueItem(101, "workflow:delivery"), issueItem(102)))
		},
		func(w http.ResponseWriter, r *http.Request) { t.Errorf("unexpected GraphQL call") })
	issues, err := c.ListMilestoneIssues(context.Background(), "ai-daming/qianshou", 1)
	if err != nil {
		t.Fatalf("ListMilestoneIssues: %v", err)
	}
	if requests != 2 {
		t.Fatalf("pages fetched = %d, want 2", requests)
	}
	if len(issues) != 101 {
		t.Fatalf("issues = %d, want 101 (100 page one minus 1 PR, plus 2 page two)", len(issues))
	}
	if issues[0].Number != 1 || issues[100].Number != 102 {
		t.Fatalf("ordering broken: first=%d last=%d", issues[0].Number, issues[100].Number)
	}
	if len(issues[99].Labels) != 1 || issues[99].Labels[0] != "workflow:delivery" {
		t.Fatalf("labels not extracted: %+v", issues[99].Labels)
	}
}

func TestListMilestoneIssuesFailsClosed(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{"not found", 404, `{"message":"Not Found"}`},
		{"unauthorized", 401, `{"message":"Bad credentials"}`},
		{"rate limited", 429, `{"message":"API rate limit exceeded"}`},
		{"server error", 500, `{"message":"oops"}`},
		{"malformed json", 200, `{not-json`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}, nil)
			issues, err := c.ListMilestoneIssues(context.Background(), "ai-daming/qianshou", 7)
			if err == nil {
				t.Fatalf("expected failure, got %d issues", len(issues))
			}
			var e *Error
			if !asError(err, &e) {
				t.Fatalf("error is not *Error: %v", err)
			}
			if e.Op != OpListMilestoneIssues {
				t.Fatalf("op = %q", e.Op)
			}
			if !strings.Contains(err.Error(), "ai-daming/qianshou") || !strings.Contains(err.Error(), "milestone 7") {
				t.Fatalf("error lacks locator context: %v", err)
			}
			if strings.Contains(err.Error(), "test-token") {
				t.Fatalf("error leaks token: %v", err)
			}
		})
	}
}

func TestListMilestoneIssuesRejectsBadSlugAndEmptyToken(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("no request expected")
	}, nil)
	if _, err := c.ListMilestoneIssues(context.Background(), "no-slash", 1); err == nil {
		t.Fatalf("bad slug accepted")
	}
	if _, err := New(""); err == nil {
		t.Fatalf("empty token accepted")
	}
}

const gqlHappy = `{"data":{"repository":{"issue":{
	"parent":{"number":1},
	"blockedBy":{"nodes":[{"number":29,"state":"OPEN"},{"number":3,"state":"CLOSED"}]}
}}}}`

func TestRelationshipsParsesParentAndBlockedBy(t *testing.T) {
	var sawAuth bool
	c := newTestClient(t,
		func(w http.ResponseWriter, r *http.Request) { t.Errorf("unexpected REST call") },
		func(w http.ResponseWriter, r *http.Request) {
			sawAuth = r.Header.Get("Authorization") == "Bearer test-token"
			var req struct {
				Query     string `json:"query"`
				Variables struct {
					Owner  string `json:"owner"`
					Repo   string `json:"repo"`
					Number int    `json:"number"`
				} `json:"variables"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("decode graphql request: %v", err)
			}
			if req.Variables.Owner != "ai-daming" || req.Variables.Repo != "qianshou" || req.Variables.Number != 30 {
				t.Errorf("unexpected variables: %+v", req.Variables)
			}
			fmt.Fprint(w, gqlHappy)
		})
	rel, err := c.Relationships(context.Background(), "ai-daming/qianshou", 30)
	if err != nil {
		t.Fatalf("Relationships: %v", err)
	}
	if !sawAuth {
		t.Fatalf("GraphQL call missing auth header")
	}
	if rel.Parent == nil || *rel.Parent != 1 {
		t.Fatalf("parent = %v, want 1", rel.Parent)
	}
	if len(rel.BlockedBy) != 2 || rel.BlockedBy[0].Number != 29 || rel.BlockedBy[0].State != "OPEN" ||
		rel.BlockedBy[1].Number != 3 || rel.BlockedBy[1].State != "CLOSED" {
		t.Fatalf("blockedBy not parsed: %+v", rel.BlockedBy)
	}
}

func TestRelationshipsHandlesNoParentNoDeps(t *testing.T) {
	c := newTestClient(t, nil, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":{"repository":{"issue":{"parent":null,"blockedBy":{"nodes":[]}}}}}`)
	})
	rel, err := c.Relationships(context.Background(), "ai-daming/qianshou", 1)
	if err != nil {
		t.Fatalf("Relationships: %v", err)
	}
	if rel.Parent != nil || len(rel.BlockedBy) != 0 {
		t.Fatalf("expected empty relationships: %+v", rel)
	}
}

func TestRelationshipsFailsClosed(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{"graphql errors", 200, `{"errors":[{"message":"Field 'blockedBy' doesn't exist"}],"data":null}`},
		{"http error", 500, `{"message":"oops"}`},
		{"missing issue", 200, `{"data":{"repository":{"issue":null}}}`},
		{"missing repository", 200, `{"data":{"repository":null}}`},
		{"malformed", 200, `{not-json`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newTestClient(t, nil, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			})
			rel, err := c.Relationships(context.Background(), "ai-daming/qianshou", 42)
			if err == nil {
				t.Fatalf("expected failure, got %+v", rel)
			}
			var e *Error
			if !asError(err, &e) {
				t.Fatalf("error is not *Error: %v", err)
			}
			if e.Op != OpFetchRelationships {
				t.Fatalf("op = %q", e.Op)
			}
			if !strings.Contains(err.Error(), "#42") {
				t.Fatalf("error lacks issue locator: %v", err)
			}
		})
	}
}

func TestListMilestoneIssuesFailsClosedOnNullBody(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `null`)
	}, nil)
	if _, err := c.ListMilestoneIssues(context.Background(), "ai-daming/qianshou", 1); err == nil {
		t.Fatalf("null body must fail closed, not read as an empty milestone")
	}
}

func TestGetIssueFailsClosedOnNullBody(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `null`)
	}, nil)
	if _, err := c.GetIssue(context.Background(), "ai-daming/qianshou", 4); err == nil {
		t.Fatalf("null body must fail closed, not fabricate an empty issue")
	}
}

func TestRelationshipsFailsClosedWhenBlockedBySchemaMissing(t *testing.T) {
	c := newTestClient(t, nil, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":{"repository":{"issue":{"parent":null}}}}`)
	})
	if _, err := c.Relationships(context.Background(), "ai-daming/qianshou", 4); err == nil {
		t.Fatalf("missing blockedBy field must fail closed, not read as no dependencies")
	}
}

func TestRelationshipsPaginatesBlockedByBeyondFirstPage(t *testing.T) {
	pageOneNodes := make([]string, 100)
	for i := range pageOneNodes {
		pageOneNodes[i] = fmt.Sprintf(`{"number":%d,"state":"OPEN"}`, i+1)
	}
	var cursors []string
	c := newTestClient(t, nil, func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Variables struct {
				After string `json:"after"`
			} `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
		}
		cursors = append(cursors, req.Variables.After)
		if req.Variables.After == "" {
			fmt.Fprintf(w, `{"data":{"repository":{"issue":{"parent":null,"blockedBy":{"pageInfo":{"hasNextPage":true,"endCursor":"CURSOR-1"},"nodes":[%s]}}}}}`,
				strings.Join(pageOneNodes, ","))
			return
		}
		fmt.Fprint(w, `{"data":{"repository":{"issue":{"parent":null,"blockedBy":{"pageInfo":{"hasNextPage":false},"nodes":[{"number":101,"state":"OPEN"},{"number":102,"state":"CLOSED"}]}}}}}`)
	})
	rel, err := c.Relationships(context.Background(), "ai-daming/qianshou", 4)
	if err != nil {
		t.Fatalf("Relationships: %v", err)
	}
	if len(rel.BlockedBy) != 102 {
		t.Fatalf("blockedBy = %d items, want 102（first:100 不允许静默截断）", len(rel.BlockedBy))
	}
	if rel.BlockedBy[101].Number != 102 || rel.BlockedBy[101].State != "CLOSED" {
		t.Fatalf("second page not merged in order: %+v", rel.BlockedBy[101])
	}
	if len(cursors) != 2 || cursors[0] != "" || cursors[1] != "CURSOR-1" {
		t.Fatalf("pagination cursors wrong: %v", cursors)
	}
}
