package scope

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ai-daming/qianshou/apps/control/internal/classification"
	"github.com/ai-daming/qianshou/apps/control/internal/config"
	"github.com/ai-daming/qianshou/apps/control/internal/ghfacts"
	"github.com/ai-daming/qianshou/apps/control/internal/ghtoken"
)

// liveClient returns a GitHub client for real-data tests. Without local
// credentials (for example in CI) these tests skip: they verify live shape,
// while every fail-closed behavior is covered by the stubbed unit tests.
func liveClient(t *testing.T) *ghfacts.Client {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	token, err := ghtoken.Resolve(ctx)
	if err != nil {
		t.Skipf("本机无 GitHub 凭据，真实数据测试跳过（%v）", err)
	}
	client, err := ghfacts.New(token)
	if err != nil {
		t.Fatalf("ghfacts.New: %v", err)
	}
	return client
}

func findItem(t *testing.T, snap *Snapshot, number int) Item {
	t.Helper()
	for _, item := range snap.Items {
		if item.Number == number {
			return item
		}
	}
	t.Fatalf("快照缺少 #%d（实际成员：%v）", number, numbersOf(snap))
	return Item{}
}

func numbersOf(snap *Snapshot) []int {
	nums := make([]int, 0, len(snap.Items))
	for _, item := range snap.Items {
		nums = append(nums, item.Number)
	}
	return nums
}

func blockedByNumbers(item Item) []int {
	nums := make([]int, 0, len(item.BlockedBy))
	for _, ref := range item.BlockedBy {
		nums = append(nums, ref.Number)
	}
	return nums
}

func containsAll(haystack, needles []int) bool {
	set := make(map[int]bool, len(haystack))
	for _, n := range haystack {
		set[n] = true
	}
	for _, n := range needles {
		if !set[n] {
			return false
		}
	}
	return true
}

// AC-5: the qianshou repository itself, milestone M1, is the primary real
// fixture — this is the self-hosting read the whole slice exists for.
func TestQianshouM1LiveSnapshot(t *testing.T) {
	client := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	snap, err := FromMilestone(ctx, client, "ai-daming/qianshou", 1, "m1")
	if err != nil {
		t.Fatalf("FromMilestone(qianshou M1): %v", err)
	}
	if snap.Mode != ModeInitiative {
		t.Fatalf("mode = %q, want initiative（总控 #1 带 %s 标签）", snap.Mode, ControlIssueLabel)
	}
	if snap.ControlIssue != 1 {
		t.Fatalf("control issue = %d, want 1", snap.ControlIssue)
	}
	if !containsAll(numbersOf(snap), []int{1, 4, 29, 30, 31}) {
		t.Fatalf("M1 成员缺失关键 Issue：%v", numbersOf(snap))
	}

	// The M1-03 slice chain created on 2026-08-16 must be visible as native
	// Sub-issues of the control issue with native Blocked by order.
	for _, number := range []int{29, 30, 31} {
		item := findItem(t, snap, number)
		if item.Parent == nil || *item.Parent != 1 {
			t.Fatalf("#%d 的 parent = %v, want 1", number, item.Parent)
		}
	}
	a := findItem(t, snap, 29)
	b := findItem(t, snap, 30)
	c := findItem(t, snap, 31)
	if !containsAll(blockedByNumbers(b), []int{29}) {
		t.Fatalf("#30 blockedBy = %v, want 含 29", blockedByNumbers(b))
	}
	if !containsAll(blockedByNumbers(c), []int{30}) {
		t.Fatalf("#31 blockedBy = %v, want 含 30", blockedByNumbers(c))
	}
	if unsatisfied := b.UnsatisfiedDependencies(); len(unsatisfied) != 1 || unsatisfied[0] != 29 {
		t.Fatalf("#30 未满足依赖 = %v, want [29]（#29 OPEN）", unsatisfied)
	}
	if unsatisfied := a.UnsatisfiedDependencies(); len(unsatisfied) != 0 {
		t.Fatalf("#29 未满足依赖 = %v, want 空（#3、#2 均已关闭）", unsatisfied)
	}

	// Classification rides along from the M1-02b normalizer.
	if !a.Classification.Valid ||
		a.Classification.Classification.Workflow != classification.WorkflowDelivery ||
		a.Classification.Classification.Kind != classification.KindTechnical ||
		a.Classification.Classification.Rigor != classification.RigorStandard {
		t.Fatalf("#29 分类异常：%+v", a.Classification)
	}
	feature := findItem(t, snap, 4)
	if !feature.Classification.Valid || feature.Classification.Classification.Kind != classification.KindFeature {
		t.Fatalf("#4 分类异常：%+v", feature.Classification)
	}
}

