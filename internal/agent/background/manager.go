package background

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

// DefaultTimeout is the maximum wall-clock time a background task may run.
const DefaultTimeout = 5 * time.Minute

// Manager tracks background tasks and collects their results in a
// thread-safe notification queue. All public methods are safe for
// concurrent use.
type Manager struct {
	mu     sync.Mutex
	tasks  map[string]*Task
	notifs []Notification

	nextID int
}

// NewManager creates an empty background task manager.
func NewManager() *Manager {
	return &Manager{
		tasks: make(map[string]*Task),
	}
}

// Start launches command as a background goroutine and returns immediately.
// The returned task ID can be used later with Status(), List(), or to
// correlate the notification when the task completes.
//
// The process is killed if ctx is cancelled, but the manager itself does
// not cancel tasks when DrainNotifications is called — the caller owns ctx.
func (m *Manager) Start(ctx context.Context, command string, timeout time.Duration) *Task {
	m.mu.Lock()
	m.nextID++
	id := fmt.Sprintf("bg-%d", m.nextID)
	now := time.Now()
	t := &Task{
		ID:        id,
		Command:   command,
		Status:    StatusRunning,
		StartedAt: now,
	}
	m.tasks[id] = t
	m.mu.Unlock()

	go m.execute(ctx, id, command, timeout)

	return t
}

// DrainNotifications returns all pending notifications and clears the queue.
// Call this before each LLM call to collect completed background tasks.
func (m *Manager) DrainNotifications() []Notification {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.notifs) == 0 {
		return nil
	}
	notifs := m.notifs
	m.notifs = nil
	return notifs
}

// Status returns a copy of the task with the given ID, or nil if it
// doesn't exist.
func (m *Manager) Status(taskID string) *Task {
	m.mu.Lock()
	defer m.mu.Unlock()

	t, ok := m.tasks[taskID]
	if !ok {
		return nil
	}
	cp := *t
	return &cp
}

// List returns a snapshot of all tracked tasks.
func (m *Manager) List() []Task {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]Task, 0, len(m.tasks))
	for _, t := range m.tasks {
		out = append(out, *t)
	}

	// Stable order by ID.
	sortTasksByID(out)
	return out
}

// RunningCount returns the number of tasks still in "running" status.
func (m *Manager) RunningCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	n := 0
	for _, t := range m.tasks {
		if t.Status == StatusRunning {
			n++
		}
	}
	return n
}

// RemoveTask deletes a task from tracking entirely. No-op if not found.
func (m *Manager) RemoveTask(taskID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.tasks, taskID)
}

// --- internal ---

// execute runs the command in a subprocess and records the result.
func (m *Manager) execute(ctx context.Context, id, command string, timeout time.Duration) {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}

	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(execCtx, "powershell", "-NoProfile", "-Command",
			"[Console]::OutputEncoding = [Text.Encoding]::UTF8; "+command)
	} else {
		cmd = exec.CommandContext(execCtx, "bash", "-c", command)
	}

	out, err := cmd.CombinedOutput()
	result := strings.TrimSpace(string(out))
	if len(result) > 50000 {
		result = result[:50000]
	}

	// Determine final status.
	var status Status
	var errorMsg string
	if err != nil {
		if execCtx.Err() != nil {
			status = StatusTimeout
			errorMsg = fmt.Sprintf("timeout after %v", timeout)
		} else {
			status = StatusFailed
			errorMsg = err.Error()
		}
	}

	// If we already have output, include it even on failure — the output
	// often contains the actual error message from the tool.
	summary := result
	if status == StatusFailed || status == StatusTimeout {
		if result == "" {
			summary = errorMsg
		}
	}

	// Truncate summary for notification (first 500 chars).
	short := summary
	if len(short) > 500 {
		short = short[:500] + "..."
	}
	if status == "" {
		status = StatusCompleted
		short = "(completed)"
		if len(summary) > 0 {
			short = fmt.Sprintf("(completed, %d bytes of output)", len(summary))
		}
	}

	m.mu.Lock()
	if t, ok := m.tasks[id]; ok {
		t.Status = status
		t.DoneAt = time.Now()
		t.Result = result
		if errorMsg != "" {
			t.Error = errorMsg
		}
	}

	if status == StatusCompleted || status == StatusFailed || status == StatusTimeout {
		m.notifs = append(m.notifs, Notification{
			TaskID:  id,
			Status:  status,
			Summary: short,
		})
	}
	m.mu.Unlock()
}

// --- helpers ---

// sortTasksByID sorts tasks in place by their numeric ID suffix.
func sortTasksByID(tasks []Task) {
	// Simple insertion sort — task lists are always tiny.
	for i := 1; i < len(tasks); i++ {
		for j := i; j > 0 && tasks[j-1].ID > tasks[j].ID; j-- {
			tasks[j], tasks[j-1] = tasks[j-1], tasks[j]
		}
	}
}
