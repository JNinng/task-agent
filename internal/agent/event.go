package agent

import "task-agent/internal/agent/tools"

type (
	EventThinking    struct{}
	EventText        struct{ Content string }
	EventToolCalls   struct{ Tools []tools.ToolUseBlock }
	EventToolResults struct{ Results []tools.ToolResult }
	EventError       struct{ Err error }
	EventDone        struct{}
)
