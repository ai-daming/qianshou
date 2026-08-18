package ghfacts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
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
	return fmt.Sprintf(`{"number":%d,"title":"issue %d","state":"open","labels":[%s],"repository_url":"https://api.github.com/repos/ai-daming/qianshou","milestone":{"number":1}}`,
		number, number, strings.Join(nameObjs, ","))
}

func prItem(number int) string {
	return fmt.Sprintf(`{"number":%d,"title":"pr %d","state":"open","labels":[],"repository_url":"https://api.github.com/repos/ai-daming/qianshou","milestone":{"number":1},"pull_request":{"url":"x"}}`, number, number)
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
			w.Header().Set("Content-Type", "application/json")
			requests++
			if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
				t.Errorf("Authorization = %q", got)
			}
			if r.URL.Query().Get("milestone") != "1" || r.URL.Query().Get("state") != "all" {
				t.Errorf("unexpected query: %s", r.URL.RawQuery)
			}
			w.Header().Set("Content-Type", "application/json")
			if r.URL.Query().Get("page") == "1" {
				w.Header().Set("Link", fmt.Sprintf(`<http://%s%s?milestone=1&state=all&per_page=100&page=2>; rel="next"`, r.Host, r.URL.Path))
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
	if issues[0].Number() != 1 || issues[100].Number() != 102 {
		t.Fatalf("ordering broken: first=%d last=%d", issues[0].Number(), issues[100].Number())
	}
	if labels := issues[99].Labels(); len(labels) != 1 || labels[0] != "workflow:delivery" {
		t.Fatalf("labels not extracted: %+v", labels)
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
				w.Header().Set("Content-Type", "application/json")
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
		w.Header().Set("Content-Type", "application/json")
		t.Errorf("no request expected")
	}, nil)
	if _, err := c.ListMilestoneIssues(context.Background(), "no-slash", 1); err == nil {
		t.Fatalf("bad slug accepted")
	}
	if _, err := New(""); err == nil {
		t.Fatalf("empty token accepted")
	}
}

const gqlHappy = `{"data":{"repository":{"nameWithOwner":"ai-daming/qianshou","issue":{
	"number":30,
	"parent":{"number":1},
	"blockedBy":{"pageInfo":{"hasNextPage":false},"nodes":[{"number":29,"state":"OPEN"},{"number":3,"state":"CLOSED"}]}
}}}}`

func TestRelationshipsParsesParentAndBlockedBy(t *testing.T) {
	var sawAuth bool
	c := newTestClient(t,
		func(w http.ResponseWriter, r *http.Request) { t.Errorf("unexpected REST call") },
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
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
	if parent, ok := rel.Parent(); !ok || parent != 1 {
		t.Fatalf("parent = (%d, %v), want (1, true)", parent, ok)
	}
	if blocked := rel.BlockedBy(); len(blocked) != 2 || blocked[0].Number != 29 || blocked[0].State != "OPEN" ||
		blocked[1].Number != 3 || blocked[1].State != "CLOSED" {
		t.Fatalf("blockedBy not parsed: %+v", blocked)
	}
}

func TestRelationshipsHandlesNoParentNoDeps(t *testing.T) {
	c := newTestClient(t, nil, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":{"repository":{"nameWithOwner":"ai-daming/qianshou","issue":{"number":1,"parent":null,"blockedBy":{"pageInfo":{"hasNextPage":false},"nodes":[]}}}}}`)
	})
	rel, err := c.Relationships(context.Background(), "ai-daming/qianshou", 1)
	if err != nil {
		t.Fatalf("Relationships: %v", err)
	}
	if _, hasParent := rel.Parent(); hasParent || len(rel.BlockedBy()) != 0 {
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
				w.Header().Set("Content-Type", "application/json")
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
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `null`)
	}, nil)
	if _, err := c.ListMilestoneIssues(context.Background(), "ai-daming/qianshou", 1); err == nil {
		t.Fatalf("null body must fail closed, not read as an empty milestone")
	}
}

func TestGetIssueFailsClosedOnNullBody(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `null`)
	}, nil)
	if _, err := c.GetIssue(context.Background(), "ai-daming/qianshou", 4); err == nil {
		t.Fatalf("null body must fail closed, not fabricate an empty issue")
	}
}

func TestRelationshipsFailsClosedWhenBlockedBySchemaMissing(t *testing.T) {
	c := newTestClient(t, nil, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":{"repository":{"nameWithOwner":"ai-daming/qianshou","issue":{"number":4,"parent":null}}}}`)
	})
	if _, err := c.Relationships(context.Background(), "ai-daming/qianshou", 4); err == nil {
		t.Fatalf("missing blockedBy field must fail closed, not read as no dependencies")
	}
}

