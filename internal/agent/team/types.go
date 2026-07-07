// Package team implements persistent agent teammates that communicate
// through JSONL inboxes, inspired by the learn-claude-code s09 tutorial.
//
// Teammate lifecycle:
//
//	spawn -> WORKING -> IDLE -> WORKING -> ... -> SHUTDOWN
//
// Communication:
//
//	+--------+    send("alice","bob","...")    +--------+
//	| alice  | -----------------------------> |  bob   |
//	| loop   |    bob.jsonl << {json_line}    |  loop  |
//	+--------+                                +--------+
//	     ^                                         |
//	     |        read_inbox("alice")              |
//	     +---- alice.jsonl -> read + drain ---------+
package team

// Status represents the life-cycle state of a teammate.
type Status string

const (
	StatusWorking  Status = "working"
	StatusIdle     Status = "idle"
	StatusShutdown Status = "shutdown"
)

// Teammate describes a single team member stored in the roster (config.json).
type Teammate struct {
	Name   string `json:"name"`
	Role   string `json:"role"`
	Status Status `json:"status"`
	Model  string `json:"model,omitempty"` // model used by this teammate
}

// Message is one line in a JSONL inbox file.
// Protocol messages (shutdown_request/response, plan_request/response) use
// the RequestID and Approve fields for request-response correlation.
type Message struct {
	Type      string `json:"type"` // "message" | "broadcast" | "shutdown_request" | "shutdown_response" | "plan_request" | "plan_response"
	From      string `json:"from"`
	Content   string `json:"content"`
	RequestID string `json:"request_id,omitempty"` // 关联请求 ID（协议消息使用）
	Approve   *bool  `json:"approve,omitempty"`    // 批准/拒绝（响应消息使用，指针区分未设置与 false）
	Timestamp int64  `json:"timestamp"`
}

// Config is the team roster persisted as team/config.json.
type Config struct {
	Lead    string     `json:"lead"`    // lead agent name (the main agent)
	Members []Teammate `json:"members"` // all teammates (including idle/shutdown)
}
