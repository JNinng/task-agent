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

// ShutdownRequestTool sends a graceful shutdown request to a teammate.
// The lead uses this instead of killing the process, giving the teammate
// a chance to finish current work and exit safely.
type ShutdownRequestTool struct {
	Mgr *TeammateManager
}

func (t *ShutdownRequestTool) Name() string { return "team_shutdown_request" }

func (t *ShutdownRequestTool) Description() string {
	return "Send a graceful shutdown request to a teammate. The teammate responds after finishing " +
		"current work — safer than killing the process. " +
		"Each request has a unique request_id; the teammate references the same request_id via " +
		"team_shutdown_response to approve or reject. " +
		"Results auto-appear in your <team-inbox> after the request is sent."
}

func (t *ShutdownRequestTool) InputSchema() anthropic.BetaToolInputSchemaParam {
	return anthropic.BetaToolInputSchemaParam{
		Properties: map[string]any{
			"teammate": map[string]any{
				"type":        "string",
				"description": "Target teammate name.",
			},
			"reason": map[string]any{
				"type":        "string",
				"description": "Reason for shutdown (optional).",
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
		return nil, fmt.Errorf("team_shutdown_request: 'teammate' is required")
	}

	reqID, err := t.Mgr.RequestShutdown(args.Teammate, args.Reason)
	if err != nil {
		return nil, err
	}

	result := fmt.Sprintf("Shutdown request %s sent to %s (status: pending). Waiting for teammate response...", reqID, args.Teammate)
	return []anthropic.BetaToolResultBlockParamContentUnion{
		{OfText: &anthropic.BetaTextBlockParam{Text: result}},
	}, nil
}

// ── ShutdownResponseTool (teammate) ───────────────────────────────────

// ShutdownResponseTool is used by teammates to respond to the lead's shutdown request.
// When approve=true, the tool internally calls Shutdown() for a graceful exit.
type ShutdownResponseTool struct {
	loop *teammateLoop
}

func (t *ShutdownResponseTool) Name() string { return "team_shutdown_response" }

func (t *ShutdownResponseTool) Description() string {
	return "Respond to a shutdown request from the lead. Reference the request_id from the " +
		"shutdown_request message. " +
		"If current work can be safely stopped, set approve: true to accept shutdown. " +
		"If performing critical work that must continue, set approve: false to reject and explain why."
}

func (t *ShutdownResponseTool) InputSchema() anthropic.BetaToolInputSchemaParam {
	return anthropic.BetaToolInputSchemaParam{
		Properties: map[string]any{
			"request_id": map[string]any{
				"type":        "string",
				"description": "The request_id from the shutdown_request message in <team-inbox>.",
			},
			"approve": map[string]any{
				"type": "boolean",
				"description": "true = accept shutdown (exit after finishing current work), " +
					"false = reject shutdown (continue working).",
			},
			"reason": map[string]any{
				"type":        "string",
				"description": "Confirmation message on accept, or reason on reject.",
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
		return nil, fmt.Errorf("team_shutdown_response: 'request_id' is required")
	}

	if err := t.loop.mgr.ResolveShutdownRequest(args.RequestID, args.Approve, args.Reason); err != nil {
		return nil, err
	}

	result := fmt.Sprintf("Responded to shutdown request %s: approve=%v", args.RequestID, args.Approve)
	if args.Approve {
		result += ". Shutting down gracefully..."
	}
	return []anthropic.BetaToolResultBlockParamContentUnion{
		{OfText: &anthropic.BetaTextBlockParam{Text: result}},
	}, nil
}

// ── PlanRequestTool (teammate) ────────────────────────────────────────

// PlanRequestTool is used by teammates to submit a plan to the lead for
// approval before executing high-risk operations.
type PlanRequestTool struct {
	Mgr        *TeammateManager
	SenderName string
}

func (t *PlanRequestTool) Name() string { return "team_plan_request" }

func (t *PlanRequestTool) Description() string {
	return "Submit a plan to the lead for approval before executing high-risk or irreversible operations. " +
		"Generates a unique request_id; the plan is sent to the lead as a plan_request message. " +
		"The lead approves or rejects via team_plan_response. Results auto-appear in your <team-inbox>. " +
		"Use for: refactoring, file deletion, destructive changes, large-scale modifications in unfamiliar codebases."
}

func (t *PlanRequestTool) InputSchema() anthropic.BetaToolInputSchemaParam {
	return anthropic.BetaToolInputSchemaParam{
		Properties: map[string]any{
			"plan": map[string]any{
				"type":        "string",
				"description": "Plan description: what to do, how to do it, and potential impact.",
			},
			"context": map[string]any{
				"type":        "string",
				"description": "Additional context (optional) to help the lead understand why this operation is needed.",
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
		return nil, fmt.Errorf("team_plan_request: 'plan' is required")
	}

	// Merge plan and context.
	content := args.Plan
	if args.Context != "" {
		content = fmt.Sprintf("Plan: %s\nContext: %s", args.Plan, args.Context)
	}

	reqID, err := t.Mgr.SubmitPlan(t.SenderName, content)
	if err != nil {
		return nil, err
	}

	result := fmt.Sprintf("Plan request %s submitted (status: pending). Waiting for lead approval...", reqID)
	return []anthropic.BetaToolResultBlockParamContentUnion{
		{OfText: &anthropic.BetaTextBlockParam{Text: result}},
	}, nil
}

// ── PlanResponseTool (lead) ───────────────────────────────────────────

// PlanResponseTool is used by the lead to approve or reject a teammate's plan.
type PlanResponseTool struct {
	Mgr *TeammateManager
}

func (t *PlanResponseTool) Name() string { return "team_plan_response" }

func (t *PlanResponseTool) Description() string {
	return "Approve or reject a plan submitted by a teammate. When a type=\"plan_request\" message " +
		"appears in your <team-inbox>, reference its request_id and use this tool to approve " +
		"(approve: true) or reject (approve: false). " +
		"Include feedback to help the teammate understand your decision."
}

func (t *PlanResponseTool) InputSchema() anthropic.BetaToolInputSchemaParam {
	return anthropic.BetaToolInputSchemaParam{
		Properties: map[string]any{
			"request_id": map[string]any{
				"type":        "string",
				"description": "The request_id from the plan_request message in <team-inbox>.",
			},
			"approve": map[string]any{
				"type": "boolean",
				"description": "true = approve the plan, teammate may proceed. " +
					"false = reject the plan, teammate should abandon the operation.",
			},
			"feedback": map[string]any{
				"type":        "string",
				"description": "Feedback. Provide suggestions on approval; explain the reason on rejection.",
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
		return nil, fmt.Errorf("team_plan_response: 'request_id' is required")
	}

	if err := t.Mgr.ResolvePlanRequest(args.RequestID, args.Approve, args.Feedback); err != nil {
		return nil, err
	}

	action := "rejected"
	if args.Approve {
		action = "approved"
	}
	result := fmt.Sprintf("Plan request %s %s. The teammate will be notified.", args.RequestID, action)
	if args.Feedback != "" {
		result += fmt.Sprintf(" Feedback: %s", args.Feedback)
	}
	return []anthropic.BetaToolResultBlockParamContentUnion{
		{OfText: &anthropic.BetaTextBlockParam{Text: result}},
	}, nil
}

// ── IdleTool (teammate) ──────────────────────────────────────────────

// IdleTool lets a teammate voluntarily enter idle state.
// When the teammate finishes current work and has no more tasks,
// it calls this tool to enter the IDLE phase, waiting for new
// messages or auto-claiming tasks.
type IdleTool struct {
	loop *teammateLoop
}

func (t *IdleTool) Name() string { return "idle" }

func (t *IdleTool) Description() string {
	return "Call this tool after completing current work to enter idle state. " +
		"In idle state, the system auto-checks inbox and task board; " +
		"you will be auto-woken if new messages or claimable tasks arrive. " +
		"If nothing happens within 60 seconds, you will auto-shutdown."
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
		{OfText: &anthropic.BetaTextBlockParam{Text: "Entering idle state. Waiting for new messages or claimable tasks..."}},
	}, nil
}

// ── ClaimTaskTool (teammate) ─────────────────────────────────────────

// ClaimTaskTool lets a teammate manually claim a task from the task board.
// The system auto-claims unassigned tasks during the IDLE phase; this tool
// is reserved for when the LLM needs to manually claim a specific task
// during the WORK phase.
type ClaimTaskTool struct {
	loop *teammateLoop
}

func (t *ClaimTaskTool) Name() string { return "claim_task" }

func (t *ClaimTaskTool) Description() string {
	return "Manually claim an unassigned task from the task board. The system auto-claims during idle; " +
		"use this tool to manually claim a specific task during the work phase."
}

func (t *ClaimTaskTool) InputSchema() anthropic.BetaToolInputSchemaParam {
	return anthropic.BetaToolInputSchemaParam{
		Properties: map[string]any{
			"task_id": map[string]any{
				"type":        "string",
				"description": "The ID of the task to claim.",
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
		return nil, fmt.Errorf("claim_task: task_id is required")
	}

	mgr := t.loop.mgr
	if mgr.taskMgr == nil {
		return nil, fmt.Errorf("claim_task: task board unavailable")
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

	result := fmt.Sprintf("Claimed task %s (owner=%s, status=in_progress)", args.TaskID, owner)
	return []anthropic.BetaToolResultBlockParamContentUnion{
		{OfText: &anthropic.BetaTextBlockParam{Text: result}},
	}, nil
}
