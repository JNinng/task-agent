package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/anthropics/anthropic-sdk-go"
)

// SubagentProgress is sent through the TUI event channel during subagent
// execution so the user can see which sub-task is running and its progress.
type SubagentProgress struct {
	Description string
	Turn        int
	MaxTurns    int
}

// eventChKey is the context key for the TUI event channel that subagent
// progress events are written to.
type eventChKey struct{}

// WithEventChannel returns a child context carrying the TUI event channel.
// SubagentTool.Execute extracts it via [EventChannelFrom] and sends
// [SubagentProgress] events during execution.
func WithEventChannel(ctx context.Context, ch chan<- any) context.Context {
	return context.WithValue(ctx, eventChKey{}, ch)
}

// EventChannelFrom extracts the TUI event channel from a context that was
// previously wrapped with [WithEventChannel]. Returns nil if not set.
func EventChannelFrom(ctx context.Context) chan<- any {
	ch, _ := ctx.Value(eventChKey{}).(chan<- any)
	return ch
}

// subagentMu serializes subagent execution so that multiple task tool calls
// from the same LLM response run one at a time rather than in parallel.
var subagentMu sync.Mutex

// maxSubagentTurns caps the subagent loop to prevent runaway sessions.
const maxSubagentTurns = 30

// SubagentTool implements the task tool that spawns a subagent with a fresh
// context, restricted tool set (no task / no todo), and a turn limit.
// Only the final text summary is returned — intermediate exploration is discarded.
type SubagentTool struct {
	client  *anthropic.Client
	model   anthropic.Model
	workdir string
}

// NewSubagentTool creates a SubagentTool that shares the parent agent's
// Anthropic client, model, and workspace root.
func NewSubagentTool(client *anthropic.Client, model anthropic.Model, workdir string) *SubagentTool {
	return &SubagentTool{client: client, model: model, workdir: workdir}
}

func (t *SubagentTool) Name() string { return "task" }

func (t *SubagentTool) Description() string {
	return "Launch a subagent to handle a complex, multi-step task. " +
		"The subagent has access to bash, read_file, write_file, and edit_file tools. " +
		"It works independently with a fresh context and returns a summary when done."
}

func (t *SubagentTool) InputSchema() anthropic.BetaToolInputSchemaParam {
	return anthropic.BetaToolInputSchemaParam{
		Properties: map[string]any{
			"prompt": map[string]any{
				"type":        "string",
				"description": "The task for the subagent to perform. Be specific about what you want done and what files or directories are involved.",
			},
			"description": map[string]any{
				"type":        "string",
				"description": "Short (3-5 word) description of the task for progress display.",
			},
		},
		Required: []string{"prompt"},
	}
}

// Execute runs the subagent loop. It acquires a global lock to guarantee
// serial execution of concurrent task calls, spawns a child agent loop with
// a restricted tool set, and returns the final text summary (or a partial
// summary if the turn limit is reached).
func (t *SubagentTool) Execute(ctx context.Context, input json.RawMessage) ([]anthropic.BetaToolResultBlockParamContentUnion, error) {
	var args struct {
		Prompt      string `json:"prompt"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return nil, fmt.Errorf("task: %w", err)
	}
	if args.Prompt == "" {
		return nil, fmt.Errorf("task: prompt is required")
	}

	// Serialize subagent execution.
	subagentMu.Lock()
	defer subagentMu.Unlock()

	// Child tool registry — no task (prevents recursion) and no todo
	// (subagent does not need its own task tracking).
	childRegistry := NewRegistry(
		BashTool{},
		&ReadFileTool{Workdir: t.workdir},
		&WriteFileTool{Workdir: t.workdir},
		&EditFileTool{Workdir: t.workdir},
	)

	// Subagent system prompt is minimal and action-oriented.
	system := []anthropic.BetaTextBlockParam{
		{Text: fmt.Sprintf(
			"You are a subagent. Complete the task and return a concise summary of what you did. "+
				"Work in %s. Use tools to solve the task. When done, write a final summary — do not ask follow-up questions.",
			t.workdir,
		)},
	}

	// Fresh message history — the subagent sees only its task prompt.
	messages := []anthropic.BetaMessageParam{
		anthropic.NewBetaUserMessage(
			anthropic.BetaContentBlockParamUnion{
				OfText: &anthropic.BetaTextBlockParam{Text: args.Prompt},
			}),
	}

	var lastText string

	for turn := 0; turn < maxSubagentTurns; turn++ {
		select {
		case <-ctx.Done():
			return textResult("(subagent: cancelled by user)"), nil
		default:
		}

		// Notify the TUI of subagent progress so the UI stays responsive.
		if ch := EventChannelFrom(ctx); ch != nil {
			desc := args.Description
			if desc == "" {
				desc = "(subagent)"
			}
			ch <- SubagentProgress{Description: desc, Turn: turn + 1, MaxTurns: maxSubagentTurns}
		}

		resp, err := t.client.Beta.Messages.New(ctx, anthropic.BetaMessageNewParams{
			Model:     t.model,
			System:    system,
			Messages:  messages,
			Tools:     childRegistry.ToParams(),
			MaxTokens: 8000,
		})
		if err != nil {
			return nil, fmt.Errorf("task: API call failed: %w", err)
		}

		messages = append(messages, resp.ToParam())

		var toolBlocks []ToolUseBlock
		for _, block := range resp.Content {
			if block.Type == "tool_use" {
				tu := block.AsToolUse()
				inputBytes, _ := json.Marshal(tu.Input)
				toolBlocks = append(toolBlocks, ToolUseBlock{
					ID:    tu.ID,
					Name:  tu.Name,
					Input: json.RawMessage(inputBytes),
				})
			} else if block.Type == "text" {
				t := block.AsText()
				lastText += t.Text
			}
		}

		if len(toolBlocks) == 0 {
			summary := strings.TrimSpace(lastText)
			if summary == "" {
				summary = "(subagent: no summary produced)"
			}
			if len(summary) > 50000 {
				summary = summary[:50000]
			}
			return textResult(summary), nil
		}

		// Reset accumulated text — only the final response's text matters.
		lastText = ""

		results, err := childRegistry.Dispatch(ctx, toolBlocks)
		if err != nil {
			return nil, fmt.Errorf("task: %w", err)
		}

		var contentBlocks []anthropic.BetaContentBlockParamUnion
		for _, result := range results {
			isError := len(result.Content) > 6 && result.Content[:6] == "Error:"
			contentBlocks = append(contentBlocks, anthropic.BetaContentBlockParamUnion{
				OfToolResult: &anthropic.BetaToolResultBlockParam{
					ToolUseID: result.ToolUseID,
					Content: []anthropic.BetaToolResultBlockParamContentUnion{
						{OfText: &anthropic.BetaTextBlockParam{Text: result.Content}},
					},
					IsError: anthropic.Bool(isError),
				},
			})
		}
		messages = append(messages, anthropic.NewBetaUserMessage(contentBlocks...))
	}

	return textResult("(subagent: 30-turn limit reached, returning partial results)"), nil
}

// textResult wraps a string as a single-element BetaToolResultBlockParamContentUnion slice.
func textResult(s string) []anthropic.BetaToolResultBlockParamContentUnion {
	return []anthropic.BetaToolResultBlockParamContentUnion{
		{OfText: &anthropic.BetaTextBlockParam{Text: s}},
	}
}
