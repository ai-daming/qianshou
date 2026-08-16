package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path"
)

// V0MigrationReport records what the V0 → v1 migration did and dropped so the
// operator can audit the machine-local change.
type V0MigrationReport struct {
	Projects              []string
	Scopes                []string
	DroppedRefreshSeconds bool
	DroppedEngineDefaults bool
}

// EngineDefaultsNote documents what the operator loses when defaults drop.
func (r *V0MigrationReport) EngineDefaultsNote() string {
	if !r.DroppedEngineDefaults {
		return ""
	}
	return "developerEngine/reviewerEngine 未迁移：角色选择属于会话，由 UI 偏好状态记住"
}

// v0 shapes mirror config/projects.json exactly as the frozen TypeScript
// prototype reads it. Unknown fields fail closed: a drifted V0 file must be
// inspected, not silently reinterpreted.
type v0File struct {
	Projects []v0Project `json:"projects"`
}

type v0Project struct {
	ID         string `json:"id"`
	Repository struct {
		Slug string `json:"slug"`
		Path string `json:"path"`
	} `json:"repository"`
	Milestone struct {
		Number int `json:"number"`
	} `json:"milestone"`
	Integration struct {
		Branch     string `json:"branch"`
		Worktree   string `json:"worktree"`
		BaseBranch string `json:"baseBranch"`
	} `json:"integration"`
	RefreshSeconds int `json:"refreshSeconds"`
	Defaults       struct {
		DeveloperEngine string `json:"developerEngine"`
		ReviewerEngine  string `json:"reviewerEngine"`
	} `json:"defaults"`
}

// MigrateV0 converts a V0 config/projects.json into a version 1 configuration.
// V0 modeled one milestone target per entry; the target model groups them as
// scopes of one repository project. The migration copies only locators and
// Landing intent — never GitHub facts — and records the V0-only fields it
// drops: refreshSeconds (refresh policy is an open decision) and the
// developer/reviewer engine defaults (role choice belongs to conversations).
func MigrateV0(data []byte) (*Config, *V0MigrationReport, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var file v0File
	if err := dec.Decode(&file); err != nil {
		return nil, nil, fmt.Errorf("V0 配置不合法（含未支持字段即失败）：%w", err)
	}
	if dec.More() {
		return nil, nil, fmt.Errorf("V0 配置不合法：第一个文档之后还有额外内容（尾随文档即失败）")
	}
	if len(file.Projects) == 0 {
		return nil, nil, fmt.Errorf("V0 配置没有任何 project 条目")
	}

	report := &V0MigrationReport{}
	cfg := &Config{Version: ConfigVersion, Engines: BuiltinEngines()}
	bySlug := make(map[string]*Project)
	order := []*Project{}
	for i, entry := range file.Projects {
		if entry.Repository.Slug == "" {
			return nil, nil, fmt.Errorf("V0 projects[%d].repository.slug 不能为空", i)
		}
		if entry.Milestone.Number <= 0 {
			return nil, nil, fmt.Errorf("V0 projects[%d]（%s）milestone.number 必须是正整数", i, entry.Repository.Slug)
		}
		if entry.Integration.BaseBranch == "" || entry.Integration.Branch == "" || entry.Integration.Worktree == "" {
			return nil, nil, fmt.Errorf("V0 projects[%d]（%s）缺少 integration（baseBranch/branch/worktree）", i, entry.Repository.Slug)
		}
		if entry.RefreshSeconds != 0 {
			report.DroppedRefreshSeconds = true
		}
		if entry.Defaults.DeveloperEngine != "" || entry.Defaults.ReviewerEngine != "" {
			report.DroppedEngineDefaults = true
		}

		slug := entry.Repository.Slug
		project, ok := bySlug[slug]
		if !ok {
			project = &Project{
				ID:         path.Base(slug),
				Repository: Repository{Provider: "github", Slug: slug},
				Local:      Local{Path: entry.Repository.Path},
			}
			bySlug[slug] = project
			order = append(order, project)
			report.Projects = append(report.Projects, project.ID)
		} else if project.Local.Path != entry.Repository.Path {
			return nil, nil, fmt.Errorf("V0 中同一仓库 %s 出现了不同的本地路径：%q 与 %q", slug, project.Local.Path, entry.Repository.Path)
		}
		scopeID := fmt.Sprintf("m%d", entry.Milestone.Number)
		project.Scopes = append(project.Scopes, Scope{
			ID:     scopeID,
			Source: Source{Type: "milestone", Number: entry.Milestone.Number},
			Landing: Landing{
				Type:       "integration-branch",
				BaseBranch: entry.Integration.BaseBranch,
				Branch:     entry.Integration.Branch,
				Worktree:   entry.Integration.Worktree,
			},
		})
		report.Scopes = append(report.Scopes, slug+"#"+scopeID)
	}

	for _, project := range order {
		cfg.Projects = append(cfg.Projects, *project)
	}

	if err := cfg.Validate(); err != nil {
		return nil, nil, fmt.Errorf("迁移结果未通过目标契约校验：%w", err)
	}
	return cfg, report, nil
}
