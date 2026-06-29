package team

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"task-agent/internal/agent/tasks"
)

func newTestManager(t *testing.T) *TeammateManager {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	c := anthropic.NewClient(
		option.WithAPIKey("test-key"),
		option.WithBaseURL(srv.URL),
	)

	teamDir := filepath.Join(t.TempDir(), "team")
	m, err := NewManager(&c, "claude-sonnet-4-6", t.TempDir(), teamDir, "lead", nil)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return m
}

func TestNewManagerCreatesConfig(t *testing.T) {
	m := newTestManager(t)

	if m.config.Lead != "lead" {
		t.Errorf("expected lead='lead', got %q", m.config.Lead)
	}
	if m.LeadName() != "lead" {
		t.Errorf("expected LeadName='lead', got %q", m.LeadName())
	}
	if len(m.Roster()) != 0 {
		t.Errorf("expected empty roster, got %d members", len(m.Roster()))
	}
}

func TestSpawnAndRoster(t *testing.T) {
	m := newTestManager(t)

	tm, err := m.Spawn("alice", "coder", "Write a hello world program.")
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if tm.Name != "alice" {
		t.Errorf("expected name='alice', got %q", tm.Name)
	}
	if tm.Role != "coder" {
		t.Errorf("expected role='coder', got %q", tm.Role)
	}
	if tm.Status != StatusWorking {
		t.Errorf("expected status=working, got %s", tm.Status)
	}

	roster := m.Roster()
	if len(roster) != 1 {
		t.Fatalf("expected 1 teammate in roster, got %d", len(roster))
	}
	if roster[0].Name != "alice" {
		t.Errorf("expected roster[0].Name='alice', got %q", roster[0].Name)
	}
}

func TestSpawnDuplicateName(t *testing.T) {
	m := newTestManager(t)

	_, err := m.Spawn("alice", "coder", "task 1")
	if err != nil {
		t.Fatalf("first Spawn should succeed: %v", err)
	}

	_, err = m.Spawn("alice", "tester", "task 2")
	if err == nil {
		t.Fatal("expected error for duplicate name")
	}
}

func TestSpawnReservedName(t *testing.T) {
	m := newTestManager(t)

	_, err := m.Spawn("lead", "coder", "task")
	if err == nil {
		t.Error("expected error for reserved name 'lead'")
	}

	_, err = m.Spawn("all", "coder", "task")
	if err == nil {
		t.Error("expected error for reserved name 'all'")
	}
}

func TestShutdown(t *testing.T) {
	m := newTestManager(t)

	_, err := m.Spawn("bob", "tester", "Run the tests.")
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	if err := m.Shutdown("bob"); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	roster := m.Roster()
	if len(roster) != 1 {
		t.Fatalf("expected 1 teammate still in roster, got %d", len(roster))
	}
	if roster[0].Status != StatusShutdown {
		t.Errorf("expected status=shutdown, got %s", roster[0].Status)
	}

	// Shutdown non-existent teammate.
	if err := m.Shutdown("bob"); err == nil {
		t.Error("expected error for duplicate shutdown")
	}
}

func TestShutdownAll(t *testing.T) {
	m := newTestManager(t)

	m.Spawn("alice", "coder", "task a")
	m.Spawn("bob", "tester", "task b")

	m.ShutdownAll()

	roster := m.Roster()
	for _, tm := range roster {
		if tm.Status != StatusShutdown {
			t.Errorf("teammate %s: expected shutdown, got %s", tm.Name, tm.Status)
		}
	}
}

