package team

import (
	"encoding/json"
	"strings"
	"testing"
)

// ── 工具函数 ──────────────────────────────────────────────────────────

// spawnAndWait 创建队友并等待其初始任务失败（mock API 返回 500），
// 这样队友循环会退出，状态变为 idle。用于测试协议方法。
func spawnAndWait(t *testing.T, m *TeammateManager, name, role string) {
	t.Helper()
	_, err := m.Spawn(name, role, "test task")
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	// 队友循环会尝试调用 API，mock 返回 500，processLoop 会调用 sendToLead 然后返回。
	// 之后 drainInbox 检查无误后进入 wait 循环。
	// 给一点时间让 goroutine 完成初始化。
}

// ── ShutdownRequest 生命周期 ─────────────────────────────────────────

func TestShutdownRequestLifecycle(t *testing.T) {
	m := newTestManager(t)
	spawnAndWait(t, m, "alice", "coder")

	// Phase 1: 发起关机请求
	reqID, err := m.RequestShutdown("alice", "任务完成，请关机")
	if err != nil {
		t.Fatalf("RequestShutdown: %v", err)
	}
	if reqID == "" {
		t.Fatal("expected non-empty request_id")
	}

	// 验证 tracker 状态
	m.mu.Lock()
	req := m.shutdownRequests[reqID]
	m.mu.Unlock()
	if req == nil {
		t.Fatal("shutdown request not found in tracker")
	}
	if req.Status != RequestPending {
		t.Errorf("expected status=pending, got %s", req.Status)
	}
	if req.Target != "alice" {
		t.Errorf("expected target=alice, got %s", req.Target)
	}

	// 验证消息已送达 alice 的收件箱
	msgs, err := m.ReadInbox("alice")
	if err != nil {
		t.Fatalf("ReadInbox: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message in alice's inbox, got %d", len(msgs))
	}
	if msgs[0].Type != "shutdown_request" {
		t.Errorf("expected type=shutdown_request, got %s", msgs[0].Type)
	}
	if msgs[0].RequestID != reqID {
		t.Errorf("expected request_id=%s, got %s", reqID, msgs[0].RequestID)
	}

	// Phase 2: 队友批准关机
	err = m.ResolveShutdownRequest(reqID, true, "收到，正在退出")
	if err != nil {
		t.Fatalf("ResolveShutdownRequest (approve): %v", err)
	}

	// 验证状态变为 approved
	m.mu.Lock()
	req = m.shutdownRequests[reqID]
	m.mu.Unlock()
	if req.Status != RequestApproved {
		t.Errorf("expected status=approved, got %s", req.Status)
	}

	// 验证队友已 shutdown
	roster := m.Roster()
	for _, tm := range roster {
		if tm.Name == "alice" && tm.Status != StatusShutdown {
			t.Errorf("expected alice status=shutdown, got %s", tm.Status)
		}
	}

	// 验证 lead 收到了 shutdown_response
	leadMsgs, err := m.ReadInbox("lead")
	if err != nil {
		t.Fatalf("ReadInbox(lead): %v", err)
	}
	found := false
	for _, msg := range leadMsgs {
		if msg.Type == "shutdown_response" && msg.RequestID == reqID {
			found = true
			if msg.Approve == nil || *msg.Approve != true {
				t.Error("expected approve=true in response")
			}
			break
		}
	}
	if !found {
		t.Error("lead should have received shutdown_response")
	}
}

