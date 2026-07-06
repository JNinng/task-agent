package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"task-agent/internal/agent"
	"time"

	"charm.land/bubbles/v2/cursor"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"task-agent/internal/agent/tools"
)

var memctxDefaultDir = filepath.Join(agent.DirAgent, "memctx")

// thinkTickMsg 思考状态下的定时刷新消息。
type thinkTickMsg struct{}

// agentCommands 定义可用的斜杠命令及其描述。
var agentCommands = map[string]string{
	"/exit":   "退出程序",
	"/q":      "退出程序（快捷方式）",
	"/todo":   "显示待办任务列表",
	"/task":   "显示持久化任务列表",
	"/clear":  "清空会话上下文",
	"/memctx": "输出上下文 /memctx [file]（* 快速导出；. 当前目录；自动补 .jsonl；非法路径回退到默认目录）",
}

// model 是 Bubble Tea 的核心模型，持有 UI 组件状态和展示内容。
// agent 循环逻辑由 Runner 管理，model 仅负责展示。
type model struct {
	session  agent.Session
	runnerCh <-chan tools.Event // 当前活跃的事件 channel

	// 控制 Runner.Run 的 goroutine 生命周期。
	// ctx 的新子 context 在每次 submit 时创建，退出时取消。
	ctx    context.Context
	cancel context.CancelFunc

	// UI 组件
	textarea     textarea.Model
	viewport     viewport.Model
	autocomplete Autocomplete
	content      []string

	senderStyle lipgloss.Style
	thinking    bool

	// 终端尺寸（缓存用于动态布局）。
	termWidth  int
	termHeight int
}

