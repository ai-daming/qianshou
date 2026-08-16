// Package configcli implements the `qianshou config` subcommands that
// bootstrap and verify the machine-local configuration home.
package configcli

import (
	"flag"
	"fmt"
	"os"

	"github.com/ai-daming/qianshou/apps/control/internal/config"
)

// Run dispatches `qianshou config <check|migrate>` and returns an error the
// caller turns into a non-zero exit.
func Run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("config 需要子命令：check 或 migrate")
	}
	switch args[0] {
	case "check":
		return runCheck(args[1:])
	case "migrate":
		return runMigrate(args[1:])
	default:
		return fmt.Errorf("未知的 config 子命令：%s（可用：check、migrate）", args[0])
	}
}

func commonFlags(name string) (*flag.FlagSet, *string, *bool) {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	home := flags.String("home", "", "Qianshou home 目录（默认 QIANSHOU_HOME 或 ~/.qianshou）")
	skipGit := flags.Bool("skip-git-binding", false, "跳过本地 checkout 与 GitHub 仓库一致性校验")
	return flags, home, skipGit
}

func resolveHome(home string) (string, error) {
	if home != "" {
		return home, nil
	}
	return config.DefaultHome()
}

func runCheck(args []string) error {
	flags, homeFlag, skipGit := commonFlags("check")
	if err := flags.Parse(args); err != nil {
		return err
	}
	home, err := resolveHome(*homeFlag)
	if err != nil {
		return err
	}
	cfg, err := config.Load(home)
	if err != nil {
		return err
	}
	if *skipGit {
		return nil
	}
	for _, project := range cfg.Projects {
		if err := config.VerifyGitBinding(project.Local.Path, project.Repository.Slug); err != nil {
			return err
		}
	}
	fmt.Printf("配置有效：%s（%d 个 Project，%d 个 Engine）\n", config.ConfigPath(home), len(cfg.Projects), len(cfg.Engines))
	return nil
}

func runMigrate(args []string) error {
	flags := flag.NewFlagSet("migrate", flag.ContinueOnError)
	source := flags.String("source", "config/projects.json", "V0 配置文件路径")
	homeFlag := flags.String("home", "", "Qianshou home 目录（默认 QIANSHOU_HOME 或 ~/.qianshou）")
	force := flags.Bool("force", false, "覆盖已存在的目标配置")
	if err := flags.Parse(args); err != nil {
		return err
	}
	home, err := resolveHome(*homeFlag)
	if err != nil {
		return err
	}
	target := config.ConfigPath(home)
	if _, err := os.Stat(target); err == nil && !*force {
		return fmt.Errorf("目标配置已存在：%s（确认覆盖请加 --force）", target)
	}
	data, err := os.ReadFile(*source)
	if err != nil {
		return fmt.Errorf("读取 V0 配置失败：%w", err)
	}
	cfg, report, err := config.MigrateV0(data)
	if err != nil {
		return err
	}
	if err := config.Save(home, cfg); err != nil {
		return err
	}
	fmt.Printf("迁移完成：%s\n", target)
	fmt.Printf("  Projects: %v\n", report.Projects)
	fmt.Printf("  Scopes:   %v\n", report.Scopes)
	if report.DroppedRefreshSeconds {
		fmt.Println("  已丢弃 refreshSeconds（刷新策略属开放决策，未纳入 v1）")
	}
	if note := report.EngineDefaultsNote(); note != "" {
		fmt.Printf("  已丢弃 %s\n", note)
	}
	return nil
}
