package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"task-agent/internal/agent/background"
	"task-agent/internal/agent/team"
	"task-agent/internal/agent/tools"
)

// Ensure Runner implements Session at compile time.
var _ Session = (*Runner)(nil)

type Runner struct {
	agent           *Agent
	messages        []anthropic.BetaMessageParam
	roundsSinceTodo int
	compactCfg      CompactionConfig
	compacted       bool                  // set when autoCompact replaces messages; skips tool_result appending
	bgMgr           *background.Manager   // tracks background tasks; nil if not wired yet
	teamMgr         *team.TeammateManager // tracks agent team; nil if not wired yet
}

func NewRunner(ag *Agent, cfg CompactionConfig, bgMgr *background.Manager, teamMgr *team.TeammateManager, setCompact func(func() (string, error))) *Runner {
	r := &Runner{agent: ag, compactCfg: cfg, bgMgr: bgMgr, teamMgr: teamMgr}
	setCompact(r.compact)
	return r
}

// Tool returns the tool registered under the given name, or nil.
func (r *Runner) Tool(name string) tools.Tool {
	if r.agent == nil || r.agent.registry == nil {
		return nil
	}
	return r.agent.registry.Tool(name)
}

// PreviewToolUse returns a human-readable one-line preview of a tool
// call, delegating to the tool's Previewer implementation.
func (r *Runner) PreviewToolUse(tc tools.ToolUseBlock) string {
	return tools.PreviewToolUse(tc, r.agent.registry)
}

// Messages returns a copy of the current message history for inspection.
func (r *Runner) Messages() []anthropic.BetaMessageParam {
	cp := make([]anthropic.BetaMessageParam, len(r.messages))
	copy(cp, r.messages)
	return cp
}

// compact wraps autoCompact for the compact tool callback.
// It returns a user-facing result string.
func (r *Runner) compact() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := r.autoCompact(ctx); err != nil {
		return "", err
	}
	r.compacted = true
	return "Conversation compacted successfully. Full transcript saved to disk.", nil
}

func (r *Runner) Run(ctx context.Context, input string) <-chan tools.Event {
	ch := make(chan tools.Event, 10)
	go func() {
		defer close(ch)
		r.runLoop(ctx, input, ch)
	}()
	return ch
}

