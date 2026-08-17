// Package ghfacts reads issue, milestone, parent/sub-issue, and dependency
// facts straight from the GitHub REST and GraphQL APIs. GitHub owns those
// facts; this package only fetches and normalizes them. Every failure mode —
// transport, status, GraphQL error, malformed or schema-drifted body — fails
// closed: callers never receive a partial fact set that could be misread as
// "no dependencies".
package ghfacts

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
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

	// read is the provenance bit: only this package's decoding (or the
	// validating constructor) sets it. A struct literal from any consumer —
	// including a Facts implementation — leaves it false, so Validate refuses
	// values that were never actually read. Value shape cannot prove
	// completeness; provenance can.
	read bool
}

// NewIssue is the only way for a consumer to construct a valid Issue from
// raw values. It validates and stamps provenance in one step, so an invalid
// or forged fact cannot exist.
func NewIssue(number int, title, state string, labels []string) (Issue, error) {
	issue := Issue{Number: number, Title: title, State: state, Labels: labels, read: true}
	if err := issue.Validate(); err != nil {
		return Issue{}, err
	}
	return issue, nil
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

	// read is the provenance bit (see Issue.read): a zero-value
	// Relationships must not be distinguishable-from-nothing AND
	// acceptable-as-evidence at the same time.
	read bool
}

// NewRelationships is the only way for a consumer to construct valid
// relationship facts from raw values; it validates and stamps provenance.
func NewRelationships(number int, parent *int, blockedBy []BlockedIssue) (Relationships, error) {
	rel := Relationships{Number: number, Parent: parent, BlockedBy: blockedBy, read: true}
	if err := rel.Validate(); err != nil {
		return Relationships{}, err
	}
	return rel, nil
}

// Validate is the unified fact invariant for one observed issue. Every decode
// exit in this package and every consumer that assembles facts into a
// snapshot (scope.Build) reuses it, so no call site can accept a collapsed,
// contradictory, or self-referential fact set.
func (i Issue) Validate() error {
	if !i.read {
		return fmt.Errorf("Issue #%d 无读取凭证：结构体字面量不构成事实（provenance 不可伪造）", i.Number)
	}
	if i.Number <= 0 {
		return fmt.Errorf("issue 缺少 number（不得折叠为零值）")
	}
	if i.Title == "" {
		return fmt.Errorf("#%d 缺少 title（没拿到不能解释为标题为空）", i.Number)
	}
	if i.State != "open" && i.State != "closed" {
		return fmt.Errorf("#%d 的 state %q 不是已知的 REST 枚举（open/closed）", i.Number, i.State)
	}
	seen := make(map[string]bool, len(i.Labels))
	for _, name := range i.Labels {
		if name == "" {
			return fmt.Errorf("#%d 存在空标签名", i.Number)
		}
		if seen[name] {
			return fmt.Errorf("#%d 的标签 %q 重复出现，同一事实出现两次即矛盾", i.Number, name)
		}
		seen[name] = true
	}
	return nil
}

// Validate is the unified fact invariant for one issue's relationships.
// Parent must be positive and not the issue itself; every blocker must carry
// a positive number, a known GraphQL state, and appear at most once. A
// zero-number CLOSED blocker is a collapsed fact that would masquerade as a
// satisfied dependency.
func (r Relationships) Validate() error {
	if !r.read {
		return fmt.Errorf("关系事实 #%d 无读取凭证：结构体字面量不构成事实（provenance 不可伪造）", r.Number)
	}
	if r.Number <= 0 {
		return fmt.Errorf("关系事实缺少所属 Issue 编号")
	}
	if r.Parent != nil {
		if *r.Parent <= 0 {
			return fmt.Errorf("#%d 的 parent 编号非正数", r.Number)
		}
		if *r.Parent == r.Number {
			return fmt.Errorf("#%d 是自己的 parent，事实矛盾", r.Number)
		}
	}
	seen := make(map[int]bool, len(r.BlockedBy))
	for _, b := range r.BlockedBy {
		if b.Number <= 0 {
			return fmt.Errorf("#%d 的依赖缺少 number（零值依赖会伪装成已满足）", r.Number)
		}
		if b.Number == r.Number {
			return fmt.Errorf("#%d 依赖自己，事实矛盾", r.Number)
		}
		if b.State != "OPEN" && b.State != "CLOSED" {
			return fmt.Errorf("#%d 的依赖 #%d state %q 不是已知的 GraphQL 枚举（OPEN/CLOSED）", r.Number, b.Number, b.State)
		}
		if seen[b.Number] {
			return fmt.Errorf("#%d 的依赖 #%d 重复出现，同一事实出现两次即矛盾", r.Number, b.Number)
		}
		seen[b.Number] = true
	}
	return nil
}

