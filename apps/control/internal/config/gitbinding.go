package config

import (
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

// VerifyGitBinding checks that the configured local repository really is the
// configured GitHub repository, so Qianshou never silently combines unrelated
// sources. The origin remote must point at github.com: a same-named repository
// on another host is a different repository. This reads Git facts only.
func VerifyGitBinding(path, slug string) error {
	cmd := exec.Command("git", "-C", path, "remote", "get-url", "origin")
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			return fmt.Errorf("校验 %s 的 Git 绑定失败：%s", path, strings.TrimSpace(string(ee.Stderr)))
		}
		return fmt.Errorf("校验 %s 的 Git 绑定失败：%w", path, err)
	}
	remote := strings.TrimSpace(string(out))
	host, remoteSlug, ok := parseRemote(remote)
	if !ok {
		return fmt.Errorf("无法从 origin remote %q 解析 host/owner/repo", remote)
	}
	if !strings.EqualFold(host, "github.com") {
		return fmt.Errorf("本地路径 %s 的 origin 指向 %s，不是 github.com：同名仓库不等于同一仓库", path, host)
	}
	if !strings.EqualFold(remoteSlug, slug) {
		return fmt.Errorf("本地路径 %s 的 origin 是 %s，与配置的 %s 不一致", path, remoteSlug, slug)
	}
	return nil
}

var remotePatterns = []*regexp.Regexp{
	// ssh://git@github.com/owner/repo.git and git@github.com:owner/repo.git
	regexp.MustCompile(`^(?:ssh://)?git@([^:/]+)[:/]([^/]+/[^/]+?)(?:\.git)?$`),
	// https://github.com/owner/repo.git
	regexp.MustCompile(`^https?://([^/]+)/([^/]+/[^/]+?)(?:\.git)?$`),
}

func parseRemote(remote string) (host, slug string, ok bool) {
	for _, pattern := range remotePatterns {
		if m := pattern.FindStringSubmatch(remote); m != nil {
			return m[1], m[2], true
		}
	}
	return "", "", false
}
