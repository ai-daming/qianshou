// Package ghfacts reads issue, milestone, parent/sub-issue, and dependency
// facts straight from the GitHub REST and GraphQL APIs. GitHub owns those
// facts; this package only fetches and normalizes them. Every failure mode —
// transport, status, GraphQL error, malformed body — fails closed: callers
// never receive a partial fact set that could be misread as "no dependencies".
package ghfacts

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
)

// Op identifies the failing operation in an Error.
type Op string

const (
	OpListMilestoneIssues Op = "list_milestone_issues"
	OpGetIssue            Op = "get_issue"
	OpFetchRelationships  Op = "fetch_relationships"
)

// Error is a structured GitHub read failure. It never contains credentials.
type Error struct {
	Op     Op
	Detail string
	Err    error
}

func (e *Error) Error() string {
	if e.Detail != "" && e.Err != nil {
		return fmt.Sprintf("GitHub 事实读取失败（%s）：%s：%v", e.Op, e.Detail, e.Err)
	}
	if e.Err != nil {
		return fmt.Sprintf("GitHub 事实读取失败（%s）：%v", e.Op, e.Err)
	}
	return fmt.Sprintf("GitHub 事实读取失败（%s）：%s", e.Op, e.Detail)
}

func (e *Error) Unwrap() error { return e.Err }

// Issue is one GitHub issue observed through a milestone.
type Issue struct {
	Number int
	Title  string
	State  string
	Labels []string
}

// BlockedIssue is one native Blocked by prerequisite with its current state.
type BlockedIssue struct {
	Number int
	State  string
}

// Relationships carries the native hierarchy and dependency edges of one
// issue. Parent and BlockedBy come from GraphQL; both are GitHub-owned.
type Relationships struct {
	Number    int
	Parent    *int
	BlockedBy []BlockedIssue
}

const (
	defaultRestBase = "https://api.github.com"
	defaultGqlURL   = "https://api.github.com/graphql"
	apiVersion      = "2022-11-28"
	pageSize        = 100
)

var slugPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// Client reads GitHub facts with one borrowed token.
type Client struct {
	restBase string
	gqlURL   string
	token    string
	hc       *http.Client
}

// New returns a client against the public GitHub API. An empty token fails
// immediately: reads without credentials must not be attempted.
func New(token string) (*Client, error) {
	return newClient(token, defaultRestBase, defaultGqlURL, http.DefaultClient)
}

func newClient(token, restBase, gqlURL string, hc *http.Client) (*Client, error) {
	if strings.TrimSpace(token) == "" {
		return nil, fmt.Errorf("ghfacts 需要 GitHub 凭据：空 token 会把读取失败伪装成空事实，必须 fail closed")
	}
	return &Client{restBase: restBase, gqlURL: gqlURL, token: token, hc: hc}, nil
}

type restIssue struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	State  string `json:"state"`
	Labels []struct {
		Name string `json:"name"`
	} `json:"labels"`
	PullRequest *struct {
		URL string `json:"url"`
	} `json:"pull_request"`
}

// ListMilestoneIssues returns every issue currently in one GitHub milestone
// (any state), excluding pull requests that GitHub mixes into the listing.
// Pagination is all-or-nothing: a failure on any page discards the result.
func (c *Client) ListMilestoneIssues(ctx context.Context, slug string, milestone int) ([]Issue, error) {
	if !slugPattern.MatchString(slug) {
		return nil, fmt.Errorf("仓库定位必须是 owner/repo，当前为 %q", slug)
	}
	var issues []Issue
	for page := 1; ; page++ {
		url := fmt.Sprintf("%s/repos/%s/issues?milestone=%d&state=all&per_page=%d&page=%d",
			c.restBase, slug, milestone, pageSize, page)
		var batch []restIssue
		status, err := c.getJSON(ctx, url, &batch)
		if err != nil {
			return nil, &Error{
				Op:     OpListMilestoneIssues,
				Detail: fmt.Sprintf("%s milestone %d 第 %d 页（status %d）", slug, milestone, page, status),
				Err:    err,
			}
		}
		for _, item := range batch {
			if item.PullRequest != nil {
				continue
			}
			labels := make([]string, 0, len(item.Labels))
			for _, l := range item.Labels {
				labels = append(labels, l.Name)
			}
			issues = append(issues, Issue{Number: item.Number, Title: item.Title, State: item.State, Labels: labels})
		}
		if len(batch) < pageSize {
			return issues, nil
		}
	}
}

