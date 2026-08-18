// Package deps 判断一个 GitHub Issue 是否被仍未关闭的 Issue 阻塞。
//
// 合同是一句话：查 blockedBy，阻塞者全关 = 能开工，有未关的 = 阻塞中；
// 判断不出来必须报错，不许装作没有依赖。
//
// 两条不可妥协的错向：
//  1. 查不出 ≠ 没有依赖——请求失败、响应缺东西、看不全（还有下一页）、
//     说的不是这个 Issue，一律报错；
//  2. 证据矛盾 ≠ 挑一个信——同一个阻塞者出现两次即报错。
//
// 语法上宽容（不校验 RFC 文法），结论上禁止歧义；不声明语法完备。
package deps

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const githubGraphQL = "https://api.github.com/graphql"

var slugPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// Blocker 是一个阻塞者及其当前状态。
type Blocker struct {
	Number int
	State  string // OPEN / CLOSED
}

// Judgment 是判断结果。BlockedBy 为空表示可以开工。
type Judgment struct {
	Issue     int
	Blockers  []Blocker
	BlockedBy []int // 状态为 OPEN 的阻塞者
}

// Judge 是生产入口：固定查询 GitHub，凭据从 GH_TOKEN / GITHUB_TOKEN / gh auth token 取。
func Judge(ctx context.Context, token, repo string, issue int) (Judgment, error) {
	return CanStart(ctx, token, githubGraphQL, repo, issue, http.DefaultClient)
}

// CanStart 查询 gqlEndpoint 判断 repo#issue 的依赖状态。测试用它指向 stub。
func CanStart(ctx context.Context, token, gqlEndpoint, repo string, issue int, hc *http.Client) (Judgment, error) {
	if strings.TrimSpace(token) == "" {
		return Judgment{}, fmt.Errorf("没有 GitHub 凭据：请设置 GH_TOKEN/GITHUB_TOKEN 或 gh auth login")
	}
	if !slugPattern.MatchString(repo) {
		return Judgment{}, fmt.Errorf("仓库定位必须是 owner/repo，当前为 %q", repo)
	}
	if issue <= 0 {
		return Judgment{}, fmt.Errorf("Issue 编号必须是正整数，当前为 %d", issue)
	}
	if hc == nil {
		hc = http.DefaultClient
	}

	const query = `query($owner:String!,$name:String!,$number:Int!){
  repository(owner:$owner, name:$name){
    nameWithOwner
    issue(number:$number){
      number
      blockedBy(first:100){ pageInfo{ hasNextPage } nodes{ number state } }
    }
  }
}`
	parts := strings.SplitN(repo, "/", 2)
	payload, err := json.Marshal(map[string]any{
		"query":     query,
		"variables": map[string]any{"owner": parts[0], "name": parts[1], "number": issue},
	})
	if err != nil {
		return Judgment{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, gqlEndpoint, bytes.NewReader(payload))
	if err != nil {
		return Judgment{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return Judgment{}, fmt.Errorf("查询 %s#%d 失败（不得当作无依赖）：%w", repo, issue, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Judgment{}, fmt.Errorf("读取 %s#%d 响应失败：%w", repo, issue, err)
	}
	if resp.StatusCode != http.StatusOK {
		return Judgment{}, fmt.Errorf("查询 %s#%d 返回 HTTP %d：%s", repo, issue, resp.StatusCode, snippet(body))
	}

	var gql struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
		Data struct {
			Repository struct {
				NameWithOwner *string `json:"nameWithOwner"`
				Issue         *struct {
					Number    int `json:"number"`
					BlockedBy *struct {
						PageInfo struct {
							HasNextPage bool `json:"hasNextPage"`
						} `json:"pageInfo"`
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
		return Judgment{}, fmt.Errorf("查询 %s#%d 的响应不是合法 JSON：%w", repo, issue, err)
	}
	if len(gql.Errors) > 0 {
		msgs := make([]string, 0, len(gql.Errors))
		for _, e := range gql.Errors {
			msgs = append(msgs, e.Message)
		}
		return Judgment{}, fmt.Errorf("查询 %s#%d 被 GraphQL 拒绝：%s", repo, issue, strings.Join(msgs, "；"))
	}
	// 查不出 ≠ 没有依赖
	if gql.Data.Repository.Issue == nil {
		return Judgment{}, fmt.Errorf("查不到 %s#%d（仓库或 Issue 不存在，不得当作无依赖）", repo, issue)
	}
	if gql.Data.Repository.NameWithOwner == nil || !strings.EqualFold(*gql.Data.Repository.NameWithOwner, repo) {
		got := "<缺失>"
		if gql.Data.Repository.NameWithOwner != nil {
			got = *gql.Data.Repository.NameWithOwner
		}
		return Judgment{}, fmt.Errorf("响应说的是 %s 的事实，不是请求的 %s", got, repo)
	}
	if gql.Data.Repository.Issue.Number != issue {
		return Judgment{}, fmt.Errorf("响应是 #%d 的事实，不是请求的 #%d", gql.Data.Repository.Issue.Number, issue)
	}
	blockedBy := gql.Data.Repository.Issue.BlockedBy
	if blockedBy == nil {
		return Judgment{}, fmt.Errorf("%s#%d 的响应缺少 blockedBy（不得当作无依赖）", repo, issue)
	}
	if blockedBy.PageInfo.HasNextPage {
		return Judgment{}, fmt.Errorf("%s#%d 的依赖超过一页，无法判断完整（不得截断当作全部）", repo, issue)
	}

	j := Judgment{Issue: issue}
	seen := make(map[int]bool)
	for _, node := range blockedBy.Nodes {
		if node.Number <= 0 {
			return Judgment{}, fmt.Errorf("%s#%d 的依赖缺少编号（不得当作无依赖）", repo, issue)
		}
		if node.State != "OPEN" && node.State != "CLOSED" {
			return Judgment{}, fmt.Errorf("%s#%d 的依赖 #%d 状态 %q 无法解读", repo, issue, node.Number, node.State)
		}
		// 证据矛盾 ≠ 挑一个信：同一依赖出现两次即异常
		if seen[node.Number] {
			return Judgment{}, fmt.Errorf("%s#%d 的依赖 #%d 出现两次，数据矛盾", repo, issue, node.Number)
		}
		seen[node.Number] = true
		j.Blockers = append(j.Blockers, Blocker{Number: node.Number, State: node.State})
		if node.State == "OPEN" {
			j.BlockedBy = append(j.BlockedBy, node.Number)
		}
	}
	return j, nil
}

func snippet(body []byte) string {
	s := strings.TrimSpace(string(body))
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}

// ResolveToken 按顺序取凭据：GH_TOKEN、GITHUB_TOKEN、gh auth token。凭据只借用不落盘。
func ResolveToken(ctx context.Context) (string, error) {
	for _, key := range []string{"GH_TOKEN", "GITHUB_TOKEN"} {
		if v := strings.TrimSpace(osGetenv(key)); v != "" {
			return v, nil
		}
	}
	if path, err := execLookPath("gh"); err == nil {
		out, err := execCommandContext(ctx, path, "auth", "token")
		if err != nil {
			return "", fmt.Errorf("gh auth token 执行失败（凭据归 gh CLI 所有）：%w", err)
		}
		token := strings.TrimSpace(string(out))
		if token == "" {
			return "", fmt.Errorf("gh auth token 输出为空：gh 未登录")
		}
		return token, nil
	}
	return "", fmt.Errorf("找不到 GitHub 凭据：已尝试 GH_TOKEN、GITHUB_TOKEN、gh auth token")
}
