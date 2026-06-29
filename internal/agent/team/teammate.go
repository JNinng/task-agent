package team

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"task-agent/internal/agent/tasks"
	"task-agent/internal/agent/tools"
)

const maxTeammateTurns = 30

// IDLE 轮询常量。作为结构体字段默认值而非包级常量，
// 方便测试时替换为更短的间隔。
const (
	defaultIdlePollInterval = 5 * time.Second
	defaultIdleTimeout      = 60 * time.Second
	defaultIdleMaxIter      = 12
)

// teammateLoop runs a full agent loop for one teammate in a goroutine.
// It processes an initial prompt, then waits on a wake channel for new
// inbox messages. Each wake-up runs the LLM loop until a text-only
// response (no tool calls) or the turn limit is reached.
type teammateLoop struct {
	name     string
	role     string
	client   *anthropic.Client
	model    anthropic.Model
	system   []anthropic.BetaTextBlockParam
	messages []anthropic.BetaMessageParam
	mgr      *TeammateManager // for status updates and message bus access
	wakeCh   chan struct{}
	quitCh   chan struct{}
	toolReg  *tools.Registry // restricted tool set
	workdir  string

	// IDLE / 自组织字段
	idleRequested    bool          // idle 工具设置此标志，processLoop 检测后退出 WORK
	idlePollInterval time.Duration // IDLE 轮询间隔（默认 5s）
	idleTimeout      time.Duration // IDLE 超时（默认 60s）
	idleMaxIter      int           // IDLE 最大轮询次数（默认 12）
}

// newTeammateLoop creates a teammate loop wired with a restricted tool set.
// The teammate has bash/read/write/edit plus team_send for replies, but
// no task (subagent) and no team_spawn/team_inbox to prevent recursion.
func newTeammateLoop(
	name, role, lead string,
	client *anthropic.Client,
	model anthropic.Model,
	workdir string,
	mgr *TeammateManager,
) *teammateLoop {
	system := []anthropic.BetaTextBlockParam{
		{Text: fmt.Sprintf(
			"You are teammate '%s' (role: %s) on an agent team led by '%s'.\n"+
				"Work in %s. Use tools to complete the tasks assigned to you.\n"+
				"When you finish a task, use team_send to send your results back\n"+
				"to the lead or the teammate who requested the work.\n"+
				"Do NOT spawn additional teammates or subagents.\n\n"+
				"### Team Protocols\n"+
				"- 当你收到 type=\"shutdown_request\" 的消息时，使用 team_shutdown_response\n"+
				"  响应，引用相同的 request_id。如果可以安全停止当前工作，设置 approve: true；\n"+
				"  如果正在执行关键操作且不能中断，设置 approve: false 并说明原因。\n"+
				"- 在执行高风险或不可逆操作前（如重构代码、删除文件、破坏性更改），\n"+
				"  先使用 team_plan_request 提交计划给 lead 审批。等待 <team-inbox> 中出现\n"+
				"  type=\"plan_response\" 的审批结果后再继续执行。",
			name, role, lead, workdir,
		)},
	}

	tl := &teammateLoop{
		name:    name,
		role:    role,
		client:  client,
		model:   model,
		system:  system,
		mgr:     mgr,
		wakeCh:  make(chan struct{}, 1), // buffered so send doesn't block
		quitCh:  make(chan struct{}),
		workdir: workdir,

		idlePollInterval: defaultIdlePollInterval,
		idleTimeout:      defaultIdleTimeout,
		idleMaxIter:      defaultIdleMaxIter,
	}

	// Build restricted tool registry for the teammate.
	// Includes protocol tools: shutdown_response (响应关机请求) and
	// plan_request (提交计划审批). Excludes shutdown_request and plan_response
	// which are lead-only.
	tl.toolReg = tools.NewRegistry(
		tools.BashTool{},
		&tools.ReadFileTool{Workdir: workdir},
		&tools.WriteFileTool{Workdir: workdir},
		&tools.EditFileTool{Workdir: workdir},
		&teammateSendTool{loop: tl},
		&ShutdownResponseTool{loop: tl},
		&PlanRequestTool{Mgr: mgr, SenderName: name},
	)

	return tl
}

// wake signals the teammate that new messages are available.
// It is non-blocking — if the teammate is already awake the signal is dropped.
func (t *teammateLoop) wake() {
	select {
	case t.wakeCh <- struct{}{}:
	default:
	}
}

// shutdown tells the teammate to exit its loop.
func (t *teammateLoop) shutdown() {
	close(t.quitCh)
}

