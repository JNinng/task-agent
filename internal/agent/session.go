package agent

import (
	"context"

	"github.com/anthropics/anthropic-sdk-go"
	"task-agent/internal/agent/tools"
)

// Session is the contract between a frontend and the agent Runner.
// Any frontend (TUI, web, API) consumes this interface — the Runner
// implementation remains unchanged.
type Session interface {
	// Run starts a new agent execution loop for the given input.
	// It returns a channel that emits typed events (text, tool calls,
	// results, etc.) and closes when the agent finishes.
	Run(ctx context.Context, input string) <-chan tools.Event

	// Tool returns the tool registered under the given name, or nil.
	Tool(name string) tools.Tool

	// Messages returns a copy of the current message history.
	Messages() []anthropic.BetaMessageParam

	// Clear resets the message history, starting a fresh conversation.
	Clear()

	// RenderTodo returns the formatted in-memory todo list string.
	RenderTodo() string

	// RenderTaskList returns the formatted persistent task graph string.
	RenderTaskList() string

	// PreviewToolUse returns a human-readable one-line preview of a tool
	// call, delegating to the tool's Previewer implementation.
	PreviewToolUse(tc tools.ToolUseBlock) string
}
