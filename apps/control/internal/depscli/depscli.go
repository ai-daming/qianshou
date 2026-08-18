// Package depscli 实现 qianshou can-start：判断一个 Issue 现在能不能开工。
package depscli

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/ai-daming/qianshou/apps/control/internal/deps"
)

// Run 执行 `qianshou can-start <issue> [-R owner/repo]`。
func Run(args []string) error {
	flags := flag.NewFlagSet("can-start", flag.ContinueOnError)
	repo := flags.String("R", "ai-daming/qianshou", "仓库（owner/repo）")
	if err := flags.Parse(args); err != nil {
		return err
	}
	rest := flags.Args()
	if len(rest) != 1 {
		return fmt.Errorf("用法：qianshou can-start <issue 号> [-R owner/repo]")
	}
	var issue int
	if _, err := fmt.Sscanf(rest[0], "%d", &issue); err != nil || issue <= 0 {
		return fmt.Errorf("Issue 编号必须是正整数，当前为 %q", rest[0])
	}
	token, err := deps.ResolveToken(context.Background())
	if err != nil {
		return err
	}
	j, err := deps.Judge(context.Background(), token, *repo, issue)
	if err != nil {
		return err
	}
	if len(j.BlockedBy) == 0 {
		fmt.Printf("可以开工：%s#%d 没有未关闭的阻塞", *repo, issue)
		if len(j.Blockers) > 0 {
			fmt.Printf("（%d 个阻塞者均已关闭）", len(j.Blockers))
		}
		fmt.Println()
		return nil
	}
	fmt.Printf("不能开工：%s#%d 仍被阻塞：", *repo, issue)
	for _, n := range j.BlockedBy {
		fmt.Printf(" #%d(未关闭)", n)
	}
	fmt.Println()
	return nil
}

// Main 供 cmd 入口使用。
func Main() {
	if err := Run(os.Args[2:]); err != nil {
		fmt.Fprintln(os.Stderr, "can-start:", err)
		os.Exit(1)
	}
}
