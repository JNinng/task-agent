package agent

import "task-agent/internal/agent/tools"

type (
	EventThinking struct{}
	EventText     struct{ Content string }

	EventToolCalls   struct{ Tools []tools.ToolUseBlock }
	EventToolResults struct{ Results []tools.ToolResult }

	EventTodoUpdate struct{ Content string }

	// EventBackgroundResult is emitted when background tasks complete and
	// their results are injected into the LLM message history.
	EventBackgroundResult struct {
		TaskID  string
		Status  string // completed | failed | timeout
		Summary string
	}

	EventError struct{ Err error }
	EventDone  struct{}
)

func (EventThinking) IsEvent()         {}
func (EventText) IsEvent()             {}
func (EventToolCalls) IsEvent()        {}
func (EventToolResults) IsEvent()      {}
func (EventTodoUpdate) IsEvent()       {}
func (EventBackgroundResult) IsEvent() {}
func (EventError) IsEvent()            {}
func (EventDone) IsEvent()             {}