// NewTUI 创建并配置 Bubble Tea 程序实例。
func NewTUI(session agent.Session, opts ...tea.ProgramOption) *tea.Program {
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

	ctx, cancel := context.WithCancel(context.Background())

	return tea.NewProgram(&model{
		session:      session,
		ctx:          ctx,
		cancel:       cancel,
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
		m.syncContent()
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

	case agent.EventThinking:
		m.thinking = true
		m.refreshViewport()
		return m, watchRunner(m.runnerCh)

	case agent.EventText:
		return m.emitEvent(msg.Content)

	case agent.EventToolCalls:
		var lines []string
		for _, tc := range msg.Tools {
			lines = append(lines, styleYellow.Render(fmt.Sprintf("> %s(%s)", tc.Name, m.session.PreviewToolUse(tc))))
		}
		return m.emitEvent(lines...)

	case tools.SubagentProgress:
		status := styleYellow.Render(fmt.Sprintf("  task: %s", msg.Description))
		if msg.Turn > 0 {
			// In-progress update from subagent loop: replace previous status line.
			if len(m.content) > 0 && strings.HasPrefix(m.content[len(m.content)-1], "\033[33m  task:") {
				m.content[len(m.content)-1] = styleYellow.Render(fmt.Sprintf("  task: %s (%d/%d)", msg.Description, msg.Turn, msg.MaxTurns))
			} else {
				m.appendContent(styleYellow.Render(fmt.Sprintf("  task: %s (%d/%d)", msg.Description, msg.Turn, msg.MaxTurns)))
			}
		} else {
			m.appendContent(status)
		}
		m.refreshViewport()
		return m, watchRunner(m.runnerCh)

	case agent.EventToolResults:
		var lines []string
		for _, tr := range msg.Results {
			out := tr.Content
			if tr.Name == "todo" {
				continue
			}
			if len(out) > 200 {
				lines = append(lines, out[:200], fmt.Sprintf("... (%d more bytes)", len(out)-200))
			} else {
				lines = append(lines, out)
			}
		}
		return m.emitEvent(lines...)

	case agent.EventBackgroundResult:
		var icon string
		switch msg.Status {
		case "completed":
			icon = styleGreen.Render("[bg done]")
		case "failed":
			icon = styleRed.Render("[bg failed]")
		case "timeout":
			icon = styleYellow.Render("[bg timeout]")
		default:
			icon = styleCyan.Render("[bg]")
		}
		return m.emitEvent(fmt.Sprintf("%s %s: %s", icon, msg.TaskID, msg.Summary))

	case agent.EventTodoUpdate:
		return m.emitEvent(styleCyan.Render(msg.Content))

	case agent.EventError:
		m.appendContent(styleRed.Render(fmt.Sprintf("Error: %v", msg.Err)))
		m.thinking = false
		m.refreshViewport()
		return m, nil

	case agent.EventDone:
		m.thinking = false
		m.appendContent("")
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
		m.cancel()
		return m, tea.Quit
	case "up", "down", "pgup", "pgdown", "home", "end":
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		m.syncContent()
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
		m.appendContent(m.senderStyle.Render(">>> ") + query)
		m.textarea.Reset()
		m.autocomplete.Reset()
		if s := m.session.RenderTodo(); s != "" {
			m.appendContent(styleCyan.Render(s))
		} else {
			m.appendContent(styleRed.Render("Todo tool not available"))
		}
		m.refreshViewport()
		return m, nil
	}

	if query == "/task" {
		m.appendContent(m.senderStyle.Render(">>> ") + query)
		m.textarea.Reset()
		m.autocomplete.Reset()
		if s := m.session.RenderTaskList(); s != "" {
			m.appendContent(styleCyan.Render(s))
		} else {
			m.appendContent(styleRed.Render("Task list tool not available"))
		}
		m.refreshViewport()
		return m, nil
	}

	if query == "/clear" {
		m.session.Clear()
		m.content = nil
		m.textarea.Reset()
		m.autocomplete.Reset()
		m.refreshViewport()
		return m, nil
	}

	if query == "/memctx" || strings.HasPrefix(query, "/memctx ") {
		m.appendContent(m.senderStyle.Render(">>> ") + query)
		m.textarea.Reset()
		m.autocomplete.Reset()
		m.handleMemctx(query)
		m.refreshViewport()
		return m, nil
	}

	m.appendContent(m.senderStyle.Render(">>> ") + query)
	m.textarea.Reset()
	m.autocomplete.Reset()
	m.thinking = true
	m.refreshViewport()

	// 取消前一次运行（若有）防止 goroutine 泄漏。
	m.cancel()
	// 排空旧 channel 防止前一次 watchRunner 残留导致事件交错。
	if m.runnerCh != nil {
		go func(old <-chan tools.Event) {
			for range old {
			}
		}(m.runnerCh)
	}
	m.ctx, m.cancel = context.WithCancel(context.Background())
	ch := m.session.Run(m.ctx, query)
	m.runnerCh = ch
	return m, tea.Batch(watchRunner(ch), thinkTick(), textarea.Blink)
}

// watchRunner 从 Runner 事件 channel 读取下一个事件并转换为 Bubble Tea 消息。
func watchRunner(ch <-chan tools.Event) tea.Cmd {
	return func() tea.Msg {
		event, ok := <-ch
		if !ok {
			return agent.EventDone{}
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

// isValidPath checks if the path is syntactically valid on this OS.
// Control characters (0-31) are universally invalid in paths.
// The filename component must not contain Windows-invalid characters
// (< > : " | ? *) — these are bad practice everywhere.
func isValidPath(p string) bool {
	if p == "" {
		return false
	}
	for _, r := range p {
		if r < 32 {
			return false
		}
	}
	base := filepath.Base(p)
	if strings.ContainsAny(base, `<>:"|?*`) {
		return false
	}
	return true
}

// handleMemctx outputs the current message context as JSON.
// Without arguments it prints to the terminal viewport.
// With a filename argument it writes JSONL to disk:
//
//	/memctx foo       → .task-agent/memctx/foo.jsonl (自动补后缀)
//	/memctx *         → .task-agent/memctx/<timestamp>.jsonl
//	/memctx .         → CWD/<timestamp>.jsonl
//	/memctx ..\x.jsonl → 上级目录的 x.jsonl
//	/memctx D:\x.jsonl → 绝对路径直写
//
// 路径含控制字符或文件名为 <>:"|?* 时整个参数替换为时间戳名，写入默认目录。
//
//	/memctx '|bad'    → .task-agent/memctx/<timestamp>.jsonl  （目录信息丢失）
//	/memctx a?b.jsonl → .task-agent/memctx/<timestamp>.jsonl
//	/memctx ../|a     → .task-agent/memctx/<timestamp>.jsonl  （../ 也丢失）
func (m *model) handleMemctx(query string) {
	msgs := m.session.Messages()

	// Parse optional filename argument
	arg := strings.TrimPrefix(query, "/memctx")
	arg = strings.TrimSpace(arg)

	if arg != "" {
		// "*" → quick export with generated name; also reject system-invalid paths
		if arg == "*" || !isValidPath(arg) {
			arg = fmt.Sprintf("memctx_%s.jsonl", time.Now().Format("2006-01-02-150405"))
		} else {
			// Default to .jsonl if no file extension
			if filepath.Ext(arg) == "" {
				arg += ".jsonl"
			}
		}

		// Write to file — resolve path relative to CWD if arg starts
		// with "." or ".." as a path component, otherwise relative to
		// .task-agent/memctx/.
		var baseDir string
		first := arg
		if idx := strings.IndexAny(arg, `/\`); idx >= 0 {
			first = arg[:idx]
		}
		if first == "." || first == ".." {
			cwd, err := os.Getwd()
			if err != nil {
				m.appendContent(styleRed.Render(fmt.Sprintf("memctx: getwd: %v", err)))
				return
			}
			baseDir = cwd
		} else {
			baseDir = memctxDefaultDir
		}
		path := filepath.Join(baseDir, arg)
		// If the resolved path is an existing directory (e.g. "."
		// resolves to CWD), generate a timestamp name inside it.
		if stat, err := os.Stat(path); err == nil && stat.IsDir() {
			path = filepath.Join(path, fmt.Sprintf("memctx_%s.jsonl", time.Now().Format("2006-01-02-150405")))
		}
		parent := filepath.Dir(path)
		if err := os.MkdirAll(parent, 0700); err != nil {
			m.appendContent(styleRed.Render(fmt.Sprintf("memctx: mkdir %s: %v", parent, err)))
			return
		}
		f, err := os.Create(path)
		if err != nil {
			m.appendContent(styleRed.Render(fmt.Sprintf("memctx: create %s: %v", path, err)))
			return
		}
		defer f.Close()

		enc := json.NewEncoder(f)
		count := 0
		for _, msg := range msgs {
			if err := enc.Encode(msg); err != nil {
				m.appendContent(styleRed.Render(fmt.Sprintf("memctx: encode msg %d: %v", count, err)))
				return
			}
			count++
		}
		m.appendContent(styleGreen.Render(fmt.Sprintf("memctx: %d messages → %s", count, path)))
		return
	}

	// Print to terminal (one JSON object per line for readability)
	if len(msgs) == 0 {
		m.appendContent("memctx: (no messages)")
		return
	}

	m.appendContent(fmt.Sprintf("memctx: %d messages", len(msgs)))
	for i, msg := range msgs {
		data, err := json.MarshalIndent(msg, "", "  ")
		if err != nil {
			m.appendContent(styleRed.Render(fmt.Sprintf("  [%d] marshal error: %v", i, err)))
			continue
		}
		// Truncate per-message output to avoid flooding the terminal
		s := string(data)
		const maxPerMsg = 2000
		if len(s) > maxPerMsg {
			s = s[:maxPerMsg] + fmt.Sprintf("\n  ... (%d more bytes)", len(s)-maxPerMsg)
		}
		m.appendContent(fmt.Sprintf("  [%d] %s", i, s))
	}
}

// 命名样式常量，替代散落在代码中的 ANSI 转义码。
var (
	styleYellow = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	styleGreen  = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	styleRed    = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	styleCyan   = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	styleDim    = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
)

// maxContentLines 限制 m.content 最大条目数，防止无限增长。
const maxContentLines = 2000

// appendContent 追加内容行到 viewport，超出上限时裁剪最旧的行。
func (m *model) appendContent(lines ...string) {
	m.content = append(m.content, lines...)
	if len(m.content) > maxContentLines {
		m.content = m.content[len(m.content)-maxContentLines:]
	}
}

// emitEvent 是多个事件 handler 的公共模式：追加内容 → 刷新 viewport → 监听下一个事件。
func (m *model) emitEvent(lines ...string) (tea.Model, tea.Cmd) {
	m.appendContent(lines...)
	m.refreshViewport()
	return m, watchRunner(m.runnerCh)
}

// syncContent 将 m.content 渲染到 viewport 中，但不移动滚动位置。
// 适用于鼠标滚动等用户主动导航的场景。
func (m *model) syncContent() {
	s := strings.Join(m.content, "\n")
	if m.thinking {
		s += "\n" + styleDim.Render("  ...")
	}
	m.viewport.SetContent(lipgloss.NewStyle().Width(m.viewport.Width()).Render(s))
}

// scrollToBottom 将 viewport 滚动到底部显示最新内容。
func (m *model) scrollToBottom() {
	m.viewport.GotoBottom()
}

// refreshViewport 根据 content 刷新 viewport 的显示内容并滚动到底部。
// 是在新事件到达时更新 UI 的便捷方法；如需保留用户滚动位置请用 syncContent。
func (m *model) refreshViewport() {
	m.syncContent()
	m.scrollToBottom()
}