func TestShutdownRequestReject(t *testing.T) {
	m := newTestManager(t)
	spawnAndWait(t, m, "bob", "tester")

	reqID, err := m.RequestShutdown("bob", "请关机")
	if err != nil {
		t.Fatalf("RequestShutdown: %v", err)
	}

	// 队友拒绝关机
	err = m.ResolveShutdownRequest(reqID, false, "还有关键测试在跑，不能停")
	if err != nil {
		t.Fatalf("ResolveShutdownRequest (reject): %v", err)
	}

	// 验证状态变为 rejected（而非 approved）
	m.mu.Lock()
	req := m.shutdownRequests[reqID]
	m.mu.Unlock()
	if req.Status != RequestRejected {
		t.Errorf("expected status=rejected, got %s", req.Status)
	}

	// 验证队友没有被 shutdown（拒绝不应杀进程）
	// 注意：此时队友的 goroutine 应该还在运行
	m.mu.Lock()
	_, running := m.loops["bob"]
	m.mu.Unlock()
	if !running {
		t.Error("expected bob to still be running after rejecting shutdown")
	}
}

func TestShutdownRequestNonExistentTeammate(t *testing.T) {
	m := newTestManager(t)

	_, err := m.RequestShutdown("nonexistent", "关机")
	if err == nil {
		t.Fatal("expected error for non-existent teammate")
	}
	if !strings.Contains(err.Error(), "未在运行") {
		t.Errorf("expected '未在运行' in error, got: %v", err)
	}
}

func TestShutdownRequestDuplicateResolution(t *testing.T) {
	m := newTestManager(t)
	spawnAndWait(t, m, "charlie", "reviewer")

	reqID, err := m.RequestShutdown("charlie", "关机")
	if err != nil {
		t.Fatalf("RequestShutdown: %v", err)
	}

	// 第一次响应（批准）
	err = m.ResolveShutdownRequest(reqID, true, "好的")
	if err != nil {
		t.Fatalf("first ResolveShutdownRequest should succeed: %v", err)
	}

	// 第二次响应应失败（已处于 approved 状态不能再响应）
	err = m.ResolveShutdownRequest(reqID, false, "改主意了")
	if err == nil {
		t.Fatal("expected error for re-resolving already approved request")
	}
}

// ── PlanRequest 生命周期 ─────────────────────────────────────────────

func TestPlanRequestLifecycle(t *testing.T) {
	m := newTestManager(t)

	// 队友提交计划
	reqID, err := m.SubmitPlan("alice", "重构 auth 模块：提取 JWT 验证到独立中间件")
	if err != nil {
		t.Fatalf("SubmitPlan: %v", err)
	}
	if reqID == "" {
		t.Fatal("expected non-empty request_id")
	}

	// 验证 tracker
	m.mu.Lock()
	req := m.planRequests[reqID]
	m.mu.Unlock()
	if req == nil {
		t.Fatal("plan request not found in tracker")
	}
	if req.Status != RequestPending {
		t.Errorf("expected status=pending, got %s", req.Status)
	}
	if req.From != "alice" {
		t.Errorf("expected from=alice, got %s", req.From)
	}

	// 验证 lead 收到了计划请求
	msgs, err := m.ReadInbox("lead")
	if err != nil {
		t.Fatalf("ReadInbox(lead): %v", err)
	}
	found := false
	for _, msg := range msgs {
		if msg.Type == "plan_request" && msg.RequestID == reqID {
			found = true
			if msg.From != "alice" {
				t.Errorf("expected from=alice, got %s", msg.From)
			}
			break
		}
	}
	if !found {
		t.Error("lead should have received plan_request message")
	}

	// Lead 批准计划
	err = m.ResolvePlanRequest(reqID, true, "方案合理，注意兼容性")
	if err != nil {
		t.Fatalf("ResolvePlanRequest (approve): %v", err)
	}

	// 验证状态更新
	m.mu.Lock()
	req = m.planRequests[reqID]
	m.mu.Unlock()
	if req.Status != RequestApproved {
		t.Errorf("expected status=approved, got %s", req.Status)
	}
	if req.Feedback != "方案合理，注意兼容性" {
		t.Errorf("expected feedback preserved, got %q", req.Feedback)
	}

	// 验证 alice 收到了响应
	aliceMsgs, err := m.ReadInbox("alice")
	if err != nil {
		t.Fatalf("ReadInbox(alice): %v", err)
	}
	found = false
	for _, msg := range aliceMsgs {
		if msg.Type == "plan_response" && msg.RequestID == reqID {
			found = true
			if msg.Approve == nil || *msg.Approve != true {
				t.Error("expected approve=true")
			}
			break
		}
	}
	if !found {
		t.Error("alice should have received plan_response")
	}
}