const (
	defaultRestBase = "https://api.github.com"
	defaultGqlURL   = "https://api.github.com/graphql"
	apiVersion      = "2022-11-28"
	pageSize        = 100
	restBodyLimit   = 4 << 20
	gqlBodyLimit    = 1 << 20
	// maxRestListPages caps the Link-driven listing loop, mirroring the
	// GraphQL-side maxRelationshipPages guard.
	maxRestListPages = 100
)

var slugPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// Client reads GitHub facts with one borrowed token.
type Client struct {
	restBase       string
	gqlURL         string
	token          string
	hc             *http.Client
	requestTimeout time.Duration
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
	// A private client with redirects refused: a redirect is a channel whose
	// identity we did not choose, and the shared default client forwards the
	// Authorization header to same-host targets (empirically verified). The
	// transport is the only thing inherited from the provided client.
	transport := http.DefaultTransport
	if hc != nil && hc.Transport != nil {
		transport = hc.Transport
	}
	strict := &http.Client{
		Transport: transport,
		Jar:       nil,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return fmt.Errorf("拒绝重定向到 %s：信道身份不可证明，凭据不得转发", req.URL.Redacted())
		},
	}
	return &Client{restBase: restBase, gqlURL: gqlURL, token: token, hc: strict, requestTimeout: 30 * time.Second}, nil
}

type restIssueLabel struct {
	Name string `json:"name"`
}

type restIssue struct {
	Number        int               `json:"number"`
	Title         *string           `json:"title"`
	State         string            `json:"state"`
	Labels        *[]restIssueLabel `json:"labels"`
	RepositoryURL *string           `json:"repository_url"`
	Milestone     *struct {
		Number int `json:"number"`
	} `json:"milestone"`
	PullRequest *struct {
		URL string `json:"url"`
	} `json:"pull_request"`
}

// validateRestItem enforces presence at the decode boundary (missing vs
// null vs empty title, missing labels) and then delegates the semantic shape
// to the unified Issue.Validate invariant shared with every consumer.
func validateRestItem(item restIssue, expectedNumber int, slug string, requestedMilestone *int) error {
	if item.Number <= 0 {
		return fmt.Errorf("响应项缺少 number（不得折叠为零值）")
	}
	if expectedNumber != 0 && item.Number != expectedNumber {
		return fmt.Errorf("响应是 #%d 的事实，不是请求的 #%d", item.Number, expectedNumber)
	}
	if item.Title == nil {
		return fmt.Errorf("#%d 缺少 title 字段或为 null（不得折叠为空标题）", item.Number)
	}
	if item.Labels == nil {
		return fmt.Errorf("#%d 缺少 labels 字段或为 null（不得折叠为无标签）", item.Number)
	}
	// Binding: GitHub echoes repository_url on every issue; a response from
	// another repository is not a fact about this request even when the
	// number matches. The witness is the canonical public-API identity in
	// full — scheme, host, path, and the absence of userinfo, query, and
	// fragment. A same-path foreign host is a different repository.
	if item.RepositoryURL == nil {
		return fmt.Errorf("#%d 缺少 repository_url（响应所属仓库不可证）", item.Number)
	}
	repoURL, err := url.Parse(*item.RepositoryURL)
	if err != nil ||
		repoURL.Scheme != "https" ||
		repoURL.Host != "api.github.com" ||
		repoURL.Path != "/repos/"+slug ||
		repoURL.User != nil ||
		repoURL.RawQuery != "" ||
		repoURL.Fragment != "" {
		return fmt.Errorf("#%d 的 repository_url %q 不是 %s 的规范 API 身份（异主同名仓库不是同一仓库）",
			item.Number, *item.RepositoryURL, slug)
	}
	// A milestone listing may only contain members of the requested milestone.
	if requestedMilestone != nil {
		if item.Milestone == nil {
			return fmt.Errorf("#%d 缺少 milestone（里程碑成员响应必须携带所属里程碑）", item.Number)
		}
		if item.Milestone.Number != *requestedMilestone {
			return fmt.Errorf("#%d 属于 milestone %d，不是请求的 milestone %d", item.Number, item.Milestone.Number, *requestedMilestone)
		}
	}
	return item.toIssue().Validate()
}

func (item restIssue) toIssue() Issue {
	labels := make([]string, 0, len(*item.Labels))
	for _, l := range *item.Labels {
		labels = append(labels, l.Name)
	}
	return Issue{Number: item.Number, Title: *item.Title, State: item.State, Labels: labels, read: true}
}