func TestSendAndInbox(t *testing.T) {
	m := newTestManager(t)

	// Send to a teammate inbox (they don't need to exist to have an inbox).
	if err := m.Send("lead", "alice", "hello", "message"); err != nil {
		t.Fatalf("Send: %v", err)
	}

	msgs, err := m.ReadInbox("alice")
	if err != nil {
		t.Fatalf("ReadInbox: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].From != "lead" {
		t.Errorf("expected from='lead', got %q", msgs[0].From)
	}
	if msgs[0].Content != "hello" {
		t.Errorf("expected content='hello', got %q", msgs[0].Content)
	}
}

func TestConfigPersistence(t *testing.T) {
	teamDir := filepath.Join(t.TempDir(), "team")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	c := anthropic.NewClient(
		option.WithAPIKey("test-key"),
		option.WithBaseURL(srv.URL),
	)

	// Create first manager and spawn a teammate.
	m1, err := NewManager(&c, "claude-sonnet-4-6", t.TempDir(), teamDir, "lead", nil)
	if err != nil {
		t.Fatalf("NewManager 1: %v", err)
	}
	m1.Spawn("alice", "coder", "task")

	// Create second manager — should load config from disk.
	m2, err := NewManager(&c, "claude-sonnet-4-6", t.TempDir(), teamDir, "lead", nil)
	if err != nil {
		t.Fatalf("NewManager 2: %v", err)
	}

	roster := m2.Roster()
	if len(roster) != 1 {
		t.Fatalf("expected 1 teammate loaded from disk, got %d", len(roster))
	}
	if roster[0].Name != "alice" {
		t.Errorf("expected alice, got %q", roster[0].Name)
	}
	if roster[0].Role != "coder" {
		t.Errorf("expected coder, got %q", roster[0].Role)
	}
}

func TestScanUnclaimedTasks(t *testing.T) {
	// 创建真实的 tasks.Manager
	tmpDir := t.TempDir()
	taskMgr, err := tasks.NewManager(tmpDir, "test-list")
	if err != nil {
		t.Fatalf("tasks.NewManager: %v", err)
	}

	// 创建 team manager，注入 taskMgr
	teamDir := filepath.Join(t.TempDir(), "team")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	c := anthropic.NewClient(
		option.WithAPIKey("test-key"),
		option.WithBaseURL(srv.URL),
	)
	m, err := NewManager(&c, "claude-sonnet-4-6", t.TempDir(), teamDir, "lead", taskMgr)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	// 创建测试任务
	t1, _ := taskMgr.Create("pending+unowned+unblocked", "应该被扫到")
	// t1 默认 status=pending, owner="", blockedBy=[]

	t2, _ := taskMgr.Create("pending+owned", "不应该被扫到")
	owner := "alice"
	inProgress := "in_progress"
	taskMgr.Update(t2.ID, tasks.TaskUpdate{Owner: &owner, Status: &inProgress})

	t3, _ := taskMgr.Create("pending+blocked", "不应该被扫到")
	taskMgr.Update(t3.ID, tasks.TaskUpdate{AddBlockedBy: []string{t1.ID}})

	t4, _ := taskMgr.Create("completed", "不应该被扫到")
	completedStatus := "completed"
	taskMgr.Update(t4.ID, tasks.TaskUpdate{Status: &completedStatus})

	// 扫描
	unclaimed, err := m.ScanUnclaimedTasks()
	if err != nil {
		t.Fatalf("ScanUnclaimedTasks: %v", err)
	}

	if len(unclaimed) != 1 {
		t.Fatalf("expected 1 unclaimed task, got %d", len(unclaimed))
	}
	if unclaimed[0].ID != t1.ID {
		t.Errorf("expected task %s, got %s", t1.ID, unclaimed[0].ID)
	}
	if unclaimed[0].Subject != "pending+unowned+unblocked" {
		t.Errorf("unexpected subject: %s", unclaimed[0].Subject)
	}
}

func TestScanUnclaimedTasksWithNilTaskMgr(t *testing.T) {
	m := newTestManager(t) // taskMgr is nil

	unclaimed, err := m.ScanUnclaimedTasks()
	if err != nil {
		t.Fatalf("ScanUnclaimedTasks with nil taskMgr: %v", err)
	}
	if len(unclaimed) != 0 {
		t.Errorf("expected 0 tasks with nil taskMgr, got %d", len(unclaimed))
	}
}
