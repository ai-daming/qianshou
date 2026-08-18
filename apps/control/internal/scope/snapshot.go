// Package scope derives the current Work Item snapshot of one Scope from
// freshly fetched GitHub facts. The snapshot is a derived view: GitHub owns
// membership, labels, hierarchy, dependencies, and states; this package only
// normalizes them and fails closed when facts are missing or contradictory,
// so an incomplete refresh can never be read as "no dependencies".
//
// There is deliberately no Facts interface: arbitrary fact implementations
// were a forging seam. Snapshots are derived only through *ghfacts.Client,
// whose decode exits are the sole producers of fact values (opaque,
// immutable, unconstructible from outside that package).
package scope

import (
	"context"
	"fmt"
	"strings"

	"github.com/ai-daming/qianshou/apps/control/internal/classification"
	"github.com/ai-daming/qianshou/apps/control/internal/config"
	"github.com/ai-daming/qianshou/apps/control/internal/ghfacts"
)

// Mode is the milestone mode contract: a scope without a recognized Control
// Issue is a flat collection; exactly one makes it an initiative.
type Mode string

const (
	ModeFlat       Mode = "flat"
	ModeInitiative Mode = "initiative"
)

// ControlIssueLabel is the only legal way to recognize a Control Issue.
// Membership is decided by this label alone — never by title, body,
// parentage, or local configuration.
const ControlIssueLabel = "type:milestone-control"

// BlockedRef is one native Blocked by prerequisite with its current state.
type BlockedRef struct {
	Number int
	State  string
}

// Item is one GitHub issue observed through the Scope.
type Item struct {
	Number         int
	Title          string
	State          string
	Labels         []string
	Classification classification.Result
	Parent         *int
	BlockedBy      []BlockedRef
}

// IsControlIssue reports raw label membership, independent of whether the
// full classification is valid.
func (i Item) IsControlIssue() bool {
	for _, label := range i.Labels {
		if strings.EqualFold(strings.TrimSpace(label), ControlIssueLabel) {
			return true
		}
	}
	return false
}

// UnsatisfiedDependencies lists the open Blocked by prerequisites. An empty
// result on a complete fact set means the issue is unblocked.
func (i Item) UnsatisfiedDependencies() []int {
	var open []int
	for _, ref := range i.BlockedBy {
		if !strings.EqualFold(ref.State, "closed") {
			open = append(open, ref.Number)
		}
	}
	return open
}

// Snapshot is the derived scope view handed to gates and the board.
type Snapshot struct {
	ScopeID      string
	Mode         Mode
	ControlIssue int // 0 in flat mode
	Items        []Item
}