func TestRelationshipsPaginatesBlockedByBeyondFirstPage(t *testing.T) {
	pageOneNodes := make([]string, 100)
	for i := range pageOneNodes {
		pageOneNodes[i] = fmt.Sprintf(`{"number":%d,"state":"OPEN"}`, 1000+i)
	}
	var cursors []string
	c := newTestClient(t, nil, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
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
			fmt.Fprintf(w, `{"data":{"repository":{"nameWithOwner":"ai-daming/qianshou","issue":{"number":4,"parent":null,"blockedBy":{"pageInfo":{"hasNextPage":true,"endCursor":"CURSOR-1"},"nodes":[%s]}}}}}`,
				strings.Join(pageOneNodes, ","))
			return
		}
		fmt.Fprint(w, `{"data":{"repository":{"nameWithOwner":"ai-daming/qianshou","issue":{"number":4,"parent":null,"blockedBy":{"pageInfo":{"hasNextPage":false},"nodes":[{"number":101,"state":"OPEN"},{"number":102,"state":"CLOSED"}]}}}}}`)
	})
	rel, err := c.Relationships(context.Background(), "ai-daming/qianshou", 4)
	if err != nil {
		t.Fatalf("Relationships: %v", err)
	}
	if blocked := rel.BlockedBy(); len(blocked) != 102 {
		t.Fatalf("blockedBy = %d items, want 102（first:100 不允许静默截断）", len(blocked))
	} else if blocked[101].Number != 102 || blocked[101].State != "CLOSED" {
		t.Fatalf("second page not merged in order: %+v", blocked[101])
	}
	if len(cursors) != 2 || cursors[0] != "" || cursors[1] != "CURSOR-1" {
		t.Fatalf("pagination cursors wrong: %v", cursors)
	}
}

func TestRelationshipsFailsClosedWhenInnerSchemaMissing(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"pageInfo missing", `{"data":{"repository":{"nameWithOwner":"ai-daming/qianshou","issue":{"number":4,"parent":null,"blockedBy":{"nodes":[]}}}}}`},
		{"pageInfo null", `{"data":{"repository":{"nameWithOwner":"ai-daming/qianshou","issue":{"number":4,"parent":null,"blockedBy":{"pageInfo":null,"nodes":[]}}}}}`},
		{"nodes missing", `{"data":{"repository":{"nameWithOwner":"ai-daming/qianshou","issue":{"number":4,"parent":null,"blockedBy":{"pageInfo":{"hasNextPage":false}}}}}}`},
		{"nodes null", `{"data":{"repository":{"nameWithOwner":"ai-daming/qianshou","issue":{"number":4,"parent":null,"blockedBy":{"pageInfo":{"hasNextPage":false},"nodes":null}}}}}`},
		{"empty blockedBy object", `{"data":{"repository":{"nameWithOwner":"ai-daming/qianshou","issue":{"number":4,"parent":null,"blockedBy":{}}}}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newTestClient(t, nil, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, tc.body)
			})
			if _, err := c.Relationships(context.Background(), "ai-daming/qianshou", 4); err == nil {
				t.Fatalf("inner schema drift accepted as no dependencies: %s", tc.name)
			}
		})
	}
}

func TestRelationshipsFailsClosedWhenNextPageLacksCursor(t *testing.T) {
	c := newTestClient(t, nil, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":{"repository":{"nameWithOwner":"ai-daming/qianshou","issue":{"number":4,"parent":null,"blockedBy":{"pageInfo":{"hasNextPage":true,"endCursor":""},"nodes":[]}}}}}`)
	})
	if _, err := c.Relationships(context.Background(), "ai-daming/qianshou", 4); err == nil {
		t.Fatalf("hasNextPage without endCursor cannot continue pagination and must fail closed")
	}
}

// --- Round 3 + same-class sweep: presence-aware decoding everywhere ---

func TestRelationshipsFailsClosedOnPresenceDrift(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"parent field missing", `{"data":{"repository":{"nameWithOwner":"ai-daming/qianshou","issue":{"number":4,"blockedBy":{"pageInfo":{"hasNextPage":false},"nodes":[]}}}}}`},
		{"parent object without number", `{"data":{"repository":{"nameWithOwner":"ai-daming/qianshou","issue":{"number":4,"parent":{},"blockedBy":{"pageInfo":{"hasNextPage":false},"nodes":[]}}}}}`},
		{"parent number zero", `{"data":{"repository":{"nameWithOwner":"ai-daming/qianshou","issue":{"number":4,"parent":{"number":0},"blockedBy":{"pageInfo":{"hasNextPage":false},"nodes":[]}}}}}`},
		{"hasNextPage missing", `{"data":{"repository":{"nameWithOwner":"ai-daming/qianshou","issue":{"number":4,"parent":null,"blockedBy":{"pageInfo":{"endCursor":"c"},"nodes":[]}}}}}`},
		{"hasNextPage null", `{"data":{"repository":{"nameWithOwner":"ai-daming/qianshou","issue":{"number":4,"parent":null,"blockedBy":{"pageInfo":{"hasNextPage":null},"nodes":[]}}}}}`},
		{"null node element", `{"data":{"repository":{"nameWithOwner":"ai-daming/qianshou","issue":{"number":4,"parent":null,"blockedBy":{"pageInfo":{"hasNextPage":false},"nodes":[null]}}}}}`},
		{"empty node object", `{"data":{"repository":{"nameWithOwner":"ai-daming/qianshou","issue":{"number":4,"parent":null,"blockedBy":{"pageInfo":{"hasNextPage":false},"nodes":[{}]}}}}}`},
		{"node number zero", `{"data":{"repository":{"nameWithOwner":"ai-daming/qianshou","issue":{"number":4,"parent":null,"blockedBy":{"pageInfo":{"hasNextPage":false},"nodes":[{"number":0,"state":"OPEN"}]}}}}}`},
		{"node state lowercase from graphql", `{"data":{"repository":{"nameWithOwner":"ai-daming/qianshou","issue":{"number":4,"parent":null,"blockedBy":{"pageInfo":{"hasNextPage":false},"nodes":[{"number":9,"state":"open"}]}}}}}`},
		{"node state unknown enum", `{"data":{"repository":{"nameWithOwner":"ai-daming/qianshou","issue":{"number":4,"parent":null,"blockedBy":{"pageInfo":{"hasNextPage":false},"nodes":[{"number":9,"state":"MERGED"}]}}}}}`},
		{"node state missing", `{"data":{"repository":{"nameWithOwner":"ai-daming/qianshou","issue":{"number":4,"parent":null,"blockedBy":{"pageInfo":{"hasNextPage":false},"nodes":[{"number":9}]}}}}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newTestClient(t, nil, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, tc.body)
			})
			if _, err := c.Relationships(context.Background(), "ai-daming/qianshou", 4); err == nil {
				t.Fatalf("presence drift accepted: %s", tc.name)
			}
		})
	}
}

func TestRelationshipsAcceptsExplicitNullParent(t *testing.T) {
	c := newTestClient(t, nil, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":{"repository":{"nameWithOwner":"ai-daming/qianshou","issue":{"number":4,"parent":null,"blockedBy":{"pageInfo":{"hasNextPage":false},"nodes":[]}}}}}`)
	})
	rel, err := c.Relationships(context.Background(), "ai-daming/qianshou", 4)
	if err != nil {
		t.Fatalf("explicit null parent is legal: %v", err)
	}
	if _, hasParent := rel.Parent(); hasParent || len(rel.BlockedBy()) != 0 {
		t.Fatalf("unexpected facts: %+v", rel)
	}
}

