// Package depscli 实现 qianshou can-start：判断一个 Issue 现在能不能开工。
// 退出码：0 = 可开工，1 = 被阻塞，2 = 判断失败。
package depscli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"

	"github.com/ai-daming/qianshou/apps/control/internal/deps"
)

// Run 执行 `qianshou can-start -R owner/repo <issue>`，返回是否被阻塞。
func Run(args []string) (blocked bool, err error) {
	flags := flag.NewFlagSet("can-start", flag.ContinueOnError)
	repo := flags.String("R", "ai-daming/qianshou", "仓库（owner/repo），须写在 issue 号之前")
	if err := flags.Parse(args); err != nil {
		return false, err
	}
	rest := flags.Args()
	if len(rest) != 1 {
		return false, fmt.Errorf("用法：qianshou can-start -R owner/repo <issue 号>")
	}
	issue, err := strconv.Atoi(rest[0])
	if err != nil || issue <= 0 {
		return false, fmt.Errorf("Issue 编号必须是正整数，当前为 %q", rest[0])
	}
	token, err := deps.ResolveToken(context.Background())
	if err != nil {
		return false, err
	}
	j, err := deps.Judge(context.Background(), token, *repo, issue)
	if err != nil {
		return false, err
	}
	if len(j.BlockedBy) == 0 {
		fmt.Printf("可以开工：%s#%d 没有未关闭的阻塞", *repo, issue)
		if len(j.Blockers) > 0 {
			fmt.Printf("（%d 个阻塞者均已关闭）", len(j.Blockers))
		}
		fmt.Println()
		return false, nil
	}
	fmt.Printf("不能开工：%s#%d 仍被阻塞：", *repo, issue)
	for _, n := range j.BlockedBy {
		fmt.Printf(" #%d(未关闭)", n)
	}
	fmt.Println()
	return true, nil
}

// Main 供 cmd 入口使用。
func Main() {
	blocked, err := Run(os.Args[2:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "can-start:", err)
		os.Exit(2)
	}
	if blocked {
		os.Exit(1)
	}
}