// ListMilestoneIssues returns every issue currently in one GitHub milestone
// (any state), excluding pull requests that GitHub mixes into the listing.
// Pagination is all-or-nothing: a failure on any page discards the result.
func (c *Client) ListMilestoneIssues(ctx context.Context, slug string, milestone int) ([]Issue, error) {
	if !slugPattern.MatchString(slug) {
		return nil, fmt.Errorf("仓库定位必须是 owner/repo，当前为 %q", slug)
	}
	var issues []Issue
	seen := make(map[int]bool)
	canonical, err := url.Parse(fmt.Sprintf("%s/repos/%s/issues?milestone=%d&state=all&per_page=%d&page=1",
		c.restBase, slug, milestone, pageSize))
	if err != nil {
		return nil, &Error{Op: OpListMilestoneIssues, Detail: fmt.Sprintf("%s milestone %d：%v", slug, milestone, err)}
	}
	nextURL := canonical.String()
	for page := 1; ; page++ {
		if page > maxRestListPages {
			return nil, &Error{
				Op:     OpListMilestoneIssues,
				Detail: fmt.Sprintf("%s milestone %d：分页超过 %d 页仍未结束，疑似异常响应", slug, milestone, maxRestListPages),
			}
		}
		var batch []restIssue
		header, err := c.fetchJSON(ctx, nextURL, restBodyLimit, &batch, restListSchema)
		if err != nil {
			return nil, &Error{
				Op:     OpListMilestoneIssues,
				Detail: fmt.Sprintf("%s milestone %d 第 %d 页：%v", slug, milestone, page, err),
				Err:    err,
			}
		}
		requestedMilestone := milestone
		for _, item := range batch {
			if err := validateRestItem(item, 0, slug, &requestedMilestone); err != nil {
				return nil, &Error{
					Op:     OpListMilestoneIssues,
					Detail: fmt.Sprintf("%s milestone %d 第 %d 页：%v", slug, milestone, page, err),
				}
			}
			if seen[item.Number] {
				return nil, &Error{
					Op:     OpListMilestoneIssues,
					Detail: fmt.Sprintf("%s milestone %d 第 %d 页：#%d 重复出现，响应异常", slug, milestone, page, item.Number),
				}
			}
			seen[item.Number] = true
			if item.PullRequest != nil {
				continue
			}
			issues = append(issues, item.toIssue())
		}
		current, err := url.Parse(nextURL)
		if err != nil {
			return nil, &Error{Op: OpListMilestoneIssues, Detail: fmt.Sprintf("%s milestone %d：%v", slug, milestone, err)}
		}
		next, err := boundNextLink(header.Values("Link"), current)
		if err != nil {
			return nil, &Error{
				Op:     OpListMilestoneIssues,
				Detail: fmt.Sprintf("%s milestone %d 第 %d 页：%v", slug, milestone, page, err),
			}
		}
		if next == "" {
			return issues, nil
		}
		nextURL = next
	}
}

// GetIssue fetches one issue by number. A missing issue is an error, not an
// empty fact.
func (c *Client) GetIssue(ctx context.Context, slug string, number int) (Issue, error) {
	if !slugPattern.MatchString(slug) {
		return Issue{}, fmt.Errorf("仓库定位必须是 owner/repo，当前为 %q", slug)
	}
	target := fmt.Sprintf("%s/repos/%s/issues/%d", c.restBase, slug, number)
	var item restIssue
	if _, err := c.fetchJSON(ctx, target, restBodyLimit, &item, restIssueSchema); err != nil {
		return Issue{}, &Error{
			Op:     OpGetIssue,
			Detail: fmt.Sprintf("%s#%d：%v", slug, number, err),
			Err:    err,
		}
	}
	if item.PullRequest != nil {
		return Issue{}, &Error{Op: OpGetIssue, Detail: fmt.Sprintf("%s#%d 是 Pull Request，不是 Issue", slug, number)}
	}
	if err := validateRestItem(item, number, slug, nil); err != nil {
		return Issue{}, &Error{Op: OpGetIssue, Detail: fmt.Sprintf("%s#%d：%v", slug, number, err)}
	}
	return item.toIssue(), nil
}

const relationshipsQuery = `query($owner:String!,$repo:String!,$number:Int!,$after:String){
  repository(owner:$owner, name:$repo){
    nameWithOwner
    issue(number:$number){
      number
      parent{ number }
      blockedBy(first:100, after:$after){ pageInfo{ hasNextPage endCursor } nodes{ number state } }
    }
  }
}`

// maxRelationshipPages guards against a hostile or broken endpoint paging
// forever; 100 pages already allow 10,000 prerequisites.
const maxRelationshipPages = 100

