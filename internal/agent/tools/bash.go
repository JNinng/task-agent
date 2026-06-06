package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"

	anthropic "github.com/anthropics/anthropic-sdk-go"
)

var dangerousPatterns = []string{
	"rm -rf /",
	"sudo",
	"shutdown",
	"reboot",
	"> /dev/",
}

// BashTool executes shell commands.
type BashTool struct{}

func (BashTool) Name() string        { return "bash" }
func (BashTool) Description() string { return "Run a shell command." }

func (BashTool) InputSchema() anthropic.BetaToolInputSchemaParam {
	return anthropic.BetaToolInputSchemaParam{
		Properties: map[string]any{
			"command": map[string]any{"type": "string"},
		},
		Required: []string{"command"},
	}
}

func (BashTool) Execute(ctx context.Context, input json.RawMessage) ([]anthropic.BetaToolResultBlockParamContentUnion, error) {
	var args struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return nil, fmt.Errorf("bash: %w", err)
	}
	output := runBash(ctx, args.Command)
	return []anthropic.BetaToolResultBlockParamContentUnion{
		{OfText: &anthropic.BetaTextBlockParam{Text: output}},
	}, nil
}

func runBash(ctx context.Context, command string) string {
	for _, d := range dangerousPatterns {
		if strings.Contains(command, d) {
			return "Error: Dangerous command blocked"
		}
	}

	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "powershell", "-NoProfile", "-Command",
			"[Console]::OutputEncoding = [Text.Encoding]::UTF8; "+command)
	} else {
		cmd = exec.CommandContext(ctx, "bash", "-c", command)
	}

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
