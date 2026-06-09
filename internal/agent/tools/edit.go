package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
)

// EditFileTool replaces exact text in a file.
type EditFileTool struct {
	Workdir string
}

func (EditFileTool) Name() string        { return "edit_file" }
func (EditFileTool) Description() string { return "Replace exact text in file." }

func (EditFileTool) InputSchema() anthropic.BetaToolInputSchemaParam {
	return anthropic.BetaToolInputSchemaParam{
		Properties: map[string]any{
			"path":     map[string]any{"type": "string"},
			"old_text": map[string]any{"type": "string"},
			"new_text": map[string]any{"type": "string"},
		},
		Required: []string{"path", "old_text", "new_text"},
	}
}

func (t EditFileTool) Execute(ctx context.Context, input json.RawMessage) ([]anthropic.BetaToolResultBlockParamContentUnion, error) {
	var args struct {
		Path    string `json:"path"`
		OldText string `json:"old_text"`
		NewText string `json:"new_text"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return nil, fmt.Errorf("edit_file: %w", err)
	}
	result, err := t.edit(args.Path, args.OldText, args.NewText)
	if err != nil {
		return nil, err
	}
	return []anthropic.BetaToolResultBlockParamContentUnion{
		{OfText: &anthropic.BetaTextBlockParam{Text: result}},
	}, nil
}

func (t EditFileTool) edit(path, oldText, newText string) (string, error) {
	p, err := safePath(t.Workdir, path)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return "", fmt.Errorf("edit_file: %w", err)
	}
	content := string(data)
	if !strings.Contains(content, oldText) {
		return "", fmt.Errorf("edit_file: text not found in %s", path)
	}
	replaced := strings.Replace(content, oldText, newText, 1)
	if err := os.WriteFile(p, []byte(replaced), 0644); err != nil {
		return "", fmt.Errorf("edit_file: %w", err)
	}
	return fmt.Sprintf("Edited %s", path), nil
}