// run 是队友的主循环。处理初始任务后进入 WORK↔IDLE 自组织循环。
//
// 生命周期：
//
//	spawn → WORK ⇄ IDLE → SHUTDOWN
//
// WORK: LLM 调用工具直到 stop_reason != tool_use 或主动调用 idle
// IDLE: 每 5s 轮询（最长 60s），检查收件箱 → 任务看板
//
// drainInbox 内部会调用 processLoop 处理每批收件箱消息，
// 因此 run() 只需在 IDLE 唤醒后显式调用 processLoop。
func (t *teammateLoop) run(initialPrompt string) {
	// Phase 1: WORK — 处理初始 spawn prompt。
	t.messages = []anthropic.BetaMessageParam{
		anthropic.NewBetaUserMessage(
			anthropic.BetaContentBlockParamUnion{
				OfText: &anthropic.BetaTextBlockParam{Text: initialPrompt},
			}),
	}
	t.processLoop()

	// Phase 2: WORK ↔ IDLE 自组织循环
	for {
		// 排空积累的收件箱消息（drainInbox 内部对每批消息调用 processLoop，
		// 且会循环直到收件箱完全为空，确保上一轮 WORK 期间到达的消息都被处理）
		t.drainInbox()

		// 进入 IDLE
		t.mgr.setStatus(t.name, StatusIdle)
		hasWork := t.idlePoll()

		if !hasWork {
			// 空闲超时或收到 quit 信号 → 优雅关机
			t.sendToLead("idle timeout, shutting down")

			// 从 manager 的 loops map 中删除自己
			t.mgr.mu.Lock()
			delete(t.mgr.loops, t.name)
			t.mgr.mu.Unlock()

			// 更新 roster 状态
			t.mgr.setStatus(t.name, StatusShutdown)
			return
		}

		// 有工作要做 — 恢复 WORK
		t.mgr.setStatus(t.name, StatusWorking)

		// 防御性身份检查：IDLE → WORK 转换时确保身份完整
		if len(t.messages) <= 3 {
			t.injectIdentity()
		}

		// 重置 idle 标志
		t.idleRequested = false

		t.processLoop()
	}
}

// idlePoll 在 IDLE 阶段轮询，检查是否有工作可做。
// 返回值：hasWork — true 表示有工作要做，false 表示应关机。
//
// 轮询逻辑：
//  1. 监听 quitCh（支持即时中断）→ return false
//  2. sleep idlePollInterval
//  3. 检查收件箱 → 有消息则注入 <team-inbox> → return true
//  4. 扫描任务看板 → 有未认领则自动认领并注入 → return true
//  5. 最多 idleMaxIter 轮后超时 → return false
func (t *teammateLoop) idlePoll() bool {
	for i := 0; i < t.idleMaxIter; i++ {
		// 可被 quitCh 中断的 sleep
		select {
		case <-t.quitCh:
			return false
		case <-time.After(t.idlePollInterval):
		}

		// 检查收件箱
		inbox, err := t.mgr.bus.ReadInbox(t.name)
		if err == nil && len(inbox) > 0 {
			block := FormatInboxMessages(inbox)
			if block != "" {
				t.messages = append(t.messages, anthropic.NewBetaUserMessage(
					anthropic.BetaContentBlockParamUnion{
						OfText: &anthropic.BetaTextBlockParam{Text: block},
					},
				))
			}
			return true
		}

		// 检查任务看板
		if t.claimAndInject() {
			return true
		}
	}

	// 超时 — 无事可做
	return false
}

