package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	anthropic "github.com/anthropics/anthropic-sdk-go"
)

// ReadFileTool reads file contents.
type ReadFileTool struct {
	Workdir string
}

func (ReadFileTool) Name() string        { return "read_file" }
func (ReadFileTool) Description() string { return "Read file contents." }

func (ReadFileTool) InputSchema() anthropic.BetaToolInputSchemaParam {
	return anthropic.BetaToolInputSchemaParam{
		Properties: map[string]any{
			"path":  map[string]any{"type": "string"},
			"limit": map[string]any{"type": "integer"},
		},
		Required: []string{"path"},
	}
}

func (t ReadFileTool) Execute(ctx context.Context, input json.RawMessage) ([]anthropic.BetaToolResultBlockParamContentUnion, error) {
	var args struct {
		Path  string `json:"path"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return nil, fmt.Errorf("read_file: %w", err)
	}
	output, err := t.read(args.Path, args.Limit)
	if err != nil {
		return nil, err
	}
	return []anthropic.BetaToolResultBlockParamContentUnion{
		{OfText: &anthropic.BetaTextBlockParam{Text: output}},
	}, nil
}

func (t ReadFileTool) read(path string, limit int) (string, error) {
	p, err := safePath(t.Workdir, path)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return "", fmt.Errorf("read_file: %w", err)
	}
	lines := strings.Split(string(data), "\n")
	if limit > 0 && limit < len(lines) {
		lines = append(lines[:limit], fmt.Sprintf("... (%d more lines)", len(lines)-limit))
	}
	result := strings.Join(lines, "\n")
	if len(result) > 50000 {
		result = result[:50000]
	}
	return result, nil
}

// safePath resolves and validates that the path stays within workdir.
func safePath(workdir, p string) (string, error) {
	absPath, err := filepath.Abs(filepath.Join(workdir, p))
	if err != nil {
		return "", fmt.Errorf("path resolution: %w", err)
	}
	realPath, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		realPath = absPath
	}
	realWorkdir, _ := filepath.EvalSymlinks(workdir)
	if !strings.HasPrefix(realPath, realWorkdir+string(os.PathSeparator)) && realPath != realWorkdir {
		return "", fmt.Errorf("path escapes workspace: %s", p)
	}
	return absPath, nil
}
