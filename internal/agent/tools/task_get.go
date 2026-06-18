package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	"task-agent/internal/agent/tasks"
)

// TaskGetTool wraps tasks.Manager.Get as a tool.
type TaskGetTool struct {
	Mgr *tasks.Manager
}

func (t *TaskGetTool) Name() string { return "task_get" }

func (t *TaskGetTool) Description() string {
	return "Get full details of a task by ID, including its dependency graph."
}

func (t *TaskGetTool) InputSchema() anthropic.BetaToolInputSchemaParam {
	return anthropic.BetaToolInputSchemaParam{
		Properties: map[string]any{
			"taskId": map[string]any{
				"type":        "string",
				"description": "The ID of the task to retrieve",
			},
		},
		Required: []string{"taskId"},
	}
}

func (t *TaskGetTool) Execute(ctx context.Context, input json.RawMessage) ([]anthropic.BetaToolResultBlockParamContentUnion, error) {
	var args struct {
		TaskID string `json:"taskId"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return nil, fmt.Errorf("task_get: %w", err)
	}

	task, err := t.Mgr.Get(args.TaskID)
	if err != nil {
		return nil, fmt.Errorf("task_get: %w", err)
	}
	if task == nil {
		return nil, fmt.Errorf("task %s not found", args.TaskID)
	}

	data, _ := json.MarshalIndent(task, "", "  ")
	text := fmt.Sprintf("Task #%s:\n%s", task.ID, string(data))
	return []anthropic.BetaToolResultBlockParamContentUnion{
		{OfText: &anthropic.BetaTextBlockParam{Text: text}},
	}, nil
}