func TestListMilestoneIssuesFailsClosedOnItemDrift(t *testing.T) {
	cases := []struct {
		name string
		item string
	}{
		{"number missing", `{"title":"x","state":"open","labels":[]}`},
		{"state missing", `{"number":1,"title":"x","labels":[]}`},
		{"state graphql-cased", `{"number":1,"title":"x","state":"OPEN","labels":[]}`},
		{"state unknown", `{"number":1,"title":"x","state":"draft","labels":[]}`},
		{"labels missing", `{"number":1,"title":"x","state":"open"}`},
		{"labels null", `{"number":1,"title":"x","state":"open","labels":null}`},
		{"label name empty", `{"number":1,"title":"x","state":"open","labels":[{"name":""}]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, "["+tc.item+"]")
			}, nil)
			if _, err := c.ListMilestoneIssues(context.Background(), "ai-daming/qianshou", 1); err == nil {
				t.Fatalf("item drift accepted: %s", tc.name)
			}
		})
	}
}

func TestListMilestoneIssuesFailsClosedOnDuplicateNumbers(t *testing.T) {
	dup := issueItem(7, "workflow:delivery")
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, milestonePage(dup, dup))
	}, nil)
	if _, err := c.ListMilestoneIssues(context.Background(), "ai-daming/qianshou", 1); err == nil {
		t.Fatalf("duplicate issue numbers accepted as two facts")
	}
}

func TestGetIssueFailsClosedOnNumberMismatch(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, issueItem(99, "workflow:delivery"))
	}, nil)
	if _, err := c.GetIssue(context.Background(), "ai-daming/qianshou", 4); err == nil {
		t.Fatalf("response for #99 accepted as fact for #4")
	}
}

// --- Round 4: unified fact invariants (RED before implementation) ---

func TestListMilestoneIssuesFailsClosedOnTitleDrift(t *testing.T) {
	cases := []struct {
		name string
		item string
	}{
		{"title missing", `{"number":1,"state":"open","labels":[]}`},
		{"title null", `{"number":1,"title":null,"state":"open","labels":[]}`},
		{"title empty", `{"number":1,"title":"","state":"open","labels":[]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, "["+tc.item+"]")
			}, nil)
			if _, err := c.ListMilestoneIssues(context.Background(), "ai-daming/qianshou", 1); err == nil {
				t.Fatalf("title drift accepted: %s", tc.name)
			}
		})
	}
}

func TestGetIssueFailsClosedOnTitleDrift(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"number":4,"title":"","state":"open","labels":[]}`)
	}, nil)
	if _, err := c.GetIssue(context.Background(), "ai-daming/qianshou", 4); err == nil {
		t.Fatalf("empty title accepted as a fact for #4")
	}
}

func TestListMilestoneIssuesFailsClosedOnDuplicateLabels(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `[{"number":1,"title":"x","state":"open","labels":[{"name":"a"},{"name":"a"}]}]`)
	}, nil)
	if _, err := c.ListMilestoneIssues(context.Background(), "ai-daming/qianshou", 1); err == nil {
		t.Fatalf("duplicate label names accepted as one fact twice")
	}
}

func TestRelationshipsFailClosedOnContradictoryPages(t *testing.T) {
	cases := []struct {
		name    string
		pageOne string
		pageTwo string
	}{
		{
			name:    "parent differs across pages",
			pageOne: `{"data":{"repository":{"nameWithOwner":"ai-daming/qianshou","issue":{"number":4,"parent":{"number":1},"blockedBy":{"pageInfo":{"hasNextPage":true,"endCursor":"C1"},"nodes":[{"number":10,"state":"OPEN"}]}}}}}`,
			pageTwo: `{"data":{"repository":{"nameWithOwner":"ai-daming/qianshou","issue":{"number":4,"parent":{"number":2},"blockedBy":{"pageInfo":{"hasNextPage":false},"nodes":[{"number":11,"state":"OPEN"}]}}}}}`,
		},
		{
			name:    "same blocker repeated across pages",
			pageOne: `{"data":{"repository":{"nameWithOwner":"ai-daming/qianshou","issue":{"number":4,"parent":null,"blockedBy":{"pageInfo":{"hasNextPage":true,"endCursor":"C1"},"nodes":[{"number":10,"state":"OPEN"}]}}}}}`,
			pageTwo: `{"data":{"repository":{"nameWithOwner":"ai-daming/qianshou","issue":{"number":4,"parent":null,"blockedBy":{"pageInfo":{"hasNextPage":false},"nodes":[{"number":10,"state":"OPEN"}]}}}}}`,
		},
		{
			name:    "same blocker with conflicting state",
			pageOne: `{"data":{"repository":{"nameWithOwner":"ai-daming/qianshou","issue":{"number":4,"parent":null,"blockedBy":{"pageInfo":{"hasNextPage":true,"endCursor":"C1"},"nodes":[{"number":10,"state":"OPEN"}]}}}}}`,
			pageTwo: `{"data":{"repository":{"nameWithOwner":"ai-daming/qianshou","issue":{"number":4,"parent":null,"blockedBy":{"pageInfo":{"hasNextPage":false},"nodes":[{"number":10,"state":"CLOSED"}]}}}}}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			call := 0
			c := newTestClient(t, nil, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				call++
				if call == 1 {
					fmt.Fprint(w, tc.pageOne)
					return
				}
				fmt.Fprint(w, tc.pageTwo)
			})
			if _, err := c.Relationships(context.Background(), "ai-daming/qianshou", 4); err == nil {
				t.Fatalf("contradictory pages merged into a snapshot that never existed: %s", tc.name)
			}
		})
	}
}