// Build derives a snapshot from an already fetched fact set. Every member
// must carry relationship facts, every fact must pass the unified invariants
// (opaque facts can only come from a real read), and more than one Control
// Issue label fails closed as inconsistent GitHub governance.
func Build(scopeID string, issues []ghfacts.Issue, rels map[int]ghfacts.Relationships) (*Snapshot, error) {
	snap := &Snapshot{ScopeID: scopeID, Mode: ModeFlat}
	var controlIssues []int
	seen := make(map[int]bool, len(issues))
	issueStates := make(map[int]string, len(issues))
	for _, src := range issues {
		if seen[src.Number()] {
			return nil, fmt.Errorf("事实异常：#%d 在成员列表中重复出现", src.Number())
		}
		seen[src.Number()] = true
		r, ok := rels[src.Number()]
		if !ok {
			return nil, fmt.Errorf("依赖事实不完整：#%d 缺少父级/Blocked by 事实，缺失不得解释为无依赖", src.Number())
		}
		if r.Number() != src.Number() {
			return nil, fmt.Errorf("事实错位：请求 #%d 的关系却得到 #%d 的（不得拼装成同一事实）", src.Number(), r.Number())
		}
		if err := src.Validate(); err != nil {
			return nil, fmt.Errorf("成员事实无效（#%d）：%w", src.Number(), err)
		}
		if err := r.Validate(); err != nil {
			return nil, fmt.Errorf("关系事实无效（#%d）：%w", src.Number(), err)
		}
		// Freshness as detectable consistency: the same issue may appear as a
		// member and as a blocker elsewhere (and be reported by several
		// referrers). Already-obtained facts must agree on its state; a
		// contradiction is not staleness to tolerate but a fact set that never
		// coexisted. Truly undetectable cross-request atomicity stays a
		// registered residual.
		recordState := func(number int, state string) error {
			normalized := strings.ToLower(state)
			if prev, ok := issueStates[number]; ok && prev != normalized {
				return fmt.Errorf("同一 Issue #%d 的状态在不同事实中矛盾（%s ↔ %s）", number, prev, normalized)
			}
			issueStates[number] = normalized
			return nil
		}
		if err := recordState(src.Number(), src.State()); err != nil {
			return nil, err
		}
		blockedBy := r.BlockedBy()
		for _, ref := range blockedBy {
			if err := recordState(ref.Number, ref.State); err != nil {
				return nil, err
			}
		}
		item := Item{
			Number:         src.Number(),
			Title:          src.Title(),
			State:          src.State(),
			Labels:         src.Labels(),
			Classification: classification.Normalize(src.Labels()),
		}
		if parent, ok := r.Parent(); ok {
			item.Parent = &parent
		}
		for _, b := range blockedBy {
			item.BlockedBy = append(item.BlockedBy, BlockedRef(b))
		}
		if item.IsControlIssue() {
			controlIssues = append(controlIssues, item.Number)
		}
		snap.Items = append(snap.Items, item)
	}
	switch len(controlIssues) {
	case 0:
		// flat stays
	case 1:
		snap.Mode = ModeInitiative
		snap.ControlIssue = controlIssues[0]
	default:
		nums := make([]string, 0, len(controlIssues))
		for _, n := range controlIssues {
			nums = append(nums, fmt.Sprintf("#%d", n))
		}
		return nil, fmt.Errorf("里程碑治理不一致：发现 %d 个带 %s 标签的 Control Issue（%s），恰好一个才合法",
			len(controlIssues), ControlIssueLabel, strings.Join(nums, "、"))
	}
	return snap, nil
}

// FromMilestone fetches all facts for a milestone scope through the real
// client and derives the snapshot. Any failure — membership listing or one
// issue's relationships — discards the whole result.
func FromMilestone(ctx context.Context, client *ghfacts.Client, slug string, milestone int, scopeID string) (*Snapshot, error) {
	issues, err := client.ListMilestoneIssues(ctx, slug, milestone)
	if err != nil {
		return nil, fmt.Errorf("读取里程碑 %s milestone %d 成员失败：%w", slug, milestone, err)
	}
	rels := make(map[int]ghfacts.Relationships, len(issues))
	for _, src := range issues {
		r, err := client.Relationships(ctx, slug, src.Number())
		if err != nil {
			return nil, fmt.Errorf("读取 #%d 的父级/依赖关系失败（部分事实不得降级为无依赖）：%w", src.Number(), err)
		}
		if r.Number() != src.Number() {
			return nil, fmt.Errorf("读取 #%d 的关系却返回 #%d 的（不得拼装成同一事实）", src.Number(), r.Number())
		}
		rels[src.Number()] = r
	}
	return Build(scopeID, issues, rels)
}

// FromScope derives the snapshot for one configured scope. Source forms the
// data model has settled are served; issue-tree traversal is an open decision
// and fails closed rather than guessing.
func FromScope(ctx context.Context, client *ghfacts.Client, project config.Project, sc config.Scope) (*Snapshot, error) {
	switch sc.Source.Type {
	case "milestone":
		return FromMilestone(ctx, client, project.Repository.Slug, sc.Source.Number, sc.ID)
	case "issue":
		src, err := client.GetIssue(ctx, project.Repository.Slug, sc.Source.Number)
		if err != nil {
			return nil, fmt.Errorf("读取 %s#%d 失败：%w", project.Repository.Slug, sc.Source.Number, err)
		}
		r, err := client.Relationships(ctx, project.Repository.Slug, sc.Source.Number)
		if err != nil {
			return nil, fmt.Errorf("读取 #%d 的父级/依赖关系失败：%w", sc.Source.Number, err)
		}
		return Build(sc.ID, []ghfacts.Issue{src}, map[int]ghfacts.Relationships{src.Number(): r})
	case "issue-tree":
		return nil, fmt.Errorf("issue-tree 遍历语义仍是开放决策（docs/architecture/data-model-and-qianshou-home.md），本切片不猜测")
	default:
		return nil, fmt.Errorf("未支持的 source.type：%q", sc.Source.Type)
	}
}
