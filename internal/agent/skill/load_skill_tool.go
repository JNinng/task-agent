package skill

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
)

// LoadSkillTool implements tools.Tool to load skill content on demand.
type LoadSkillTool struct {
	loader *Loader
}

// NewLoadSkillTool creates a LoadSkillTool backed by the given loader.
func NewLoadSkillTool(loader *Loader) *LoadSkillTool {
	return &LoadSkillTool{loader: loader}
}

func (t *LoadSkillTool) Name() string { return "load_skill" }

func (t *LoadSkillTool) Description() string {
	return "Load a skill's full instructions by name. Use this when a task matches an available skill."
}

func (t *LoadSkillTool) InputSchema() anthropic.BetaToolInputSchemaParam {
	return anthropic.BetaToolInputSchemaParam{
		Properties: map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "The name of the skill to load",
			},
		},
		Required: []string{"name"},
	}
}

func (t *LoadSkillTool) Execute(ctx context.Context, input json.RawMessage) ([]anthropic.BetaToolResultBlockParamContentUnion, error) {
	var args struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return nil, fmt.Errorf("load_skill: %w", err)
	}
	content, err := t.loader.Get(args.Name)
	if err != nil {
		return nil, err
	}
	return []anthropic.BetaToolResultBlockParamContentUnion{
		{OfText: &anthropic.BetaTextBlockParam{Text: content}},
	}, nil
}
