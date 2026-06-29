package team

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"task-agent/internal/agent/tasks"
)

// newTestLoop 创建一个用于单元测试的 teammateLoop。
// 不启动 goroutine，仅用于测试方法逻辑。
func newTestLoop(t *testing.T, name string) *teammateLoop {
	t.Helper()

	tl := &teammateLoop{
		name:             name,
		role:             "tester",
		messages:         nil,
		mgr:              nil,
		wakeCh:           make(chan struct{}, 1),
		quitCh:           make(chan struct{}),
		idlePollInterval: 10 * time.Millisecond,
		idleTimeout:      100 * time.Millisecond,
		idleMaxIter:      3,
	}

	// 设置 mgr 以便 injectIdentity 可以访问 config.Lead
	tl.mgr = &TeammateManager{
		config: Config{Lead: "lead"},
	}

	return tl
}

// ── injectIdentity 测试 ─────────────────────────────────────────────

func TestInjectIdentity(t *testing.T) {
	tl := newTestLoop(t, "alice")

	if len(tl.messages) != 0 {
		t.Fatalf("expected 0 messages, got %d", len(tl.messages))
	}

	tl.injectIdentity()

	if len(tl.messages) != 2 {
		t.Fatalf("expected 2 messages after injectIdentity, got %d", len(tl.messages))
	}

	// 第一条是 user 消息，包含 <identity>
	userMsg := tl.messages[0]
	if userMsg.Role != "user" {
		t.Errorf("expected role='user', got %q", userMsg.Role)
	}
	if len(userMsg.Content) == 0 || userMsg.Content[0].OfText == nil {
		t.Fatal("expected text content in first message")
	}
	userText := userMsg.Content[0].OfText.Text
	if !strings.Contains(userText, "<identity>") {
		t.Errorf("expected <identity> tag in user message, got: %s", userText)
	}
	if !strings.Contains(userText, "alice") {
		t.Errorf("expected 'alice' in identity message, got: %s", userText)
	}

	// 第二条是 assistant 确认
	assistantMsg := tl.messages[1]
	if assistantMsg.Role != "assistant" {
		t.Errorf("expected role='assistant', got %q", assistantMsg.Role)
	}
	if len(assistantMsg.Content) == 0 || assistantMsg.Content[0].OfText == nil {
		t.Fatal("expected text content in second message")
	}
	assistantText := assistantMsg.Content[0].OfText.Text
	if !strings.Contains(assistantText, "Continuing") {
		t.Errorf("expected 'Continuing' in assistant message, got: %s", assistantText)
	}
}

func TestInjectIdentityPrependsToExistingMessages(t *testing.T) {
	tl := newTestLoop(t, "bob")

	// 放入一条已有消息
	tl.messages = []anthropic.BetaMessageParam{
		{Role: "user", Content: []anthropic.BetaContentBlockParamUnion{
			{OfText: &anthropic.BetaTextBlockParam{Text: "do something"}},
		}},
	}

	tl.injectIdentity()

	if len(tl.messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(tl.messages))
	}

	// 原有消息应该在索引 2
	if tl.messages[2].Content[0].OfText.Text != "do something" {
		t.Errorf("original message shifted or lost")
	}

	// 第一条应该是 identity
	if !strings.Contains(tl.messages[0].Content[0].OfText.Text, "bob") {
		t.Errorf("identity should mention 'bob'")
	}
}

// ── IdleTool 测试 ───────────────────────────────────────────────────

func TestIdleTool(t *testing.T) {
	tl := newTestLoop(t, "alice")

	tool := &IdleTool{loop: tl}

	// 初始状态：idleRequested 应为 false
	if tl.idleRequested {
		t.Error("idleRequested should be false initially")
	}

	// 执行 idle 工具
	result, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("IdleTool.Execute: %v", err)
	}

	if !tl.idleRequested {
		t.Error("idleRequested should be true after idle tool call")
	}

	if len(result) == 0 || result[0].OfText == nil {
		t.Fatal("expected text result")
	}
}

// ── ClaimTaskTool 测试 ──────────────────────────────────────────────

func TestClaimTaskTool(t *testing.T) {
	// 创建真实 taskMgr
	tmpDir := t.TempDir()
	taskMgr, err := tasks.NewManager(tmpDir, "test-claim")
	if err != nil {
		t.Fatalf("tasks.NewManager: %v", err)
	}

	// 创建未分配任务
	task, err := taskMgr.Create("test task", "a task to claim")
	if err != nil {
		t.Fatalf("Create task: %v", err)
	}

	tl := newTestLoop(t, "claimer")
	tl.mgr.taskMgr = taskMgr

	tool := &ClaimTaskTool{loop: tl}

	input := json.RawMessage(fmt.Sprintf(`{"task_id": "%s"}`, task.ID))
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("ClaimTaskTool.Execute: %v", err)
	}

	// 验证结果
	if len(result) == 0 || result[0].OfText == nil {
		t.Fatal("expected text result")
	}
	if !strings.Contains(result[0].OfText.Text, task.ID) {
		t.Errorf("result should mention task ID: %s", result[0].OfText.Text)
	}

	// 验证任务已更新
	updated, err := taskMgr.Get(task.ID)
	if err != nil {
		t.Fatalf("Get task: %v", err)
	}
	if updated.Owner != "claimer" {
		t.Errorf("expected owner='claimer', got %q", updated.Owner)
	}
	if updated.Status != "in_progress" {
		t.Errorf("expected status='in_progress', got %q", updated.Status)
	}
}