// Relationships fetches the native parent and Blocked by facts of one issue.
// Blocked by is cursor-paginated: silently truncating after the first page
// would drop real prerequisites. A missing issue or repository — or a
// response schema that no longer carries the blockedBy field — is an error,
// never an empty result: absence of evidence must not be read as absence of
// dependencies.
func (c *Client) Relationships(ctx context.Context, slug string, number int) (Relationships, error) {
	if !slugPattern.MatchString(slug) {
		return Relationships{}, fmt.Errorf("仓库定位必须是 owner/repo，当前为 %q", slug)
	}
	parts := strings.SplitN(slug, "/", 2)
	rel := Relationships{Number: number}
	after := ""
	// Every page describes the same issue: parent must agree across pages and
	// a blocker may appear at most once. Contradictory pages fail as a whole
	// instead of merging into a snapshot that never existed.
	parentSeen := false
	var parentValue *int
	seenBlockers := make(map[int]string)
	for page := 0; ; page++ {
		if page >= maxRelationshipPages {
			return Relationships{}, &Error{
				Op:     OpFetchRelationships,
				Detail: fmt.Sprintf("%s#%d：blockedBy 分页超过 %d 页，疑似异常响应", slug, number, maxRelationshipPages),
			}
		}
		variables := map[string]any{"owner": parts[0], "repo": parts[1], "number": number}
		if page > 0 {
			variables["after"] = after
		}
		facts, err := c.fetchRelationshipsPage(ctx, slug, number, variables)
		if err != nil {
			return Relationships{}, err
		}
		if !parentSeen {
			parentSeen = true
			parentValue = facts.parent
		} else if !sameParent(parentValue, facts.parent) {
			return Relationships{}, &Error{
				Op: OpFetchRelationships,
				Detail: fmt.Sprintf("%s#%d：第 %d 页的 parent 与首页矛盾（%s ↔ %s），不得拼接",
					slug, number, page+1, describeParent(parentValue), describeParent(facts.parent)),
			}
		}
		for _, b := range facts.nodes {
			if prevState, dup := seenBlockers[b.Number]; dup {
				detail := fmt.Sprintf("依赖 #%d 跨页重复出现", b.Number)
				if prevState != b.State {
					detail += fmt.Sprintf("且状态矛盾（%s ↔ %s）", prevState, b.State)
				}
				return Relationships{}, &Error{
					Op:     OpFetchRelationships,
					Detail: fmt.Sprintf("%s#%d：%s", slug, number, detail),
				}
			}
			seenBlockers[b.Number] = b.State
		}
		rel.BlockedBy = append(rel.BlockedBy, facts.nodes...)
		if !facts.hasNext {
			rel.Parent = parentValue
			rel.read = true
			if err := rel.Validate(); err != nil {
				return Relationships{}, &Error{
					Op:     OpFetchRelationships,
					Detail: fmt.Sprintf("%s#%d：%v", slug, number, err),
				}
			}
			return rel, nil
		}
		after = facts.endCursor
	}
}

func sameParent(a, b *int) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func describeParent(p *int) string {
	if p == nil {
		return "无父级（null）"
	}
	return fmt.Sprintf("#%d", *p)
}

// relationshipPage decodes presence-aware: a missing parent field stays a nil
// RawMessage (distinct from explicit null), a missing hasNextPage stays a nil
// bool (distinct from false), and null node elements stay nil pointers.
// fetchRelationshipsPage validates all of it and returns normalized facts.
type relationshipPage struct {
	Number    *int            `json:"number"`
	Parent    json.RawMessage `json:"parent"`
	BlockedBy *struct {
		PageInfo *struct {
			HasNextPage *bool  `json:"hasNextPage"`
			EndCursor   string `json:"endCursor"`
		} `json:"pageInfo"`
		Nodes *[]*struct {
			Number int    `json:"number"`
			State  string `json:"state"`
		} `json:"nodes"`
	} `json:"blockedBy"`
}

type pageFacts struct {
	parent    *int
	nodes     []BlockedIssue
	hasNext   bool
	endCursor string
}

