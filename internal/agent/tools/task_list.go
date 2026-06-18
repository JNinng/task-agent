package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"task-agent/internal/agent/tasks"
)

// TaskListTool wraps tasks.Manager.List as a tool.
type TaskListTool struct {
	Mgr *tasks.Manager
}

func (t *TaskListTool) Name() string { return "task_list" }

func (t *TaskListTool) Description() string {
	return "List all tasks in the persistent task graph. Shows status, owner, and active blockers."
}

func (t *TaskListTool) InputSchema() anthropic.BetaToolInputSchemaParam {
	return anthropic.BetaToolInputSchemaParam{
		Properties: map[string]any{
			"status": map[string]any{
				"type":        "string",
				"enum":        []string{"pending", "in_progress", "completed"},
				"description": "Filter by status. Omit to show all.",
			},
		},
	}
}

func (t *TaskListTool) Execute(ctx context.Context, input json.RawMessage) ([]anthropic.BetaToolResultBlockParamContentUnion, error) {
	var args struct {
		Status string `json:"status"`
	}
	// Input is optional — empty input is fine.
	if len(input) > 0 {
		if err := json.Unmarshal(input, &args); err != nil {
			return nil, fmt.Errorf("task_list: %w", err)
		}
	}

	filter := tasks.ListFilter{ExcludeDeleted: true}
	if args.Status != "" {
		filter.Status = args.Status
	}

	list, err := t.Mgr.List(filter)
	if err != nil {
		return nil, fmt.Errorf("task_list: %w", err)
	}

	if len(list) == 0 {
		text := "No tasks."
		if args.Status != "" {
			text = fmt.Sprintf("No tasks with status %q.", args.Status)
		}
		return []anthropic.BetaToolResultBlockParamContentUnion{
			{OfText: &anthropic.BetaTextBlockParam{Text: text}},
		}, nil
	}

	markers := map[string]string{
		"pending":     "[ ]",
		"in_progress": "[>]",
		"completed":   "[x]",
	}

	lines := make([]string, 0, len(list)+2)
	done := 0
	for _, t := range list {
		m := markers[t.Status]
		line := fmt.Sprintf("%s #%s: %s", m, t.ID, t.Subject)
		if t.Owner != "" {
			line += fmt.Sprintf(" (%s)", t.Owner)
		}
		if len(t.BlockedBy) > 0 {
			ids := make([]string, len(t.BlockedBy))
			for i, bid := range t.BlockedBy {
				ids[i] = "#" + bid
			}
			line += fmt.Sprintf(" [blocked by %s]", strings.Join(ids, ", "))
		}
		if t.ActiveForm != "" && t.Status == "in_progress" {
			line += fmt.Sprintf(" — %s", t.ActiveForm)
		}
		lines = append(lines, line)
		if t.Status == "completed" {
			done++
		}
	}

	// Summary footer.
	lines = append(lines, "")
	var parts []string
	for _, s := range []string{"pending", "in_progress", "completed"} {
		n := countStatus(list, s)
		if n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, s))
		}
	}
	lines = append(lines, fmt.Sprintf("(%s)", strings.Join(parts, ", ")))

	return []anthropic.BetaToolResultBlockParamContentUnion{
		{OfText: &anthropic.BetaTextBlockParam{Text: strings.Join(lines, "\n")}},
	}, nil
}

func countStatus(tasks []tasks.Task, status string) int {
	n := 0
	for _, t := range tasks {
		if t.Status == status {
			n++
		}
	}
	return n
}

// idToInt parses a numeric task ID string.
func idToInt(id string) int {
	n, _ := strconv.Atoi(id)
	return n
}
