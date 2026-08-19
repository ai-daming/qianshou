package deps

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCanStartBatchKeepsIssueScopedFailureLocal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		fmt.Fprint(w, `{
			"errors":[{"message":"field unavailable","path":["repository","issue_30","blockedBy"]}],
			"data":{"repository":{"nameWithOwner":"ai-daming/qianshou","issue_30":null,
				"issue_31":{"number":31,"blockedBy":{"pageInfo":{"hasNextPage":false},"nodes":[]}}}}
		}`)
	}))
	defer srv.Close()

	got, err := CanStartBatch(context.Background(), "tok", srv.URL, "ai-daming/qianshou", []int{30, 31}, srv.Client())
	if err != nil {
		t.Fatalf("CanStartBatch: %v", err)
	}
	if got.Errors[30] == nil {
		t.Fatal("Issue-scoped GraphQL error was lost")
	}
	if _, exists := got.Judgments[30]; exists {
		t.Fatal("Issue with a GraphQL error received a judgment")
	}
	if judgment, exists := got.Judgments[31]; !exists || len(judgment.BlockedBy) != 0 {
		t.Fatalf("usable sibling judgment = %+v, exists = %v", judgment, exists)
	}
}

func TestCanStartBatchTreatsRateLimitAsSystemicEvenWithIssuePath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		fmt.Fprint(w, `{
			"errors":[{"message":"rate limit exceeded","path":["repository","issue_30"]}],
			"data":{"repository":{"nameWithOwner":"ai-daming/qianshou","issue_30":null}}
		}`)
	}))
	defer srv.Close()

	if _, err := CanStartBatch(context.Background(), "tok", srv.URL, "ai-daming/qianshou", []int{30}, srv.Client()); err == nil {
		t.Fatal("rate limit was reduced to an Issue-scoped error")
	}
}

func TestCanStartBatchKeepsIssueEvidenceDefectsLocal(t *testing.T) {
	cases := []struct {
		name    string
		issue30 string
	}{
		{
			name:    "truncated dependency page",
			issue30: `{"number":30,"blockedBy":{"pageInfo":{"hasNextPage":true},"nodes":[]}}`,
		},
		{
			name:    "contradictory duplicate conclusion key",
			issue30: `{"number":30,"blockedBy":{"pageInfo":{"hasNextPage":false},"nodes":[{"number":29,"state":"OPEN"}],"nodes":[]}}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				fmt.Fprintf(w, `{"data":{"repository":{"nameWithOwner":"ai-daming/qianshou","issue_30":%s,
					"issue_31":{"number":31,"blockedBy":{"pageInfo":{"hasNextPage":false},"nodes":[]}}}}}`, tc.issue30)
			}))
			defer srv.Close()

			got, err := CanStartBatch(context.Background(), "tok", srv.URL, "ai-daming/qianshou", []int{30, 31}, srv.Client())
			if err != nil {
				t.Fatalf("Issue-scoped defect failed the whole batch: %v", err)
			}
			if got.Errors[30] == nil {
				t.Fatal("Issue-scoped defect did not produce an Issue error")
			}
			if _, exists := got.Judgments[31]; !exists {
				t.Fatal("usable sibling judgment was discarded")
			}
		})
	}
}