func (c *Client) fetchRelationshipsPage(ctx context.Context, slug string, number int, variables map[string]any) (pageFacts, error) {
	var zero pageFacts
	payload, err := json.Marshal(map[string]any{"query": relationshipsQuery, "variables": variables})
	if err != nil {
		return zero, &Error{Op: OpFetchRelationships, Detail: fmt.Sprintf("%s#%d", slug, number), Err: err}
	}
	res, err := c.readResponse(ctx, http.MethodPost, c.gqlURL, bytes.NewReader(payload), gqlBodyLimit)
	if err != nil {
		return zero, &Error{Op: OpFetchRelationships, Detail: fmt.Sprintf("%s#%d：%v", slug, number, err), Err: err}
	}
	body := res.body
	if res.status != http.StatusOK {
		return zero, &Error{
			Op:     OpFetchRelationships,
			Detail: fmt.Sprintf("%s#%d（status %d）：%s", slug, number, res.status, snippet(body)),
		}
	}
	var gql struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
		Data struct {
			Repository struct {
				NameWithOwner *string           `json:"nameWithOwner"`
				Issue         *relationshipPage `json:"issue"`
			} `json:"repository"`
		} `json:"data"`
	}
	if err := scanStrictJSON(body, gqlResponseSchema); err != nil {
		return zero, &Error{
			Op:     OpFetchRelationships,
			Detail: fmt.Sprintf("%s#%d：响应未通过严格 JSON 扫描（外部事实不得静默择一/等价折叠）：%v", slug, number, err),
		}
	}
	if err := json.Unmarshal(body, &gql); err != nil {
		return zero, &Error{
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
		return zero, &Error{
			Op:     OpFetchRelationships,
			Detail: fmt.Sprintf("%s#%d：GraphQL 错误：%s", slug, number, strings.Join(msgs, "；")),
		}
	}
	issue := gql.Data.Repository.Issue
	if issue == nil {
		return zero, &Error{
			Op:     OpFetchRelationships,
			Detail: fmt.Sprintf("%s#%d：Issue 或仓库不存在（缺失事实不得解释为无依赖）", slug, number),
		}
	}
	// Binding: the response must echo the identity we asked for. A perfectly
	// well-formed document about another repository or issue is still not a
	// fact about this request.
	if gql.Data.Repository.NameWithOwner == nil {
		return zero, &Error{
			Op:     OpFetchRelationships,
			Detail: fmt.Sprintf("%s#%d：响应缺少 repository.nameWithOwner（身份不可证）", slug, number),
		}
	}
	if !strings.EqualFold(*gql.Data.Repository.NameWithOwner, slug) {
		return zero, &Error{
			Op:     OpFetchRelationships,
			Detail: fmt.Sprintf("响应来自 %s，不是请求的 %s", *gql.Data.Repository.NameWithOwner, slug),
		}
	}
	if issue.Number == nil || *issue.Number != number {
		return zero, &Error{
			Op:     OpFetchRelationships,
			Detail: fmt.Sprintf("响应是 #%d 的事实，不是请求的 #%d", derefInt(issue.Number), number),
		}
	}
	blockedBy := issue.BlockedBy
	if blockedBy == nil {
		return zero, &Error{
			Op:     OpFetchRelationships,
			Detail: fmt.Sprintf("%s#%d：响应 schema 缺少 blockedBy 字段（不得解释为无依赖）", slug, number),
		}
	}
	if blockedBy.PageInfo == nil {
		return zero, &Error{
			Op:     OpFetchRelationships,
			Detail: fmt.Sprintf("%s#%d：响应 schema 缺少 blockedBy.pageInfo（不得解释为无依赖）", slug, number),
		}
	}
	if blockedBy.PageInfo.HasNextPage == nil {
		return zero, &Error{
			Op:     OpFetchRelationships,
			Detail: fmt.Sprintf("%s#%d：blockedBy.pageInfo 缺少 hasNextPage 或为 null（不得按 false 折叠）", slug, number),
		}
	}
	if blockedBy.Nodes == nil {
		return zero, &Error{
			Op:     OpFetchRelationships,
			Detail: fmt.Sprintf("%s#%d：响应 schema 缺少或置 null blockedBy.nodes（不得解释为无依赖）", slug, number),
		}
	}

	// parent must be present; explicit null means "no parent", a missing
	// field means the response is not the schema we asked for.
	var parent *int
	if len(issue.Parent) == 0 {
		return zero, &Error{
			Op:     OpFetchRelationships,
			Detail: fmt.Sprintf("%s#%d：响应 schema 缺少 parent 字段（缺失与 null 是不同事实）", slug, number),
		}
	}
	if string(issue.Parent) != "null" {
		var decoded struct {
			Number int `json:"number"`
		}
		if err := json.Unmarshal(issue.Parent, &decoded); err != nil || decoded.Number <= 0 {
			return zero, &Error{
				Op:     OpFetchRelationships,
				Detail: fmt.Sprintf("%s#%d：parent 事实不完整（number 缺失或非正数）", slug, number),
			}
		}
		parent = &decoded.Number
	}

	nodes := make([]BlockedIssue, 0, len(*blockedBy.Nodes))
	for _, node := range *blockedBy.Nodes {
		if node == nil {
			return zero, &Error{
				Op:     OpFetchRelationships,
				Detail: fmt.Sprintf("%s#%d：blockedBy.nodes 含 null 元素（不得折叠为零值依赖）", slug, number),
			}
		}
		nodes = append(nodes, BlockedIssue{Number: node.Number, State: node.State})
	}

	if *blockedBy.PageInfo.HasNextPage && blockedBy.PageInfo.EndCursor == "" {
		return zero, &Error{
			Op:     OpFetchRelationships,
			Detail: fmt.Sprintf("%s#%d：hasNextPage 为 true 但缺少 endCursor，无法继续分页（不得截断为无更多依赖）", slug, number),
		}
	}
	return pageFacts{
		parent:    parent,
		nodes:     nodes,
		hasNext:   *blockedBy.PageInfo.HasNextPage,
		endCursor: blockedBy.PageInfo.EndCursor,
	}, nil
}

