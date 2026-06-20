package team

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
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
