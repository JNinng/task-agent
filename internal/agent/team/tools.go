package team

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"

	"task-agent/internal/agent/tasks"
)

// ── TeamSpawnTool ───────────────────────────────────────────────────

// SpawnTool creates a new teammate and starts its agent loop.
// This is the lead agent's spawn tool.
type SpawnTool struct {
	Mgr *TeammateManager
}

func (t *SpawnTool) Name() string { return "team_spawn" }

func (t *SpawnTool) Description() string {
	return "DELEGATE a task to a persistent teammate. This should be your FIRST action — " +
		"do NOT run bash/read_file/listing before spawning. The teammate does all " +
		"exploration and work independently; your job is to hand off, not prepare.\n\n" +
		"USAGE RULES:\n" +
		"- Call this IMMEDIATELY when the user asks for work a teammate can do.\n" +
		"- Write a COMPLETE prompt: include the exact commands to run, what to check,\n" +
		"  and how to report results. The teammate sees only this prompt.\n" +
		"- Do NOT explore the project first. The teammate explores.\n" +
		"- Results are auto-delivered to your inbox — you will see them without polling.\n\n" +
		"EXAMPLE: user says 'run tests' →\n" +
		"  team_spawn(name='tester', role='tester',\n" +
		"    prompt='Run: cd F:/path && go test ./... -v -count=1 && go vet ./...\n" +
		"    Report: pass/fail counts, failures with file:line, and any vet warnings.')\n\n" +
		"After spawning, do NOT call team_inbox. The teammate's reply arrives automatically."
}

func (t *SpawnTool) InputSchema() anthropic.BetaToolInputSchemaParam {
	return anthropic.BetaToolInputSchemaParam{
		Properties: map[string]any{
			"name": map[string]any{
				"type": "string",
				"description": "Short unique name (e.g. 'tester', 'builder', 'reviewer'). " +
					"Use a descriptive role-based name.",
			},
			"role": map[string]any{
				"type":        "string",
				"description": "What this teammate does (e.g. 'Go test runner', 'code reviewer').",
			},
			"prompt": map[string]any{
				"type": "string",
				"description": "Complete task instructions. Be SPECIFIC: include exact commands, " +
					"file paths, expected outputs, and how to report results. " +
					"The teammate sees ONLY this prompt — it must be self-contained. " +
					"Example: 'cd F:/project && go test ./... -v -count=1 2>&1. " +
					"Report: total passed/failed, each failure with file:line and error message.'",
			},
		},
		Required: []string{"name", "role", "prompt"},
	}
}

func (t *SpawnTool) Execute(_ context.Context, input json.RawMessage) ([]anthropic.BetaToolResultBlockParamContentUnion, error) {
	var args struct {
		Name   string `json:"name"`
		Role   string `json:"role"`
		Prompt string `json:"prompt"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return nil, fmt.Errorf("team_spawn: %w", err)
	}
	if args.Name == "" || args.Role == "" || args.Prompt == "" {
		return nil, fmt.Errorf("team_spawn: name, role, and prompt are required")
	}

	tm, err := t.Mgr.Spawn(args.Name, args.Role, args.Prompt)
	if err != nil {
		return nil, err
	}

	result := fmt.Sprintf("Teammate '%s' spawned (role: %s, status: %s). "+
		"Their result will be auto-delivered when done — no need to poll.",
		tm.Name, tm.Role, tm.Status)
	return []anthropic.BetaToolResultBlockParamContentUnion{
		{OfText: &anthropic.BetaTextBlockParam{Text: result}},
	}, nil
}

// ── TeamSendTool ────────────────────────────────────────────────────

// SendTool sends a message to a teammate (or broadcasts to all).
// The lead agent uses this to assign work, request status, or coordinate.
type SendTool struct {
	Mgr        *TeammateManager
	SenderName string // typically "lead"
}

func (t *SendTool) Name() string { return "team_send" }

func (t *SendTool) Description() string {
	return "Send a follow-up message to a teammate or broadcast to all. " +
		"Use this ONLY for follow-up work after the teammate is already spawned — " +
		"the initial task should be in the team_spawn prompt. " +
		"Use cases: assign additional work, ask for status update, request a specific check. " +
		"Set to='all' to broadcast to every teammate. " +
		"Recipients are automatically woken to process your message."
}

func (t *SendTool) InputSchema() anthropic.BetaToolInputSchemaParam {
	return anthropic.BetaToolInputSchemaParam{
		Properties: map[string]any{
			"to": map[string]any{
				"type":        "string",
				"description": "Teammate name to send to, or 'all' to broadcast to every teammate.",
			},
			"content": map[string]any{
				"type":        "string",
				"description": "The message content. Be clear and specific about what you need.",
			},
			"msg_type": map[string]any{
				"type":        "string",
				"description": "Message type: 'message' (default) or 'broadcast'.",
			},
		},
		Required: []string{"to", "content"},
	}
}

func (t *SendTool) Execute(_ context.Context, input json.RawMessage) ([]anthropic.BetaToolResultBlockParamContentUnion, error) {
	var args struct {
		To      string `json:"to"`
		Content string `json:"content"`
		MsgType string `json:"msg_type"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return nil, fmt.Errorf("team_send: %w", err)
	}
	if args.To == "" || args.Content == "" {
		return nil, fmt.Errorf("team_send: 'to' and 'content' are required")
	}

	if err := t.Mgr.Send(t.SenderName, args.To, args.Content, args.MsgType); err != nil {
		return nil, err
	}

	target := args.To
	if target == "all" {
		target = "all teammates"
	}
	result := fmt.Sprintf("Message sent to %s.", target)
	return []anthropic.BetaToolResultBlockParamContentUnion{
		{OfText: &anthropic.BetaTextBlockParam{Text: result}},
	}, nil
}

