package tasks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func tmpDir(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	return d
}

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	m, err := NewManager(tmpDir(t), "test-list")
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return m
}

// ─── Create & Get ─────────────────────────────────────────────────────────────

func TestCreateAndGet(t *testing.T) {
	m := newTestManager(t)

	task, err := m.Create("Setup project", "Initialize the repo")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if task.ID != "1" {
		t.Errorf("expected ID 1, got %s", task.ID)
	}
	if task.Subject != "Setup project" {
		t.Errorf("expected subject 'Setup project', got %s", task.Subject)
	}
	if task.Status != "pending" {
		t.Errorf("expected status pending, got %s", task.Status)
	}

	// Read back.
	got, err := m.Get("1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("Get returned nil")
	}
	if got.Subject != task.Subject {
		t.Errorf("Get subject mismatch: %s vs %s", got.Subject, task.Subject)
	}
}

func TestCreateEmptySubject(t *testing.T) {
	m := newTestManager(t)
	_, err := m.Create("  ", "")
	if err == nil {
		t.Fatal("expected error for empty subject")
	}
}

func TestGetNonExistent(t *testing.T) {
	m := newTestManager(t)
	got, err := m.Get("999")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for non-existent task, got %+v", got)
	}
}

// ─── Update ───────────────────────────────────────────────────────────────────

func TestUpdateStatus(t *testing.T) {
	m := newTestManager(t)
	m.Create("Task A", "")

	got, err := m.Update("1", TaskUpdate{Status: strPtr("in_progress")})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got.Status != "in_progress" {
		t.Errorf("expected in_progress, got %s", got.Status)
	}
}

func TestUpdateInvalidStatus(t *testing.T) {
	m := newTestManager(t)
	m.Create("Task A", "")

	_, err := m.Update("1", TaskUpdate{Status: strPtr("bogus")})
	if err == nil {
		t.Fatal("expected error for invalid status")
	}
}

// ─── Dependencies ─────────────────────────────────────────────────────────────

func TestDependencyBidirectional(t *testing.T) {
	m := newTestManager(t)
	m.Create("Task 1", "")
	m.Create("Task 2", "")

	// Task 1 blocks Task 2.
	_, err := m.Update("1", TaskUpdate{AddBlocks: []string{"2"}})
	if err != nil {
		t.Fatalf("addBlocks: %v", err)
	}

	t1, _ := m.Get("1")
	t2, _ := m.Get("2")

	if !contains(t1.Blocks, "2") {
		t.Errorf("task 1.Blocks should contain 2: %v", t1.Blocks)
	}
	if !contains(t2.BlockedBy, "1") {
		t.Errorf("task 2.BlockedBy should contain 1: %v", t2.BlockedBy)
	}
}

func TestDependencyViaAddBlockedBy(t *testing.T) {
	m := newTestManager(t)
	m.Create("Task 1", "")
	m.Create("Task 2", "")

	// Task 2 is blocked by Task 1.
	_, err := m.Update("2", TaskUpdate{AddBlockedBy: []string{"1"}})
	if err != nil {
		t.Fatalf("addBlockedBy: %v", err)
	}

	t1, _ := m.Get("1")
	t2, _ := m.Get("2")

	if !contains(t1.Blocks, "2") {
		t.Errorf("task 1.Blocks should contain 2: %v", t1.Blocks)
	}
	if !contains(t2.BlockedBy, "1") {
		t.Errorf("task 2.BlockedBy should contain 1: %v", t2.BlockedBy)
	}
}

func TestDependencySelfBlock(t *testing.T) {
	m := newTestManager(t)
	m.Create("Task A", "")

	_, err := m.Update("1", TaskUpdate{AddBlocks: []string{"1"}})
	if err == nil {
		t.Fatal("expected error when task blocks itself")
	}
}

func TestDependencyRemove(t *testing.T) {
	m := newTestManager(t)
	m.Create("Task 1", "")
	m.Create("Task 2", "")
	m.Update("1", TaskUpdate{AddBlocks: []string{"2"}})

	_, err := m.Update("2", TaskUpdate{RemoveBlockedBy: []string{"1"}})
	if err != nil {
		t.Fatalf("removeBlockedBy: %v", err)
	}

	t1, _ := m.Get("1")
	t2, _ := m.Get("2")
	if contains(t1.Blocks, "2") {
		t.Error("task 1 should no longer block 2")
	}
	if contains(t2.BlockedBy, "1") {
		t.Error("task 2 should no longer be blocked by 1")
	}
}

// ─── Auto-unlock on completion ────────────────────────────────────────────────