func (c *Client) authorize(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", apiVersion)
	req.Header.Set("Content-Type", "application/json")
}

// httpResponse is the transport envelope: the only shape through which a
// response body may reach JSON decoding. Every transport-integrity conjunct
// is decided here and nowhere else — deadline, size limit, and content type.
type httpResponse struct {
	status int
	body   []byte
	header http.Header
}

// readResponse performs one request and owns all transport integrity. It
// reads limit+1 bytes so an oversized response is an error instead of a
// silently truncated prefix that may still parse as valid JSON, and a 200
// body that is not application/json is rejected so proxy error pages can
// never be decoded into facts.
func (c *Client) readResponse(ctx context.Context, method, url string, body io.Reader, limit int64) (httpResponse, error) {
	if c.requestTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.requestTimeout)
		defer cancel()
	}
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return httpResponse{}, err
	}
	c.authorize(req)
	resp, err := c.hc.Do(req)
	if err != nil {
		return httpResponse{}, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return httpResponse{status: resp.StatusCode, header: resp.Header}, err
	}
	if int64(len(raw)) > limit {
		return httpResponse{status: resp.StatusCode, header: resp.Header},
			fmt.Errorf("响应超过 %d 字节上限（截断出的合法前缀不得解释为完整事实）", limit)
	}
	if resp.StatusCode == http.StatusOK {
		ctValues := resp.Header.Values("Content-Type")
		if len(ctValues) > 1 {
			return httpResponse{status: resp.StatusCode, header: resp.Header},
				fmt.Errorf("存在 %d 个 Content-Type 值（歧义不得任选其一）", len(ctValues))
		}
		mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(resp.Header.Get("Content-Type")))
		if err != nil || mediaType != "application/json" {
			return httpResponse{status: resp.StatusCode, header: resp.Header},
				fmt.Errorf("Content-Type %q 不是 application/json（错误页或代理响应不得解释为事实）", resp.Header.Get("Content-Type"))
		}
	}
	return httpResponse{status: resp.StatusCode, body: raw, header: resp.Header}, nil
}

func (c *Client) fetchJSON(ctx context.Context, url string, limit int64, out any, schemaFor func(path string) (map[string]bool, bool)) (http.Header, error) {
	res, err := c.readResponse(ctx, http.MethodGet, url, nil, limit)
	if err != nil {
		return nil, err
	}
	if res.status != http.StatusOK {
		return res.header, fmt.Errorf("HTTP %d：%s", res.status, snippet(res.body))
	}
	trimmed := bytes.TrimSpace(res.body)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return res.header, fmt.Errorf("响应体为空或 null（不得解释为空集合）")
	}
	if err := scanStrictJSON(trimmed, schemaFor); err != nil {
		return res.header, fmt.Errorf("响应未通过严格 JSON 扫描（外部事实不得静默择一/等价折叠）：%w", err)
	}
	if err := json.Unmarshal(trimmed, out); err != nil {
		return res.header, fmt.Errorf("响应不是合法 JSON：%w", err)
	}
	return res.header, nil
}