func TestClaimTaskToolMissingID(t *testing.T) {
	tl := newTestLoop(t, "alice")
	tool := &ClaimTaskTool{loop: tl}

	_, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error for missing task_id")
	}
}

// ── idlePoll 测试 ───────────────────────────────────────────────────

func TestIdlePollQuitChannel(t *testing.T) {
	tl := newTestLoop(t, "alice")

	// 提前关闭 quitCh，idlePoll 应立即返回 false
	close(tl.quitCh)

	hasWork := tl.idlePoll()
	if hasWork {
		t.Error("idlePoll should return false when quitCh is closed")
	}
}

func TestIdlePollInboxWakeup(t *testing.T) {
	tl := newTestLoop(t, "alice")

	// 设置 mgr 以便访问 bus
	teamDir := filepath.Join(t.TempDir(), "team")
	bus, err := NewMessageBus(teamDir)
	if err != nil {
		t.Fatalf("NewMessageBus: %v", err)
	}
	tl.mgr.bus = bus
	tl.mgr.dir = teamDir

	// 在收件箱中放入一条消息
	err = bus.Send("alice", Message{
		Type:      "message",
		From:      "lead",
		Content:   "new task for you",
		Timestamp: time.Now().Unix(),
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	// idlePoll 应检测到收件箱消息并返回 true
	hasWork := tl.idlePoll()
	if !hasWork {
		t.Error("idlePoll should return true when inbox has messages")
	}

	// 验证消息已被注入到 tl.messages
	if len(tl.messages) == 0 {
		t.Fatal("expected inbox message injected into tl.messages")
	}
	found := false
	for _, msg := range tl.messages {
		for _, block := range msg.Content {
			if block.OfText != nil && strings.Contains(block.OfText.Text, "new task for you") {
				found = true
			}
		}
	}
	if !found {
		t.Error("injected message should contain 'new task for you'")
	}
}

func TestIdlePollTimeout(t *testing.T) {
	tl := newTestLoop(t, "alice")
	tl.idleMaxIter = 2
	tl.idlePollInterval = 5 * time.Millisecond

	// 为 bus 设置空收件箱，避免 nil pointer dereference
	teamDir := filepath.Join(t.TempDir(), "team")
	bus, err := NewMessageBus(teamDir)
	if err != nil {
		t.Fatalf("NewMessageBus: %v", err)
	}
	tl.mgr.bus = bus
	tl.mgr.dir = teamDir

	// 没有收件箱消息，没有任务 — 应超时返回 false
	hasWork := tl.idlePoll()
	if hasWork {
		t.Error("idlePoll should return false on timeout (no work)")
	}
}

// ── claimAndInject 测试 ─────────────────────────────────────────────

func TestClaimAndInject(t *testing.T) {
	tmpDir := t.TempDir()
	taskMgr, err := tasks.NewManager(tmpDir, "test-claim-inject")
	if err != nil {
		t.Fatalf("tasks.NewManager: %v", err)
	}

	// 创建一个未分配任务
	task, err := taskMgr.Create("urgent fix", "fix the critical bug")
	if err != nil {
		t.Fatalf("Create task: %v", err)
	}

	tl := newTestLoop(t, "worker")
	tl.mgr.taskMgr = taskMgr

	// 初始无消息
	if len(tl.messages) != 0 {
		t.Fatalf("expected 0 messages initially, got %d", len(tl.messages))
	}

	claimed := tl.claimAndInject()
	if !claimed {
		t.Fatal("claimAndInject should return true for unclaimed task")
	}

	// 验证消息注入
	if len(tl.messages) != 1 {
		t.Fatalf("expected 1 message after claimAndInject, got %d", len(tl.messages))
	}
	injectedText := tl.messages[0].Content[0].OfText.Text
	if !strings.Contains(injectedText, "<auto-claimed>") {
		t.Errorf("expected <auto-claimed> block, got: %s", injectedText)
	}
	if !strings.Contains(injectedText, task.ID) {
		t.Errorf("expected task ID %s in claim block, got: %s", task.ID, injectedText)
	}
	if !strings.Contains(injectedText, "urgent fix") {
		t.Errorf("expected subject in claim block")
	}

	// 验证任务状态已更新
	updated, err := taskMgr.Get(task.ID)
	if err != nil {
		t.Fatalf("Get task: %v", err)
	}
	if updated.Owner != "worker" {
		t.Errorf("expected owner='worker', got %q", updated.Owner)
	}
	if updated.Status != "in_progress" {
		t.Errorf("expected status='in_progress', got %q", updated.Status)
	}
}

func TestClaimAndInjectNoTasks(t *testing.T) {
	tl := newTestLoop(t, "worker")

	claimed := tl.claimAndInject()
	if claimed {
		t.Error("claimAndInject should return false when taskMgr is nil")
	}

	if len(tl.messages) != 0 {
		t.Errorf("expected 0 messages, got %d", len(tl.messages))
	}
}
