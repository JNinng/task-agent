package background

import (
	"context"
	"testing"
	"time"
)

func TestStartAndDrain(t *testing.T) {
	m := NewManager()

	task := m.Start(context.Background(), "echo hello", 10*time.Second)
	if task.ID == "" {
		t.Fatal("expected non-empty task ID")
	}
	if task.Status != StatusRunning {
		t.Errorf("expected running, got %s", task.Status)
	}

	// Wait for completion.
	time.Sleep(500 * time.Millisecond)

	// Drain notifications.
	notifs := m.DrainNotifications()
	if len(notifs) != 1 {
		t.Fatalf("expected 1 notification, got %d", len(notifs))
	}
	if notifs[0].TaskID != task.ID {
		t.Errorf("notification task_id mismatch: %s vs %s", notifs[0].TaskID, task.ID)
	}
	if notifs[0].Status != StatusCompleted {
		t.Errorf("expected completed, got %s", notifs[0].Status)
	}

	// Check task status.
	task = m.Status(task.ID)
	if task == nil {
		t.Fatal("task should exist")
	}
	if task.Status != StatusCompleted {
		t.Errorf("expected completed, got %s", task.Status)
	}
	if task.Result == "" {
		t.Error("expected non-empty result")
	}
}

func TestMultipleTasks(t *testing.T) {
	m := NewManager()

	m.Start(context.Background(), "echo a", 10*time.Second)
	m.Start(context.Background(), "echo b", 10*time.Second)

	time.Sleep(500 * time.Millisecond)

	notifs := m.DrainNotifications()
	if len(notifs) != 2 {
		t.Fatalf("expected 2 notifications, got %d", len(notifs))
	}

	tasks := m.List()
	if len(tasks) != 2 {
		t.Errorf("expected 2 tasks, got %d", len(tasks))
	}
}

func TestTimeout(t *testing.T) {
	m := NewManager()

	m.Start(context.Background(), "sleep 10", 100*time.Millisecond)

	time.Sleep(500 * time.Millisecond)

	notifs := m.DrainNotifications()
	if len(notifs) != 1 {
		t.Fatalf("expected 1 notification, got %d", len(notifs))
	}
	if notifs[0].Status != StatusTimeout {
		t.Errorf("expected timeout, got %s", notifs[0].Status)
	}
}

func TestDrainEmpty(t *testing.T) {
	m := NewManager()
	notifs := m.DrainNotifications()
	if notifs != nil {
		t.Errorf("expected nil for empty drain, got %v", notifs)
	}
}

func TestStatusNonExistent(t *testing.T) {
	m := NewManager()
	task := m.Status("nonexistent")
	if task != nil {
		t.Errorf("expected nil for non-existent task, got %v", task)
	}
}

func TestListEmpty(t *testing.T) {
	m := NewManager()
	tasks := m.List()
	if len(tasks) != 0 {
		t.Errorf("expected empty list, got %d", len(tasks))
	}
}

func TestRunningCount(t *testing.T) {
	m := NewManager()

	// Immediately after Start, tasks are running (status is set atomically
	// inside Start before the goroutine launches).
	m.Start(context.Background(), "echo a", 5*time.Second)
	m.Start(context.Background(), "echo b", 5*time.Second)

	if n := m.RunningCount(); n != 2 {
		t.Errorf("expected 2 running immediately after start, got %d", n)
	}

	// Wait for completion then poll status until both finish.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		_ = m.DrainNotifications()
		if m.RunningCount() == 0 {
			return // success
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Error("expected both tasks to finish within 3s")
}

func TestRemoveTask(t *testing.T) {
	m := NewManager()
	m.Start(context.Background(), "echo x", 5*time.Second)

	tasks := m.List()
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}

	m.RemoveTask(tasks[0].ID)
	tasks = m.List()
	if len(tasks) != 0 {
		t.Errorf("expected 0 tasks after remove, got %d", len(tasks))
	}
}

func TestConcurrentStart(t *testing.T) {
	m := NewManager()
	const n = 10

	for i := 0; i < n; i++ {
		go func() {
			m.Start(context.Background(), "echo concurrent", 5*time.Second)
		}()
	}

	time.Sleep(500 * time.Millisecond)

	notifs := m.DrainNotifications()
	if len(notifs) != n {
		t.Errorf("expected %d notifications, got %d", n, len(notifs))
	}
}

func TestDrainClearsQueue(t *testing.T) {
	m := NewManager()
	m.Start(context.Background(), "echo test", 5*time.Second)

	time.Sleep(500 * time.Millisecond)

	notifs1 := m.DrainNotifications()
	if len(notifs1) == 0 {
		t.Fatal("expected notifications in first drain")
	}

	notifs2 := m.DrainNotifications()
	if notifs2 != nil {
		t.Errorf("expected nil after drain, got %v", notifs2)
	}
}