// scanStrictJSON walks the whole document and refuses three decoder
// equivalence hazards before json.Unmarshal sees the body:
//
//   - invalid UTF-8: the Go decoder silently replaces it with U+FFFD, which
//     would launder corrupted bytes into plausible fact strings;
//   - duplicate keys under case folding: encoding/json matches struct fields
//     case-insensitively, so hasNextPage and HasNextPage are the same field
//     to the decoder but different keys to a byte-level scan — the scan must
//     use the decoder's equivalence relation (strings.EqualFold);
//   - wrong-case-only schema fields: a lone HasNextPage would satisfy a
//     presence pointer without being the field the contract names. For every
//     object the caller supplies the exact-case key set its schema declares;
//     a key that case-folds onto a declared key without matching it exactly
//     is rejected. Keys outside the declared set are tolerated (providers
//     add fields over time) and missing keys stay the presence checks'
//     responsibility.
//
// The config file's last-wins duplicate policy is a different domain (local,
// trusted, validated as effective semantics); external evidence is held to
// this stricter rule.
func scanStrictJSON(data []byte, schemaFor func(path string) (map[string]bool, bool)) error {
	if !utf8.Valid(data) {
		return fmt.Errorf("包含非法 UTF-8（不得被解码器替换为 U+FFFD 后洗白）")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	var walk func(path string) error
	walk = func(path string) error {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		delim, ok := tok.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := make(map[string]bool)
			for dec.More() {
				keyTok, err := dec.Token()
				if err != nil {
					return err
				}
				key, _ := keyTok.(string)
				for prev := range seen {
					if strings.EqualFold(prev, key) {
						return fmt.Errorf("对象存在大小写等价的重复键 %q 与 %q（解码器会 last-wins 合并）", prev, key)
					}
				}
				seen[key] = true
				if allowed, enforce := schemaFor(path); enforce {
					foldMatch, exactMatch := false, false
					for want := range allowed {
						if want == key {
							exactMatch = true
						} else if strings.EqualFold(want, key) {
							foldMatch = true
						}
					}
					if foldMatch && !exactMatch {
						return fmt.Errorf("路径 %s 的键 %q 与 schema 字段仅大小写不同（不得等价满足 presence）", path, key)
					}
				}
				child := key
				if path != "" {
					child = path + "." + key
				}
				if err := walk(child); err != nil {
					return err
				}
			}
			if _, err := dec.Token(); err != nil {
				return err
			}
		case '[':
			for dec.More() {
				if err := walk(path + "[]"); err != nil {
					return err
				}
			}
			if _, err := dec.Token(); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(""); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return fmt.Errorf("第一个 JSON 值之后还有额外内容")
	}
	return nil
}

// gqlResponseSchema is the exact-case key contract of every GraphQL response
// object this package decodes, keyed by scan path.
func gqlResponseSchema(path string) (map[string]bool, bool) {
	schemas := map[string]map[string]bool{
		"":                                {"data": true, "errors": true},
		"data":                            {"repository": true},
		"data.repository":                 {"nameWithOwner": true, "issue": true},
		"data.repository.issue":           {"number": true, "parent": true, "blockedBy": true},
		"data.repository.issue.parent":    {"number": true},
		"data.repository.issue.blockedBy": {"pageInfo": true, "nodes": true},
		"data.repository.issue.blockedBy.pageInfo": {"hasNextPage": true, "endCursor": true},
		"data.repository.issue.blockedBy.nodes[]":  {"number": true, "state": true},
	}
	s, ok := schemas[path]
	return s, ok
}

// issueObjectKeys is the exact-case key contract shared by every REST issue
// object, wherever it appears.
func issueObjectKeys() map[string]bool {
	return map[string]bool{"number": true, "title": true, "state": true, "labels": true,
		"pull_request": true, "repository_url": true, "milestone": true}
}

// restListSchema is the exact-case contract for milestone listing roots (a
// JSON array of issue objects).
func restListSchema(path string) (map[string]bool, bool) {
	switch path {
	case "[]":
		return issueObjectKeys(), true
	case "[].labels[]":
		return map[string]bool{"name": true}, true
	case "[].milestone":
		return map[string]bool{"number": true}, true
	}
	return nil, false
}

// restIssueSchema is the exact-case contract for a single-issue root object.
func restIssueSchema(path string) (map[string]bool, bool) {
	switch path {
	case "":
		return issueObjectKeys(), true
	case "labels[]":
		return map[string]bool{"name": true}, true
	case "milestone":
		return map[string]bool{"number": true}, true
	}
	return nil, false
}

// boundNextLink derives the next listing page from every Link header value
// and the canonical current-page URL. Obligations:
//   - channel: the next page must share the current page's origin — the
//     borrowed token must never leave it;
//   - scope: the path and the immutable query parameters (milestone, state,
//     per_page) must be exactly the requested ones, and only the page number
//     may change, strictly monotonically — a same-origin link to another
//     repository or milestone is still facts about something else;
//   - ambiguity: all header values are considered; more than one rel=next,
//     or an uninterpretable link-value, fails instead of silently picking
//     one or reading it as "no next page".
//
// Relation types follow RFC 8288: rel values are whitespace-separated lists
// inside a quoted string, and a link carrying "next" anywhere in its
// relation list is a next link. The returned URL is rebuilt from the
// canonical parameters; nothing the server sent is replayed verbatim.
func boundNextLink(linkValues []string, current *url.URL) (string, error) {
	nextRaw := ""
	sawLink := false
	for _, value := range linkValues {
		if strings.TrimSpace(value) == "" {
			continue
		}
		if err := validateQuoteClosure(value); err != nil {
			return "", err
		}
		for _, segment := range splitOutsideQuotes(value, ',') {
			segment = strings.TrimSpace(segment)
			if segment == "" {
				continue
			}
			target, rels, err := parseLinkValue(segment)
			if err != nil {
				return "", err
			}
			sawLink = true
			isNext := false
			for _, rel := range rels {
				if strings.EqualFold(rel, "next") {
					isNext = true
				}
			}
			if !isNext {
				continue
			}
			if nextRaw != "" {
				return "", fmt.Errorf("存在多个 rel=next 的下一页（%s 与 %s）：分页信号歧义，不得任选其一", nextRaw, target)
			}
			nextRaw = target
		}
	}
	if !sawLink || nextRaw == "" {
		return "", nil
	}
	next, err := url.Parse(nextRaw)
	if err != nil || next.Scheme == "" || next.Host == "" {
		return "", fmt.Errorf("Link rel=next 的 URL 不可解析：%q", nextRaw)
	}
	if next.Scheme != current.Scheme || next.Host != current.Host {
		return "", fmt.Errorf("拒绝跨源下一页 %s://%s（凭据不得离开 %s://%s）", next.Scheme, next.Host, current.Scheme, current.Host)
	}
	if next.Path != current.Path {
		return "", fmt.Errorf("下一页路径 %q 偏离请求端点 %q（事实不得逃逸 Scope）", next.Path, current.Path)
	}
	q := next.Query()
	allowed := map[string]bool{"milestone": true, "state": true, "per_page": true, "page": true}
	for key := range q {
		if !allowed[key] {
			return "", fmt.Errorf("下一页携带未知参数 %q（不得注入查询语义）", key)
		}
	}
	for _, immutable := range []string{"milestone", "state", "per_page"} {
		if q.Get(immutable) != current.Query().Get(immutable) {
			return "", fmt.Errorf("下一页的 %s=%q 与请求的 %q 不一致（不可变参数被篡改）", immutable, q.Get(immutable), current.Query().Get(immutable))
		}
	}
	page, err := strconv.Atoi(q.Get("page"))
	if err != nil || page <= 0 {
		return "", fmt.Errorf("下一页缺少合法页码：%q", q.Get("page"))
	}
	currentPage, err := strconv.Atoi(current.Query().Get("page"))
	if err != nil || page != currentPage+1 {
		return "", fmt.Errorf("下一页页码 %d 不满足从 %d 起的单调递增", page, currentPage)
	}
	rebuilt := *current
	rq := rebuilt.Query()
	rq.Set("page", strconv.Itoa(page))
	rebuilt.RawQuery = rq.Encode()
	return rebuilt.String(), nil
}

// splitOutsideQuotes splits on sep unless inside a double-quoted string, so
// quoted values containing the separator survive (RFC 8288 allows them).
func splitOutsideQuotes(s string, sep byte) []string {
	var parts []string
	start, quoted := 0, false
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '"':
			quoted = !quoted
		case sep:
			if !quoted {
				parts = append(parts, s[start:i])
				start = i + 1
			}
		}
	}
	parts = append(parts, s[start:])
	return parts
}

