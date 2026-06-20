package team

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
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

	teamDir := filepath.Join(t.TempDir(), ".team")
	m, err := NewManager(&c, "claude-sonnet-4-6", t.TempDir(), teamDir, "lead")
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
	teamDir := filepath.Join(t.TempDir(), ".team")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	c := anthropic.NewClient(
		option.WithAPIKey("test-key"),
		option.WithBaseURL(srv.URL),
	)

	// Create first manager and spawn a teammate.
	m1, err := NewManager(&c, "claude-sonnet-4-6", t.TempDir(), teamDir, "lead")
	if err != nil {
		t.Fatalf("NewManager 1: %v", err)
	}
	m1.Spawn("alice", "coder", "task")

	// Create second manager — should load config from disk.
	m2, err := NewManager(&c, "claude-sonnet-4-6", t.TempDir(), teamDir, "lead")
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
