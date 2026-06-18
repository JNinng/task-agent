// Package tasks implements a persistent task graph (DAG) for multi-step
// agent work. Each task is a JSON file on disk; dependencies are tracked
// bidirectionally (blocks + blockedBy) so both directions are O(1).
//
// The design bridges the learn-claude-code s07 tutorial (DAG + persistence)
// with production hardening from the real Claude Code TodoV2 system:
// high-water-mark to prevent ID reuse, soft-delete, and bidirectional edges.
package tasks

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ─── Data model ───────────────────────────────────────────────────────────────

// Task is a single node in the dependency graph.
type Task struct {
	ID          string         `json:"id"`
	Subject     string         `json:"subject"`
	Description string         `json:"description"`
	ActiveForm  string         `json:"activeForm,omitempty"`
	Owner       string         `json:"owner,omitempty"`
	Status      string         `json:"status"`    // pending | in_progress | completed | deleted
	Blocks      []string       `json:"blocks"`    // tasks this one blocks
	BlockedBy   []string       `json:"blockedBy"` // tasks that block this one
	Metadata    map[string]any `json:"metadata,omitempty"`
}

// TaskUpdate carries partial field updates. Nil pointer means "don't change".
// Metadata is merged: a nil map value deletes the key.
type TaskUpdate struct {
	Subject         *string
	Description     *string
	ActiveForm      *string
	Owner           *string
	Status          *string
	AddBlocks       []string
	AddBlockedBy    []string
	RemoveBlockedBy []string
	Metadata        map[string]any // merged — nil value deletes key
}

// ListFilter restricts which tasks List returns.
type ListFilter struct {
	ExcludeDeleted bool // default true in List()
	Owner          string
	Status         string
}

// validStatus reports whether s is a recognised task status.
func validStatus(s string) bool {
	switch s {
	case "pending", "in_progress", "completed", "deleted":
		return true
	}
	return false
}

// ─── File paths ───────────────────────────────────────────────────────────────

const (
	highWatermarkFile = ".highwatermark"
	taskFileSuffix    = ".json"
)

// ─── Manager ──────────────────────────────────────────────────────────────────

// Manager persists tasks as individual JSON files under a task-list directory.
// All public methods are safe for concurrent use.
type Manager struct {
	mu  sync.Mutex
	dir string // absolute path to the task-list directory
}

// NewManager creates a Manager backed by baseDir/tasks/taskListID.
// If taskListID is empty it defaults to "default".
func NewManager(baseDir, taskListID string) (*Manager, error) {
	if taskListID == "" {
		taskListID = "default"
	}
	dir := filepath.Join(baseDir, "tasks", taskListID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("create tasks dir: %w", err)
	}
	return &Manager{dir: dir}, nil
}

// Dir returns the absolute path to this manager's task directory.
func (m *Manager) Dir() string { return m.dir }

// ─── CRUD ─────────────────────────────────────────────────────────────────────

// Create allocates a new task with status "pending" and persists it.
func (m *Manager) Create(subject, description string) (*Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	subject = strings.TrimSpace(subject)
	if subject == "" {
		return nil, fmt.Errorf("task subject is required")
	}

	id := m.nextIDLocked()
	t := &Task{
		ID:          strconv.Itoa(id),
		Subject:     subject,
		Description: strings.TrimSpace(description),
		Status:      "pending",
		Blocks:      []string{},
		BlockedBy:   []string{},
	}
	if err := m.saveLocked(t); err != nil {
		return nil, err
	}
	m.writeHighWatermarkLocked(id)
	return t, nil
}

// Get loads a task by ID. Returns nil if the task doesn't exist or the on-disk
// data fails schema validation (corrupt file → graceful degradation).
func (m *Manager) Get(taskID string) (*Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.loadLocked(taskID)
}

