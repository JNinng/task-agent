package team

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
)

// ── TeamSpawnTool ───────────────────────────────────────────────────

// TeamSpawnTool creates a new teammate and starts its agent loop.
// This is the lead agent's spawn tool.
type TeamSpawnTool struct {
	Mgr *TeammateManager
}

func (t *TeamSpawnTool) Name() string { return "team_spawn" }

func (t *TeamSpawnTool) Description() string {
	return "Create a new teammate with a name, role, and initial task prompt. " +
		"The teammate runs independently in the background. Its results are " +
		"automatically delivered to you — no need to poll team_inbox. " +
		"Use team_send to assign follow-up work or ask questions."
}

func (t *TeamSpawnTool) InputSchema() anthropic.BetaToolInputSchemaParam {
	return anthropic.BetaToolInputSchemaParam{
		Properties: map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "Unique name for the teammate (e.g. 'alice', 'bob').",
			},
			"role": map[string]any{
				"type":        "string",
				"description": "The teammate's role or specialty (e.g. 'coder', 'tester', 'reviewer').",
			},
			"prompt": map[string]any{
				"type":        "string",
				"description": "The initial task or instructions for the teammate.",
			},
		},
		Required: []string{"name", "role", "prompt"},
	}
}

func (t *TeamSpawnTool) Execute(_ context.Context, input json.RawMessage) ([]anthropic.BetaToolResultBlockParamContentUnion, error) {
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
	return "Send a message to a teammate or broadcast to all. " +
		"Use this to assign tasks, request status updates, or coordinate work. " +
		"Set to='all' to broadcast to every teammate."
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
				"description": "The message content.",
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
	return "Manually check your inbox for teammate messages (rarely needed — " +
		"teammate replies are automatically injected before your next response). " +
		"Only use this when you explicitly need to drain the inbox mid-turn."
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