// ── TeamInboxTool ───────────────────────────────────────────────────

// TeamInboxTool reads and drains the lead agent's inbox.
// Teammates send replies here; the lead checks this to see their responses.
type TeamInboxTool struct {
	Mgr        *TeammateManager
	ReaderName string // typically "lead"
}

func (t *TeamInboxTool) Name() string { return "team_inbox" }

func (t *TeamInboxTool) Description() string {
	return "EMERGENCY-ONLY: manually read and drain your inbox. " +
		"DO NOT USE THIS ROUTINELY. Teammate replies are automatically injected " +
		"into your context before each response — you see them without asking. " +
		"Calling this repeatedly wastes tokens and is never necessary. " +
		"Only use team_inbox when you have a specific reason to drain the inbox " +
		"mid-turn (e.g., a command requires the raw message list)."
}

func (t *TeamInboxTool) InputSchema() anthropic.BetaToolInputSchemaParam {
	return anthropic.BetaToolInputSchemaParam{
		Properties: map[string]any{},
		Required:   []string{},
	}
}

func (t *TeamInboxTool) Execute(_ context.Context, input json.RawMessage) ([]anthropic.BetaToolResultBlockParamContentUnion, error) {
	msgs, err := t.Mgr.ReadInbox(t.ReaderName)
	if err != nil {
		return nil, fmt.Errorf("team_inbox: %w", err)
	}

	if len(msgs) == 0 {
		return []anthropic.BetaToolResultBlockParamContentUnion{
			{OfText: &anthropic.BetaTextBlockParam{Text: "(inbox empty)"}},
		}, nil
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Inbox (%d messages):\n", len(msgs)))
	for _, msg := range msgs {
		b.WriteString(fmt.Sprintf("  [%s] %s: %s\n", msg.Type, msg.From, msg.Content))
	}
	return []anthropic.BetaToolResultBlockParamContentUnion{
		{OfText: &anthropic.BetaTextBlockParam{Text: b.String()}},
	}, nil
}

// ── teammateSendTool (teammate-internal) ──────────────────────────────

// teammateSendTool is the team_send tool available to teammates so they
// can reply to the lead or communicate with other teammates.
type teammateSendTool struct {
	loop *teammateLoop
}

func (t *teammateSendTool) Name() string { return "team_send" }

func (t *teammateSendTool) Description() string {
	return "Send a message to the lead or another teammate. Use this to report results, ask questions, or coordinate."
}

func (t *teammateSendTool) InputSchema() anthropic.BetaToolInputSchemaParam {
	return anthropic.BetaToolInputSchemaParam{
		Properties: map[string]any{
			"to": map[string]any{
				"type":        "string",
				"description": "The name of the teammate to send to, 'lead' for the team leader, or 'all' to broadcast.",
			},
			"content": map[string]any{
				"type":        "string",
				"description": "The message content.",
			},
		},
		Required: []string{"to", "content"},
	}
}

func (t *teammateSendTool) Execute(_ context.Context, input json.RawMessage) ([]anthropic.BetaToolResultBlockParamContentUnion, error) {
	var args struct {
		To      string `json:"to"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return nil, fmt.Errorf("team_send: %w", err)
	}
	if args.To == "" || args.Content == "" {
		return nil, fmt.Errorf("team_send: 'to' and 'content' are required")
	}

	// Resolve "lead" alias.
	to := args.To
	if to == "lead" {
		to = t.loop.mgr.config.Lead
	}

	if to == "all" {
		if err := t.loop.mgr.broadcast(t.loop.name, args.Content, "message"); err != nil {
			return nil, err
		}
	} else {
		if err := t.loop.mgr.bus.Send(to, Message{
			Type:      "message",
			From:      t.loop.name,
			Content:   args.Content,
			Timestamp: time.Now().Unix(),
		}); err != nil {
			return nil, fmt.Errorf("team_send: %w", err)
		}
		// Wake the recipient if they are a teammate.
		t.loop.mgr.wakeTeammate(to)
	}

	return []anthropic.BetaToolResultBlockParamContentUnion{
		{OfText: &anthropic.BetaTextBlockParam{Text: fmt.Sprintf("Message sent to %s.", args.To)}},
	}, nil
}

// ── ShutdownRequestTool (lead) ────────────────────────────────────────

// ShutdownRequestTool 向队友发起优雅关机请求。
// Lead 使用此工具替代直接杀进程，让队友有机会完成当前工作后安全退出。
type ShutdownRequestTool struct {
	Mgr *TeammateManager
}

func (t *ShutdownRequestTool) Name() string { return "team_shutdown_request" }

func (t *ShutdownRequestTool) Description() string {
	return "向队友发起优雅关机请求。队友会在完成当前工作后响应，比直接杀进程更安全。" +
		"每个请求带唯一 request_id，队友通过 team_shutdown_response 引用同一 request_id 来批准或拒绝。" +
		"关机请求发出后，结果会自动出现在你的 <team-inbox> 中。"
}

func (t *ShutdownRequestTool) InputSchema() anthropic.BetaToolInputSchemaParam {
	return anthropic.BetaToolInputSchemaParam{
		Properties: map[string]any{
			"teammate": map[string]any{
				"type":        "string",
				"description": "目标队友名称。",
			},
			"reason": map[string]any{
				"type":        "string",
				"description": "关机原因（可选）。",
			},
		},
		Required: []string{"teammate"},
	}
}

func (t *ShutdownRequestTool) Execute(_ context.Context, input json.RawMessage) ([]anthropic.BetaToolResultBlockParamContentUnion, error) {
	var args struct {
		Teammate string `json:"teammate"`
		Reason   string `json:"reason"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return nil, fmt.Errorf("team_shutdown_request: %w", err)
	}
	if args.Teammate == "" {
		return nil, fmt.Errorf("team_shutdown_request: 'teammate' 是必填项")
	}

	reqID, err := t.Mgr.RequestShutdown(args.Teammate, args.Reason)
	if err != nil {
		return nil, err
	}

	result := fmt.Sprintf("关机请求 %s 已发送给 %s (状态: pending)。等待队友响应...", reqID, args.Teammate)
	return []anthropic.BetaToolResultBlockParamContentUnion{
		{OfText: &anthropic.BetaTextBlockParam{Text: result}},
	}, nil
}

// ── ShutdownResponseTool (teammate) ───────────────────────────────────

// ShutdownResponseTool 队友用来响应 lead 的关机请求。
// approve=true 时，工具内部会调用 Shutdown() 优雅退出。
type ShutdownResponseTool struct {
	loop *teammateLoop
}

func (t *ShutdownResponseTool) Name() string { return "team_shutdown_response" }

func (t *ShutdownResponseTool) Description() string {
	return "响应 lead 的关机请求。引用 shutdown_request 消息中的 request_id。" +
		"如果当前工作可以安全停止，设置 approve: true 同意关机。" +
		"如果正在执行关键操作需要继续，设置 approve: false 拒绝关机并说明原因。"
}

func (t *ShutdownResponseTool) InputSchema() anthropic.BetaToolInputSchemaParam {
	return anthropic.BetaToolInputSchemaParam{
		Properties: map[string]any{
			"request_id": map[string]any{
				"type":        "string",
				"description": "关机请求的 request_id，来自 <team-inbox> 中 shutdown_request 消息的属性。",
			},
			"approve": map[string]any{
				"type":        "boolean",
				"description": "true = 同意关机（完成当前工作后退出），false = 拒绝关机（继续工作）。",
			},
			"reason": map[string]any{
				"type":        "string",
				"description": "同意时的确认信息，或拒绝时的原因说明。",
			},
		},
		Required: []string{"request_id", "approve"},
	}
}

func (t *ShutdownResponseTool) Execute(_ context.Context, input json.RawMessage) ([]anthropic.BetaToolResultBlockParamContentUnion, error) {
	var args struct {
		RequestID string `json:"request_id"`
		Approve   bool   `json:"approve"`
		Reason    string `json:"reason"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return nil, fmt.Errorf("team_shutdown_response: %w", err)
	}
	if args.RequestID == "" {
		return nil, fmt.Errorf("team_shutdown_response: 'request_id' 是必填项")
	}

	if err := t.loop.mgr.ResolveShutdownRequest(args.RequestID, args.Approve, args.Reason); err != nil {
		return nil, err
	}

	result := fmt.Sprintf("已响应关机请求 %s: approve=%v", args.RequestID, args.Approve)
	if args.Approve {
		result += "。正在优雅退出..."
	}
	return []anthropic.BetaToolResultBlockParamContentUnion{
		{OfText: &anthropic.BetaTextBlockParam{Text: result}},
	}, nil
}

// ── PlanRequestTool (teammate) ────────────────────────────────────────

// PlanRequestTool 队友用来在执行高风险操作前提交计划给 lead 审批。
type PlanRequestTool struct {
	Mgr        *TeammateManager
	SenderName string
}

func (t *PlanRequestTool) Name() string { return "team_plan_request" }

func (t *PlanRequestTool) Description() string {
	return "在执行高风险/不可逆操作前，向 lead 提交计划审批。" +
		"生成唯一 request_id，计划以 plan_request 消息发送给 lead。" +
		"lead 会用 team_plan_response 批准或拒绝。审批结果会自动出现在你的 <team-inbox> 中。" +
		"使用场景: 重构、删除文件、破坏性更改、不熟悉的代码库中的大规模修改。"
}

func (t *PlanRequestTool) InputSchema() anthropic.BetaToolInputSchemaParam {
	return anthropic.BetaToolInputSchemaParam{
		Properties: map[string]any{
			"plan": map[string]any{
				"type":        "string",
				"description": "计划描述，包括要做什么、怎么做、可能的影响。",
			},
			"context": map[string]any{
				"type":        "string",
				"description": "补充背景信息（可选），帮助 lead 理解为什么需要这个操作。",
			},
		},
		Required: []string{"plan"},
	}
}

func (t *PlanRequestTool) Execute(_ context.Context, input json.RawMessage) ([]anthropic.BetaToolResultBlockParamContentUnion, error) {
	var args struct {
		Plan    string `json:"plan"`
		Context string `json:"context"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return nil, fmt.Errorf("team_plan_request: %w", err)
	}
	if args.Plan == "" {
		return nil, fmt.Errorf("team_plan_request: 'plan' 是必填项")
	}

	// 合并 plan 和 context
	content := args.Plan
	if args.Context != "" {
		content = fmt.Sprintf("计划: %s\n背景: %s", args.Plan, args.Context)
	}

	reqID, err := t.Mgr.SubmitPlan(t.SenderName, content)
	if err != nil {
		return nil, err
	}

	result := fmt.Sprintf("计划请求 %s 已提交 (状态: pending)。等待 lead 审批...", reqID)
	return []anthropic.BetaToolResultBlockParamContentUnion{
		{OfText: &anthropic.BetaTextBlockParam{Text: result}},
	}, nil
}

// ── PlanResponseTool (lead) ───────────────────────────────────────────

// PlanResponseTool lead 用来批准或拒绝队友提交的计划。
type PlanResponseTool struct {
	Mgr *TeammateManager
}

func (t *PlanResponseTool) Name() string { return "team_plan_response" }

func (t *PlanResponseTool) Description() string {
	return "审批队友提交的计划。当你的 <team-inbox> 中出现 type=\"plan_request\" 的消息时，" +
		"引用该消息的 request_id，使用此工具批准 (approve: true) 或拒绝 (approve: false)。" +
		"建议附带反馈意见，帮助队友理解你的决定。"
}

func (t *PlanResponseTool) InputSchema() anthropic.BetaToolInputSchemaParam {
	return anthropic.BetaToolInputSchemaParam{
		Properties: map[string]any{
			"request_id": map[string]any{
				"type":        "string",
				"description": "计划请求的 request_id，来自 <team-inbox> 中 plan_request 消息的属性。",
			},
			"approve": map[string]any{
				"type":        "boolean",
				"description": "true = 批准计划，队友可以开始执行。false = 拒绝计划，队友应放弃该操作。",
			},
			"feedback": map[string]any{
				"type":        "string",
				"description": "反馈意见。批准时可提供建议，拒绝时必须说明原因。",
			},
		},
		Required: []string{"request_id", "approve"},
	}
}

func (t *PlanResponseTool) Execute(_ context.Context, input json.RawMessage) ([]anthropic.BetaToolResultBlockParamContentUnion, error) {
	var args struct {
		RequestID string `json:"request_id"`
		Approve   bool   `json:"approve"`
		Feedback  string `json:"feedback"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return nil, fmt.Errorf("team_plan_response: %w", err)
	}
	if args.RequestID == "" {
		return nil, fmt.Errorf("team_plan_response: 'request_id' 是必填项")
	}

	if err := t.Mgr.ResolvePlanRequest(args.RequestID, args.Approve, args.Feedback); err != nil {
		return nil, err
	}

	action := "拒绝"
	if args.Approve {
		action = "批准"
	}
	result := fmt.Sprintf("已%s计划请求 %s。队友将收到通知。", action, args.RequestID)
	if args.Feedback != "" {
		result += fmt.Sprintf(" 反馈: %s", args.Feedback)
	}
	return []anthropic.BetaToolResultBlockParamContentUnion{
		{OfText: &anthropic.BetaTextBlockParam{Text: result}},
	}, nil
}

// ── IdleTool (teammate) ──────────────────────────────────────────────

// IdleTool 队友用来主动进入空闲状态。
// 当队友完成当前工作且没有更多任务时调用此工具，
// 进入 IDLE 阶段等待新消息或自动认领任务。
type IdleTool struct {
	loop *teammateLoop
}

func (t *IdleTool) Name() string { return "idle" }

func (t *IdleTool) Description() string {
	return "完成当前工作后调用此工具进入空闲状态。" +
		"在空闲状态下，系统会自动检查收件箱和任务看板，" +
		"如果有新消息或可认领的任务会自动唤醒你。" +
		"如果 60 秒内没有任何事情，你会自动关机退出。"
}

func (t *IdleTool) InputSchema() anthropic.BetaToolInputSchemaParam {
	return anthropic.BetaToolInputSchemaParam{
		Properties: map[string]any{},
		Required:   []string{},
	}
}

func (t *IdleTool) Execute(_ context.Context, input json.RawMessage) ([]anthropic.BetaToolResultBlockParamContentUnion, error) {
	t.loop.idleRequested = true
	return []anthropic.BetaToolResultBlockParamContentUnion{
		{OfText: &anthropic.BetaTextBlockParam{Text: "进入空闲状态。等待新消息或可认领任务..."}},
	}, nil
}

// ── ClaimTaskTool (teammate) ─────────────────────────────────────────

// ClaimTaskTool 队友用来手动认领任务看板中的任务。
// 系统会在 IDLE 阶段自动认领未分配任务，此工具保留给 LLM
// 在工作阶段手动认领特定任务的需求。
type ClaimTaskTool struct {
	loop *teammateLoop
}

func (t *ClaimTaskTool) Name() string { return "claim_task" }

func (t *ClaimTaskTool) Description() string {
	return "手动认领任务看板中未分配的任务。系统会在空闲时自动认领，" +
		"此工具用于在工作阶段手动认领特定任务。"
}

func (t *ClaimTaskTool) InputSchema() anthropic.BetaToolInputSchemaParam {
	return anthropic.BetaToolInputSchemaParam{
		Properties: map[string]any{
			"task_id": map[string]any{
				"type":        "string",
				"description": "要认领的任务 ID。",
			},
		},
		Required: []string{"task_id"},
	}
}

func (t *ClaimTaskTool) Execute(_ context.Context, input json.RawMessage) ([]anthropic.BetaToolResultBlockParamContentUnion, error) {
	var args struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return nil, fmt.Errorf("claim_task: %w", err)
	}
	if args.TaskID == "" {
		return nil, fmt.Errorf("claim_task: task_id 是必填项")
	}

	mgr := t.loop.mgr
	if mgr.taskMgr == nil {
		return nil, fmt.Errorf("claim_task: 任务看板不可用")
	}

	owner := t.loop.name
	inProgress := "in_progress"
	_, err := mgr.taskMgr.Update(args.TaskID, tasks.TaskUpdate{
		Owner:  &owner,
		Status: &inProgress,
	})
	if err != nil {
		return nil, fmt.Errorf("claim_task: %w", err)
	}

	result := fmt.Sprintf("已认领任务 %s（owner=%s, status=in_progress）", args.TaskID, owner)
	return []anthropic.BetaToolResultBlockParamContentUnion{
		{OfText: &anthropic.BetaTextBlockParam{Text: result}},
	}, nil
}