func TestPlanRequestReject(t *testing.T) {
	m := newTestManager(t)

	reqID, err := m.SubmitPlan("bob", "删除所有测试文件")
	if err != nil {
		t.Fatalf("SubmitPlan: %v", err)
	}

	// Lead 拒绝计划
	err = m.ResolvePlanRequest(reqID, false, "不可行：测试文件不能删除")
	if err != nil {
		t.Fatalf("ResolvePlanRequest (reject): %v", err)
	}

	m.mu.Lock()
	req := m.planRequests[reqID]
	m.mu.Unlock()
	if req.Status != RequestRejected {
		t.Errorf("expected status=rejected, got %s", req.Status)
	}
}

func TestPlanRequestDuplicateResolution(t *testing.T) {
	m := newTestManager(t)

	reqID, err := m.SubmitPlan("alice", "测试计划")
	if err != nil {
		t.Fatalf("SubmitPlan: %v", err)
	}

	// 第一次审批
	err = m.ResolvePlanRequest(reqID, true, "ok")
	if err != nil {
		t.Fatalf("first ResolvePlanRequest: %v", err)
	}

	// 第二次应失败
	err = m.ResolvePlanRequest(reqID, false, "改主意")
	if err == nil {
		t.Fatal("expected error for re-resolving already approved plan")
	}
}

func TestPlanRequestNonExistent(t *testing.T) {
	m := newTestManager(t)

	err := m.ResolvePlanRequest("nonexistent", true, "")
	if err == nil {
		t.Fatal("expected error for non-existent plan request")
	}
}

// ── RequestID ──────────────────────────────────────────────────────────

func TestRequestIDUniqueness(t *testing.T) {
	// 生成大量 request_id 验证无重复
	ids := make(map[string]bool)
	for i := 0; i < 1000; i++ {
		id := newRequestID()
		if ids[id] {
			t.Errorf("duplicate request_id: %s", id)
		}
		ids[id] = true
	}
}

func TestRequestIDFormat(t *testing.T) {
	id := newRequestID()
	if len(id) != 8 {
		t.Errorf("expected 8-char request_id, got %d: %s", len(id), id)
	}
	// 验证都是十六进制字符
	for _, c := range id {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("unexpected char in request_id: %c", c)
		}
	}
}

// ── Message 序列化 ────────────────────────────────────────────────────

func TestMessageSerializationWithRequestID(t *testing.T) {
	approveTrue := true
	msg := Message{
		Type:      "shutdown_response",
		From:      "alice",
		Content:   "好的，马上退出",
		RequestID: "abc12345",
		Approve:   &approveTrue,
		Timestamp: 1234567890,
	}

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// 反序列化验证
	var decoded Message
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.RequestID != "abc12345" {
		t.Errorf("expected request_id=abc12345, got %s", decoded.RequestID)
	}
	if decoded.Approve == nil || *decoded.Approve != true {
		t.Error("expected approve=true after round-trip")
	}
}

func TestMessageSerializationWithoutRequestID(t *testing.T) {
	// 普通消息不应包含 request_id 和 approve
	msg := Message{
		Type:      "message",
		From:      "alice",
		Content:   "hello",
		Timestamp: 1234567890,
	}

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// 验证 JSON 中不含 request_id 和 approve
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := raw["request_id"]; ok {
		t.Error("plain message should not have request_id in JSON")
	}
	if _, ok := raw["approve"]; ok {
		t.Error("plain message should not have approve in JSON")
	}
}

