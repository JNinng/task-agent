package agent

import (
	"context"
	"encoding/json"

	"github.com/anthropics/anthropic-sdk-go"
	"task-agent/internal/agent/tools"
)

type Runner struct {
	agent           *Agent
	messages        []anthropic.BetaMessageParam
	roundsSinceTodo int
}

func NewRunner(ag *Agent) *Runner {
	return &Runner{agent: ag}
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
			r.roundsSinceTodo++
			if r.roundsSinceTodo >= 3 {
				r.roundsSinceTodo = 0
				r.messages = append(r.messages, anthropic.NewBetaUserMessage(
					anthropic.BetaContentBlockParamUnion{
						OfText: &anthropic.BetaTextBlockParam{
							Text: "<reminder>Update your todos.</reminder>",
						},
					}))
				continue
			}
			ch <- EventDone{}
			return
		}

		ch <- EventToolCalls{Tools: toolBlocks}

		results, err := r.agent.registry.Dispatch(ctx, toolBlocks)
		if err != nil {
			ch <- EventError{Err: err}
			return
		}

		ch <- EventToolResults{Results: results}

		usedTodo := false
		for _, tb := range toolBlocks {
			if tb.Name == "todo" {
				usedTodo = true
				break
			}
		}

		if usedTodo {
			r.roundsSinceTodo = 0
			if t, ok := r.agent.registry.Tool("todo").(*tools.TodoWriteTool); ok {
				ch <- EventTodoUpdate{Content: t.Render()}
			}
		} else {
			r.roundsSinceTodo++
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
		if r.roundsSinceTodo >= 3 {
			r.roundsSinceTodo = 0
			contentBlocks = append(contentBlocks, anthropic.BetaContentBlockParamUnion{
				OfText: &anthropic.BetaTextBlockParam{Text: "<reminder>Update your todos.</reminder>"},
			})
		}
		r.messages = append(r.messages, anthropic.NewBetaUserMessage(contentBlocks...))
	}
}
