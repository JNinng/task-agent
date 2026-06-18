package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	"task-agent/internal/agent/tasks"
)

// TaskCreateTool wraps tasks.Manager.Create as a tool.
type TaskCreateTool struct {
	Mgr *tasks.Manager
}

func (t *TaskCreateTool) Name() string { return "task_create" }

func (t *TaskCreateTool) Description() string {
	return "Create a new task in the persistent task graph. " +
		"Use for multi-step work that needs dependency tracking. " +
		"After creating tasks, use task_update to add dependencies."
}

func (t *TaskCreateTool) InputSchema() anthropic.BetaToolInputSchemaParam {
	return anthropic.BetaToolInputSchemaParam{
		Properties: map[string]any{
			"subject": map[string]any{
				"type":        "string",
				"description": "Task title in imperative form (e.g., 'Run tests')",
			},
			"description": map[string]any{
				"type":        "string",
				"description": "What needs to be done",
			},
			"activeForm": map[string]any{
				"type":        "string",
				"description": "Present continuous form shown in progress UI (e.g., 'Running tests')",
			},
			"metadata": map[string]any{
				"type":        "object",
				"description": "Arbitrary metadata to attach to the task",
			},
		},
		Required: []string{"subject", "description"},
	}
}

func (t *TaskCreateTool) Execute(ctx context.Context, input json.RawMessage) ([]anthropic.BetaToolResultBlockParamContentUnion, error) {
	var args struct {
		Subject     string         `json:"subject"`
		Description string         `json:"description"`
		ActiveForm  string         `json:"activeForm"`
		Metadata    map[string]any `json:"metadata"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return nil, fmt.Errorf("task_create: %w", err)
	}

	task, err := t.Mgr.Create(args.Subject, args.Description)
	if err != nil {
		return nil, fmt.Errorf("task_create: %w", err)
	}

	// Apply optional fields after creation.
	if args.ActiveForm != "" || args.Metadata != nil {
		upd := tasks.TaskUpdate{}
		if args.ActiveForm != "" {
			upd.ActiveForm = &args.ActiveForm
		}
		if args.Metadata != nil {
			upd.Metadata = args.Metadata
		}
		task, err = t.Mgr.Update(task.ID, upd)
		if err != nil {
			return nil, fmt.Errorf("task_create metadata: %w", err)
		}
	}

	text := fmt.Sprintf("Task #%s created successfully: %s", task.ID, task.Subject)
	return []anthropic.BetaToolResultBlockParamContentUnion{
		{OfText: &anthropic.BetaTextBlockParam{Text: text}},
	}, nil
}