func (r *Runner) runLoop(ctx context.Context, input string, ch chan<- tools.Event) {
	r.messages = append(r.messages, anthropic.NewBetaUserMessage(
		anthropic.BetaContentBlockParamUnion{
			OfText: &anthropic.BetaTextBlockParam{Text: input},
		}))

	ch <- EventThinking{}

	for {
		// Layer 1: micro_compact — replace old tool_results with placeholders
		microCompact(r.messages, r.compactCfg.MicroKeepRecent)

		// Layer 1b: inject completed background task notifications before the
		// next LLM call so the model can react to results.
		if r.bgMgr != nil {
			r.injectBackgroundNotifications(ch)
		}

		// Layer 1c: inject teammate inbox messages before the next LLM call
		// so the lead can react to teammate replies.
		if r.teamMgr != nil {
			r.injectTeamInbox(ch)
		}

		resp, err := r.agent.client.Beta.Messages.New(ctx, anthropic.BetaMessageNewParams{
			Model:     r.agent.model,
			System:    r.agent.system,
			Messages:  r.messages,
			Tools:     r.agent.registry.ToParams(),
			MaxTokens: 8000,
		})
		if err != nil {
			ch <- EventError{Err: err}
			return
		}

		// Layer 2: auto_compact — check token threshold after API response
		if r.compactCfg.AutoThreshold > 0 && resp.Usage.InputTokens > int64(r.compactCfg.AutoThreshold) {
			// Append the response so the compressed summary includes this turn
			r.messages = append(r.messages, resp.ToParam())
			ch <- EventText{Content: fmt.Sprintf(
				"[context: %d tokens — auto-compacting]", int64(r.compactCfg.AutoThreshold))}
			if err := r.autoCompact(ctx); err != nil {
				ch <- EventText{Content: fmt.Sprintf("[compact warning: %v]", err)}
			} else {
				ch <- EventText{Content: "[auto-compact done]"}
			}
			ch <- EventThinking{}
			continue
		}

		r.messages = append(r.messages, resp.ToParam())

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
				t := block.AsText()
				ch <- EventText{Content: t.Text}
			}
		}

		if len(toolBlocks) == 0 {
			if r.hasIncompleteTodos() {
				r.roundsSinceTodo++
			}
			if r.hasIncompleteTodos() && r.roundsSinceTodo >= 3 {
				r.roundsSinceTodo = 0
				r.messages = append(r.messages, anthropic.NewBetaUserMessage(
					anthropic.BetaContentBlockParamUnion{
						OfText: &anthropic.BetaTextBlockParam{
							Text: "<reminder>Update your todo list or task graph.</reminder>",
						},
					}))
				continue
			}
			ch <- EventDone{}
			return
		}

		ch <- EventToolCalls{Tools: toolBlocks}

		// Emit subagent progress events for task tool calls so the TUI
		// can show which sub-task is currently running, and wire the
		// event channel through context so the subagent can send
		// turn-by-turn progress while it executes.
		for _, tb := range toolBlocks {
			if tb.Name == "task" {
				var taskArgs struct {
					Description string `json:"description"`
				}
				json.Unmarshal(tb.Input, &taskArgs)
				desc := taskArgs.Description
				if desc == "" {
					var taskArgs2 struct {
						Prompt string `json:"prompt"`
					}
					json.Unmarshal(tb.Input, &taskArgs2)
					desc = taskArgs2.Prompt
					if len(desc) > 60 {
						desc = desc[:60] + "..."
					}
				}
				ch <- tools.SubagentProgress{Description: desc, Turn: 0, MaxTurns: 30}
			}
		}

		taskCtx := ctx
		if hasTaskBlock(toolBlocks) {
			taskCtx = tools.WithEventChannel(ctx, ch)
		}
		results, err := r.agent.registry.Dispatch(taskCtx, toolBlocks)
		if err != nil {
			ch <- EventError{Err: err}
			return
		}

		ch <- EventToolResults{Results: results}

		usedTodo := false
		for _, tb := range toolBlocks {
			switch tb.Name {
			case "todo", "task_create", "task_update":
				usedTodo = true
			}
		}

		if usedTodo {
			r.roundsSinceTodo = 0
			if t, ok := r.agent.registry.Tool("todo").(*tools.TodoWriteTool); ok {
				ch <- EventTodoUpdate{Content: t.Render()}
			}
		} else if r.hasIncompleteTodos() {
			r.roundsSinceTodo++
		}

		// When compaction replaced r.messages during tool dispatch (e.g.
		// the compact tool), skip appending tool_result blocks — their
		// tool_use counterparts no longer exist in the message history
		// and the API would reject orphaned tool_result blocks.
		//
		// Unlike autoCompact (layer 2) which continues to let the model
		// pick up mid-task, a manual compact means the model explicitly
		// requested compaction. There is nothing to respond to: exit the
		// loop so the user sees the compact result and types the next
		// input. This prevents the model from auto-responding with
		// redundant project exploration or capability re-listing.
		if r.compacted {
			r.compacted = false
			ch <- EventDone{}
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
		if r.hasIncompleteTodos() && r.roundsSinceTodo >= 3 {
			r.roundsSinceTodo = 0
			contentBlocks = append(contentBlocks, anthropic.BetaContentBlockParamUnion{
				OfText: &anthropic.BetaTextBlockParam{Text: "<reminder>Update your todo list or task graph.</reminder>"},
			})
		}
		r.messages = append(r.messages, anthropic.NewBetaUserMessage(contentBlocks...))
	}
}

// hasIncompleteTodos reports whether the todo list or task graph has any
// pending or in_progress items that still need work.
func (r *Runner) hasIncompleteTodos() bool {
	// Check in-memory todo list (session-only).
	if t, ok := r.agent.registry.Tool("todo").(*tools.TodoWriteTool); ok {
		if t.HasIncomplete() {
			return true
		}
	}
	// Check persistent task graph (survives sessions).
	if t, ok := r.agent.registry.Tool("task_create").(*tools.TaskCreateTool); ok {
		has, err := t.Mgr.HasIncomplete()
		if err == nil && has {
			return true
		}
	}
	return false
}

// hasTaskBlock reports whether any tool_use block is a subagent task call.
func hasTaskBlock(blocks []tools.ToolUseBlock) bool {
	for _, b := range blocks {
		if b.Name == "task" {
			return true
		}
	}
	return false
}

// injectBackgroundNotifications drains the background manager's notification
// queue and injects completed task results as a user message before the next
// LLM call. Each notification becomes a <background-result> block so the
// model can clearly distinguish them from the main conversation.
func (r *Runner) injectBackgroundNotifications(ch chan<- tools.Event) {
	notifs := r.bgMgr.DrainNotifications()
	if len(notifs) == 0 {
		return
	}

	var b strings.Builder
	b.WriteString("<background-results>\n")
	for _, n := range notifs {
		b.WriteString(fmt.Sprintf("  <result task_id=%q status=%q>%s</result>\n",
			n.TaskID, string(n.Status), n.Summary))

		if ch != nil {
			ch <- EventBackgroundResult{
				TaskID:  n.TaskID,
				Status:  string(n.Status),
				Summary: n.Summary,
			}
		}
	}
	b.WriteString("</background-results>")

	r.messages = append(r.messages, anthropic.NewBetaUserMessage(
		anthropic.BetaContentBlockParamUnion{
			OfText: &anthropic.BetaTextBlockParam{Text: b.String()},
		}))
}

