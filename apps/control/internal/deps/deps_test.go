package deps

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 合同就一句话：判断某 Issue 是否被仍未关闭的 Issue 阻塞；
// 判断不出来必须报错，不许装作没有依赖。

func stubClient(t *testing.T, status int, body string) (string, *http.Client) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, srv.Client()
}

const happyBody = `{"data":{"repository":{"nameWithOwner":"ai-daming/qianshou","issue":{"number":30,
"blockedBy":{"pageInfo":{"hasNextPage":false},"nodes":[{"number":29,"state":"OPEN"},{"number":3,"state":"CLOSED"}]}}}}}`

func TestCanStartReportsOpenBlockers(t *testing.T) {
	url, hc := stubClient(t, 200, happyBody)
	j, err := CanStart(context.Background(), "tok", url, "ai-daming/qianshou", 30, hc)
	if err != nil {
		t.Fatalf("CanStart: %v", err)
	}
	if len(j.BlockedBy) != 1 || j.BlockedBy[0] != 29 {
		t.Fatalf("BlockedBy = %v, want [29]", j.BlockedBy)
	}
	if len(j.Blockers) != 2 {
		t.Fatalf("Blockers = %+v", j.Blockers)
	}
}

func TestCanStartWithNoBlockers(t *testing.T) {
	body := `{"data":{"repository":{"nameWithOwner":"ai-daming/qianshou","issue":{"number":4,
"blockedBy":{"pageInfo":{"hasNextPage":false},"nodes":[]}}}}}`
	url, hc := stubClient(t, 200, body)
	j, err := CanStart(context.Background(), "tok", url, "ai-daming/qianshou", 4, hc)
	if err != nil {
		t.Fatalf("CanStart: %v", err)
	}
	if len(j.BlockedBy) != 0 {
		t.Fatalf("BlockedBy = %v, want empty", j.BlockedBy)
	}
}

// 两条不可妥协：查不出 = 报错；矛盾 = 报错。
func TestCanStartFailsClosed(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{"http error", 500, `{"message":"oops"}`},
		{"graphql errors", 200, `{"errors":[{"message":"nope"}]}`},
		{"issue missing", 200, `{"data":{"repository":{"nameWithOwner":"ai-daming/qianshou","issue":null}}}`},
		{"repository missing", 200, `{"data":{"repository":null}}`},
		{"wrong repository identity", 200, `{"data":{"repository":{"nameWithOwner":"other/repo","issue":{"number":30,
			"blockedBy":{"pageInfo":{"hasNextPage":false},"nodes":[]}}}}}`},
		{"wrong issue echo", 200, `{"data":{"repository":{"nameWithOwner":"ai-daming/qianshou","issue":{"number":99,
			"blockedBy":{"pageInfo":{"hasNextPage":false},"nodes":[]}}}}}`},
		{"more pages than we can decide", 200, `{"data":{"repository":{"nameWithOwner":"ai-daming/qianshou","issue":{"number":30,
			"blockedBy":{"pageInfo":{"hasNextPage":true},"nodes":[]}}}}}`},
		{"unknown state", 200, `{"data":{"repository":{"nameWithOwner":"ai-daming/qianshou","issue":{"number":30,
			"blockedBy":{"pageInfo":{"hasNextPage":false},"nodes":[{"number":9,"state":"merged"}]}}}}}`},
		{"zero-number blocker", 200, `{"data":{"repository":{"nameWithOwner":"ai-daming/qianshou","issue":{"number":30,
			"blockedBy":{"pageInfo":{"hasNextPage":false},"nodes":[{"number":0,"state":"OPEN"}]}}}}}`},
		{"duplicate blocker contradicting itself", 200, `{"data":{"repository":{"nameWithOwner":"ai-daming/qianshou","issue":{"number":30,
			"blockedBy":{"pageInfo":{"hasNextPage":false},"nodes":[{"number":9,"state":"OPEN"},{"number":9,"state":"CLOSED"}]}}}}}`},
		{"duplicate blocker same state", 200, `{"data":{"repository":{"nameWithOwner":"ai-daming/qianshou","issue":{"number":30,
			"blockedBy":{"pageInfo":{"hasNextPage":false},"nodes":[{"number":9,"state":"OPEN"},{"number":9,"state":"OPEN"}]}}}}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			url, hc := stubClient(t, tc.status, tc.body)
			if _, err := CanStart(context.Background(), "tok", url, "ai-daming/qianshou", 30, hc); err == nil {
				t.Fatalf("judgment fabricated from unusable data")
			}
		})
	}
}

func TestCanStartRejectsBadInput(t *testing.T) {
	url, hc := stubClient(t, 200, happyBody)
	if _, err := CanStart(context.Background(), "", url, "ai-daming/qianshou", 30, hc); err == nil {
		t.Fatalf("empty token accepted")
	}
	if _, err := CanStart(context.Background(), "tok", url, "not-a-slug", 30, hc); err == nil {
		t.Fatalf("bad repo accepted")
	}
	if _, err := CanStart(context.Background(), "tok", url, "ai-daming/qianshou", 0, hc); err == nil {
		t.Fatalf("issue 0 accepted")
	}
	_ = strings.TrimSpace
}
