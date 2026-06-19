package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"task-agent/internal/agent/tools"
)

type Runner struct {
	agent           *Agent
	messages        []anthropic.BetaMessageParam
	roundsSinceTodo int
	compactCfg      CompactionConfig
	compacted       bool // set when autoCompact replaces messages; skips tool_result appending
}

func NewRunner(ag *Agent, cfg CompactionConfig, setCompact func(func() (string, error))) *Runner {
	r := &Runner{agent: ag, compactCfg: cfg}
	setCompact(r.compact)
	return r
}

// Messages returns a copy of the current message history for inspection.
func (r *Runner) Messages() []anthropic.BetaMessageParam {
	cp := make([]anthropic.BetaMessageParam, len(r.messages))
	copy(cp, r.messages)
	return cp
}

// compact wraps autoCompact for the compact tool callback.
// It returns a user-facing result string.
func (r *Runner) compact() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := r.autoCompact(ctx); err != nil {
		return "", err
	}
	r.compacted = true
	return "Conversation compacted successfully. Full transcript saved to disk.", nil
}

func (r *Runner) Run(ctx context.Context, input string) <-chan any {
	ch := make(chan any, 10)
	go func() {
		defer close(ch)
		r.runLoop(ctx, input, ch)
	}()
	return ch
}