func TestRelationshipsFailClosedOnSelfReferences(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{
			name: "issue is its own parent",
			body: `{"data":{"repository":{"nameWithOwner":"ai-daming/qianshou","issue":{"number":4,"parent":{"number":4},"blockedBy":{"pageInfo":{"hasNextPage":false},"nodes":[]}}}}}`,
		},
		{
			name: "issue blocks itself",
			body: `{"data":{"repository":{"nameWithOwner":"ai-daming/qianshou","issue":{"number":4,"parent":null,"blockedBy":{"pageInfo":{"hasNextPage":false},"nodes":[{"number":4,"state":"OPEN"}]}}}}}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newTestClient(t, nil, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, tc.body)
			})
			if _, err := c.Relationships(context.Background(), "ai-daming/qianshou", 4); err == nil {
				t.Fatalf("self-referential fact accepted: %s", tc.name)
			}
		})
	}
}

// --- Round 5 falsification set: transport and pagination-protocol layers ---

func TestRestResponseOverLimitFailsClosed(t *testing.T) {
	// A JSON array opener followed by over-limit whitespace stays a valid
	// document when truncated: only an over-limit read can catch it.
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("[]"))
		w.Write(bytes.Repeat([]byte(" "), 4<<20+1))
	}, nil)
	if _, err := c.ListMilestoneIssues(context.Background(), "ai-daming/qianshou", 1); err == nil {
		t.Fatalf("over-limit response must fail closed, not truncate into a valid prefix")
	}
}

func TestRestTrailingGarbageBeyondLimitFailsClosed(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("[]"))
		w.Write(bytes.Repeat([]byte(" "), 4<<20+1))
		w.Write([]byte("]"))
	}, nil)
	if _, err := c.ListMilestoneIssues(context.Background(), "ai-daming/qianshou", 1); err == nil {
		t.Fatalf("reviewer repro: [] + >4MiB padding + ] read as an empty milestone")
	}
}

func TestGraphQLTrailingGarbageBeyondLimitFailsClosed(t *testing.T) {
	c := newTestClient(t, nil, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":{"repository":{"nameWithOwner":"ai-daming/qianshou","issue":{"number":4,"parent":null,"blockedBy":{"pageInfo":{"hasNextPage":false},"nodes":[]}}}}}`))
		w.Write(bytes.Repeat([]byte(" "), 1<<20+1))
		w.Write([]byte("]"))
	})
	if _, err := c.Relationships(context.Background(), "ai-daming/qianshou", 4); err == nil {
		t.Fatalf("reviewer repro: valid doc + >1MiB padding + ] read as no dependencies")
	}
}

func TestListMilestoneIssuesFollowsLinkHeaderNext(t *testing.T) {
	var requests int
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		requests++
		if r.URL.Query().Get("page") == "" || r.URL.Query().Get("page") == "1" {
			// short page that still has a next page: count-based logic stops here
			w.Header().Set("Link", fmt.Sprintf(`<http://%s%s?milestone=1&state=all&per_page=100&page=2>; rel="next"`, r.Host, r.URL.Path))
			fmt.Fprint(w, milestonePage(issueItem(1, "workflow:delivery")))
			return
		}
		fmt.Fprint(w, milestonePage(issueItem(2, "workflow:delivery")))
	}, nil)
	issues, err := c.ListMilestoneIssues(context.Background(), "ai-daming/qianshou", 1)
	if err != nil {
		t.Fatalf("ListMilestoneIssues: %v", err)
	}
	if requests != 2 || len(issues) != 2 {
		t.Fatalf("requests = %d issues = %d, want 2/2（Link rel=next 必须跟随，短页不算结束）", requests, len(issues))
	}
}

func TestListMilestoneIssuesFailsClosedOnForeignNextLink(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Link", `<http://evil.example.com/repos/o/r/issues?page=2>; rel="next"`)
		fmt.Fprint(w, milestonePage(issueItem(1, "workflow:delivery")))
	}, nil)
	_, err := c.ListMilestoneIssues(context.Background(), "ai-daming/qianshou", 1)
	if err == nil {
		t.Fatalf("foreign next link followed or ignored: token must never leave the API origin")
	}
	if !strings.Contains(err.Error(), "evil.example.com") && !strings.Contains(err.Error(), "同源") {
		t.Fatalf("error should name the origin violation: %v", err)
	}
}

