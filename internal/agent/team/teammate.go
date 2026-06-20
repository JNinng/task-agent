package team

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"task-agent/internal/agent/tools"
)

const maxTeammateTurns = 30

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
				"Do NOT spawn additional teammates or subagents.",
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
	}

	// Build restricted tool registry for the teammate.
	tl.toolReg = tools.NewRegistry(
		tools.BashTool{},
		&tools.ReadFileTool{Workdir: workdir},
		&tools.WriteFileTool{Workdir: workdir},
		&tools.EditFileTool{Workdir: workdir},
		&teammateSendTool{loop: tl},
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

// run is the main loop. It processes the initial prompt, drains any
// messages that arrived during processing, then waits for wake signals.
// When woken, it keeps draining the inbox until empty before going idle,
// which avoids lost messages when wake signals are dropped (channel
// buffer = 1, non-blocking send).
func (t *teammateLoop) run(initialPrompt string) {
	// Phase 1: process the initial spawn prompt.
	t.messages = []anthropic.BetaMessageParam{
		anthropic.NewBetaUserMessage(
			anthropic.BetaContentBlockParamUnion{
				OfText: &anthropic.BetaTextBlockParam{Text: initialPrompt},
			}),
	}
	t.processLoop()

	// Drain any messages that arrived while we were processing the
	// initial task (wake signals may have been dropped).
	t.drainInbox()

	// Phase 2: wait for wake signals, then drain the inbox
	// completely each time. This is robust against dropped wake
	// signals: even if a signal is lost, the next successful wake
	// will process all accumulated messages.
	t.mgr.setStatus(t.name, StatusIdle)

	for {
		select {
		case <-t.quitCh:
			return
		case <-t.wakeCh:
			t.mgr.setStatus(t.name, StatusWorking)
			t.drainInbox()
			t.mgr.setStatus(t.name, StatusIdle)
		}
	}
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
			b.WriteString(fmt.Sprintf("  <message from=%q type=%q>%s</message>\n",
				msg.From, msg.Type, msg.Content))
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

// ── teammate-internal team_send tool ──────────────────────────────

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
