// Package config owns the machine-local Qianshou configuration contract
// (version 1) defined by docs/architecture/data-model-and-qianshou-home.md.
//
// Configuration only locates work: engines, repository slugs, local checkout
// bindings, Scope selectors, and Landing intent. It must never copy GitHub
// facts (issue lists, titles, labels, relationships, states) or engine role
// bindings; strict decoding and validation fail closed on such drift.
package config

// Engine is one executable AI coding tool. Role belongs to a conversation,
// never to an engine or a project.
type Engine struct {
	ID      string `json:"id"`
	Adapter string `json:"adapter"`
	Command string `json:"command"`
}

// Repository locates the managed repository on the provider.
type Repository struct {
	Provider string `json:"provider"`
	Slug     string `json:"slug"`
}

// Local is the machine-local checkout binding of one repository.
type Local struct {
	Path string `json:"path"`
}

// Source is the remote Scope selector. It stores only the selector; Work
// Items are derived from GitHub on every refresh.
type Source struct {
	// Type is one of milestone, issue, issue-tree.
	Type string `json:"type"`
	// Number is the milestone number or the issue number.
	Number int `json:"number,omitempty"`
	// RootIssueNumber is the root issue of an issue-tree Scope.
	RootIssueNumber int `json:"rootIssueNumber,omitempty"`
}

// Landing describes where accepted code is intended to land. Git remains the
// owner of whether branches, worktrees, and ancestry actually exist.
type Landing struct {
	// Type is one of integration-branch, base-branch.
	Type       string `json:"type"`
	BaseBranch string `json:"baseBranch"`
	Branch     string `json:"branch,omitempty"`
	Worktree   string `json:"worktree,omitempty"`
}

// Scope is one delivery boundary under a Project.
type Scope struct {
	ID      string  `json:"id"`
	Source  Source  `json:"source"`
	Landing Landing `json:"landing"`
}

// Project is one repository and its machine-local checkout binding.
type Project struct {
	ID         string     `json:"id"`
	Repository Repository `json:"repository"`
	Local      Local      `json:"local"`
	Scopes     []Scope    `json:"scopes"`
}

// Config is the root of ~/.qianshou/config.json.
type Config struct {
	Version  int       `json:"version"`
	Engines  []Engine  `json:"engines"`
	Projects []Project `json:"projects"`
}

// BuiltinEngines returns the initial engine registry. Whether engine commands
// stay configurable remains an open decision; the identity boundary is
// stable regardless.
func BuiltinEngines() []Engine {
	return []Engine{
		{ID: "codex", Adapter: "codex-cli", Command: "codex"},
		{ID: "claude-code", Adapter: "claude-code-cli", Command: "claude"},
	}
}

// ScopeTypes lists the supported source selector types.
func ScopeTypes() []string { return []string{"milestone", "issue", "issue-tree"} }

// LandingTypes lists the supported landing strategy types.
func LandingTypes() []string { return []string{"integration-branch", "base-branch"} }