// Update applies partial updates to a task. When status transitions to
// "completed", dependencies are automatically cleared from other tasks.
// When status transitions to "deleted", the task is soft-deleted and its
// edges are cleaned up.
func (m *Manager) Update(taskID string, u TaskUpdate) (*Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	t, err := m.loadLocked(taskID)
	if err != nil {
		return nil, err
	}
	if t == nil {
		return nil, fmt.Errorf("task %s not found", taskID)
	}

	changed := false

	if u.Subject != nil {
		t.Subject = strings.TrimSpace(*u.Subject)
		if t.Subject == "" {
			return nil, fmt.Errorf("task subject cannot be empty")
		}
		changed = true
	}
	if u.Description != nil {
		t.Description = *u.Description
		changed = true
	}
	if u.ActiveForm != nil {
		t.ActiveForm = *u.ActiveForm
		changed = true
	}
	if u.Owner != nil {
		t.Owner = *u.Owner
		changed = true
	}
	if u.Status != nil {
		if !validStatus(*u.Status) {
			return nil, fmt.Errorf("invalid status %q", *u.Status)
		}
		oldStatus := t.Status
		t.Status = *u.Status
		changed = true

		if t.Status == "completed" && oldStatus != "completed" {
			m.clearDependencyLocked(taskID)
		}
		if t.Status == "deleted" && oldStatus != "deleted" {
			m.clearDependencyLocked(taskID)
		}
	}

	// --- dependency edges ---
	// blockTaskLocked/unblockTaskLocked persist the changes, but we also
	// update the in-memory copy so the final saveLocked below doesn't
	// overwrite with stale data.
	for _, blockID := range u.AddBlocks {
		if err := m.blockTaskLocked(taskID, blockID); err != nil {
			return nil, err
		}
		if !contains(t.Blocks, blockID) {
			t.Blocks = append(t.Blocks, blockID)
		}
		changed = true
	}
	for _, blockerID := range u.AddBlockedBy {
		if err := m.blockTaskLocked(blockerID, taskID); err != nil {
			return nil, err
		}
		if !contains(t.BlockedBy, blockerID) {
			t.BlockedBy = append(t.BlockedBy, blockerID)
		}
		changed = true
	}
	for _, blockerID := range u.RemoveBlockedBy {
		if err := m.unblockTaskLocked(blockerID, taskID); err != nil {
			return nil, err
		}
		t.BlockedBy = removeStr(t.BlockedBy, blockerID)
		changed = true
	}

	// --- metadata merge ---
	if u.Metadata != nil {
		if t.Metadata == nil {
			t.Metadata = make(map[string]any)
		}
		for k, v := range u.Metadata {
			if v == nil {
				delete(t.Metadata, k)
			} else {
				t.Metadata[k] = v
			}
		}
		changed = true
	}

	if changed {
		if err := m.saveLocked(t); err != nil {
			return nil, err
		}
	}
	return t, nil
}

// Delete soft-deletes a task (status → "deleted") and cleans up edges.
func (m *Manager) Delete(taskID string) error {
	_, err := m.Update(taskID, TaskUpdate{Status: strPtr("deleted")})
	return err
}

// List returns tasks matching the filter. By default excludes soft-deleted
// tasks unless the caller explicitly sets ExcludeDeleted=false.
func (m *Manager) List(filter ListFilter) ([]Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	entries, err := os.ReadDir(m.dir)
	if err != nil {
		return nil, fmt.Errorf("read tasks dir: %w", err)
	}

	var tasks []Task
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, taskFileSuffix) {
			continue
		}
		idStr := strings.TrimSuffix(name, taskFileSuffix)
		if _, err := strconv.Atoi(idStr); err != nil {
			continue // skip non-numeric filenames
		}

		t, err := m.loadOneFileLocked(filepath.Join(m.dir, name))
		if err != nil || t == nil {
			continue // corrupt file → skip (graceful degradation)
		}

		// By default, hide deleted tasks.
		if filter.ExcludeDeleted || filter.Status == "" {
			if t.Status == "deleted" {
				continue
			}
		}
		if filter.Owner != "" && t.Owner != filter.Owner {
			continue
		}
		if filter.Status != "" && t.Status != filter.Status {
			continue
		}

		// Filter out completed blockers from blockedBy for display.
		t = m.filterActiveBlockersLocked(t)

		tasks = append(tasks, *t)
	}

	// Stable order by ID (numeric).
	sort.Slice(tasks, func(i, j int) bool {
		a, _ := strconv.Atoi(tasks[i].ID)
		b, _ := strconv.Atoi(tasks[j].ID)
		return a < b
	})
	return tasks, nil
}

