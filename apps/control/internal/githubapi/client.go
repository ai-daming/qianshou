// Package githubapi reads current Project facts from GitHub without caching them.
package githubapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ai-daming/qianshou/apps/control/internal/deps"
	"github.com/ai-daming/qianshou/apps/control/internal/strictjson"
)

const (
	DefaultRESTEndpoint    = "https://api.github.com"
	DefaultGraphQLEndpoint = "https://api.github.com/graphql"
	responseBodyLimit      = 4 << 20
	dependencyBatchSize    = 100
)

var slugPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

type DependencyStatus string

const (
	DependencyReady   DependencyStatus = "READY"
	DependencyBlocked DependencyStatus = "BLOCKED"
	DependencyError   DependencyStatus = "ERROR"
)

type DependencyErrorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type Dependency struct {
	Status    DependencyStatus       `json:"status"`
	BlockedBy []int                  `json:"blockedBy,omitempty"`
	Error     *DependencyErrorDetail `json:"error,omitempty"`
}

type Milestone struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	State  string `json:"state"`
}

type Issue struct {
	Number     int        `json:"number"`
	Title      string     `json:"title"`
	State      string     `json:"state"`
	Labels     []string   `json:"labels"`
	Dependency Dependency `json:"dependency"`
}

type Client struct {
	token           string
	restEndpoint    string
	graphqlEndpoint string
	httpClient      *http.Client
}

func New(token string) *Client {
	return NewClient(token, DefaultRESTEndpoint, DefaultGraphQLEndpoint, http.DefaultClient)
}

func NewClient(token, restEndpoint, graphqlEndpoint string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{
		token:           strings.TrimSpace(token),
		restEndpoint:    strings.TrimRight(restEndpoint, "/"),
		graphqlEndpoint: graphqlEndpoint,
		httpClient:      httpClient,
	}
}