func TestListMilestoneIssuesFailsClosedOnMalformedLinkHeader(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Link", `not-a-link-header`)
		fmt.Fprint(w, milestonePage(issueItem(1, "workflow:delivery")))
	}, nil)
	if _, err := c.ListMilestoneIssues(context.Background(), "ai-daming/qianshou", 1); err == nil {
		t.Fatalf("malformed Link header must fail closed, not pass silently")
	}
}

func TestRequestsTimeOutInsteadOfHanging(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second) // hang, then answer a perfectly valid page
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, milestonePage(issueItem(1, "workflow:delivery")))
	}, nil)
	c.requestTimeout = 150 * time.Millisecond
	start := time.Now()
	if _, err := c.ListMilestoneIssues(context.Background(), "ai-daming/qianshou", 1); err == nil {
		t.Fatalf("hung response must fail via deadline, not block forever")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("deadline did not apply: %v", elapsed)
	}
}

func TestNonJSONContentTypeFailsClosed(t *testing.T) {
	restC := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "[]") // valid JSON with a wrong type isolates the check
	}, nil)
	if _, err := restC.ListMilestoneIssues(context.Background(), "ai-daming/qianshou", 1); err == nil {
		t.Fatalf("200 + text/html accepted as facts (proxy error page scenario)")
	}
	gqlC := newTestClient(t, nil, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `{"data":{"repository":{"nameWithOwner":"ai-daming/qianshou","issue":{"number":4,"parent":null,"blockedBy":{"pageInfo":{"hasNextPage":false},"nodes":[]}}}}}`)
	})
	if _, err := gqlC.Relationships(context.Background(), "ai-daming/qianshou", 4); err == nil {
		t.Fatalf("GraphQL 200 + text/html accepted as facts")
	}
}

func TestListMilestoneIssuesFailsClosedOnUnboundedPagination(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		page := r.URL.Query().Get("page")
		if page == "" {
			page = "1"
		}
		n, err := strconv.Atoi(page)
		if err != nil {
			t.Errorf("bad page param: %v", err)
			return
		}
		w.Header().Set("Link", fmt.Sprintf(`<http://%s%s?milestone=1&state=all&per_page=100&page=%d>; rel="next"`, r.Host, r.URL.Path, n+1))
		full := make([]string, pageSize)
		for i := range full {
			full[i] = issueItem((n-1)*pageSize + i + 1)
		}
		fmt.Fprint(w, milestonePage(full...))
	}, nil)
	if _, err := c.ListMilestoneIssues(context.Background(), "ai-daming/qianshou", 1); err == nil {
		t.Fatalf("endless next links must hit the page cap, not loop forever")
	}
}

// --- Round 6 falsification set: binding, ambiguity, channel dimensions ---

func TestRedirectsAreRefusedAndTokenNeverLeaves(t *testing.T) {
	var targetRequests int
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetRequests++
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, milestonePage(issueItem(77)))
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		http.Redirect(w, r, target.URL+"/repos/ai-daming/qianshou/issues", http.StatusFound)
	}))
	defer source.Close()
	c, err := newClient("test-token", source.URL, source.URL, source.Client())
	if err != nil {
		t.Fatalf("newClient: %v", err)
	}
	if _, err := c.ListMilestoneIssues(context.Background(), "ai-daming/qianshou", 1); err == nil {
		t.Fatalf("redirected response accepted as facts from the requested origin")
	}
	if targetRequests != 0 {
		t.Fatalf("redirect was followed: credential reached a different origin %d time(s)", targetRequests)
	}
}

func TestContentTypeMustBeExactApplicationJSON(t *testing.T) {
	t.Run("prefix lookalike rejected", func(t *testing.T) {
		c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/jsonp")
			fmt.Fprint(w, "[]")
		}, nil)
		if _, err := c.ListMilestoneIssues(context.Background(), "ai-daming/qianshou", 1); err == nil {
			t.Fatalf("application/jsonp accepted as application/json")
		}
	})
	t.Run("charset parameter accepted", func(t *testing.T) {
		c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			fmt.Fprint(w, milestonePage(issueItem(1, "workflow:delivery")))
		}, nil)
		if _, err := c.ListMilestoneIssues(context.Background(), "ai-daming/qianshou", 1); err != nil {
			t.Fatalf("application/json with charset must pass: %v", err)
		}
	})
}

func TestNextLinkMustStayWithinRequestedScope(t *testing.T) {
	cases := []struct {
		name string
		link string
		path string
	}{
		{
			name: "same origin, different repository and milestone",
			link: `<http://%s%s?milestone=999&state=all&per_page=100&page=2>; rel="next"`,
			path: "/repos/other/repo/issues",
		},
		{
			name: "same endpoint, mutated milestone",
			link: `<http://%s%s?milestone=999&state=all&per_page=100&page=2>; rel="next"`,
			path: "/repos/ai-daming/qianshou/issues",
		},
		{
			name: "unexpected extra parameter",
			link: `<http://%s%s?milestone=1&state=all&per_page=100&page=2&injected=1>; rel="next"`,
			path: "/repos/ai-daming/qianshou/issues",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Query().Get("page") == "" || r.URL.Query().Get("page") == "1" {
					// the poisoned next link; page two answers without a
					// further link so only binding, not duplicates, can catch it
					w.Header().Set("Link", fmt.Sprintf(tc.link, r.Host, tc.path))
					fmt.Fprint(w, milestonePage(issueItem(1, "workflow:delivery")))
					return
				}
				fmt.Fprint(w, milestonePage(issueItem(2, "workflow:delivery")))
			}, nil)
			if _, err := c.ListMilestoneIssues(context.Background(), "ai-daming/qianshou", 1); err == nil {
				t.Fatalf("next link escaped the requested scope: %s", tc.name)
			}
		})
	}
}

