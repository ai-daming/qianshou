package config

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// ValidationError carries every determinable violation so drift is reported in
// one pass instead of one fix per run.
type ValidationError struct {
	Issues []string
}

func (e *ValidationError) Error() string {
	head := fmt.Sprintf("配置校验失败（%d 项）：", len(e.Issues))
	return head + "\n- " + strings.Join(e.Issues, "\n- ")
}

var slugPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// Validate checks the configuration against the data-model contract. It is a
// pure function: repository/local-path identity is verified separately by
// VerifyGitBinding because it needs local Git facts.
func (c *Config) Validate() error {
	var issues []string
	add := func(format string, args ...any) {
		issues = append(issues, fmt.Sprintf(format, args...))
	}

	if c.Version != ConfigVersion {
		add("version 必须是 %d，当前为 %d", ConfigVersion, c.Version)
	}

	if len(c.Engines) == 0 {
		add("engines 至少需要一个（内置：codex、claude-code）")
	}
	seenEngine := make(map[string]bool)
	for i, e := range c.Engines {
		switch {
		case e.ID == "":
			add("engines[%d].id 不能为空", i)
		case seenEngine[e.ID]:
			add("engines[%d].id 重复：%s", i, e.ID)
		default:
			seenEngine[e.ID] = true
		}
		if e.Adapter == "" {
			add("engines[%d]（%s）缺少 adapter", i, e.ID)
		}
		if e.Command == "" {
			add("engines[%d]（%s）缺少 command", i, e.ID)
		}
	}

	if len(c.Projects) == 0 {
		add("projects 至少需要一个：配置的作用是定位 Project")
	}
	seenProject := make(map[string]bool)
	for i, p := range c.Projects {
		label := fmt.Sprintf("projects[%d]", i)
		if p.ID != "" {
			label = fmt.Sprintf("projects[%d]（%s）", i, p.ID)
			if seenProject[p.ID] {
				add("%s.id 重复：%s", label, p.ID)
			}
			seenProject[p.ID] = true
		} else {
			add("%s.id 不能为空", label)
		}
		if p.Repository.Provider != "github" {
			add("%s.repository.provider 只支持 github，当前为 %q", label, p.Repository.Provider)
		}
		if !slugPattern.MatchString(p.Repository.Slug) {
			add("%s.repository.slug 必须形如 owner/repo，当前为 %q", label, p.Repository.Slug)
		}
		if !filepath.IsAbs(p.Local.Path) {
			add("%s.local.path 必须是绝对路径，当前为 %q", label, p.Local.Path)
		}
		if len(p.Scopes) == 0 {
			add("%s.scopes 至少需要一个", label)
		}
		seenScope := make(map[string]bool)
		for j, s := range p.Scopes {
			scopeLabel := fmt.Sprintf("%s.scopes[%d]", label, j)
			if s.ID != "" {
				scopeLabel = fmt.Sprintf("%s.scopes[%d]（%s）", label, j, s.ID)
				if seenScope[s.ID] {
					add("%s.id 重复：%s", scopeLabel, s.ID)
				}
				seenScope[s.ID] = true
			} else {
				add("%s.id 不能为空", scopeLabel)
			}
			issues = append(issues, validateSource(scopeLabel, s.Source)...)
			issues = append(issues, validateLanding(scopeLabel, s.Landing)...)
		}
	}

	if len(issues) > 0 {
		return &ValidationError{Issues: issues}
	}
	return nil
}

func validateSource(label string, s Source) []string {
	var issues []string
	add := func(format string, args ...any) {
		issues = append(issues, fmt.Sprintf(format, args...))
	}
	switch s.Type {
	case "milestone", "issue":
		if s.Number <= 0 {
			add("%s.source.number（%s）必须是正整数", label, s.Type)
		}
		if s.RootIssueNumber != 0 {
			add("%s.source.rootIssueNumber 只属于 issue-tree，当前 source.type 为 %s", label, s.Type)
		}
	case "issue-tree":
		if s.RootIssueNumber <= 0 {
			add("%s.source.rootIssueNumber 必须是正整数", label)
		}
		if s.Number != 0 {
			add("%s.source.number 只属于 milestone/issue，当前 source.type 为 issue-tree", label)
		}
	default:
		add("%s.source.type 必须是 milestone / issue / issue-tree，当前为 %q", label, s.Type)
	}
	return issues
}

func validateLanding(label string, l Landing) []string {
	var issues []string
	add := func(format string, args ...any) {
		issues = append(issues, fmt.Sprintf(format, args...))
	}
	if l.BaseBranch == "" {
		add("%s.landing.baseBranch 不能为空", label)
	}
	switch l.Type {
	case "integration-branch":
		if l.Branch == "" {
			add("%s.landing.branch 不能为空（integration-branch 需要 branch）", label)
		}
		if l.Worktree == "" {
			add("%s.landing.worktree 不能为空（integration-branch 需要 worktree）", label)
		} else if !filepath.IsAbs(l.Worktree) {
			add("%s.landing.worktree 必须是绝对路径，当前为 %q", label, l.Worktree)
		}
	case "base-branch":
		if l.Branch != "" || l.Worktree != "" {
			add("%s.landing.branch/worktree 只属于 integration-branch，当前 landing.type 为 base-branch", label)
		}
	default:
		add("%s.landing.type 必须是 integration-branch / base-branch，当前为 %q", label, l.Type)
	}
	return issues
}