// HasIncomplete reports whether any non-deleted task is pending or in_progress.
func (m *Manager) HasIncomplete() (bool, error) {
	tasks, err := m.List(ListFilter{ExcludeDeleted: true})
	if err != nil {
		return false, err
	}
	for _, t := range tasks {
		if t.Status == "pending" || t.Status == "in_progress" {
			return true, nil
		}
	}
	return false, nil
}

// CountByStatus returns the number of tasks in each status.
func (m *Manager) CountByStatus() (pending, inProgress, completed int, err error) {
	tasks, err := m.List(ListFilter{ExcludeDeleted: true})
	if err != nil {
		return 0, 0, 0, err
	}
	for _, t := range tasks {
		switch t.Status {
		case "pending":
			pending++
		case "in_progress":
			inProgress++
		case "completed":
			completed++
		}
	}
	return
}

// Reset removes all task files and resets the high-watermark.
func (m *Manager) Reset() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	entries, err := os.ReadDir(m.dir)
	if err != nil {
		return fmt.Errorf("read tasks dir: %w", err)
	}

	// Record current max ID before deletion.
	maxID := m.readHighWatermarkLocked()
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, taskFileSuffix) {
			continue
		}
		idStr := strings.TrimSuffix(name, taskFileSuffix)
		if id, err := strconv.Atoi(idStr); err == nil && id > maxID {
			maxID = id
		}
		os.Remove(filepath.Join(m.dir, name))
	}

	m.writeHighWatermarkLocked(maxID)
	return nil
}

// ─── Dependency helpers ───────────────────────────────────────────────────────

// blockTaskLocked adds an edge: blocker blocks blocked.
// Both task files are updated. Caller must hold m.mu.
func (m *Manager) blockTaskLocked(blockerID, blockedID string) error {
	if blockerID == blockedID {
		return fmt.Errorf("task cannot block itself")
	}

	blocker, err := m.loadLocked(blockerID)
	if err != nil {
		return err
	}
	if blocker == nil {
		return fmt.Errorf("blocker task %s not found", blockerID)
	}

	blocked, err := m.loadLocked(blockedID)
	if err != nil {
		return err
	}
	if blocked == nil {
		return fmt.Errorf("blocked task %s not found", blockedID)
	}

	// Add blockedID to blocker.Blocks (dedup).
	if !contains(blocker.Blocks, blockedID) {
		blocker.Blocks = append(blocker.Blocks, blockedID)
	}
	// Add blockerID to blocked.BlockedBy (dedup).
	if !contains(blocked.BlockedBy, blockerID) {
		blocked.BlockedBy = append(blocked.BlockedBy, blockerID)
	}

	if err := m.saveLocked(blocker); err != nil {
		return err
	}
	return m.saveLocked(blocked)
}

// unblockTaskLocked removes an edge: blocker no longer blocks blocked.
func (m *Manager) unblockTaskLocked(blockerID, blockedID string) error {
	blocker, err := m.loadLocked(blockerID)
	if err != nil {
		return err
	}
	if blocker == nil {
		return nil // already gone
	}

	blocked, err := m.loadLocked(blockedID)
	if err != nil {
		return err
	}
	if blocked == nil {
		return nil
	}

	blocker.Blocks = removeStr(blocker.Blocks, blockedID)
	blocked.BlockedBy = removeStr(blocked.BlockedBy, blockerID)

	if err := m.saveLocked(blocker); err != nil {
		return err
	}
	return m.saveLocked(blocked)
}

// clearDependencyLocked removes completedID from every other task's blockedBy
// and removes the reverse edges from completed task's Blocks list.
func (m *Manager) clearDependencyLocked(completedID string) error {
	// Update all tasks that list completedID in their blockedBy.
	entries, err := os.ReadDir(m.dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, taskFileSuffix) {
			continue
		}
		idStr := strings.TrimSuffix(name, taskFileSuffix)
		if idStr == completedID {
			continue // skip self
		}

		path := filepath.Join(m.dir, name)
		t, err := m.loadOneFileLocked(path)
		if err != nil || t == nil {
			continue
		}
		if contains(t.BlockedBy, completedID) {
			t.BlockedBy = removeStr(t.BlockedBy, completedID)
			m.saveOneFileLocked(path, t)
		}
	}
	return nil
}

