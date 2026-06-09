package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	anthropic "github.com/anthropics/anthropic-sdk-go"
)

// TodoItem represents a single todo entry.
type TodoItem struct {
	ID     string `json:"id"`
	Text   string `json:"text"`
	Status string `json:"status"`
}

// TodoWriteTool tracks todo items for multi-step task planning.
// It is stateful — items persist across tool calls within a session.
type TodoWriteTool struct {
	Items []TodoItem
}

func (t *TodoWriteTool) Name() string { return "todo" }

func (t *TodoWriteTool) Description() string {
	return "Update the in-memory task list. Only send items that changed — unchanged items are kept. State is managed automatically, no files to explore."
}

func (t *TodoWriteTool) InputSchema() anthropic.BetaToolInputSchemaParam {
	return anthropic.BetaToolInputSchemaParam{
		Properties: map[string]any{
			"items": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"id":     map[string]any{"type": "string"},
						"text":   map[string]any{"type": "string"},
						"status": map[string]any{"type": "string", "enum": []string{"pending", "in_progress", "completed"}},
					},
					"required": []string{"id", "text", "status"},
				},
			},
		},
		Required: []string{"items"},
	}
}

func (t *TodoWriteTool) Execute(ctx context.Context, input json.RawMessage) ([]anthropic.BetaToolResultBlockParamContentUnion, error) {
	var args struct {
		Items []struct {
			ID     string `json:"id"`
			Text   string `json:"text"`
			Status string `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return nil, fmt.Errorf("todo: %w", err)
	}

	// Build lookup of existing items.
	existing := make(map[string]TodoItem, len(t.Items))
	order := make([]string, 0, len(t.Items))
	for _, item := range t.Items {
		existing[item.ID] = item
		order = append(order, item.ID)
	}

	// Merge incoming items: validate and upsert.
	for i, item := range args.Items {
		id := item.ID
		if id == "" {
			id = fmt.Sprintf("%d", i+1)
		}
		text := strings.TrimSpace(item.Text)
		if text == "" {
			return nil, fmt.Errorf("Item %s: text required", id)
		}
		status := strings.ToLower(item.Status)
		if status == "" {
			status = "pending"
		}
		switch status {
		case "pending", "in_progress", "completed":
		default:
			return nil, fmt.Errorf("Item %s: invalid status '%s'", id, item.Status)
		}
		if _, exists := existing[id]; !exists {
			order = append(order, id)
		}
		existing[id] = TodoItem{ID: id, Text: text, Status: status}
	}

	// Build merged slice in preserved order.
	merged := make([]TodoItem, 0, len(existing))
	inProgressCount := 0
	for _, id := range order {
		item := existing[id]
		if item.Status == "in_progress" {
			inProgressCount++
		}
		merged = append(merged, item)
	}

	if len(merged) > 20 {
		return nil, fmt.Errorf("Max 20 todos allowed")
	}
	if inProgressCount > 1 {
		return nil, fmt.Errorf("Only one task can be in_progress at a time")
	}

	t.Items = merged
	return []anthropic.BetaToolResultBlockParamContentUnion{
		{OfText: &anthropic.BetaTextBlockParam{Text: t.Render()}},
	}, nil
}

// Render returns the formatted todo list string.
func (t *TodoWriteTool) Render() string {
	if len(t.Items) == 0 {
		return "No todos."
	}
	markers := map[string]string{
		"pending":     "[ ]",
		"in_progress": "[>]",
		"completed":   "[x]",
	}
	lines := make([]string, 0, len(t.Items)+2)
	done := 0
	for _, item := range t.Items {
		m := markers[item.Status]
		lines = append(lines, fmt.Sprintf("%s #%s: %s", m, item.ID, item.Text))
		if item.Status == "completed" {
			done++
		}
	}
	lines = append(lines, fmt.Sprintf("\n(%d/%d completed)", done, len(t.Items)))
	return strings.Join(lines, "\n")
}