// parseLinkValue parses one RFC 8288 link-value ("<target>; k=v; …").
// Uninterpretable content is an error, never "no next page".
func parseLinkValue(segment string) (target string, rels []string, err error) {
	rest := strings.TrimSpace(segment)
	if !strings.HasPrefix(rest, "<") {
		return "", nil, fmt.Errorf("Link 值缺少 <> 目标：%q", segment)
	}
	end := strings.Index(rest, ">")
	if end < 0 {
		return "", nil, fmt.Errorf("Link 值的 <> 未闭合：%q", segment)
	}
	target = rest[1:end]
	rest = rest[end+1:]
	sawRel := false
	for _, param := range splitOutsideQuotes(rest, ';') {
		param = strings.TrimSpace(param)
		if param == "" {
			continue
		}
		eq := strings.Index(param, "=")
		if eq < 0 {
			return "", nil, fmt.Errorf("Link 参数不可解析：%q（不得静默忽略分页信号）", param)
		}
		key := strings.TrimSpace(param[:eq])
		val := strings.TrimSpace(param[eq+1:])
		if len(val) >= 2 && val[0] == '"' && val[len(val)-1] == '"' {
			val = val[1 : len(val)-1]
		}
		// RFC 8288 §3.3: a parameter must not occur more than once in a
		// link-value; the first occurrence wins and later ones are ignored —
		// never the other way round, which would let a trailing rel erase a
		// parsed next.
		if strings.EqualFold(key, "rel") {
			if sawRel {
				continue
			}
			sawRel = true
			rels = strings.Fields(val)
		}
	}
	return target, rels, nil
}

// validateQuoteClosure rejects a header value whose quoted strings never
// close. Without this, an unterminated quote silently swallows every
// parameter after it and the missing pagination signal reads as termination.
func validateQuoteClosure(value string) error {
	quoted, escaped := false, false
	for _, r := range value {
		switch {
		case escaped:
			escaped = false
		case quoted && r == '\\':
			escaped = true
		case r == '"':
			quoted = !quoted
		}
	}
	if quoted {
		return fmt.Errorf("Link 头存在未闭合引号：%q（语法不完整不得解释为没有下一页）", value)
	}
	return nil
}

func derefInt(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

func snippet(body []byte) string {
	s := strings.TrimSpace(string(body))
	if len(s) > 400 {
		return s[:400] + "…"
	}
	return s
}
