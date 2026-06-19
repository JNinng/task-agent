package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"task-agent/internal/agent/background"
)

// CheckBackgroundTool lets the agent query the status of background tasks.
// Without a task_id it lists all tasks; with one it shows the full detail.
type CheckBackgroundTool struct {
	Mgr *background.Manager
}

func (t *CheckBackgroundTool) Name() string { return "check_background" }

func (t *CheckBackgroundTool) Description() string {
	return "Check the status of background tasks. " +
		"Without task_id, lists all background tasks. " +
		"With a task_id, shows the full detail including command and result."
}

func (t *CheckBackgroundTool) InputSchema() anthropic.BetaToolInputSchemaParam {
	return anthropic.BetaToolInputSchemaParam{
		Properties: map[string]any{
			"task_id": map[string]any{
				"type":        "string",
				"description": "Optional: specific task ID to check (e.g. 'bg-1'). Omit to list all tasks.",
			},
		},
	}
}

func (t *CheckBackgroundTool) Execute(_ context.Context, input json.RawMessage) ([]anthropic.BetaToolResultBlockParamContentUnion, error) {
	var args struct {
		TaskID string `json:"task_id,omitempty"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return nil, fmt.Errorf("check_background: %w", err)
	}

	if args.TaskID != "" {
		return t.showTask(args.TaskID)
	}
	return t.listAll()
}

func (t *CheckBackgroundTool) showTask(taskID string) ([]anthropic.BetaToolResultBlockParamContentUnion, error) {
	task := t.Mgr.Status(taskID)
	if task == nil {
		return textResult(fmt.Sprintf("Background task %s not found.", taskID)), nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Task: %s\n", task.ID)
	fmt.Fprintf(&b, "Command: %s\n", task.Command)
	fmt.Fprintf(&b, "Status: %s\n", task.Status)
	fmt.Fprintf(&b, "Started: %s\n", task.StartedAt.Format(time.RFC3339))

	if task.Status != background.StatusRunning {
		fmt.Fprintf(&b, "Finished: %s\n", task.DoneAt.Format(time.RFC3339))
	}

	if task.Error != "" {
		fmt.Fprintf(&b, "Error: %s\n", task.Error)
	}

	if task.Result != "" {
		const maxInline = 2000
		result := task.Result
		if len(result) > maxInline {
			result = result[:maxInline] + fmt.Sprintf("\n... (%d more bytes)", len(task.Result)-maxInline)
		}
		fmt.Fprintf(&b, "Result:\n%s\n", result)
	}

	return textResult(b.String()), nil
}

func (t *CheckBackgroundTool) listAll() ([]anthropic.BetaToolResultBlockParamContentUnion, error) {
	tasks := t.Mgr.List()
	if len(tasks) == 0 {
		return textResult("No background tasks."), nil
	}

	var b strings.Builder
	running := 0
	for _, task := range tasks {
		if task.Status == background.StatusRunning {
			running++
		}
		age := time.Since(task.StartedAt).Round(time.Second)
		fmt.Fprintf(&b, "  %s [%s] %s (%s)\n", task.ID, task.Status, task.Command, age)
	}

	header := fmt.Sprintf("Background tasks: %d total, %d running\n", len(tasks), running)
	return textResult(header + b.String()), nil
}