func TestAllLinkHeaderValuesAreConsidered(t *testing.T) {
	t.Run("next in second value is followed", func(t *testing.T) {
		var requests int
		c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			requests++
			if r.URL.Query().Get("page") == "" || r.URL.Query().Get("page") == "1" {
				w.Header().Add("Link", fmt.Sprintf(`<http://%s%s?milestone=1&state=all&per_page=100&page=1>; rel="prev"`, r.Host, r.URL.Path))
				w.Header().Add("Link", fmt.Sprintf(`<http://%s%s?milestone=1&state=all&per_page=100&page=2>; rel="next"`, r.Host, r.URL.Path))
				fmt.Fprint(w, milestonePage(issueItem(1, "workflow:delivery")))
				return
			}
			fmt.Fprint(w, milestonePage(issueItem(2, "workflow:delivery")))
		}, nil)
		issues, err := c.ListMilestoneIssues(context.Background(), "ai-daming/qianshou", 1)
		if err != nil {
			t.Fatalf("ListMilestoneIssues: %v", err)
		}
		if requests != 2 || len(issues) != 2 {
			t.Fatalf("second Link value ignored: requests=%d issues=%d", requests, len(issues))
		}
	})
	t.Run("two next links are ambiguity, not choice", func(t *testing.T) {
		c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Add("Link", fmt.Sprintf(`<http://%s%s?milestone=1&state=all&per_page=100&page=2>; rel="next"`, r.Host, r.URL.Path))
			w.Header().Add("Link", fmt.Sprintf(`<http://%s%s?milestone=1&state=all&per_page=100&page=3>; rel="next"`, r.Host, r.URL.Path))
			fmt.Fprint(w, milestonePage(issueItem(1, "workflow:delivery")))
		}, nil)
		if _, err := c.ListMilestoneIssues(context.Background(), "ai-daming/qianshou", 1); err == nil {
			t.Fatalf("contradictory next pages silently resolved by picking one")
		}
	})
}

func TestGraphQLResponseMustEchoRequestedIdentity(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{
			name: "issue number mismatch",
			body: `{"data":{"repository":{"nameWithOwner":"ai-daming/qianshou","issue":{"number":99,"parent":null,"blockedBy":{"pageInfo":{"hasNextPage":false},"nodes":[]}}}}}`,
		},
		{
			name: "repository identity mismatch",
			body: `{"data":{"repository":{"nameWithOwner":"other/repo","issue":{"number":4,"parent":null,"blockedBy":{"pageInfo":{"hasNextPage":false},"nodes":[]}}}}}`,
		},
		{
			name: "identity fields missing",
			body: `{"data":{"repository":{"issue":{"parent":null,"blockedBy":{"pageInfo":{"hasNextPage":false},"nodes":[]}}}}}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newTestClient(t, nil, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, tc.body)
			})
			if _, err := c.Relationships(context.Background(), "ai-daming/qianshou", 4); err == nil {
				t.Fatalf("response about something else accepted as facts for #4: %s", tc.name)
			}
		})
	}
}

func TestNetworkJSONRejectsDuplicateKeys(t *testing.T) {
	t.Run("graphql pagination signal", func(t *testing.T) {
		c := newTestClient(t, nil, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"data":{"repository":{"nameWithOwner":"ai-daming/qianshou","issue":{"number":4,"parent":null,"blockedBy":{"pageInfo":{"hasNextPage":true,"endCursor":"C1","hasNextPage":false},"nodes":[]}}}}}`)
		})
		if _, err := c.Relationships(context.Background(), "ai-daming/qianshou", 4); err == nil {
			t.Fatalf("duplicate hasNextPage silently resolved by last-wins")
		}
	})
	t.Run("rest item field", func(t *testing.T) {
		c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `[{"number":1,"number":2,"title":"x","state":"open","labels":[]}]`)
		}, nil)
		if _, err := c.ListMilestoneIssues(context.Background(), "ai-daming/qianshou", 1); err == nil {
			t.Fatalf("duplicate REST field silently resolved by last-wins")
		}
	})
}

// --- Round 7 falsification set: equivalence classes, REST binding,
// header cardinality, RFC 8288 relation lists ---

