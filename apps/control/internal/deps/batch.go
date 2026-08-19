package deps

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const maxDependencyBatchSize = 100

// BatchJudgments separates Issue-scoped evidence failures from failures of the
// shared GitHub request. A non-nil error from CanStartBatch means no collection
// response can safely be assembled from that batch.
type BatchJudgments struct {
	Judgments map[int]Judgment
	Errors    map[int]error
}

// CanStartBatch reads dependency facts for at most one REST Issue page. GraphQL
// aliases remove the per-Issue request fan-out while retaining an independently
// validated judgment for every Issue in the response.
func CanStartBatch(ctx context.Context, token, gqlEndpoint, repo string, issues []int, hc *http.Client) (BatchJudgments, error) {
	result := BatchJudgments{Judgments: make(map[int]Judgment, len(issues)), Errors: make(map[int]error)}
	if strings.TrimSpace(token) == "" {
		return BatchJudgments{}, fmt.Errorf("没有 GitHub 凭据：请设置 GH_TOKEN/GITHUB_TOKEN 或 gh auth login")
	}
	if !slugPattern.MatchString(repo) {
		return BatchJudgments{}, fmt.Errorf("仓库定位必须是 owner/repo，当前为 %q", repo)
	}
	if len(issues) == 0 {
		return result, nil
	}
	if len(issues) > maxDependencyBatchSize {
		return BatchJudgments{}, fmt.Errorf("单次依赖查询最多接受 %d 个 Issue", maxDependencyBatchSize)
	}
	if hc == nil {
		hc = http.DefaultClient
	}

	aliases := make(map[string]int, len(issues))
	var query strings.Builder
	query.WriteString("query($owner:String!,$name:String!){\n  repository(owner:$owner, name:$name){\n    nameWithOwner\n")
	for _, issue := range issues {
		if issue <= 0 {
			return BatchJudgments{}, fmt.Errorf("Issue 编号必须是正整数，当前为 %d", issue)
		}
		alias := issueAlias(issue)
		if _, exists := aliases[alias]; exists {
			return BatchJudgments{}, fmt.Errorf("Issue #%d 在批次中出现两次", issue)
		}
		aliases[alias] = issue
		fmt.Fprintf(&query, "    %s: issue(number:%d){\n      number\n      blockedBy(first:100){ pageInfo{ hasNextPage } nodes{ number state } }\n    }\n", alias, issue)
	}
	query.WriteString("  }\n}")

	parts := strings.SplitN(repo, "/", 2)
	payload, err := json.Marshal(map[string]any{
		"query":     query.String(),
		"variables": map[string]any{"owner": parts[0], "name": parts[1]},
	})
	if err != nil {
		return BatchJudgments{}, err
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, gqlEndpoint, bytes.NewReader(payload))
	if err != nil {
		return BatchJudgments{}, fmt.Errorf("cannot build GitHub dependency request")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return BatchJudgments{}, fmt.Errorf("GitHub dependency request failed: %w", err)
	}
	defer resp.Body.Close()
	const bodyLimit = 1 << 20
	body, err := io.ReadAll(io.LimitReader(resp.Body, bodyLimit+1))
	if err != nil {
		return BatchJudgments{}, fmt.Errorf("GitHub dependency response could not be read")
	}
	if len(body) > bodyLimit {
		return BatchJudgments{}, fmt.Errorf("GitHub dependency response exceeded the safe size limit")
	}
	if resp.StatusCode != http.StatusOK {
		return BatchJudgments{}, fmt.Errorf("GitHub dependency request returned HTTP %d", resp.StatusCode)
	}
	duplicates, err := findDuplicateKeyPaths(body)
	if err != nil {
		return BatchJudgments{}, fmt.Errorf("GitHub dependency response is not valid JSON")
	}
	for _, duplicate := range duplicates {
		issue, ok := issueFromJSONPath(duplicate, aliases)
		if !ok {
			return BatchJudgments{}, fmt.Errorf("GitHub dependency response contains contradictory shared fields")
		}
		result.Errors[issue] = fmt.Errorf("GitHub dependency response for Issue #%d contains contradictory fields", issue)
	}

	type graphQLError struct {
		Message string `json:"message"`
		Path    []any  `json:"path"`
	}
	var envelope struct {
		Errors []graphQLError `json:"errors"`
		Data   *struct {
			Repository json.RawMessage `json:"repository"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return BatchJudgments{}, fmt.Errorf("GitHub dependency response is not valid JSON")
	}
	if len(envelope.Errors) > 0 && (resp.Header.Get("X-RateLimit-Remaining") == "0" || resp.Header.Get("Retry-After") != "") {
		return BatchJudgments{}, fmt.Errorf("GitHub dependency facts are rate limited")
	}
	for _, graphErr := range envelope.Errors {
		issue, ok := issueFromErrorPath(graphErr.Path, aliases)
		if !ok {
			return BatchJudgments{}, fmt.Errorf("GitHub rejected the shared dependency query")
		}
		result.Errors[issue] = fmt.Errorf("GitHub rejected dependency facts for Issue #%d", issue)
	}
	if envelope.Data == nil || len(envelope.Data.Repository) == 0 || bytes.Equal(bytes.TrimSpace(envelope.Data.Repository), []byte("null")) {
		return BatchJudgments{}, fmt.Errorf("GitHub dependency response is missing the repository")
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(envelope.Data.Repository, &fields); err != nil || fields == nil {
		return BatchJudgments{}, fmt.Errorf("GitHub dependency repository response is invalid")
	}
	identityRaw, exists := fields["nameWithOwner"]
	if !exists {
		return BatchJudgments{}, fmt.Errorf("GitHub dependency response is missing repository identity")
	}
	delete(fields, "nameWithOwner")
	var identity string
	if err := json.Unmarshal(identityRaw, &identity); err != nil || !strings.EqualFold(identity, repo) {
		return BatchJudgments{}, fmt.Errorf("GitHub dependency response identifies a different repository")
	}
	for field := range fields {
		if _, expected := aliases[field]; !expected {
			return BatchJudgments{}, fmt.Errorf("GitHub dependency response contains an unexpected Issue field")
		}
	}

	for alias, issue := range aliases {
		if result.Errors[issue] != nil {
			continue
		}
		raw, exists := fields[alias]
		if !exists || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			result.Errors[issue] = fmt.Errorf("GitHub dependency response is missing Issue #%d", issue)
			continue
		}
		judgment, err := decodeBatchJudgment(raw, repo, issue)
		if err != nil {
			result.Errors[issue] = err
			continue
		}
		result.Judgments[issue] = judgment
	}
	return result, nil
}

func issueAlias(issue int) string {
	return "issue_" + strconv.Itoa(issue)
}

func issueFromErrorPath(path []any, aliases map[string]int) (int, bool) {
	if len(path) < 2 || path[0] != "repository" {
		return 0, false
	}
	alias, ok := path[1].(string)
	if !ok {
		return 0, false
	}
	issue, ok := aliases[alias]
	return issue, ok
}

func issueFromJSONPath(path []string, aliases map[string]int) (int, bool) {
	if len(path) < 3 || path[0] != "data" || path[1] != "repository" {
		return 0, false
	}
	issue, ok := aliases[path[2]]
	return issue, ok
}

func findDuplicateKeyPaths(data []byte) ([][]string, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	duplicates := make([][]string, 0)
	var walk func([]string) error
	walk = func(path []string) error {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delimiter, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delimiter {
		case '{':
			seen := make(map[string]struct{})
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return err
				}
				key, ok := keyToken.(string)
				if !ok {
					return fmt.Errorf("JSON object key is not a string")
				}
				keyPath := appendPath(path, key)
				folded := strings.ToLower(key)
				if _, exists := seen[folded]; exists {
					duplicates = append(duplicates, keyPath)
				}
				seen[folded] = struct{}{}
				if err := walk(keyPath); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		case '[':
			for decoder.More() {
				if err := walk(path); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		default:
			return fmt.Errorf("unexpected JSON delimiter")
		}
	}
	if err := walk(nil); err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("more than one JSON value")
		}
		return nil, err
	}
	return duplicates, nil
}

func appendPath(path []string, key string) []string {
	next := make([]string, len(path)+1)
	copy(next, path)
	next[len(path)] = key
	return next
}

func decodeBatchJudgment(data []byte, repo string, expectedIssue int) (Judgment, error) {
	var issue struct {
		Number    *int `json:"number"`
		BlockedBy *struct {
			PageInfo *struct {
				HasNextPage *bool `json:"hasNextPage"`
			} `json:"pageInfo"`
			Nodes *[]struct {
				Number int    `json:"number"`
				State  string `json:"state"`
			} `json:"nodes"`
		} `json:"blockedBy"`
	}
	if err := json.Unmarshal(data, &issue); err != nil {
		return Judgment{}, fmt.Errorf("%s#%d 的依赖响应不是合法 JSON", repo, expectedIssue)
	}
	if issue.Number == nil || *issue.Number != expectedIssue {
		return Judgment{}, fmt.Errorf("依赖响应不是请求的 %s#%d", repo, expectedIssue)
	}
	if issue.BlockedBy == nil || issue.BlockedBy.PageInfo == nil || issue.BlockedBy.PageInfo.HasNextPage == nil || issue.BlockedBy.Nodes == nil {
		return Judgment{}, fmt.Errorf("%s#%d 的依赖响应不完整", repo, expectedIssue)
	}
	if *issue.BlockedBy.PageInfo.HasNextPage {
		return Judgment{}, fmt.Errorf("%s#%d 的依赖超过一页，无法判断完整", repo, expectedIssue)
	}

	judgment := Judgment{Issue: expectedIssue}
	seen := make(map[int]struct{}, len(*issue.BlockedBy.Nodes))
	for _, blocker := range *issue.BlockedBy.Nodes {
		if blocker.Number <= 0 || (blocker.State != "OPEN" && blocker.State != "CLOSED") {
			return Judgment{}, fmt.Errorf("%s#%d 的依赖包含无法判断的阻塞者", repo, expectedIssue)
		}
		if _, exists := seen[blocker.Number]; exists {
			return Judgment{}, fmt.Errorf("%s#%d 的依赖 #%d 出现两次", repo, expectedIssue, blocker.Number)
		}
		seen[blocker.Number] = struct{}{}
		judgment.Blockers = append(judgment.Blockers, Blocker{Number: blocker.Number, State: blocker.State})
		if blocker.State == "OPEN" {
			judgment.BlockedBy = append(judgment.BlockedBy, blocker.Number)
		}
	}
	return judgment, nil
}
