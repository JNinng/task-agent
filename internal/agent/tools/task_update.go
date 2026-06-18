package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"task-agent/internal/agent/tasks"
)

// TaskUpdateTool wraps tasks.Manager.Update as a tool.
type TaskUpdateTool struct {
	Mgr *tasks.Manager
}

func (t *TaskUpdateTool) Name() string { return "task_update" }

func (t *TaskUpdateTool) Description() string {
	return "Update a task's status, owner, or dependencies. " +
		"When a task is marked completed, all dependent tasks are automatically unblocked. " +
		"Use addBlocks/addBlockedBy to create dependency edges."
}

func (t *TaskUpdateTool) InputSchema() anthropic.BetaToolInputSchemaParam {
	return anthropic.BetaToolInputSchemaParam{
		Properties: map[string]any{
			"taskId": map[string]any{
				"type":        "string",
				"description": "The ID of the task to update",
			},
			"status": map[string]any{
				"type":        "string",
				"enum":        []string{"pending", "in_progress", "completed"},
				"description": "New status",
			},
			"owner": map[string]any{
				"type":        "string",
				"description": "Agent name that owns this task (swarm mode)",
			},
			"addBlocks": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Task IDs that this task blocks (establishes dependency: this → those)",
			},
			"addBlockedBy": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Task IDs that block this task (establishes dependency: those → this)",
			},
			"metadata": map[string]any{
				"type":        "object",
				"description": "Arbitrary metadata to merge. Set a key to null to delete it.",
			},
		},
		Required: []string{"taskId"},
	}
}

func (t *TaskUpdateTool) Execute(ctx context.Context, input json.RawMessage) ([]anthropic.BetaToolResultBlockParamContentUnion, error) {
	var args struct {
		TaskID       string         `json:"taskId"`
		Status       *string        `json:"status"`
		Owner        *string        `json:"owner"`
		AddBlocks    []string       `json:"addBlocks"`
		AddBlockedBy []string       `json:"addBlockedBy"`
		Metadata     map[string]any `json:"metadata"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return nil, fmt.Errorf("task_update: %w", err)
	}

	upd := tasks.TaskUpdate{
		Status:       args.Status,
		Owner:        args.Owner,
		AddBlocks:    args.AddBlocks,
		AddBlockedBy: args.AddBlockedBy,
		Metadata:     args.Metadata,
	}

	task, err := t.Mgr.Update(args.TaskID, upd)
	if err != nil {
		return nil, fmt.Errorf("task_update: %w", err)
	}

	var parts []string
	parts = append(parts, fmt.Sprintf("Task #%s updated successfully.", task.ID))
	if args.Status != nil {
		parts = append(parts, fmt.Sprintf("Status: %s", task.Status))
	}
	if args.Owner != nil {
		parts = append(parts, fmt.Sprintf("Owner: %s", task.Owner))
	}
	if len(task.BlockedBy) > 0 {
		ids := make([]string, len(task.BlockedBy))
		for i, bid := range task.BlockedBy {
			ids[i] = "#" + bid
		}
		parts = append(parts, fmt.Sprintf("Blocked by: %s", strings.Join(ids, ", ")))
	}
	if len(args.AddBlocks) > 0 {
		ids := make([]string, len(args.AddBlocks))
		for i, bid := range args.AddBlocks {
			ids[i] = "#" + bid
		}
		parts = append(parts, fmt.Sprintf("Now blocks: %s", strings.Join(ids, ", ")))
	}

	return []anthropic.BetaToolResultBlockParamContentUnion{
		{OfText: &anthropic.BetaTextBlockParam{Text: strings.Join(parts, "\n")}},
	}, nil
}