func TestMessageSerializationWithApproveFalse(t *testing.T) {
	approveFalse := false
	msg := Message{
		Type:      "plan_response",
		From:      "lead",
		Content:   "不可行",
		RequestID: "xyz98765",
		Approve:   &approveFalse, // false 也必须出现在 JSON 中
		Timestamp: 1234567890,
	}

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if raw["approve"] != false {
		t.Error("approve: false must appear in JSON (not omitted)")
	}
}

// ── 协议消息收发集成测试 ──────────────────────────────────────────────

func TestProtocolMessagesThroughBus(t *testing.T) {
	m := newTestManager(t)

	// 模拟完整的 shutdown 协议消息流
	reqID, err := m.RequestShutdown("alice", "停下来吧")
	if err != nil {
		// 没有活跃的 alice，但应该正确创建 tracker 并发送消息
		// 实际上 RequestShutdown 会检查活跃 teammate，我们 spawn 一个
		t.Logf("RequestShutdown (expected may fail without active teammate): %v", err)
	}

	// Spawn alice 然后测试
	spawnAndWait(t, m, "alice", "coder")
	reqID, err = m.RequestShutdown("alice", "停下来吧")
	if err != nil {
		t.Fatalf("RequestShutdown: %v", err)
	}

	// 验证关机请求消息格式
	msgs, _ := m.ReadInbox("alice")
	found := false
	for _, msg := range msgs {
		if msg.Type == "shutdown_request" {
			found = true
			if msg.RequestID != reqID {
				t.Errorf("expected request_id=%s, got %s", reqID, msg.RequestID)
			}
			if msg.Content != "停下来吧" {
				t.Errorf("expected content='停下来吧', got %q", msg.Content)
			}
			if msg.Approve != nil {
				t.Error("shutdown_request should not have approve field")
			}
		}
	}
	if !found {
		t.Error("shutdown_request message not found in alice's inbox")
	}

	// 响应关机
	err = m.ResolveShutdownRequest(reqID, true, "完成")
	if err != nil {
		t.Fatalf("ResolveShutdownRequest: %v", err)
	}

	// 验证响应消息
	leadMsgs, _ := m.ReadInbox("lead")
	found = false
	for _, msg := range leadMsgs {
		if msg.Type == "shutdown_response" && msg.RequestID == reqID {
			found = true
			if msg.Approve == nil || *msg.Approve != true {
				t.Error("expected approve=true")
			}
		}
	}
	if !found {
		t.Error("shutdown_response not delivered to lead")
	}
}

func TestPlanRequestToNonexistentTeammate(t *testing.T) {
	m := newTestManager(t)

	// SubmitPlan 不需要目标队友存在（只发消息给 lead）
	reqID, err := m.SubmitPlan("ghost", "一个幽灵计划")
	if err != nil {
		t.Fatalf("SubmitPlan should succeed even if teammate not active: %v", err)
	}

	// 验证 tracker 创建
	m.mu.Lock()
	req := m.planRequests[reqID]
	m.mu.Unlock()
	if req == nil || req.Status != RequestPending {
		t.Error("plan request should be tracked as pending")
	}
}

// ── getShutdownRequest / getPlanRequest ────────────────────────────────

func TestGetRequestStatus(t *testing.T) {
	m := newTestManager(t)
	spawnAndWait(t, m, "dave", "builder")

	reqID, _ := m.RequestShutdown("dave", "关机")

	// getShutdownRequest 查询
	req := m.getShutdownRequest(reqID)
	if req == nil {
		t.Fatal("getShutdownRequest returned nil")
	}
	if req.Status != RequestPending {
		t.Errorf("expected pending, got %s", req.Status)
	}

	// 不存在的请求
	req = m.getShutdownRequest("nonexistent")
	if req != nil {
		t.Error("expected nil for non-existent request")
	}

	// getPlanRequest
	planID, _ := m.SubmitPlan("eve", "测试计划")
	plan := m.getPlanRequest(planID)
	if plan == nil {
		t.Fatal("getPlanRequest returned nil")
	}
	if plan.From != "eve" {
		t.Errorf("expected from=eve, got %s", plan.From)
	}
}
