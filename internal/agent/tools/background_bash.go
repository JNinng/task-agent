package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"task-agent/internal/agent/background"
)

// BackgroundBashTool launches a shell command in the background and returns
// immediately. The agent can continue working on other things while the
// command runs. Results are injected before the next LLM call via the
// notification queue or can be checked explicitly with check_background.
type BackgroundBashTool struct {
	Mgr *background.Manager
}

func (t *BackgroundBashTool) Name() string { return "background_bash" }

func (t *BackgroundBashTool) Description() string {
	return "Run a shell command in the background. Returns immediately with a task ID. " +
		"The command continues running while you do other work. " +
		"Use check_background to poll status. " +
		"Results are automatically sent to you when the command completes. " +
		"Use this for long-running commands (npm install, pytest, docker build, sleep). " +
		"For quick commands that need immediate output, use bash instead."
}

func (t *BackgroundBashTool) InputSchema() anthropic.BetaToolInputSchemaParam {
	return anthropic.BetaToolInputSchemaParam{
		Properties: map[string]any{
			"command": map[string]any{
				"type":        "string",
				"description": "The shell command to run in the background.",
			},
			"timeout": map[string]any{
				"type":        "number",
				"description": "Timeout in seconds (default 300 = 5 minutes, max 3600 = 1 hour).",
			},
		},
		Required: []string{"command"},
	}
}

func (t *BackgroundBashTool) Execute(_ context.Context, input json.RawMessage) ([]anthropic.BetaToolResultBlockParamContentUnion, error) {
	var args struct {
		Command string  `json:"command"`
		Timeout float64 `json:"timeout,omitempty"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return nil, fmt.Errorf("background_bash: %w", err)
	}
	if args.Command == "" {
		return nil, fmt.Errorf("background_bash: command is required")
	}

	timeout := time.Duration(0)
	if args.Timeout > 0 {
		timeout = time.Duration(args.Timeout) * time.Second
		if timeout > 3600*time.Second {
			timeout = 3600 * time.Second
		}
	}

	// Use context.Background() so the background process is not killed
	// when the tool execution context is cancelled after the tool returns.
	task := t.Mgr.Start(context.Background(), args.Command, timeout)

	result := fmt.Sprintf(
		"Background task %s started.\nCommand: %s\nStatus: running\nUse check_background to poll, or I'll notify you when it completes.",
		task.ID, task.Command)

	return []anthropic.BetaToolResultBlockParamContentUnion{
		{OfText: &anthropic.BetaTextBlockParam{Text: result}},
	}, nil
}
