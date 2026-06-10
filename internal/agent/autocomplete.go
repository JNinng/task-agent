package agent

import (
	"fmt"
	"sort"
	"strings"

	"charm.land/bubbles/v2/textarea"
	"charm.land/lipgloss/v2"
)

// defaultMaxVisible 限制同时可见的建议条目数，防止补全面板占满整个终端。
const defaultMaxVisible = 5

// Suggestion 表示单条自动补全建议。
type Suggestion struct {
	Text        string // 完整替换文本，含触发符，如 "/exit"
	Description string // 描述列中显示的提示文字
	ReplaceFrom int    // 在原输入值中开始替换的列位置
}

// Autocomplete 在检测到触发字符时提供输入联想建议。它设计为 textarea
// 或 textinput 组件的增强组件，配合使用。
type Autocomplete struct {
	suggestions []Suggestion
	selected    int
	active      bool
	scrollOff   int
	maxVisible  int
	listWidth   int

	items   map[string]string
	trigger rune

	// 用户确认选择后保存的接受值。
	acceptedText        string
	acceptedReplaceFrom int

	// 公开样式，调用方可自定义外观。
	NormalStyle   lipgloss.Style
	SelectedStyle lipgloss.Style
	DescStyle     lipgloss.Style
	DescSelStyle  lipgloss.Style
}

// NewAutocomplete 使用给定的数据源和触发字符创建 Autocomplete。
// items 将补全文本映射到其描述。
func NewAutocomplete(items map[string]string, trigger rune) Autocomplete {
	baseSelected := lipgloss.NewStyle().
		Background(lipgloss.Color("5")).
		Foreground(lipgloss.Color("0"))

	return Autocomplete{
		items:         items,
		trigger:       trigger,
		maxVisible:    defaultMaxVisible,
		NormalStyle:   lipgloss.NewStyle().Padding(0, 1),
		SelectedStyle: baseSelected.Padding(0, 1),
		DescStyle:     lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Padding(0, 1),
		DescSelStyle: lipgloss.NewStyle().
			Background(lipgloss.Color("5")).
			Foreground(lipgloss.Color("15")).
			Padding(0, 1),
	}
}

// Active 返回建议面板当前是否可见。
func (a *Autocomplete) Active() bool { return a.active }

// AcceptedText 返回用户确认的建议文本。
func (a *Autocomplete) AcceptedText() string { return a.acceptedText }

// AcceptedReplaceFrom 返回在原输入值中开始替换的列位置。
func (a *Autocomplete) AcceptedReplaceFrom() int { return a.acceptedReplaceFrom }

// SetListWidth 设置渲染建议面板时使用的宽度。
func (a *Autocomplete) SetListWidth(w int) { a.listWidth = w }

// Reset 隐藏建议面板并清空内部状态。
func (a *Autocomplete) Reset() {
	a.suggestions = nil
	a.selected = 0
	a.active = false
	a.scrollOff = 0
}

// Height 返回建议面板占用的行数，隐藏时返回 0。
// 高度已考虑描述换行后的实际行数，确保终端布局准确。
func (a *Autocomplete) Height() int {
	if !a.active || len(a.suggestions) == 0 {
		return 0
	}

	listWidth := a.listWidth
	if listWidth <= 0 {
		listWidth = 30
	}

	visible := min(len(a.suggestions), a.maxVisible)
	end := min(a.scrollOff+visible, len(a.suggestions))

	// 测量命令列宽度（与 View 中的逻辑一致）。
	codeWidth := 0
	for _, s := range a.suggestions[a.scrollOff:end] {
		if w := lipgloss.Width(s.Text); w > codeWidth {
			codeWidth = w
		}
	}
	codeWidth += 2
	descWidth := listWidth - codeWidth
	if descWidth < 1 {
		descWidth = 1
	}

	// 累计行数：1 行顶部分隔线 + 每条建议的换行行数。
	lines := 1
	for i := a.scrollOff; i < end; i++ {
		dl := wrapText(a.suggestions[i].Description, descWidth)
		lines += max(1, len(dl))
	}
	return lines
}

// Scan 在给定光标列位置扫描输入值中的触发字符，并填充匹配的建议。
// 每次文本变更后都应调用此方法。
func (a *Autocomplete) Scan(value string, col int) {
	a.suggestions = nil
	a.selected = 0
	a.active = false
	a.scrollOff = 0

	if len(value) == 0 {
		return
	}

	if col > len(value) {
		col = len(value)
	}

	// 找到当前行的起始位置。
	lineStart := col
	for lineStart > 0 && value[lineStart-1] != '\n' {
		lineStart--
	}

	currentLine := value[lineStart:col]
	triggerStr := string(a.trigger)

	// 仅当触发字符位于行首时才激活自动补全。
	if !strings.HasPrefix(currentLine, triggerStr) {
		return
	}
	triggerPos := 0

	word := currentLine[triggerPos:] // 例如 "/exi"
	if len(word) < 1 {
		return
	}

	// 收集匹配的建议。
	for code, desc := range a.items {
		if strings.HasPrefix(strings.ToLower(code), strings.ToLower(word)) {
			a.suggestions = append(a.suggestions, Suggestion{
				Text:        code,
				Description: desc,
				ReplaceFrom: lineStart + triggerPos,
			})
		}
	}

	// 按字母排序以保证列表稳定。
	sort.Slice(a.suggestions, func(i, j int) bool {
		return a.suggestions[i].Text < a.suggestions[j].Text
	})

	if len(a.suggestions) > 0 {
		a.active = true
	}
}

