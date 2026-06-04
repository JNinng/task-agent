package agent

import (
	"context"
	"encoding/json"

	"github.com/anthropics/anthropic-sdk-go"
)

// 以下类型是 Runner 在 agent 循环中发出的事件，供展示层消费。
type (
	EventThinking  struct{}
	EventText      struct{ Content string }
	EventToolCall  struct{ Command string }
	EventToolResult struct{ Output string }
	EventError     struct{ Err error }
	EventDone      struct{}
)

// Runner 管理 agent 对话循环，独立于任何展示层。
type Runner struct {
	agent    *Agent
	messages []anthropic.MessageParam
}

// NewRunner 使用给定的 Agent 创建 Runner。
func NewRunner(ag *Agent) *Runner {
	return &Runner{agent: ag}
}

// Run 执行一轮 agent 循环：发送用户输入，然后运行 think-act-observe 循环，
// 在返回的 channel 上发出事件。channel 在轮次完成时关闭。
func (r *Runner) Run(ctx context.Context, input string) <-chan any {
	ch := make(chan any, 10)
	go func() {
		defer close(ch)
		r.runLoop(ctx, input, ch)
	}()
	return ch
}

func (r *Runner) runLoop(ctx context.Context, input string, ch chan<- any) {
	r.messages = append(r.messages, anthropic.NewUserMessage(anthropic.NewTextBlock(input)))
	ch <- EventThinking{}

	for {
		resp, err := r.agent.client.Messages.New(ctx, anthropic.MessageNewParams{
			Model:     r.agent.model,
			System:    r.agent.system,
			Messages:  r.messages,
			Tools:     r.agent.tools,
			MaxTokens: 8000,
		})
		if err != nil {
			ch <- EventError{Err: err}
			return
		}

		// 将助手消息持久化到对话历史
		blocks := make([]anthropic.ContentBlockParamUnion, len(resp.Content))
		for i, b := range resp.Content {
			blocks[i] = b.ToParam()
		}
		r.messages = append(r.messages, anthropic.NewAssistantMessage(blocks...))

		var firstBash *struct {
			id      string
			command string
		}
		for _, block := range resp.Content {
			switch block.Type {
			case "text":
				ch <- EventText{Content: block.AsText().Text}
			case "tool_use":
				tu := block.AsToolUse()
				if tu.Name != "bash" || firstBash != nil {
					continue
				}
				var input struct{ Command string `json:"command"` }
				if err := json.Unmarshal(tu.Input, &input); err != nil {
					ch <- EventError{Err: err}
					return
				}
				firstBash = &struct {
					id      string
					command string
				}{id: tu.ID, command: input.Command}
			}
		}

		if firstBash == nil {
			ch <- EventDone{}
			return
		}

		ch <- EventToolCall{Command: firstBash.command}
		output := runBash(ctx, firstBash.command)
		ch <- EventToolResult{Output: output}

		r.messages = append(r.messages, anthropic.NewUserMessage(
			anthropic.NewToolResultBlock(firstBash.id, output, false)))
	}
}