// filterActiveBlockersLocked returns a copy of t with completed/deleted entries
// removed from blockedBy (for display purposes — the on-disk data is unchanged).
func (m *Manager) filterActiveBlockersLocked(t *Task) *Task {
	if len(t.BlockedBy) == 0 {
		return t
	}

	filtered := make([]string, 0, len(t.BlockedBy))
	for _, bid := range t.BlockedBy {
		bt, err := m.loadLocked(bid)
		if err != nil || bt == nil {
			continue // doesn't exist → drop
		}
		if bt.Status == "completed" || bt.Status == "deleted" {
			continue // resolved → drop from display
		}
		filtered = append(filtered, bid)
	}

	copy := *t
	copy.BlockedBy = filtered
	return &copy
}

// ─── High-watermark ───────────────────────────────────────────────────────────

func (m *Manager) readHighWatermarkLocked() int {
	data, err := os.ReadFile(filepath.Join(m.dir, highWatermarkFile))
	if err != nil {
		return 0
	}
	v, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	return v
}

func (m *Manager) writeHighWatermarkLocked(id int) {
	os.WriteFile(filepath.Join(m.dir, highWatermarkFile),
		[]byte(strconv.Itoa(id)), 0600)
}

func (m *Manager) nextIDLocked() int {
	hwm := m.readHighWatermarkLocked()
	maxID := hwm

	entries, err := os.ReadDir(m.dir)
	if err != nil {
		return maxID + 1
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, taskFileSuffix) {
			continue
		}
		id, err := strconv.Atoi(strings.TrimSuffix(name, taskFileSuffix))
		if err != nil {
			continue
		}
		if id > maxID {
			maxID = id
		}
	}
	return maxID + 1
}

// ─── File I/O (caller must hold m.mu) ─────────────────────────────────────────

func (m *Manager) taskPath(id string) string {
	return filepath.Join(m.dir, id+taskFileSuffix)
}

func (m *Manager) saveLocked(t *Task) error {
	return m.saveOneFileLocked(m.taskPath(t.ID), t)
}

func (m *Manager) saveOneFileLocked(path string, t *Task) error {
	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal task %s: %w", t.ID, err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("write task %s: %w", t.ID, err)
	}
	return nil
}

func (m *Manager) loadLocked(taskID string) (*Task, error) {
	return m.loadOneFileLocked(m.taskPath(taskID))
}

func (m *Manager) loadOneFileLocked(path string) (*Task, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read task file: %w", err)
	}

	var t Task
	if err := json.Unmarshal(data, &t); err != nil {
		// Corrupt file → graceful degradation.
		return nil, nil
	}

	// Schema validation: required fields.
	if t.ID == "" || t.Subject == "" || !validStatus(t.Status) {
		return nil, nil
	}
	// Normalise empty slices (JSON unmarshal may produce nil slices).
	if t.Blocks == nil {
		t.Blocks = []string{}
	}
	if t.BlockedBy == nil {
		t.BlockedBy = []string{}
	}

	return &t, nil
}

// ─── TaskListID resolution ────────────────────────────────────────────────────

var defaultSessionID string

func init() {
	// Stable per-process session ID: hostname-pid-timestamp.
	host, _ := os.Hostname()
	defaultSessionID = fmt.Sprintf("%s-%d-%d", host, os.Getpid(), time.Now().Unix())
}

// ResolveTaskListID picks the task-list identifier using a 3-level priority:
//  1. TASK_AGENT_TASK_LIST_ID env var
//  2. Explicit team name (for swarm mode — reserved)
//  3. Process-level session ID (default)
func ResolveTaskListID(teamName string) string {
	if v := os.Getenv("TASK_AGENT_TASK_LIST_ID"); v != "" {
		return v
	}
	if teamName != "" {
		return teamName
	}
	return defaultSessionID
}

// ─── Utility ──────────────────────────────────────────────────────────────────

func contains(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

func removeStr(slice []string, s string) []string {
	out := make([]string, 0, len(slice))
	for _, v := range slice {
		if v != s {
			out = append(out, v)
		}
	}
	return out
}

func strPtr(s string) *string { return &s }