// Apply 将已接受的建议写入 textarea，替换触发符到光标之间的文本，
// 并将光标定位到替换文本的末尾。应在 HandleKey 返回 accepted=true 后调用。
func (a *Autocomplete) Apply(ta *textarea.Model) {
	currentValue := ta.Value()
	col := ta.Column()
	newValue := currentValue[:a.acceptedReplaceFrom] +
		a.acceptedText + currentValue[col:]
	ta.SetValue(newValue)
	ta.SetCursorColumn(a.acceptedReplaceFrom + len(a.acceptedText))
}

// ScanTextarea 是对 Scan 的便捷封装，直接从 textarea 读取值和光标位置。
func (a *Autocomplete) ScanTextarea(ta textarea.Model) {
	a.Scan(ta.Value(), ta.Column())
}

// HandleKey 处理自动补全导航和选择的按键事件。
// handled 表示该按键是否被自动补全消费。
// accepted 表示用户是否确认了某条建议（Tab 键）。
// accepted 为 true 时，通过 Apply 将结果写入 textarea。
func (a *Autocomplete) HandleKey(key string) (handled bool, accepted bool) {
	if !a.active || len(a.suggestions) == 0 {
		return false, false
	}

	switch key {
	case "esc":
		a.Reset()
		return true, false

	case "tab":
		s := a.suggestions[a.selected]
		a.acceptedText = s.Text
		a.acceptedReplaceFrom = s.ReplaceFrom
		a.Reset()
		return true, true

	case "ctrl+n", "down":
		a.selected = (a.selected + 1) % len(a.suggestions)
		a.scrollSelectedIntoView()
		return true, false

	case "ctrl+p", "up":
		a.selected--
		if a.selected < 0 {
			a.selected = len(a.suggestions) - 1
		}
		a.scrollSelectedIntoView()
		return true, false
	}

	return false, false
}

// scrollSelectedIntoView 调整滚动偏移，使选中项在建议列表窗口中可见。
func (a *Autocomplete) scrollSelectedIntoView() {
	if a.selected < a.scrollOff {
		a.scrollOff = a.selected
	} else if a.selected >= a.scrollOff+a.maxVisible {
		a.scrollOff = a.selected - a.maxVisible + 1
	}
}

// View 将建议列表渲染为两列表格：命令列（左）和描述列（右）。
// 当描述超过可用宽度时，自动换行到下一行并缩进到描述列下方，
// 确保完整文字可见而不被截断。
func (a *Autocomplete) View() string {
	if !a.active || len(a.suggestions) == 0 {
		return ""
	}

	listWidth := a.listWidth
	if listWidth <= 0 {
		listWidth = 30
	}

	total := len(a.suggestions)
	visible := min(total, a.maxVisible)
	end := min(a.scrollOff+visible, total)

	// ── 列宽计算 ─────────────────────────────────────────────────
	// 测量可见范围内最宽的命令文本。
	codeWidth := 0
	for _, s := range a.suggestions[a.scrollOff:end] {
		if w := lipgloss.Width(s.Text); w > codeWidth {
			codeWidth = w
		}
	}
	codeWidth += 2 // 内边距
	descWidth := listWidth - codeWidth
	if descWidth < 1 {
		descWidth = 1 // 安全兜底：防止描述列宽度坍缩为零
	}

	// ── 顶部分隔线 ────────────────────────────────────────────────
	borderText := fmt.Sprintf(" %d/%d ", visible, total)
	borderStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("5")).
		Width(listWidth)
	topBorder := borderStyle.Render(
		borderText + strings.Repeat("─", max(0, listWidth/2-len(borderText))),
	)

	var rows []string
	rows = append(rows, topBorder)

	// ── 表格主体 ─────────────────────────────────────────────────
	for i := a.scrollOff; i < end; i++ {
		s := a.suggestions[i]
		selected := i == a.selected

		codeStyle := a.NormalStyle
		descStyle := a.DescStyle
		if selected {
			codeStyle = a.SelectedStyle
			descStyle = a.DescSelStyle
		}

		// 将描述按可用宽度换行，确保每个词都可见。
		descLines := wrapText(s.Description, descWidth)
		if len(descLines) == 0 {
			descLines = []string{""}
		}

		// 首行：命令单元格 + 首行描述单元格。
		codeCell := codeStyle.Width(codeWidth).Render(s.Text)
		descCell := descStyle.Width(descWidth).Render(descLines[0])
		rows = append(rows, codeCell+descCell)

		// 续行：带样式的空白占位（命令列区域）+ 换行描述行。
		// 使用带样式的占位确保选中背景色覆盖整行宽度。
		spacerCell := codeStyle.Width(codeWidth).Render("")
		for _, line := range descLines[1:] {
			rows = append(rows, spacerCell+descStyle.Width(descWidth).Render(line))
		}
	}

	return lipgloss.JoinVertical(lipgloss.Left, rows...)
}

// wrapText 将文本按给定显示宽度拆分为多行，在单词边界处断行。
// 若单个单词超过宽度则将其保留在独立行中，不做强制切断。
func wrapText(text string, width int) []string {
	if width <= 0 {
		return []string{text}
	}
	var lines []string
	var current string
	words := strings.Fields(text)
	for _, word := range words {
		candidate := current
		if candidate == "" {
			candidate = word
		} else {
			candidate += " " + word
		}
		if lipgloss.Width(candidate) > width {
			if current == "" {
				// 单单词超宽——保持完整放在独立行中。
				lines = append(lines, word)
			} else {
				lines = append(lines, current)
				current = word
			}
		} else {
			current = candidate
		}
	}
	if current != "" {
		lines = append(lines, current)
	}
	if len(lines) == 0 {
		lines = append(lines, "")
	}
	return lines
}
