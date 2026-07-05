package team

import (
	"crypto/rand"
	"fmt"
	"strings"
	"time"
)

// RequestStatus 是请求-响应协议的共享状态机状态。
// pending → approved | rejected
type RequestStatus string

const (
	RequestPending  RequestStatus = "pending"
	RequestApproved RequestStatus = "approved"
	RequestRejected RequestStatus = "rejected"
)

// ShutdownRequest 追踪一次关机请求的生命周期。
type ShutdownRequest struct {
	RequestID string        `json:"request_id"`
	Target    string        `json:"target"`           // 目标队友名
	Reason    string        `json:"reason,omitempty"` // 关机原因
	Status    RequestStatus `json:"status"`           // pending | approved | rejected
	Timestamp int64         `json:"timestamp"`
}

// PlanRequest 追踪一次计划审批请求的生命周期。
type PlanRequest struct {
	RequestID string        `json:"request_id"`
	From      string        `json:"from"`               // 发起计划的队友名
	Plan      string        `json:"plan"`               // 计划描述
	Status    RequestStatus `json:"status"`             // pending | approved | rejected
	Feedback  string        `json:"feedback,omitempty"` // 审批反馈
	Timestamp int64         `json:"timestamp"`
}

// newRequestID 生成一个简短的唯一请求 ID（8 位十六进制）。
func newRequestID() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("%08x", b)
}

// ── Tracker 方法（在 m.mu 保护下由 TeammateManager 调用）──

// trackShutdownRequest 创建一个新的关机请求并加入 tracker。
// 调用方必须持有 m.mu。
func (m *TeammateManager) trackShutdownRequest(target, reason string) *ShutdownRequest {
	req := &ShutdownRequest{
		RequestID: newRequestID(),
		Target:    target,
		Reason:    reason,
		Status:    RequestPending,
		Timestamp: time.Now().Unix(),
	}
	m.shutdownRequests[req.RequestID] = req
	return req
}

// trackPlanRequest 创建一个新的计划请求并加入 tracker。
// 调用方必须持有 m.mu。
func (m *TeammateManager) trackPlanRequest(from, plan string) *PlanRequest {
	req := &PlanRequest{
		RequestID: newRequestID(),
		From:      from,
		Plan:      plan,
		Status:    RequestPending,
		Timestamp: time.Now().Unix(),
	}
	m.planRequests[req.RequestID] = req
	return req
}

// resolveShutdownRequest 更新关机请求的状态。
// 调用方必须持有 m.mu。
func (m *TeammateManager) resolveShutdownRequest(requestID string, approve bool, reason string) error {
	req, ok := m.shutdownRequests[requestID]
	if !ok {
		return fmt.Errorf("shutdown_request %s: request not found", requestID)
	}
	if req.Status != RequestPending {
		return fmt.Errorf("shutdown_request %s: request is already %s, cannot respond again", requestID, req.Status)
	}
	if approve {
		req.Status = RequestApproved
	} else {
		req.Status = RequestRejected
	}
	req.Reason = reason
	return nil
}

// resolvePlanRequest 更新计划请求的状态。
// 调用方必须持有 m.mu。
func (m *TeammateManager) resolvePlanRequest(requestID string, approve bool, feedback string) error {
	req, ok := m.planRequests[requestID]
	if !ok {
		return fmt.Errorf("plan_request %s: request not found", requestID)
	}
	if req.Status != RequestPending {
		return fmt.Errorf("plan_request %s: request is already %s, cannot respond again", requestID, req.Status)
	}
	if approve {
		req.Status = RequestApproved
	} else {
		req.Status = RequestRejected
	}
	req.Feedback = feedback
	return nil
}

// getShutdownRequest 查询关机请求状态。
func (m *TeammateManager) getShutdownRequest(requestID string) *ShutdownRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.shutdownRequests[requestID]
}

// getPlanRequest 查询计划请求状态。
func (m *TeammateManager) getPlanRequest(requestID string) *PlanRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.planRequests[requestID]
}

// FormatInboxMessages 将收件箱消息渲染为 <team-inbox> XML 块。
// 协议消息（shutdown_request/response, plan_request/response）会包含
// request_id 和 approve 属性，确保 lead 和 teammate 都能正确引用。
// 当 msgs 为空时返回空字符串。
//
// 此函数是 runner.go:injectTeamInbox 和 teammate.go:drainInbox 的
// 共享渲染逻辑，保证两边始终同步。
func FormatInboxMessages(msgs []Message) string {
	if len(msgs) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("<team-inbox>\n")
	for _, msg := range msgs {
		attrs := fmt.Sprintf("from=%q type=%q", msg.From, msg.Type)
		if msg.RequestID != "" {
			attrs += fmt.Sprintf(" request_id=%q", msg.RequestID)
		}
		if msg.Approve != nil {
			attrs += fmt.Sprintf(" approve=%v", *msg.Approve)
		}
		b.WriteString(fmt.Sprintf("  <message %s>%s</message>\n", attrs, msg.Content))
	}
	b.WriteString("</team-inbox>")
	return b.String()
}