func TestDecoderEquivalenceClassesFailClosed(t *testing.T) {
	t.Run("lone wrong-case schema field", func(t *testing.T) {
		c := newTestClient(t, nil, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"data":{"repository":{"nameWithOwner":"ai-daming/qianshou","issue":{"number":4,"parent":null,"blockedBy":{"pageInfo":{"HasNextPage":false},"nodes":[]}}}}}`)
		})
		if _, err := c.Relationships(context.Background(), "ai-daming/qianshou", 4); err == nil {
			t.Fatalf("wrong-case-only schema field satisfied the presence contract")
		}
	})
	t.Run("case-variant duplicate keys merge by decoder", func(t *testing.T) {
		c := newTestClient(t, nil, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"data":{"repository":{"nameWithOwner":"ai-daming/qianshou","issue":{"number":4,"parent":null,"blockedBy":{"pageInfo":{"hasNextPage":true,"endCursor":"C1","HasNextPage":false},"nodes":[]}}}}}`)
		})
		if _, err := c.Relationships(context.Background(), "ai-daming/qianshou", 4); err == nil {
			t.Fatalf("case-variant duplicate terminated pagination via last-wins")
		}
	})
	t.Run("invalid utf-8 in rest body", func(t *testing.T) {
		c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`[{"number":1,"title":"a`))
			w.Write([]byte{0xff})
			w.Write([]byte(`b","state":"open","labels":[]}]`))
		}, nil)
		if _, err := c.ListMilestoneIssues(context.Background(), "ai-daming/qianshou", 1); err == nil {
			t.Fatalf("invalid UTF-8 silently replaced with U+FFFD instead of failing")
		}
	})
	t.Run("invalid utf-8 in graphql body", func(t *testing.T) {
		c := newTestClient(t, nil, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"data":{"repository":{"nameWithOwner":"ai-daming/qianshou","issue":{"number":4,"ti`)
			fmt.Fprint(w, "t")
			w.Write([]byte{0xfe})
			fmt.Fprint(w, `le":"x","parent":null,"blockedBy":{"pageInfo":{"hasNextPage":false},"nodes":[]}}}}}`)
		})
		if _, err := c.Relationships(context.Background(), "ai-daming/qianshou", 4); err == nil {
			t.Fatalf("invalid UTF-8 silently replaced in GraphQL response")
		}
	})
}

func TestRestResponsesMustBindToRequestedRepository(t *testing.T) {
	cases := []struct {
		name string
		item string
	}{
		{
			name: "repository_url points at another repository",
			item: `{"number":1,"title":"x","state":"open","labels":[],"repository_url":"https://api.github.com/repos/other/repo","milestone":{"number":1}}`,
		},
		{
			name: "repository_url missing",
			item: `{"number":1,"title":"x","state":"open","labels":[],"milestone":{"number":1}}`,
		},
		{
			name: "milestone differs from the requested one",
			item: `{"number":1,"title":"x","state":"open","labels":[],"repository_url":"https://api.github.com/repos/ai-daming/qianshou","milestone":{"number":999}}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, "["+tc.item+"]")
			}, nil)
			if _, err := c.ListMilestoneIssues(context.Background(), "ai-daming/qianshou", 1); err == nil {
				t.Fatalf("facts from another repository/milestone accepted: %s", tc.name)
			}
		})
	}
}

func TestGetIssueRejectsForeignRepositoryResponse(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"number":4,"title":"x","state":"open","labels":[],"repository_url":"https://api.github.com/repos/other/repo"}`)
	}, nil)
	if _, err := c.GetIssue(context.Background(), "ai-daming/qianshou", 4); err == nil {
		t.Fatalf("same-number response from another repository accepted")
	}
}

func TestHeaderCardinalityIsChecked(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Content-Type", "application/json")
		w.Header().Add("Content-Type", "text/html")
		fmt.Fprint(w, "[]")
	}, nil)
	if _, err := c.ListMilestoneIssues(context.Background(), "ai-daming/qianshou", 1); err == nil {
		t.Fatalf("two Content-Type values resolved by silently picking the first")
	}
}

func TestLinkRelationTypeListsAreParsedPerRFC8288(t *testing.T) {
	var requests int
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		requests++
		if r.URL.Query().Get("page") == "" || r.URL.Query().Get("page") == "1" {
			w.Header().Set("Link", fmt.Sprintf(`<http://%s%s?milestone=1&state=all&per_page=100&page=2>; rel="prev next"`, r.Host, r.URL.Path))
			fmt.Fprint(w, milestonePage(issueItem(1, "workflow:delivery")))
			return
		}
		fmt.Fprint(w, milestonePage(issueItem(2, "workflow:delivery")))
	}, nil)
	issues, err := c.ListMilestoneIssues(context.Background(), "ai-daming/qianshou", 1)
	if err != nil {
		t.Fatalf("ListMilestoneIssues: %v", err)
	}
	if requests != 2 || len(issues) != 2 {
		t.Fatalf(`rel="prev next" not treated as containing next: requests=%d issues=%d`, requests, len(issues))
	}
}

// --- Round 8 falsification set ---

func TestLinkRepeatedRelParamKeepsFirstOccurrence(t *testing.T) {
	var requests int
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		requests++
		if r.URL.Query().Get("page") == "" || r.URL.Query().Get("page") == "1" {
			w.Header().Set("Link", fmt.Sprintf(`<http://%s%s?milestone=1&state=all&per_page=100&page=2>; rel="next"; rel="prev"`, r.Host, r.URL.Path))
			fmt.Fprint(w, milestonePage(issueItem(1, "workflow:delivery")))
			return
		}
		fmt.Fprint(w, milestonePage(issueItem(2, "workflow:delivery")))
	}, nil)
	issues, err := c.ListMilestoneIssues(context.Background(), "ai-daming/qianshou", 1)
	if err != nil {
		t.Fatalf("ListMilestoneIssues: %v", err)
	}
	if requests != 2 || len(issues) != 2 {
		t.Fatalf(`rel="next"; rel="prev" erased next via last-wins: requests=%d issues=%d`, requests, len(issues))
	}
}

