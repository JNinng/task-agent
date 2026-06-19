// Package background implements a background task system inspired by
// the learn-claude-code s08 tutorial. Long-running commands (npm install,
// pytest, docker build, etc.) are launched in a goroutine so the agent
// can continue working on other things while they execute. Results are
// collected in a thread-safe notification queue and injected before the
// next LLM call.
//
// Task life cycle: running -> completed | failed | timeout
//
//	running     - goroutine is executing
//	completed   - process exited successfully with output
//	failed      - process exited with non-zero status
//	timeout     - process exceeded the configured deadline
package background

import "time"

// Status represents the life-cycle state of a background task.
type Status string

const (
	StatusRunning   Status = "running"
	StatusCompleted Status = "completed"
	StatusFailed    Status = "failed"
	StatusTimeout   Status = "timeout"
)

// Task describes a single background task.
type Task struct {
	ID        string    `json:"id"`
	Command   string    `json:"command"`
	Status    Status    `json:"status"`
	StartedAt time.Time `json:"startedAt"`
	DoneAt    time.Time `json:"doneAt,omitempty"`
	PID       int       `json:"pid,omitempty"`
	Result    string    `json:"result,omitempty"` // stdout + stderr (truncated to 50000)
	Error     string    `json:"error,omitempty"`  // non-empty when status is failed/timeout
}

// Notification is a short summary pushed to the notification queue when a
// task completes. It is injected into the LLM message history before the
// next API call so the model can react to the result.
type Notification struct {
	TaskID  string `json:"task_id"`
	Status  Status `json:"status"`
	Summary string `json:"summary"` // first 500 chars of result
}