// drainInbox repeatedly reads the teammate's inbox and processes all
// pending messages. It loops until the inbox is empty, handling the
// case where new messages arrive while we are processing a batch.
// Each batch of messages is injected as a <team-inbox> block and
// processed through the LLM + tool-call cycle.
func (t *teammateLoop) drainInbox() {
	for {
		inbox, err := t.mgr.bus.ReadInbox(t.name)
		if err != nil || len(inbox) == 0 {
			return
		}

		var b strings.Builder
		b.WriteString("<team-inbox>\n")
		for _, msg := range inbox {
			// 协议消息包含更多属性便于 LLM 理解上下文
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
		t.messages = append(t.messages, anthropic.NewBetaUserMessage(
			anthropic.BetaContentBlockParamUnion{
				OfText: &anthropic.BetaTextBlockParam{Text: b.String()},
			}))

		t.processLoop()
	}
}

// processLoop runs the LLM + tool-call cycle until a text-only response
// or the turn limit is reached. When the loop finishes, any final text
// response is automatically forwarded to the lead so the lead doesn't
// need to poll an empty inbox.
func (t *teammateLoop) processLoop() {
	var lastText string

	for turn := 0; turn < maxTeammateTurns; turn++ {
		// Shrink context to prevent unbounded growth — keep first
		// message (initial assignment) and last 20 messages.
		if len(t.messages) > 40 {
			keep := 20
			t.messages = append(t.messages[:1], t.messages[len(t.messages)-keep:]...)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		resp, err := t.client.Beta.Messages.New(ctx, anthropic.BetaMessageNewParams{
			Model:     t.model,
			System:    t.system,
			Messages:  t.messages,
			Tools:     t.toolReg.ToParams(),
			MaxTokens: 8000,
		})
		cancel()
		if err != nil {
			// On API error, send failure to lead and exit loop.
			t.sendToLead(fmt.Sprintf("API error: %v", err))
			return
		}

		t.messages = append(t.messages, resp.ToParam())

		var toolBlocks []tools.ToolUseBlock
		for _, block := range resp.Content {
			if block.Type == "tool_use" {
				tu := block.AsToolUse()
				inputBytes, _ := json.Marshal(tu.Input)
				toolBlocks = append(toolBlocks, tools.ToolUseBlock{
					ID:    tu.ID,
					Name:  tu.Name,
					Input: json.RawMessage(inputBytes),
				})
			} else if block.Type == "text" {
				// Accumulate text from this response; only the last
				// turn's text matters for the final summary.
				txt := block.AsText()
				lastText = txt.Text
			}
		}

		if len(toolBlocks) == 0 {
			// Text-only response — task complete.
			// Auto-forward the final text to lead if teammate didn't
			// already send a reply via team_send.
			if lastText != "" {
				t.sendToLead(lastText)
			}
			return
		}

		// Reset lastText when tools are called — the model is still
		// working; only the final text response matters.
		lastText = ""

		// Dispatch tools.
		ctx2, cancel2 := context.WithTimeout(context.Background(), 120*time.Second)
		results, err := t.toolReg.Dispatch(ctx2, toolBlocks)
		cancel2()
		if err != nil {
			t.sendToLead(fmt.Sprintf("Tool dispatch error: %v", err))
			return
		}

		var contentBlocks []anthropic.BetaContentBlockParamUnion
		for _, result := range results {
			isError := len(result.Content) > 6 && result.Content[:6] == "Error:"
			contentBlocks = append(contentBlocks, anthropic.BetaContentBlockParamUnion{
				OfToolResult: &anthropic.BetaToolResultBlockParam{
					ToolUseID: result.ToolUseID,
					Content: []anthropic.BetaToolResultBlockParamContentUnion{
						{OfText: &anthropic.BetaTextBlockParam{Text: result.Content}},
					},
					IsError: anthropic.Bool(isError),
				},
			})
		}
		t.messages = append(t.messages, anthropic.NewBetaUserMessage(contentBlocks...))
	}

	// Turn limit reached — send partial summary to lead.
	t.sendToLead(fmt.Sprintf("(teammate '%s': %d-turn limit reached, last output: %s)",
		t.name, maxTeammateTurns, lastText))
}

// sendToLead sends a message from this teammate to the lead agent.
func (t *teammateLoop) sendToLead(content string) {
	_ = t.mgr.bus.Send(t.mgr.config.Lead, Message{
		Type:      "message",
		From:      t.name,
		Content:   content,
		Timestamp: time.Now().Unix(),
	})
}

// injectIdentity 在消息历史开头插入身份声明，确保压缩后的上下文
// 仍然包含队友的名称、角色和团队归属信息。
//
// 插入格式：
//
//	user: <identity>You are 'name', role: role, team lead: lead. Continue...</identity>
//	assistant: I am name. Continuing.
func (t *teammateLoop) injectIdentity() {
	identityBlock := anthropic.BetaContentBlockParamUnion{
		OfText: &anthropic.BetaTextBlockParam{
			Text: fmt.Sprintf(
				"<identity>You are '%s', role: %s, team lead: %s. "+
					"Continue with your assigned work.</identity>",
				t.name, t.role, t.mgr.config.Lead,
			),
		},
	}
	ackBlock := anthropic.BetaContentBlockParamUnion{
		OfText: &anthropic.BetaTextBlockParam{
			Text: fmt.Sprintf("I am %s. Continuing.", t.name),
		},
	}

	// 在消息列表开头插入身份声明对
	t.messages = append(
		[]anthropic.BetaMessageParam{
			{Role: "user", Content: []anthropic.BetaContentBlockParamUnion{identityBlock}},
			{Role: "assistant", Content: []anthropic.BetaContentBlockParamUnion{ackBlock}},
		},
		t.messages...,
	)
}

// claimAndInject 扫描任务看板，认领第一个未分配任务，
// 并将 <auto-claimed> 块注入消息历史。返回 true 表示成功认领。
func (t *teammateLoop) claimAndInject() bool {
	if t.mgr.taskMgr == nil {
		return false
	}

	unclaimed, err := t.mgr.ScanUnclaimedTasks()
	if err != nil || len(unclaimed) == 0 {
		return false
	}

	// 认领第一个未分配任务
	target := unclaimed[0]
	owner := t.name
	inProgress := "in_progress"
	_, err = t.mgr.taskMgr.Update(target.ID, tasks.TaskUpdate{
		Owner:  &owner,
		Status: &inProgress,
	})
	if err != nil {
		return false
	}

	// 注入 <auto-claimed> 块到消息历史
	claimBlock := fmt.Sprintf(
		"<auto-claimed>\n"+
			"  已自动认领任务:\n"+
			"  ID: %s\n"+
			"  主题: %s\n"+
			"  描述: %s\n"+
			"  Owner: %s (我)\n"+
			"  Status: in_progress\n"+
			"</auto-claimed>\n\n"+
			"请立即开始执行此任务。",
		target.ID, target.Subject, target.Description, t.name,
	)

	t.messages = append(t.messages, anthropic.NewBetaUserMessage(
		anthropic.BetaContentBlockParamUnion{
			OfText: &anthropic.BetaTextBlockParam{Text: claimBlock},
		},
	))

	return true
}