func TestCompleteUnlocksDependents(t *testing.T) {
	m := newTestManager(t)
	m.Create("Task 1", "")
	m.Create("Task 2", "")
	m.Create("Task 3", "")
	m.Update("1", TaskUpdate{AddBlocks: []string{"2", "3"}})

	// Complete task 1.
	_, err := m.Update("1", TaskUpdate{Status: strPtr("completed")})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}

	t2, _ := m.Get("2")
	t3, _ := m.Get("3")

	if contains(t2.BlockedBy, "1") {
		t.Error("task 2 should no longer be blocked by completed task 1")
	}
	if contains(t3.BlockedBy, "1") {
		t.Error("task 3 should no longer be blocked by completed task 1")
	}
}

func TestCompleteUnlocksChain(t *testing.T) {
	// Task 1 → Task 2 → Task 3
	m := newTestManager(t)
	m.Create("Task 1", "")
	m.Create("Task 2", "")
	m.Create("Task 3", "")
	m.Update("1", TaskUpdate{AddBlocks: []string{"2"}})
	m.Update("2", TaskUpdate{AddBlocks: []string{"3"}})

	// Complete Task 1 → Task 2 is unblocked.
	m.Update("1", TaskUpdate{Status: strPtr("completed")})
	t2, _ := m.Get("2")
	if contains(t2.BlockedBy, "1") {
		t.Error("task 2 should be unblocked after task 1 completes")
	}
	// Task 3 is still blocked by Task 2 (Task 2 is not completed).
	t3, _ := m.Get("3")
	if !contains(t3.BlockedBy, "2") {
		t.Error("task 3 should still be blocked by task 2")
	}

	// Complete Task 2 → Task 3 is unblocked.
	m.Update("2", TaskUpdate{Status: strPtr("completed")})
	t3, _ = m.Get("3")
	if contains(t3.BlockedBy, "2") {
		t.Error("task 3 should be unblocked after task 2 completes")
	}
}

// ─── DAG pattern: diamond ─────────────────────────────────────────────────────
//
//	  +--> B --+
//	A          +--> D
//	  +--> C --+
func TestDiamondDependency(t *testing.T) {
	m := newTestManager(t)
	m.Create("A", "")
	m.Create("B", "")
	m.Create("C", "")
	m.Create("D", "")
	m.Update("1", TaskUpdate{AddBlocks: []string{"2", "3"}}) // A blocks B, C
	m.Update("2", TaskUpdate{AddBlocks: []string{"4"}})       // B blocks D
	m.Update("3", TaskUpdate{AddBlocks: []string{"4"}})       // C blocks D

	// D is blocked by both B and C.
	d, _ := m.Get("4")
	if len(d.BlockedBy) != 2 {
		t.Errorf("D should have 2 blockers, got %d: %v", len(d.BlockedBy), d.BlockedBy)
	}

	// Complete B alone → D still blocked by C.
	m.Update("2", TaskUpdate{Status: strPtr("completed")})
	d, _ = m.Get("4")
	if !contains(d.BlockedBy, "3") {
		t.Error("D should still be blocked by C")
	}
	if contains(d.BlockedBy, "2") {
		t.Error("D should no longer be blocked by B (completed)")
	}

	// Complete C → D is fully unblocked.
	m.Update("3", TaskUpdate{Status: strPtr("completed")})
	d, _ = m.Get("4")
	if len(d.BlockedBy) != 0 {
		t.Errorf("D should have 0 blockers after B and C complete, got: %v", d.BlockedBy)
	}
}

// ─── List ─────────────────────────────────────────────────────────────────────

