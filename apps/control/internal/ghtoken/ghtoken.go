// Package ghtoken resolves GitHub API credentials without ever persisting
// them. Credentials stay owned by their CLIs: Qianshou only borrows a token
// from the environment or from `gh auth token` for the lifetime of a request.
package ghtoken

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"unicode"
)

// Resolve returns a GitHub API token. Order: GH_TOKEN, GITHUB_TOKEN, then
// `gh auth token`. Every failure mode fails closed with the tried sources.
func Resolve(ctx context.Context) (string, error) {
	for _, key := range []string{"GH_TOKEN", "GITHUB_TOKEN"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			if err := validateTokenShape(value); err != nil {
				return "", fmt.Errorf("%s=%w", key, err)
			}
			return value, nil
		}
	}
	if path, err := exec.LookPath("gh"); err == nil {
		cmd := exec.CommandContext(ctx, path, "auth", "token")
		out, err := cmd.Output()
		if err != nil {
			var ee *exec.ExitError
			detail := ""
			if errors.As(err, &ee) && len(ee.Stderr) > 0 {
				detail = "：" + strings.TrimSpace(string(ee.Stderr))
			}
			return "", fmt.Errorf("gh auth token 执行失败%s（凭据归 gh CLI 所有，Qianshou 不另行存储）", detail)
		}
		token := strings.TrimSpace(string(out))
		if token == "" {
			return "", fmt.Errorf("gh auth token 输出为空：gh 未登录")
		}
		if err := validateTokenShape(token); err != nil {
			return "", fmt.Errorf("gh auth token %w", err)
		}
		return token, nil
	}
	return "", fmt.Errorf("找不到 GitHub 凭据：已尝试 GH_TOKEN、GITHUB_TOKEN、gh auth token（PATH 中无 gh）")
}

// validateTokenShape rejects tokens that still carry whitespace or any
// control character after trimming: such a value is not a credential but a
// broken environment, and sending it as a header would only obscure the
// failure.
func validateTokenShape(token string) error {
	if strings.ContainsFunc(token, func(r rune) bool { return r == ' ' || r == '\t' || unicode.IsControl(r) }) {
		return fmt.Errorf("包含空白或控制字符，不是合法凭据形状")
	}
	return nil
}
