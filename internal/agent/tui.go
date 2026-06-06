package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/cursor"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// thinkTickMsg 思考状态下的定时刷新消息。
type thinkTickMsg struct{}

// model 是 Bubble Tea 的核心模型，持有 UI 组件状态和展示内容。
// agent 循环逻辑由 Runner 管理，model 仅负责展示。
type model struct {
	runner   *Runner
	runnerCh <-chan any // 当前活跃的事件 channel

	// UI 组件
	textarea textarea.Model
	viewport viewport.Model
	content  []string

	senderStyle lipgloss.Style
	thinking    bool
	err         error
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

	return tea.NewProgram(&model{
		runner:      runner,
		textarea:    ta,
		viewport:    vp,
		content:     []string{},
		senderStyle: lipgloss.NewStyle().Foreground(lipgloss.Color("5")),
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
		m.viewport.SetWidth(msg.Width)
		m.textarea.SetWidth(msg.Width)
		m.viewport.SetHeight(msg.Height - m.textarea.Height())
		m.refreshViewport()

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

	case EventToolCall:
		m.content = append(m.content, fmt.Sprintf("\033[33m$ %s\033[0m", msg.Command))
		m.refreshViewport()
		return m, watchRunner(m.runnerCh)

	case EventToolResult:
		out := msg.Output
		if len(out) > 200 {
			m.content = append(m.content, out[:200], fmt.Sprintf("... (%d more bytes)", len(out)-200))
		} else {
			m.content = append(m.content, out)
		}
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
	v := tea.NewView(viewportView + "\n" + m.textarea.View())

	c := m.textarea.Cursor()
	if c != nil {
		c.Y += lipgloss.Height(viewportView)
	}
	v.Cursor = c
	v.AltScreen = true
	return v
}

// handleKey 处理按键消息。
func (m *model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.thinking {
		return m, nil
	}

	switch msg.String() {
	case "enter":
		return m.submit()
	case "ctrl+c", "esc":
		return m, tea.Quit
	default:
		var cmd tea.Cmd
		m.textarea, cmd = m.textarea.Update(msg)
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

	m.content = append(m.content, m.senderStyle.Render(">>> ")+query)
	m.textarea.Reset()
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

// refreshViewport 根据 content 刷新 viewport 的显示内容。
func (m *model) refreshViewport() {
	s := strings.Join(m.content, "\n")
	if m.thinking {
		s += "\n  \033[2m...\033[0m"
	}
	m.viewport.SetContent(lipgloss.NewStyle().Width(m.viewport.Width()).Render(s))
	m.viewport.GotoBottom()
}
