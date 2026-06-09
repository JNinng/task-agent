package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/anthropics/anthropic-sdk-go"
)

// WriteFileTool writes content to a file.
type WriteFileTool struct {
	Workdir string
}

func (WriteFileTool) Name() string        { return "write_file" }
func (WriteFileTool) Description() string { return "Write content to file." }

func (WriteFileTool) InputSchema() anthropic.BetaToolInputSchemaParam {
	return anthropic.BetaToolInputSchemaParam{
		Properties: map[string]any{
			"path":    map[string]any{"type": "string"},
			"content": map[string]any{"type": "string"},
		},
		Required: []string{"path", "content"},
	}
}

func (t WriteFileTool) Execute(ctx context.Context, input json.RawMessage) ([]anthropic.BetaToolResultBlockParamContentUnion, error) {
	var args struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return nil, fmt.Errorf("write_file: %w", err)
	}
	result, err := t.write(args.Path, args.Content)
	if err != nil {
		return nil, err
	}
	return []anthropic.BetaToolResultBlockParamContentUnion{
		{OfText: &anthropic.BetaTextBlockParam{Text: result}},
	}, nil
}

func (t WriteFileTool) write(path, content string) (string, error) {
	p, err := safePath(t.Workdir, path)
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(p)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("write_file: %w", err)
	}
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		return "", fmt.Errorf("write_file: %w", err)
	}
	return fmt.Sprintf("Wrote %d bytes to %s", len(content), path), nil
}
