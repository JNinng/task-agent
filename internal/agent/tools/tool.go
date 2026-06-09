package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	"golang.org/x/sync/errgroup"
)

// Tool is an alias for the SDK's BetaTool interface.
type Tool = anthropic.BetaTool

// ToolUseBlock carries the info extracted from a BetaContentBlockUnion tool_use.
type ToolUseBlock struct {
	ID    string
	Name  string
	Input json.RawMessage
}

// ToolResult pairs a tool_use ID with its output content.
type ToolResult struct {
	ToolUseID string
	Name      string
	Content   string
}

// Registry maps tool names to Tool implementations.
type Registry struct {
	tools map[string]Tool
}

// NewRegistry creates a Registry and builds the dispatch map.
func NewRegistry(tools ...Tool) *Registry {
	m := make(map[string]Tool, len(tools))
	for _, t := range tools {
		m[t.Name()] = t
	}
	return &Registry{tools: m}
}

// Dispatch executes all tool_use blocks in parallel via errgroup.
// A tool error becomes an error result string — it does not abort sibling tools.
func (r *Registry) Dispatch(ctx context.Context, blocks []ToolUseBlock) ([]ToolResult, error) {
	g, gctx := errgroup.WithContext(ctx)
	results := make([]ToolResult, len(blocks))

	for i, block := range blocks {
		i, block := i, block
		g.Go(func() error {
			tool, ok := r.tools[block.Name]
			if !ok {
				results[i] = ToolResult{
					ToolUseID: block.ID,
					Name:      block.Name,
					Content:   fmt.Sprintf("Error: Tool '%s' not found", block.Name),
				}
				return nil
			}
			content, err := tool.Execute(gctx, block.Input)
			if err != nil {
				results[i] = ToolResult{
					ToolUseID: block.ID,
					Name:      block.Name,
					Content:   fmt.Sprintf("Error: %v", err),
				}
				return nil
			}
			results[i] = ToolResult{
				ToolUseID: block.ID,
				Name:      block.Name,
				Content:   contentBlockToString(content),
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return results, nil
}

// Tool returns the tool registered under the given name, or nil.
func (r *Registry) Tool(name string) Tool {
	return r.tools[name]
}

// ToParams converts registered tools to BetaToolUnionParam slice for the API.
func (r *Registry) ToParams() []anthropic.BetaToolUnionParam {
	params := make([]anthropic.BetaToolUnionParam, 0, len(r.tools))
	for _, t := range r.tools {
		params = append(params, anthropic.BetaToolUnionParam{
			OfTool: &anthropic.BetaToolParam{
				Name:        t.Name(),
				Description: anthropic.String(t.Description()),
				InputSchema: t.InputSchema(),
			},
		})
	}
	return params
}

// contentBlockToString extracts text from BetaToolResultBlockParamContentUnion slice.
func contentBlockToString(content []anthropic.BetaToolResultBlockParamContentUnion) string {
	var texts []string
	for _, c := range content {
		if c.OfText != nil {
			texts = append(texts, c.OfText.Text)
		}
	}
	if len(texts) == 0 {
		return "(no output)"
	}
	result := ""
	for i, t := range texts {
		if i > 0 {
			result += "\n"
		}
		result += t
	}
	return result
}