func (r *Runner) runLoop(ctx context.Context, input string, ch chan<- any) {
	r.messages = append(r.messages, anthropic.NewBetaUserMessage(
		anthropic.BetaContentBlockParamUnion{
			OfText: &anthropic.BetaTextBlockParam{Text: input},
		}))

	ch <- EventThinking{}

	for {
		// Layer 1: micro_compact — replace old tool_results with placeholders
		microCompact(r.messages, r.compactCfg.MicroKeepRecent)

		resp, err := r.agent.client.Beta.Messages.New(ctx, anthropic.BetaMessageNewParams{
			Model:     r.agent.model,
			System:    r.agent.system,
			Messages:  r.messages,
			Tools:     r.agent.registry.ToParams(),
			MaxTokens: 8000,
		})
		if err != nil {
			ch <- EventError{Err: err}
			return
		}

		// Layer 2: auto_compact — check token threshold after API response
		if r.compactCfg.AutoThreshold > 0 && resp.Usage.InputTokens > int64(r.compactCfg.AutoThreshold) {
			// Append the response so the compressed summary includes this turn
			r.messages = append(r.messages, resp.ToParam())
			ch <- EventText{Content: fmt.Sprintf(
				"[context: %d tokens — auto-compacting]", int64(r.compactCfg.AutoThreshold))}
			if err := r.autoCompact(ctx); err != nil {
				ch <- EventText{Content: fmt.Sprintf("[compact warning: %v]", err)}
			} else {
				ch <- EventText{Content: "[auto-compact done]"}
			}
			ch <- EventThinking{}
			continue
		}

		r.messages = append(r.messages, resp.ToParam())

		var toolBlocks []tools.ToolUseBlock
		for _, block := range resp.Content {
			if block.Type == "tool_use" {
				tu := block.AsToolUse()
				inputBytes, _ := json.Marshal(tu.Input)
				toolBlocks = append(toolBlocks, tools.ToolUseBlock{
					ID:    tu.ID,
					Name:  tu.Name,
					Input: json.RawMessage(inputBytes),
				})
			} else if block.Type == "text" {
				t := block.AsText()
				ch <- EventText{Content: t.Text}
			}
		}

		if len(toolBlocks) == 0 {
			if r.hasIncompleteTodos() {
				r.roundsSinceTodo++
			}
			if r.hasIncompleteTodos() && r.roundsSinceTodo >= 3 {
				r.roundsSinceTodo = 0
				r.messages = append(r.messages, anthropic.NewBetaUserMessage(
					anthropic.BetaContentBlockParamUnion{
						OfText: &anthropic.BetaTextBlockParam{
							Text: "<reminder>Update your todo list or task graph.</reminder>",
						},
					}))
				continue
			}
			ch <- EventDone{}
			return
		}

		ch <- EventToolCalls{Tools: toolBlocks}

		// Emit subagent progress events for task tool calls so the TUI
		// can show which sub-task is currently running, and wire the
		// event channel through context so the subagent can send
		// turn-by-turn progress while it executes.
		for _, tb := range toolBlocks {
			if tb.Name == "task" {
				var taskArgs struct {
					Description string `json:"description"`
				}
				json.Unmarshal(tb.Input, &taskArgs)
				desc := taskArgs.Description
				if desc == "" {
					var taskArgs2 struct {
						Prompt string `json:"prompt"`
					}
					json.Unmarshal(tb.Input, &taskArgs2)
					desc = taskArgs2.Prompt
					if len(desc) > 60 {
						desc = desc[:60] + "..."
					}
				}
				ch <- tools.SubagentProgress{Description: desc, Turn: 0, MaxTurns: 30}
			}
		}

		taskCtx := ctx
		if hasTaskBlock(toolBlocks) {
			taskCtx = tools.WithEventChannel(ctx, ch)
		}
		results, err := r.agent.registry.Dispatch(taskCtx, toolBlocks)
		if err != nil {
			ch <- EventError{Err: err}
			return
		}

		ch <- EventToolResults{Results: results}

		usedTodo := false
		for _, tb := range toolBlocks {
			switch tb.Name {
			case "todo", "task_create", "task_update":
				usedTodo = true
			}
		}

		if usedTodo {
			r.roundsSinceTodo = 0
			if t, ok := r.agent.registry.Tool("todo").(*tools.TodoWriteTool); ok {
				ch <- EventTodoUpdate{Content: t.Render()}
			}
		} else if r.hasIncompleteTodos() {
			r.roundsSinceTodo++
		}

		// When compaction replaced r.messages during tool dispatch (e.g.
		// the compact tool), skip appending tool_result blocks — their
		// tool_use counterparts no longer exist in the message history
		// and the API would reject orphaned tool_result blocks.
		//
		// Unlike autoCompact (layer 2) which continues to let the model
		// pick up mid-task, a manual compact means the model explicitly
		// requested compaction. There is nothing to respond to: exit the
		// loop so the user sees the compact result and types the next
		// input. This prevents the model from auto-responding with
		// redundant project exploration or capability re-listing.
		if r.compacted {
			r.compacted = false
			ch <- EventDone{}
			return
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
		if r.hasIncompleteTodos() && r.roundsSinceTodo >= 3 {
			r.roundsSinceTodo = 0
			contentBlocks = append(contentBlocks, anthropic.BetaContentBlockParamUnion{
				OfText: &anthropic.BetaTextBlockParam{Text: "<reminder>Update your todo list or task graph.</reminder>"},
			})
		}
		r.messages = append(r.messages, anthropic.NewBetaUserMessage(contentBlocks...))
	}
}

// hasIncompleteTodos reports whether the todo list or task graph has any
// pending or in_progress items that still need work.
func (r *Runner) hasIncompleteTodos() bool {
	// Check in-memory todo list (session-only).
	if t, ok := r.agent.registry.Tool("todo").(*tools.TodoWriteTool); ok {
		if t.HasIncomplete() {
			return true
		}
	}
	// Check persistent task graph (survives sessions).
	if t, ok := r.agent.registry.Tool("task_create").(*tools.TaskCreateTool); ok {
		has, err := t.Mgr.HasIncomplete()
		if err == nil && has {
			return true
		}
	}
	return false
}

// hasTaskBlock reports whether any tool_use block is a subagent task call.
func hasTaskBlock(blocks []tools.ToolUseBlock) bool {
	for _, b := range blocks {
		if b.Name == "task" {
			return true
		}
	}
	return false
}