func TestLinkUnclosedQuoteFailsClosed(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Link", fmt.Sprintf(`<http://%s%s?milestone=1&state=all&per_page=100&page=2>; rel="next`, r.Host, r.URL.Path))
		fmt.Fprint(w, milestonePage(issueItem(1, "workflow:delivery")))
	}, nil)
	if _, err := c.ListMilestoneIssues(context.Background(), "ai-daming/qianshou", 1); err == nil {
		t.Fatalf("unclosed quote swallowed the next-page signal and read as termination")
	}
}

func TestGetIssueRootEnforcesExactCaseSchema(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"Number":4,"Title":"x","State":"open","Labels":[],"Repository_URL":"https://api.github.com/repos/ai-daming/qianshou","Milestone":{"Number":1}}`)
	}, nil)
	if _, err := c.GetIssue(context.Background(), "ai-daming/qianshou", 4); err == nil {
		t.Fatalf("wrong-case-only fields satisfied the single-issue root object")
	}
}

func TestRepositoryURLMustMatchCanonicalAPIIdentity(t *testing.T) {
	cases := []struct {
		name string
		url  string
	}{
		{"foreign host with same path", "https://evil.example/repos/ai-daming/qianshou"},
		{"userinfo smuggled", "https://user:pass@api.github.com/repos/ai-daming/qianshou"},
		{"query injected", "https://api.github.com/repos/ai-daming/qianshou?x=1"},
		{"fragment injected", "https://api.github.com/repos/ai-daming/qianshou#f"},
		{"plain http", "http://api.github.com/repos/ai-daming/qianshou"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `[{"number":1,"title":"x","state":"open","labels":[],"repository_url":%q,"milestone":{"number":1}}]`, tc.url)
			}, nil)
			if _, err := c.ListMilestoneIssues(context.Background(), "ai-daming/qianshou", 1); err == nil {
				t.Fatalf("repository_url %q accepted as canonical API identity", tc.url)
			}
		})
	}
}

// --- Round 9 SELF-ATTACK on the round-8 link validator/splitter pair ---

func TestLinkQuotedPairInTitleParam(t *testing.T) {
	var requests int
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		requests++
		if r.URL.Query().Get("page") == "" || r.URL.Query().Get("page") == "1" {
			w.Header().Set("Link", fmt.Sprintf(`<http://%s%s?milestone=1&state=all&per_page=100&page=2>; title="a\"b"; rel="next"`, r.Host, r.URL.Path))
			fmt.Fprint(w, milestonePage(issueItem(1, "workflow:delivery")))
			return
		}
		fmt.Fprint(w, milestonePage(issueItem(2, "workflow:delivery")))
	}, nil)
	issues, err := c.ListMilestoneIssues(context.Background(), "ai-daming/qianshou", 1)
	if err != nil {
		t.Fatalf("ListMilestoneIssues: %v", err)
	}
	if requests != 2 || len(issues) != 2 {
		t.Fatalf(`legal quoted-pair title="a\\"b" desynchronized the splitter and swallowed next: requests=%d issues=%d`, requests, len(issues))
	}
}

func TestLinkQuotedPairInRelValue(t *testing.T) {
	var requests int
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		requests++
		if r.URL.Query().Get("page") == "" || r.URL.Query().Get("page") == "1" {
			w.Header().Set("Link", fmt.Sprintf(`<http://%s%s?milestone=1&state=all&per_page=100&page=2>; rel="ne\xt"`, r.Host, r.URL.Path))
			fmt.Fprint(w, milestonePage(issueItem(1, "workflow:delivery")))
			return
		}
		fmt.Fprint(w, milestonePage(issueItem(2, "workflow:delivery")))
	}, nil)
	issues, err := c.ListMilestoneIssues(context.Background(), "ai-daming/qianshou", 1)
	if err != nil {
		t.Fatalf("ListMilestoneIssues: %v", err)
	}
	if requests != 2 || len(issues) != 2 {
		t.Fatalf(`rel="ne\\xt" unquotes to next per RFC 9110 quoted-pair: requests=%d issues=%d`, requests, len(issues))
	}
}

func TestLinkMissingRequiredRelFailsClosed(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Link", fmt.Sprintf(`<http://%s%s?milestone=1&state=all&per_page=100&page=2>`, r.Host, r.URL.Path))
		fmt.Fprint(w, milestonePage(issueItem(1, "workflow:delivery")))
	}, nil)
	if _, err := c.ListMilestoneIssues(context.Background(), "ai-daming/qianshou", 1); err == nil {
		t.Fatalf("link-value without the required rel parameter read as normal termination (RFC 8288 §3.3)")
	}
}

// --- Round 10 SELF-ATTACK: owning a grammar means owning its alphabet. ---

func TestLinkGrammarRejectsMalformedABNF(t *testing.T) {
	cases := []struct {
		name string
		link string
	}{
		{name: "quote inside unquoted token value", link: `rel=ne"xt`},
		{name: "invalid relation-type in quoted value", link: `rel="next,"`},
		{name: "illegal parameter name character", link: `bad@name=x; rel="next"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Query().Get("page") == "" || r.URL.Query().Get("page") == "1" {
					w.Header().Set("Link", fmt.Sprintf(`<http://%s%s?milestone=1&state=all&per_page=100&page=2>; %s`, r.Host, r.URL.Path, tc.link))
					fmt.Fprint(w, milestonePage(issueItem(1, "workflow:delivery")))
					return
				}
				fmt.Fprint(w, milestonePage(issueItem(2, "workflow:delivery")))
			}, nil)
			if _, err := c.ListMilestoneIssues(context.Background(), "ai-daming/qianshou", 1); err == nil {
				t.Fatalf("malformed grammar accepted: %s", tc.name)
			}
		})
	}
}
