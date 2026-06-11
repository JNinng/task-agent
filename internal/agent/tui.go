package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/cursor"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"task-agent/internal/agent/tools"
)

// thinkTickMsg 思考状态下的定时刷新消息。
type thinkTickMsg struct{}

// agentCommands 定义可用的斜杠命令及其描述。
var agentCommands = map[string]string{
	"/exit":  "退出程序",
	"/q":     "退出程序（快捷方式）",
	"/todo":  "显示待办任务列表",
	"/clear": "清空会话上下文",
}

// model 是 Bubble Tea 的核心模型，持有 UI 组件状态和展示内容。
// agent 循环逻辑由 Runner 管理，model 仅负责展示。
type model struct {
	runner   *Runner
	runnerCh <-chan any // 当前活跃的事件 channel

	// UI 组件
	textarea     textarea.Model
	viewport     viewport.Model
	autocomplete Autocomplete
	content      []string

	senderStyle lipgloss.Style
	thinking    bool
	err         error

	// 终端尺寸（缓存用于动态布局）。
	termWidth  int
	termHeight int
}

// NewTUI 创建并配置 Bubble Tea 程序实例。
func NewTUI(runner *Runner, opts ...tea.ProgramOption) *tea.Program {
	ta := textarea.New()
	ta.Placeholder = "Ask something..."
	ta.SetVirtualCursor(false)
	ta.Focus()
	ta.Prompt = "┃ "
	ta.CharLimit = 8000
	ta.SetWidth(80)
	ta.SetHeight(3)
	ta.ShowLineNumbers = false

	s := ta.Styles()
	s.Focused.CursorLine = lipgloss.NewStyle()
	ta.SetStyles(s)

	ta.KeyMap.InsertNewline.SetEnabled(false)

	vp := viewport.New(viewport.WithWidth(80), viewport.WithHeight(20))
	vp.KeyMap.Left.SetEnabled(false)
	vp.KeyMap.Right.SetEnabled(false)

	ac := NewAutocomplete(agentCommands, '/')
	ac.SetListWidth(80)

	return tea.NewProgram(&model{
		runner:       runner,
		textarea:     ta,
		viewport:     vp,
		autocomplete: ac,
		content:      []string{},
		senderStyle:  lipgloss.NewStyle().Foreground(lipgloss.Color("5")),
	}, opts...)
}

// Init 初始化 Bubble Tea 程序。
func (m *model) Init() tea.Cmd {
	return textarea.Blink
}

// Update 是 Bubble Tea 的事件循环，处理所有消息。
func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.termWidth = msg.Width
		m.termHeight = msg.Height
		m.viewport.SetWidth(msg.Width)
		m.textarea.SetWidth(msg.Width)
		m.autocomplete.SetListWidth(msg.Width)
		m.resizeViewport()
		m.refreshViewport()

	case tea.MouseMsg:
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd

	case tea.KeyPressMsg:
		return m.handleKey(msg)

	case cursor.BlinkMsg:
		var cmd tea.Cmd
		m.textarea, cmd = m.textarea.Update(msg)
		return m, cmd

	case thinkTickMsg:
		if m.thinking {
			m.refreshViewport()
			return m, thinkTick()
		}
		return m, nil

	case EventThinking:
		m.thinking = true
		m.refreshViewport()
		return m, watchRunner(m.runnerCh)

	case EventText:
		m.content = append(m.content, msg.Content)
		m.refreshViewport()
		return m, watchRunner(m.runnerCh)

	case EventToolCalls:
		for _, tc := range msg.Tools {
			preview := toolPreview(tc)
			m.content = append(m.content, fmt.Sprintf("\033[33m> %s(%s)\033[0m", tc.Name, preview))
		}
		m.refreshViewport()
		return m, watchRunner(m.runnerCh)

	case tools.SubagentProgress:
		status := fmt.Sprintf("\033[33m  task: %s\033[0m", msg.Description)
		if msg.Turn > 0 {
			// In-progress update from subagent loop: replace previous status line.
			if len(m.content) > 0 && strings.HasPrefix(m.content[len(m.content)-1], "\033[33m  task:") {
				m.content[len(m.content)-1] = fmt.Sprintf("\033[33m  task: %s (%d/%d)\033[0m", msg.Description, msg.Turn, msg.MaxTurns)
			} else {
				m.content = append(m.content, fmt.Sprintf("\033[33m  task: %s (%d/%d)\033[0m", msg.Description, msg.Turn, msg.MaxTurns))
			}
		} else {
			m.content = append(m.content, status)
		}
		m.refreshViewport()
		return m, watchRunner(m.runnerCh)

	case EventToolResults:
		for _, tr := range msg.Results {
			out := tr.Content
			if tr.Name == "todo" {
				continue // rendered by EventTodoUpdate
			}
			if len(out) > 200 {
				m.content = append(m.content, out[:200], fmt.Sprintf("... (%d more bytes)", len(out)-200))
			} else {
				m.content = append(m.content, out)
			}
		}
		m.refreshViewport()
		return m, watchRunner(m.runnerCh)

	case EventTodoUpdate:
		m.content = append(m.content, "\033[36m"+msg.Content+"\033[0m")
		m.refreshViewport()
		return m, watchRunner(m.runnerCh)

	case EventError:
		m.content = append(m.content, fmt.Sprintf("\033[31mError: %v\033[0m", msg.Err))
		m.thinking = false
		m.refreshViewport()
		return m, nil

	case EventDone:
		m.thinking = false
		m.content = append(m.content, "")
		m.refreshViewport()
		return m, nil
	}

	return m, nil
}