func (c *Client) ListMilestones(ctx context.Context, repo string) ([]Milestone, error) {
	if err := validateRepo(repo); err != nil {
		return nil, err
	}
	endpoint := fmt.Sprintf("%s/repos/%s/milestones?state=all&per_page=100", c.restEndpoint, repo)
	result := make([]Milestone, 0)
	seen := make(map[int]struct{})
	err := c.eachPage(ctx, endpoint, func(body []byte) error {
		var raw *[]struct {
			URL    *string `json:"url"`
			Number *int    `json:"number"`
			Title  *string `json:"title"`
			State  *string `json:"state"`
		}
		if err := decodeTrustedJSON(body, &raw); err != nil {
			return err
		}
		if raw == nil {
			return fmt.Errorf("milestone response is not an array")
		}
		for _, item := range *raw {
			if item.URL == nil || item.Number == nil || *item.Number <= 0 || item.Title == nil || item.State == nil {
				return fmt.Errorf("milestone response is missing required fields")
			}
			if err := c.verifyRESTIdentity(*item.URL, repo, fmt.Sprintf("/milestones/%d", *item.Number)); err != nil {
				return err
			}
			if _, exists := seen[*item.Number]; exists {
				return fmt.Errorf("milestone %d appeared more than once", *item.Number)
			}
			seen[*item.Number] = struct{}{}
			state := strings.ToUpper(*item.State)
			if state != "OPEN" && state != "CLOSED" {
				return fmt.Errorf("milestone state is not understood")
			}
			result = append(result, Milestone{Number: *item.Number, Title: *item.Title, State: state})
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("GitHub milestone facts are unavailable: %w", err)
	}
	return result, nil
}

func (c *Client) ListMilestoneIssues(ctx context.Context, repo string, milestone int) ([]Issue, error) {
	if err := validateRepo(repo); err != nil {
		return nil, err
	}
	if milestone <= 0 {
		return nil, fmt.Errorf("milestone number must be positive")
	}
	endpoint := fmt.Sprintf("%s/repos/%s/issues?milestone=%d&state=all&per_page=100", c.restEndpoint, repo, milestone)
	issues, err := c.readIssues(ctx, endpoint, repo, milestone)
	if err != nil {
		return nil, fmt.Errorf("GitHub milestone Issue facts are unavailable: %w", err)
	}
	for start := 0; start < len(issues); start += dependencyBatchSize {
		end := min(start+dependencyBatchSize, len(issues))
		numbers := make([]int, 0, end-start)
		for i := start; i < end; i++ {
			numbers = append(numbers, issues[i].Number)
		}
		batch, err := deps.CanStartBatch(ctx, c.token, c.graphqlEndpoint, repo, numbers, c.httpClient)
		if err != nil {
			return nil, fmt.Errorf("GitHub dependency facts are unavailable for the Milestone: %w", err)
		}
		for i := start; i < end; i++ {
			issueNumber := issues[i].Number
			if batch.Errors[issueNumber] != nil {
				issues[i].Dependency = dependencyError()
				continue
			}
			judgment, ok := batch.Judgments[issueNumber]
			if !ok {
				return nil, fmt.Errorf("GitHub dependency batch omitted Issue #%d", issueNumber)
			}
			issues[i].Dependency = dependencyFromJudgment(judgment)
		}
	}
	return issues, nil
}

func (c *Client) GetIssue(ctx context.Context, repo string, issueNumber int) (Issue, error) {
	if err := validateRepo(repo); err != nil {
		return Issue{}, err
	}
	if issueNumber <= 0 {
		return Issue{}, fmt.Errorf("Issue number must be positive")
	}
	endpoint := fmt.Sprintf("%s/repos/%s/issues/%d", c.restEndpoint, repo, issueNumber)
	body, _, err := c.get(ctx, endpoint)
	if err != nil {
		return Issue{}, fmt.Errorf("GitHub Issue facts are unavailable: %w", err)
	}
	issue, isPullRequest, err := c.decodeIssue(body, repo, 0)
	if err != nil {
		return Issue{}, err
	}
	if isPullRequest || issue.Number != issueNumber {
		return Issue{}, fmt.Errorf("GitHub response does not identify the requested Issue")
	}
	issue.Dependency = c.dependency(ctx, repo, issue.Number)
	return issue, nil
}

func (c *Client) readIssues(ctx context.Context, endpoint, repo string, milestone int) ([]Issue, error) {
	result := make([]Issue, 0)
	seen := make(map[int]struct{})
	err := c.eachPage(ctx, endpoint, func(body []byte) error {
		var pages *[]json.RawMessage
		if err := decodeTrustedJSON(body, &pages); err != nil {
			return err
		}
		if pages == nil {
			return fmt.Errorf("Issue response is not an array")
		}
		for _, raw := range *pages {
			issue, isPullRequest, err := c.decodeIssue(raw, repo, milestone)
			if err != nil {
				return err
			}
			if _, exists := seen[issue.Number]; exists {
				return fmt.Errorf("Issue or pull request %d appeared more than once", issue.Number)
			}
			seen[issue.Number] = struct{}{}
			if !isPullRequest {
				result = append(result, issue)
			}
		}
		return nil
	})
	return result, err
}

func (c *Client) decodeIssue(body []byte, repo string, expectedMilestone int) (Issue, bool, error) {
	var raw struct {
		RepositoryURL *string `json:"repository_url"`
		Number        *int    `json:"number"`
		Title         *string `json:"title"`
		State         *string `json:"state"`
		Labels        *[]struct {
			Name *string `json:"name"`
		} `json:"labels"`
		Milestone *struct {
			Number *int `json:"number"`
		} `json:"milestone"`
		PullRequest json.RawMessage `json:"pull_request"`
	}
	if err := decodeTrustedJSON(body, &raw); err != nil {
		return Issue{}, false, err
	}
	if raw.RepositoryURL == nil || raw.Number == nil || *raw.Number <= 0 || raw.Title == nil || raw.State == nil || raw.Labels == nil {
		return Issue{}, false, fmt.Errorf("Issue response is missing required fields")
	}
	if err := c.verifyRESTIdentity(*raw.RepositoryURL, repo, ""); err != nil {
		return Issue{}, false, err
	}
	if expectedMilestone > 0 && (raw.Milestone == nil || raw.Milestone.Number == nil || *raw.Milestone.Number != expectedMilestone) {
		return Issue{}, false, fmt.Errorf("Issue response does not belong to the requested Milestone")
	}
	state := strings.ToUpper(*raw.State)
	if state != "OPEN" && state != "CLOSED" {
		return Issue{}, false, fmt.Errorf("Issue state is not understood")
	}
	issue := Issue{Number: *raw.Number, Title: *raw.Title, State: state, Labels: make([]string, 0, len(*raw.Labels))}
	seen := make(map[string]struct{}, len(*raw.Labels))
	for _, label := range *raw.Labels {
		if label.Name == nil || strings.TrimSpace(*label.Name) == "" {
			return Issue{}, false, fmt.Errorf("Issue label is missing its name")
		}
		key := strings.ToLower(*label.Name)
		if _, exists := seen[key]; exists {
			return Issue{}, false, fmt.Errorf("Issue response contains a duplicate label")
		}
		seen[key] = struct{}{}
		issue.Labels = append(issue.Labels, *label.Name)
	}
	isPullRequest := raw.PullRequest != nil
	if isPullRequest {
		if bytes.Equal(bytes.TrimSpace(raw.PullRequest), []byte("null")) {
			return Issue{}, false, fmt.Errorf("pull_request marker is null")
		}
		var marker struct {
			URL *string `json:"url"`
		}
		if err := decodeTrustedJSON(raw.PullRequest, &marker); err != nil || marker.URL == nil {
			return Issue{}, false, fmt.Errorf("pull_request marker is incomplete")
		}
		if err := c.verifyRESTIdentity(*marker.URL, repo, fmt.Sprintf("/pulls/%d", issue.Number)); err != nil {
			return Issue{}, false, err
		}
	}
	return issue, isPullRequest, nil
}

func (c *Client) dependency(ctx context.Context, repo string, issue int) Dependency {
	judgment, err := deps.CanStart(ctx, c.token, c.graphqlEndpoint, repo, issue, c.httpClient)
	if err != nil {
		return dependencyError()
	}
	return dependencyFromJudgment(judgment)
}

func dependencyFromJudgment(judgment deps.Judgment) Dependency {
	if len(judgment.BlockedBy) > 0 {
		return Dependency{Status: DependencyBlocked, BlockedBy: judgment.BlockedBy}
	}
	return Dependency{Status: DependencyReady}
}

func dependencyError() Dependency {
	return Dependency{
		Status: DependencyError,
		Error: &DependencyErrorDetail{
			Code:    "DEPENDENCY_FACTS_UNAVAILABLE",
			Message: "GitHub dependency facts are incomplete or unavailable.",
		},
	}
}

func validateRepo(repo string) error {
	if !slugPattern.MatchString(repo) {
		return fmt.Errorf("repository must be owner/repo")
	}
	return nil
}

func (c *Client) eachPage(ctx context.Context, first string, consume func([]byte) error) error {
	current, err := url.Parse(first)
	if err != nil {
		return fmt.Errorf("invalid GitHub endpoint")
	}
	seen := make(map[string]struct{})
	for current != nil {
		key := current.String()
		if _, exists := seen[key]; exists {
			return fmt.Errorf("GitHub pagination loop detected")
		}
		seen[key] = struct{}{}
		body, links, err := c.get(ctx, key)
		if err != nil {
			return err
		}
		if err := consume(body); err != nil {
			return err
		}
		current, err = parseNextLink(links, current)
		if err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) get(ctx context.Context, endpoint string) ([]byte, []string, error) {
	if c.token == "" {
		return nil, nil, fmt.Errorf("GitHub credentials are unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("cannot build GitHub request")
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("GitHub request failed")
	}
	defer resp.Body.Close()
	if resp.Request == nil || resp.Request.URL.String() != req.URL.String() {
		return nil, nil, fmt.Errorf("GitHub response came from a different evidence URL")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, responseBodyLimit+1))
	if err != nil {
		return nil, nil, fmt.Errorf("GitHub response could not be read")
	}
	if len(body) > responseBodyLimit {
		return nil, nil, fmt.Errorf("GitHub response exceeded the safe size limit")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("GitHub returned HTTP %d", resp.StatusCode)
	}
	return body, resp.Header.Values("Link"), nil
}

func decodeTrustedJSON(data []byte, target any) error {
	if err := strictjson.Decode(data, target, false); err != nil {
		return fmt.Errorf("invalid or contradictory JSON response: %w", err)
	}
	return nil
}

func (c *Client) verifyRESTIdentity(rawURL, repo, suffix string) error {
	base, err := url.Parse(c.restEndpoint)
	if err != nil {
		return fmt.Errorf("configured REST endpoint is invalid")
	}
	identity, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("GitHub response identity is invalid")
	}
	expectedPath := strings.TrimRight(base.EscapedPath(), "/") + "/repos/" + repo + suffix
	if identity.Scheme != base.Scheme || !strings.EqualFold(identity.Host, base.Host) || !strings.EqualFold(identity.EscapedPath(), expectedPath) || identity.RawQuery != "" || identity.Fragment != "" || identity.User != nil {
		return fmt.Errorf("GitHub response identifies a different repository or resource")
	}
	return nil
}

// parseNextLink accepts the RFC 8288 shape GitHub emits, while rejecting any
// syntax or target that makes the next collection page ambiguous.
func parseNextLink(values []string, current *url.URL) (*url.URL, error) {
	var next *url.URL
	for _, value := range values {
		parts, err := splitHeader(value, ',')
		if err != nil {
			return nil, fmt.Errorf("cannot parse GitHub Link header: %w", err)
		}
		for _, part := range parts {
			segments, err := splitHeader(part, ';')
			if err != nil || len(segments) < 2 {
				return nil, fmt.Errorf("cannot parse GitHub Link header")
			}
			targetText := strings.TrimSpace(segments[0])
			if len(targetText) < 3 || targetText[0] != '<' || targetText[len(targetText)-1] != '>' {
				return nil, fmt.Errorf("cannot parse GitHub Link target")
			}
			isNext := false
			seenRel := false
			for _, parameter := range segments[1:] {
				name, value, ok := strings.Cut(strings.TrimSpace(parameter), "=")
				if !ok || strings.TrimSpace(name) == "" || strings.TrimSpace(value) == "" {
					return nil, fmt.Errorf("cannot parse GitHub Link parameter")
				}
				if !strings.EqualFold(strings.TrimSpace(name), "rel") {
					continue
				}
				if seenRel {
					return nil, fmt.Errorf("GitHub Link target has duplicate rel parameters")
				}
				seenRel = true
				relValue, err := unquoteParameter(strings.TrimSpace(value))
				if err != nil {
					return nil, err
				}
				for _, relation := range strings.Fields(relValue) {
					if relation == "next" {
						isNext = true
					}
				}
			}
			if !isNext {
				continue
			}
			if next != nil {
				return nil, fmt.Errorf("GitHub Link header has more than one next target")
			}
			ref, err := url.Parse(targetText[1 : len(targetText)-1])
			if err != nil {
				return nil, fmt.Errorf("cannot parse GitHub next URL")
			}
			candidate := current.ResolveReference(ref)
			if candidate.Scheme != current.Scheme || !strings.EqualFold(candidate.Host, current.Host) || candidate.Path != current.Path || candidate.User != nil || candidate.Fragment != "" {
				return nil, fmt.Errorf("GitHub next URL crossed the requested API collection boundary")
			}
			if err := validateNextQuery(current, candidate); err != nil {
				return nil, err
			}
			next = candidate
		}
	}
	return next, nil
}

func validateNextQuery(current, candidate *url.URL) error {
	currentQuery := current.Query()
	nextQuery := candidate.Query()
	pageValues, exists := nextQuery["page"]
	if !exists || len(pageValues) != 1 {
		return fmt.Errorf("GitHub next URL has an ambiguous page selector")
	}
	page, err := strconv.Atoi(pageValues[0])
	if err != nil || page <= 0 {
		return fmt.Errorf("GitHub next URL has an invalid page selector")
	}
	currentQuery.Del("page")
	nextQuery.Del("page")
	if currentQuery.Encode() != nextQuery.Encode() {
		return fmt.Errorf("GitHub next URL changed the requested collection selector")
	}
	return nil
}

func splitHeader(input string, separator byte) ([]string, error) {
	var parts []string
	start := 0
	inQuote := false
	inAngle := false
	escaped := false
	for i := 0; i < len(input); i++ {
		ch := input[i]
		if escaped {
			escaped = false
			continue
		}
		if inQuote && ch == '\\' {
			escaped = true
			continue
		}
		switch ch {
		case '"':
			inQuote = !inQuote
		case '<':
			if !inQuote {
				if inAngle {
					return nil, fmt.Errorf("nested angle bracket")
				}
				inAngle = true
			}
		case '>':
			if !inQuote {
				if !inAngle {
					return nil, fmt.Errorf("unmatched angle bracket")
				}
				inAngle = false
			}
		default:
			if ch == separator && !inQuote && !inAngle {
				part := strings.TrimSpace(input[start:i])
				if part == "" {
					return nil, fmt.Errorf("empty Link component")
				}
				parts = append(parts, part)
				start = i + 1
			}
		}
	}
	if inQuote || inAngle || escaped {
		return nil, fmt.Errorf("unterminated Link component")
	}
	part := strings.TrimSpace(input[start:])
	if part == "" {
		return nil, fmt.Errorf("empty Link component")
	}
	return append(parts, part), nil
}

func unquoteParameter(value string) (string, error) {
	if !strings.HasPrefix(value, `"`) {
		return value, nil
	}
	decoded, err := strconv.Unquote(value)
	if err != nil {
		return "", fmt.Errorf("cannot parse quoted Link parameter")
	}
	return decoded, nil
}