func TestList(t *testing.T) {
	m := newTestManager(t)
	m.Create("A", "")
	m.Create("B", "")
	m.Create("C", "")

	tasks, err := m.List(ListFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(tasks) != 3 {
		t.Errorf("expected 3 tasks, got %d", len(tasks))
	}
}

func TestListExcludesDeleted(t *testing.T) {
	m := newTestManager(t)
	m.Create("A", "")
	m.Create("B", "")
	m.Delete("2")

	tasks, _ := m.List(ListFilter{})
	if len(tasks) != 1 {
		t.Errorf("expected 1 task after soft-delete, got %d", len(tasks))
	}
}

func TestListFilterStatus(t *testing.T) {
	m := newTestManager(t)
	m.Create("A", "")
	m.Create("B", "")
	m.Update("1", TaskUpdate{Status: strPtr("completed")})

	tasks, _ := m.List(ListFilter{Status: "pending"})
	if len(tasks) != 1 {
		t.Errorf("expected 1 pending task, got %d", len(tasks))
	}
}

// ─── Delete ───────────────────────────────────────────────────────────────────

func TestSoftDelete(t *testing.T) {
	m := newTestManager(t)
	m.Create("A", "")

	if err := m.Delete("1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// Still on disk.
	_, err := os.Stat(filepath.Join(m.dir, "1.json"))
	if err != nil {
		t.Errorf("file should exist after soft-delete: %v", err)
	}

	// Listed as deleted.
	tasks, _ := m.List(ListFilter{ExcludeDeleted: false, Status: "deleted"})
	if len(tasks) != 1 {
		t.Errorf("expected 1 deleted task, got %d", len(tasks))
	}
}

func TestDeleteClearsEdges(t *testing.T) {
	m := newTestManager(t)
	m.Create("Task 1", "")
	m.Create("Task 2", "")
	m.Update("1", TaskUpdate{AddBlocks: []string{"2"}})

	m.Delete("1")

	t2, _ := m.Get("2")
	if contains(t2.BlockedBy, "1") {
		t.Error("task 2 should no longer be blocked by deleted task 1")
	}
}

// ─── High-watermark ───────────────────────────────────────────────────────────

func TestHighWatermarkPreventsIDReuse(t *testing.T) {
	m := newTestManager(t)
	m.Create("A", "")
	m.Create("B", "")
	m.Create("C", "")

	// Delete task 3.
	m.Delete("3")

	// Create new task — should get ID 4, not 3.
	task, err := m.Create("D", "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if task.ID != "4" {
		t.Errorf("expected ID 4 after delete, got %s (high-watermark should prevent reuse)", task.ID)
	}
}

func TestHighWatermarkSurvivesReset(t *testing.T) {
	m := newTestManager(t)
	m.Create("A", "")
	m.Create("B", "")

	m.Reset()

	task, _ := m.Create("C", "")
	if task.ID != "3" {
		t.Errorf("expected ID 3 after reset, got %s", task.ID)
	}
}

// ─── Corrupt file ─────────────────────────────────────────────────────────────

func TestCorruptFileGracefulDegradation(t *testing.T) {
	m := newTestManager(t)
	m.Create("A", "")
	m.Create("B", "")

	// Corrupt task 2's file.
	os.WriteFile(filepath.Join(m.dir, "2.json"), []byte("not valid json {{{"), 0600)

	// Get returns nil, no error.
	got, err := m.Get("2")
	if err != nil {
		t.Fatalf("Get should not error on corrupt file: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for corrupt file, got %+v", got)
	}

	// List skips it.
	tasks, _ := m.List(ListFilter{})
	if len(tasks) != 1 {
		t.Errorf("expected 1 task after corruption, got %d", len(tasks))
	}
}

func TestMissingFieldGracefulDegradation(t *testing.T) {
	m := newTestManager(t)
	m.Create("A", "")

	// Write a file missing required fields.
	os.WriteFile(filepath.Join(m.dir, "99.json"), []byte(`{"id":"99"}`), 0600)

	got, _ := m.Get("99")
	if got != nil {
		t.Error("expected nil for file missing subject")
	}
}

// ─── HasIncomplete ────────────────────────────────────────────────────────────

func TestHasIncomplete(t *testing.T) {
	m := newTestManager(t)
	m.Create("A", "")
	m.Create("B", "")

	has, err := m.HasIncomplete()
	if err != nil {
		t.Fatalf("HasIncomplete: %v", err)
	}
	if !has {
		t.Error("expected HasIncomplete=true with pending tasks")
	}

	m.Update("1", TaskUpdate{Status: strPtr("completed")})
	m.Update("2", TaskUpdate{Status: strPtr("completed")})

	has, _ = m.HasIncomplete()
	if has {
		t.Error("expected HasIncomplete=false after all completed")
	}
}

// ─── CountByStatus ────────────────────────────────────────────────────────────

func TestCountByStatus(t *testing.T) {
	m := newTestManager(t)
	m.Create("A", "")
	m.Create("B", "")
	m.Create("C", "")
	m.Update("1", TaskUpdate{Status: strPtr("in_progress")})
	m.Update("2", TaskUpdate{Status: strPtr("completed")})

	p, ip, c, err := m.CountByStatus()
	if err != nil {
		t.Fatalf("CountByStatus: %v", err)
	}
	if p != 1 || ip != 1 || c != 1 {
		t.Errorf("expected 1/1/1, got %d/%d/%d", p, ip, c)
	}
}

// ─── Metadata merge ───────────────────────────────────────────────────────────

func TestMetadataMerge(t *testing.T) {
	m := newTestManager(t)
	m.Create("Task", "")

	_, err := m.Update("1", TaskUpdate{
		Metadata: map[string]any{"foo": "bar", "num": float64(42)},
	})
	if err != nil {
		t.Fatalf("Update metadata: %v", err)
	}

	task, _ := m.Get("1")
	if task.Metadata["foo"] != "bar" {
		t.Errorf("expected foo=bar, got %v", task.Metadata["foo"])
	}
	if task.Metadata["num"] != float64(42) {
		t.Errorf("expected num=42, got %v", task.Metadata["num"])
	}

	// Delete a key.
	m.Update("1", TaskUpdate{Metadata: map[string]any{"foo": nil}})
	task, _ = m.Get("1")
	if _, ok := task.Metadata["foo"]; ok {
		t.Error("foo should have been deleted")
	}
}

// ─── Reset ────────────────────────────────────────────────────────────────────

func TestReset(t *testing.T) {
	m := newTestManager(t)
	m.Create("A", "")
	m.Create("B", "")

	if err := m.Reset(); err != nil {
		t.Fatalf("Reset: %v", err)
	}

	tasks, _ := m.List(ListFilter{})
	if len(tasks) != 0 {
		t.Errorf("expected 0 tasks after reset, got %d", len(tasks))
	}
}

// ─── Concurrent safety (basic) ────────────────────────────────────────────────

func TestConcurrentCreate(t *testing.T) {
	m := newTestManager(t)

	const n = 20
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func(idx int) {
			_, err := m.Create("Task", "")
			errs <- err
		}(i)
	}
	for i := 0; i < n; i++ {
		if err := <-errs; err != nil {
			t.Errorf("concurrent create: %v", err)
		}
	}

	tasks, _ := m.List(ListFilter{})
	if len(tasks) != n {
		t.Errorf("expected %d tasks, got %d", n, len(tasks))
	}

	// IDs should be unique.
	seen := make(map[string]bool)
	for _, tk := range tasks {
		if seen[tk.ID] {
			t.Errorf("duplicate ID %s", tk.ID)
		}
		seen[tk.ID] = true
	}
}

// ─── ResolveTaskListID ────────────────────────────────────────────────────────

func TestResolveTaskListIDDefault(t *testing.T) {
	id := ResolveTaskListID("")
	if id == "" {
		t.Error("expected non-empty default ID")
	}
}

func TestResolveTaskListIDExplicitTeam(t *testing.T) {
	id := ResolveTaskListID("my-team")
	if id != "my-team" {
		t.Errorf("expected 'my-team', got %s", id)
	}
}

func TestResolveTaskListIDEnvVar(t *testing.T) {
	os.Setenv("TASK_AGENT_TASK_LIST_ID", "env-team")
	defer os.Unsetenv("TASK_AGENT_TASK_LIST_ID")

	id := ResolveTaskListID("my-team")
	if id != "env-team" {
		t.Errorf("expected env var to take priority, got %s", id)
	}
}

// ─── Persistence across Manager instances ─────────────────────────────────────

func TestPersistenceAcrossInstances(t *testing.T) {
	base := tmpDir(t)

	m1, _ := NewManager(base, "persist-test")
	m1.Create("Task A", "Desc A")
	m1.Update("1", TaskUpdate{Status: strPtr("in_progress")})

	// New manager pointing at same directory.
	m2, _ := NewManager(base, "persist-test")
	task, err := m2.Get("1")
	if err != nil {
		t.Fatalf("Get from second manager: %v", err)
	}
	if task == nil {
		t.Fatal("task should survive across manager instances")
	}
	if task.Status != "in_progress" {
		t.Errorf("expected status in_progress, got %s", task.Status)
	}
}

// ─── JSON round-trip ──────────────────────────────────────────────────────────

func TestJSONRoundTrip(t *testing.T) {
	original := Task{
		ID:          "5",
		Subject:     "Refactor",
		Description: "Clean up",
		ActiveForm:  "Refactoring",
		Owner:       "agent-1",
		Status:      "pending",
		Blocks:      []string{"6", "7"},
		BlockedBy:   []string{"4"},
		Metadata:    map[string]any{"priority": "high"},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var restored Task
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if restored.ID != original.ID {
		t.Error("ID mismatch")
	}
	if restored.Subject != original.Subject {
		t.Error("subject mismatch")
	}
	if len(restored.Blocks) != 2 {
		t.Error("blocks mismatch")
	}
	if len(restored.BlockedBy) != 1 {
		t.Error("blockedBy mismatch")
	}
}