// GetIssue fetches one issue by number. A missing issue is an error, not an
// empty fact.
func (c *Client) GetIssue(ctx context.Context, slug string, number int) (Issue, error) {
	if !slugPattern.MatchString(slug) {
		return Issue{}, fmt.Errorf("仓库定位必须是 owner/repo，当前为 %q", slug)
	}
	url := fmt.Sprintf("%s/repos/%s/issues/%d", c.restBase, slug, number)
	var item restIssue
	status, err := c.getJSON(ctx, url, &item)
	if err != nil {
		return Issue{}, &Error{
			Op:     OpGetIssue,
			Detail: fmt.Sprintf("%s#%d（status %d）", slug, number, status),
			Err:    err,
		}
	}
	if item.PullRequest != nil {
		return Issue{}, &Error{Op: OpGetIssue, Detail: fmt.Sprintf("%s#%d 是 Pull Request，不是 Issue", slug, number)}
	}
	labels := make([]string, 0, len(item.Labels))
	for _, l := range item.Labels {
		labels = append(labels, l.Name)
	}
	return Issue{Number: item.Number, Title: item.Title, State: item.State, Labels: labels}, nil
}

const relationshipsQuery = `query($owner:String!,$repo:String!,$number:Int!){
  repository(owner:$owner, name:$repo){
    issue(number:$number){
      parent{ number }
      blockedBy(first:100){ nodes{ number state } }
    }
  }
}`

// Relationships fetches the native parent and Blocked by facts of one issue.
// A missing issue or repository is an error, not an empty result: absence of
// evidence must not be read as absence of dependencies.
func (c *Client) Relationships(ctx context.Context, slug string, number int) (Relationships, error) {
	if !slugPattern.MatchString(slug) {
		return Relationships{}, fmt.Errorf("仓库定位必须是 owner/repo，当前为 %q", slug)
	}
	parts := strings.SplitN(slug, "/", 2)
	payload, err := json.Marshal(map[string]any{
		"query":     relationshipsQuery,
		"variables": map[string]any{"owner": parts[0], "repo": parts[1], "number": number},
	})
	if err != nil {
		return Relationships{}, &Error{Op: OpFetchRelationships, Detail: fmt.Sprintf("%s#%d", slug, number), Err: err}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.gqlURL, bytes.NewReader(payload))
	if err != nil {
		return Relationships{}, &Error{Op: OpFetchRelationships, Detail: fmt.Sprintf("%s#%d", slug, number), Err: err}
	}
	c.authorize(req)
	resp, err := c.hc.Do(req)
	if err != nil {
		return Relationships{}, &Error{Op: OpFetchRelationships, Detail: fmt.Sprintf("%s#%d", slug, number), Err: err}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Relationships{}, &Error{Op: OpFetchRelationships, Detail: fmt.Sprintf("%s#%d（status %d）", slug, number, resp.StatusCode), Err: err}
	}
	if resp.StatusCode != http.StatusOK {
		return Relationships{}, &Error{
			Op:     OpFetchRelationships,
			Detail: fmt.Sprintf("%s#%d（status %d）：%s", slug, number, resp.StatusCode, snippet(body)),
		}
	}
	var gql struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
		Data struct {
			Repository struct {
				Issue *struct {
					Parent *struct {
						Number int `json:"number"`
					} `json:"parent"`
					BlockedBy struct {
						Nodes []struct {
							Number int    `json:"number"`
							State  string `json:"state"`
						} `json:"nodes"`
					} `json:"blockedBy"`
				} `json:"issue"`
			} `json:"repository"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &gql); err != nil {
		return Relationships{}, &Error{
			Op:     OpFetchRelationships,
			Detail: fmt.Sprintf("%s#%d：响应不是合法 JSON", slug, number),
			Err:    err,
		}
	}
	if len(gql.Errors) > 0 {
		msgs := make([]string, 0, len(gql.Errors))
		for _, e := range gql.Errors {
			msgs = append(msgs, e.Message)
		}
		return Relationships{}, &Error{
			Op:     OpFetchRelationships,
			Detail: fmt.Sprintf("%s#%d：GraphQL 错误：%s", slug, number, strings.Join(msgs, "；")),
		}
	}
	issue := gql.Data.Repository.Issue
	if issue == nil {
		return Relationships{}, &Error{
			Op:     OpFetchRelationships,
			Detail: fmt.Sprintf("%s#%d：Issue 或仓库不存在（缺失事实不得解释为无依赖）", slug, number),
		}
	}
	rel := Relationships{Number: number}
	if issue.Parent != nil {
		parent := issue.Parent.Number
		rel.Parent = &parent
	}
	for _, node := range issue.BlockedBy.Nodes {
		rel.BlockedBy = append(rel.BlockedBy, BlockedIssue{Number: node.Number, State: node.State})
	}
	return rel, nil
}

func (c *Client) authorize(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", apiVersion)
	req.Header.Set("Content-Type", "application/json")
}

func (c *Client) getJSON(ctx context.Context, url string, out any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	c.authorize(req)
	resp, err := c.hc.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return resp.StatusCode, err
	}
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, fmt.Errorf("HTTP %d：%s", resp.StatusCode, snippet(body))
	}
	if err := json.Unmarshal(body, out); err != nil {
		return resp.StatusCode, fmt.Errorf("响应不是合法 JSON：%w", err)
	}
	return resp.StatusCode, nil
}

func snippet(body []byte) string {
	s := strings.TrimSpace(string(body))
	if len(s) > 400 {
		return s[:400] + "…"
	}
	return s
}