// injectTeamInbox drains the lead agent's team inbox and injects teammate
// messages before the next LLM call. Each message becomes a <team-inbox>
// block so the model can clearly distinguish them from the main conversation.
func (r *Runner) injectTeamInbox(ch chan<- tools.Event) {
	msgs, err := r.teamMgr.ReadInbox(r.teamMgr.LeadName())
	if err != nil || len(msgs) == 0 {
		return
	}

	block := team.FormatInboxMessages(msgs)
	if block == "" {
		return
	}

	r.messages = append(r.messages, anthropic.NewBetaUserMessage(
		anthropic.BetaContentBlockParamUnion{
			OfText: &anthropic.BetaTextBlockParam{Text: block},
		}))
}

// Clear resets the message history to start a fresh conversation.
func (r *Runner) Clear() {
	r.messages = nil
}

// RenderTodo returns the formatted in-memory todo list string.
// Returns an empty string if the todo tool is not available.
func (r *Runner) RenderTodo() string {
	t, ok := r.agent.registry.Tool("todo").(*tools.TodoWriteTool)
	if !ok {
		return ""
	}
	return t.Render()
}

// RenderTaskList returns the formatted persistent task graph string.
// Returns an empty string if the task_list tool is not available.
func (r *Runner) RenderTaskList() string {
	t, ok := r.agent.registry.Tool("task_list").(*tools.TaskListTool)
	if !ok {
		return ""
	}
	return t.Render()
}

// RenderTeamRoster returns the formatted team roster string.
// Returns an empty string if there is no team manager.
func (r *Runner) RenderTeamRoster() string {
	if r.teamMgr == nil {
		return ""
	}
	roster := r.teamMgr.Roster()
	if len(roster) == 0 {
		return "No teammates."
	}

	statusIcon := map[team.Status]string{
		team.StatusWorking:  "[>]",
		team.StatusIdle:     "[-]",
		team.StatusShutdown: "[x]",
	}
	statusLabel := map[team.Status]string{
		team.StatusWorking:  "working",
		team.StatusIdle:     "idle",
		team.StatusShutdown: "shutdown",
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Team lead: %s\n", r.teamMgr.LeadName()))
	b.WriteString(fmt.Sprintf("%-4s %s %s %s\n",
		"", padDisplay("Name", 18), padDisplay("Role", 26), "Status"))

	for _, m := range roster {
		icon := statusIcon[m.Status]
		label := statusLabel[m.Status]
		if icon == "" {
			icon = "[?]"
			label = string(m.Status)
		}
		line := fmt.Sprintf("%s %s %s %s",
			icon,
			padDisplay(m.Name, 18),
			padDisplay(m.Role, 26),
			label)
		if m.Model != "" {
			line += fmt.Sprintf(" (%s)", m.Model)
		}
		b.WriteString(line + "\n")
	}

	return b.String()
}

// displayWidth returns the visual width of s in a terminal, where CJK
// characters (Chinese/Japanese/Korean) occupy 2 columns and ASCII occupies 1.
func displayWidth(s string) int {
	w := 0
	for _, r := range s {
		// CJK Unified Ideographs (4E00-9FFF), CJK Unified Ideographs Extension A (3400-4DBF),
		// CJK Compatibility Ideographs (F900-FAFF), fullwidth forms (FF01-FF60,FFE0-FFE6),
		// and common CJK punctuation/ruby ranges.
		if r >= 0x2E80 && r <= 0xFE4F || r >= 0xFF01 && r <= 0xFF60 || r >= 0xFFE0 && r <= 0xFFE6 {
			w += 2
		} else {
			w++
		}
	}
	return w
}

// padDisplay pads s to at least width visual columns. If s is wider, it's
// truncated with "…" (single ellipsis, 1 column) to fit width.
func padDisplay(s string, width int) string {
	dw := displayWidth(s)
	if dw >= width {
		// Truncate with ellipsis if too wide.
		runes := []rune(s)
		var buf strings.Builder
		remain := width - 1 // leave room for ellipsis
		for _, r := range runes {
			rw := 2
			if !(r >= 0x2E80 && r <= 0xFE4F || r >= 0xFF01 && r <= 0xFF60 || r >= 0xFFE0 && r <= 0xFFE6) {
				rw = 1
			}
			if remain-rw < 0 {
				break
			}
			buf.WriteRune(r)
			remain -= rw
		}
		buf.WriteRune('…')
		return buf.String()
	}
	return s + strings.Repeat(" ", width-dw)
}
