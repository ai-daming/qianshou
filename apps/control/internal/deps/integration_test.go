package deps

import (
	"context"
	"testing"
	"time"
)

// 真实数据验证：无凭据环境跳过（CI），本机跑真实判断。
func liveToken(t *testing.T) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	token, err := ResolveToken(ctx)
	if err != nil {
		t.Skipf("本机无 GitHub 凭据，真实数据测试跳过（%v）", err)
	}
	return token
}

func TestQianshouM1RealJudgments(t *testing.T) {
	token := liveToken(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// #30 被 #29 阻塞（#29 未关闭）
	j, err := Judge(ctx, token, "ai-daming/qianshou", 30)
	if err != nil {
		t.Fatalf("判断 #30：%v", err)
	}
	if len(j.BlockedBy) != 1 || j.BlockedBy[0] != 29 {
		t.Fatalf("#30 判定 BlockedBy = %v，want [29]", j.BlockedBy)
	}

	// #4 的阻塞者 #3、#2 均已关闭 → 可开工
	j4, err := Judge(ctx, token, "ai-daming/qianshou", 4)
	if err != nil {
		t.Fatalf("判断 #4：%v", err)
	}
	if len(j4.BlockedBy) != 0 {
		t.Fatalf("#4 判定 BlockedBy = %v，want 空", j4.BlockedBy)
	}
}

func TestMamamateM7RealJudgment(t *testing.T) {
	token := liveToken(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// M7 契约：#19 被 #224 阻塞
	j, err := Judge(ctx, token, "ai-daming/mamamate", 19)
	if err != nil {
		t.Fatalf("判断 mamamate#19：%v", err)
	}
	found := false
	for _, b := range j.Blockers {
		if b.Number == 224 {
			found = true
		}
	}
	if !found {
		t.Fatalf("mamamate#19 的阻塞者里没有 #224：%+v", j.Blockers)
	}
	// 结论一致性：BlockedBy 恰为 OPEN 的阻塞者
	for _, open := range j.BlockedBy {
		state := ""
		for _, b := range j.Blockers {
			if b.Number == open {
				state = b.State
			}
		}
		if state != "OPEN" {
			t.Fatalf("BlockedBy 里的 #%d 状态是 %q，不是 OPEN", open, state)
		}
	}
}
