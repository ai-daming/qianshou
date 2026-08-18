package deps

import (
	"context"
	"os"
	"os/exec"
)

// env.go 把环境访问隔离在一个文件里，方便测试不碰真实环境。
func osGetenv(key string) string               { return os.Getenv(key) }
func execLookPath(name string) (string, error) { return exec.LookPath(name) }
func execCommandContext(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
}
