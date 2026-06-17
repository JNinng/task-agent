package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
)

// CompactTool implements tools.Tool to manually trigger conversation compaction.
type CompactTool struct {
	trigger func() (string, error)
}

// NewCompactTool creates a CompactTool. The trigger function is called when the
// model invokes this tool — it should perform the compaction and return a
// human-readable result string.
func NewCompactTool(trigger func() (string, error)) *CompactTool {
	return &CompactTool{trigger: trigger}
}

func (t *CompactTool) Name() string { return "compact" }

func (t *CompactTool) Description() string {
	return "Manually compress the conversation to free context space. " +
		"Use when the agent seems to be losing track of earlier context " +
		"or when you hit context limitations. A full transcript is saved to disk."
}

func (t *CompactTool) InputSchema() anthropic.BetaToolInputSchemaParam {
	return anthropic.BetaToolInputSchemaParam{
		Properties: map[string]any{
			"reason": map[string]any{
				"type":        "string",
				"description": "Why you are compacting now",
			},
		},
		Required: []string{},
	}
}

func (t *CompactTool) Execute(ctx context.Context, input json.RawMessage) ([]anthropic.BetaToolResultBlockParamContentUnion, error) {
	if t.trigger == nil {
		return []anthropic.BetaToolResultBlockParamContentUnion{
			{OfText: &anthropic.BetaTextBlockParam{Text: "Error: compact not initialized"}},
		}, nil
	}
	result, err := t.trigger()
	if err != nil {
		return []anthropic.BetaToolResultBlockParamContentUnion{
			{OfText: &anthropic.BetaTextBlockParam{Text: fmt.Sprintf("Error: compact failed: %v", err)}},
		}, nil
	}
	return []anthropic.BetaToolResultBlockParamContentUnion{
		{OfText: &anthropic.BetaTextBlockParam{Text: result}},
	}, nil
}
