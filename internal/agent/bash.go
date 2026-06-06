package agent

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// dangerousPatterns 定义危险命令模式，匹配的命令将被拦截。
var dangerousPatterns = []string{
	"rm -rf /",
	"sudo",
	"shutdown",
	"reboot",
	"> /dev/",
}

// runBash 在指定上下文中执行 shell 命令，返回执行输出。
// Windows 下使用 PowerShell，其他平台使用 bash。
// 输出超过 50000 字节会被截断。
func runBash(ctx context.Context, command string) string {
	// 安全检查：拦截危险命令
	for _, d := range dangerousPatterns {
		if strings.Contains(command, d) {
			return "Error: Dangerous command blocked"
		}
	}

	// 设置 120 秒超时
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()

	// 根据平台选择 shell
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "powershell", "-NoProfile", "-Command",
			"[Console]::OutputEncoding = [Text.Encoding]::UTF8; "+command)
	} else {
		cmd = exec.CommandContext(ctx, "bash", "-c", command)
	}

	// 合并 stdout 和 stderr
	out, err := cmd.CombinedOutput()
	if err != nil {
		out = append(out, []byte(fmt.Sprintf("\nError: %v", err))...)
	}

	result := strings.TrimSpace(string(out))
	if result == "" {
		return "(no output)"
	}
	if len(result) > 50000 {
		result = result[:50000]
	}
	return result
}
