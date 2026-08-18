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

	// 断言用关系不变量而非易变的在线真值：
	// BlockedBy 恰为 Blockers 中状态 OPEN 的那些；已知成员关系稳定。
	assertInvariant := func(t *testing.T, j Judgment) {
		t.Helper()
		open := map[int]bool{}
		for _, b := range j.Blockers {
			if b.State == "OPEN" {
				open[b.Number] = true
			}
		}
		if len(j.BlockedBy) != len(open) {
			t.Fatalf("BlockedBy %v 与 OPEN 阻塞者 %v 不一致", j.BlockedBy, open)
		}
		for _, n := range j.BlockedBy {
			if !open[n] {
				t.Fatalf("#%d 在 BlockedBy 里但不是 OPEN", n)
			}
		}
	}
	j, err := Judge(ctx, token, "ai-daming/qianshou", 30)
	if err != nil {
		t.Fatalf("判断 #30：%v", err)
	}
	assertInvariant(t, j)
	has29 := false
	for _, b := range j.Blockers {
		if b.Number == 29 {
			has29 = true
		}
	}
	if !has29 {
		t.Fatalf("#30 的阻塞者里没有 #29：%+v", j.Blockers)
	}

	j4, err := Judge(ctx, token, "ai-daming/qianshou", 4)
	if err != nil {
		t.Fatalf("判断 #4：%v", err)
	}
	assertInvariant(t, j4)
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