// AC-5: mamamate M7 is the second real fixture — a different project, an
// initiative with the dependency chain documented in the architecture doc.
//
// Observed 2026-08-16: milestone 7 carries TWO type:milestone-control labels
// (#151, the documented Control Issue, and closed #228 "M7 合同基线"). The
// contract invariant is therefore asserted in both legal outcomes: consistent
// governance yields the healthy initiative snapshot; the current drift yields
// an explicit multi-control failure that names every candidate. The snapshot
// must never silently pick one of them.
func TestMamamateM7LiveSnapshot(t *testing.T) {
	client := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	snap, err := FromMilestone(ctx, client, "ai-daming/mamamate", 7, "m7")
	if err != nil {
		for _, number := range []int{151, 228} {
			if !strings.Contains(err.Error(), fmt.Sprintf("#%d", number)) {
				t.Fatalf("多总控漂移未点名 #%d：%v", number, err)
			}
		}
		return // drift surfaced, fail closed — resolves when mamamate fixes #228's label
	}
	if snap.Mode != ModeInitiative || snap.ControlIssue != 151 {
		t.Fatalf("mode = %q control = %d, want initiative/151", snap.Mode, snap.ControlIssue)
	}
	if !containsAll(numbersOf(snap), []int{151, 224, 19, 149, 152, 148}) {
		t.Fatalf("M7 成员缺失关键 Issue：%v", numbersOf(snap))
	}
	expect := map[int][]int{
		19:  {224},
		149: {19},
		152: {149},
		148: {149},
	}
	for number, want := range expect {
		item := findItem(t, snap, number)
		if !containsAll(blockedByNumbers(item), want) {
			t.Fatalf("mamamate #%d blockedBy = %v, want 含 %v", number, blockedByNumbers(item), want)
		}
	}
}

// AC-1/V0 迁移：真实 V0 配置必须迁移为合法 v1（mamamate Project + m7 Scope）。
func TestMigrateRealV0ConfigFile(t *testing.T) {
	path := filepath.Join("..", "..", "..", "..", "config", "projects.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("仓库中没有 V0 配置 %s：%v", path, err)
	}
	cfg, report, err := config.MigrateV0(data)
	if err != nil {
		t.Fatalf("MigrateV0(真实文件): %v", err)
	}
	if len(cfg.Projects) != 1 || cfg.Projects[0].ID != "mamamate" {
		t.Fatalf("迁移结果 Project 异常：%+v", cfg.Projects)
	}
	if len(cfg.Projects[0].Scopes) != 1 || cfg.Projects[0].Scopes[0].ID != "m7" {
		t.Fatalf("迁移结果 Scope 异常：%+v", cfg.Projects[0].Scopes)
	}
	if !report.DroppedEngineDefaults || !report.DroppedRefreshSeconds {
		t.Fatalf("迁移报告未记录丢弃项：%+v", report)
	}
}

// AC-6：本机自举配置存在时必须能被产品自身加载并校验。
func TestBootstrapHomeLoadsWhenPresent(t *testing.T) {
	home, err := config.DefaultHome()
	if err != nil {
		t.Skipf("无法解析 Qianshou home：%v", err)
	}
	if _, err := os.Stat(config.ConfigPath(home)); err != nil {
		t.Skipf("本机尚未落位自举配置（%s）：%v", config.ConfigPath(home), err)
	}
	cfg, err := config.Load(home)
	if err != nil {
		t.Fatalf("自举配置加载失败：%v", err)
	}
	for _, project := range cfg.Projects {
		if project.ID == "qianshou" {
			found := false
			for _, sc := range project.Scopes {
				if sc.ID == "m1" && sc.Source.Type == "milestone" && sc.Source.Number == 1 {
					found = true
				}
			}
			if !found {
				t.Fatalf("qianshou Project 缺少 m1 milestone Scope：%+v", project.Scopes)
			}
			return
		}
	}
	t.Fatalf("自举配置缺少 qianshou Project（现有：%v）", projectIDs(cfg))
}

func projectIDs(cfg *config.Config) []string {
	ids := make([]string, 0, len(cfg.Projects))
	for _, p := range cfg.Projects {
		ids = append(ids, p.ID)
	}
	return ids
}