// View 渲染当前 UI 为终端字符串。
func (m *model) View() tea.View {
	viewportView := m.viewport.View()

	var middle string
	if m.autocomplete.Active() {
		middle = m.autocomplete.View() + "\n"
	}

	v := tea.NewView(viewportView + "\n" + middle + m.textarea.View())

	c := m.textarea.Cursor()
	if c != nil {
		// 测量 textarea 上方所有内容的高度来计算光标 Y 偏移，
		// 避免脆弱的手动换行符计数。
		prefix := viewportView + "\n" + middle
		c.Y += lipgloss.Height(prefix) - 1
	}
	v.Cursor = c
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

// handleKey 处理按键消息。
func (m *model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.thinking {
		return m, nil
	}

	// 优先委托给自动补全——当建议面板打开时它会拦截导航键。
	if handled, accepted := m.autocomplete.HandleKey(msg.String()); handled {
		if accepted {
			m.autocomplete.Apply(&m.textarea)
		}
		m.resizeViewport()
		return m, nil
	}

	switch msg.String() {
	case "enter":
		return m.submit()
	case "ctrl+c", "esc":
		return m, tea.Quit
	case "up", "down", "pgup", "pgdown", "home", "end":
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	default:
		var cmd tea.Cmd
		m.textarea, cmd = m.textarea.Update(msg)
		// 每次文本变更后扫描自动补全触发字符。
		m.autocomplete.ScanTextarea(m.textarea)
		m.resizeViewport()
		return m, cmd
	}
}

// submit 提交用户输入，通过 Runner 启动新一轮 agent 循环。
func (m *model) submit() (tea.Model, tea.Cmd) {
	query := strings.TrimSpace(m.textarea.Value())
	if query == "" {
		return m, nil
	}

	if query == "/q" || query == "/exit" {
		return m, tea.Quit
	}

	if query == "/todo" {
		m.content = append(m.content, m.senderStyle.Render(">>> ")+query)
		m.textarea.Reset()
		m.autocomplete.Reset()
		if t, ok := m.runner.agent.registry.Tool("todo").(*tools.TodoWriteTool); ok {
			m.content = append(m.content, "\033[36m"+t.Render()+"\033[0m")
		} else {
			m.content = append(m.content, "\033[31mTodo tool not available\033[0m")
		}
		m.refreshViewport()
		return m, nil
	}

	m.content = append(m.content, m.senderStyle.Render(">>> ")+query)
	m.textarea.Reset()
	m.autocomplete.Reset()
	m.thinking = true
	m.refreshViewport()

	ch := m.runner.Run(context.Background(), query)
	m.runnerCh = ch
	return m, tea.Batch(watchRunner(ch), thinkTick(), textarea.Blink)
}

// watchRunner 从 Runner 事件 channel 读取下一个事件并转换为 Bubble Tea 消息。
func watchRunner(ch <-chan any) tea.Cmd {
	return func() tea.Msg {
		event, ok := <-ch
		if !ok {
			return nil
		}
		return event
	}
}

// thinkTick 创建一个 150ms 的定时器，用于思考期间的 UI 刷新。
func thinkTick() tea.Cmd {
	return tea.Tick(150*time.Millisecond, func(t time.Time) tea.Msg {
		return thinkTickMsg{}
	})
}

// resizeViewport 重新计算视口高度，使 textarea 和
// 建议列表不重叠地排布。多减 1 是为 viewport 与
// 建议/文本区域之间的 "\n" 分隔行留空间。
func (m *model) resizeViewport() {
	if m.termHeight <= 0 {
		return
	}
	viewportHeight := max(1, m.termHeight-m.textarea.Height()-1-m.autocomplete.Height())
	m.viewport.SetHeight(viewportHeight)
	m.viewport.GotoBottom()
}

// toolPreview returns a concise preview string for a tool call block.
func toolPreview(tc tools.ToolUseBlock) string {
	switch tc.Name {
	case "bash":
		var args struct {
			Command string `json:"command"`
		}
		if err := json.Unmarshal(tc.Input, &args); err == nil && args.Command != "" {
			s := args.Command
			if len(s) > 80 {
				s = s[:80] + "..."
			}
			return s
		}
	case "todo":
		var args struct {
			Items []struct {
				ID     string `json:"id"`
				Text   string `json:"text"`
				Status string `json:"status"`
			} `json:"items"`
		}
		if err := json.Unmarshal(tc.Input, &args); err == nil {
			total := len(args.Items)
			done := 0
			for _, item := range args.Items {
				if item.Status == "completed" {
					done++
				}
			}
			return fmt.Sprintf("%d/%d done", done, total)
		}
	case "task":
		var args struct {
			Prompt      string `json:"prompt"`
			Description string `json:"description"`
		}
		if err := json.Unmarshal(tc.Input, &args); err == nil {
			if args.Description != "" {
				return args.Description
			}
			s := args.Prompt
			if len(s) > 80 {
				s = s[:80] + "..."
			}
			return s
		}
	case "read_file", "write_file", "edit_file":
		var args struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal(tc.Input, &args); err == nil && args.Path != "" {
			return args.Path
		}
	}
	return "..."
}

// refreshViewport 根据 content 刷新 viewport 的显示内容。
func (m *model) refreshViewport() {
	s := strings.Join(m.content, "\n")
	if m.thinking {
		s += "\n  \033[2m...\033[0m"
	}
	m.viewport.SetContent(lipgloss.NewStyle().Width(m.viewport.Width()).Render(s))
	m.viewport.GotoBottom()
}
