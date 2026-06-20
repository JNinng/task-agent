# Agent UI 架构分析

## 一、整体分层架构

```
┌─────────────────────────────────────────────────────────┐
│                    cmd/app/main.go                       │
│                    (程序入口点)                            │
└────────────────────┬────────────────────────────────────┘
                     │
┌────────────────────▼────────────────────────────────────┐
│                 internal/app/app.go                      │
│         ① agent.New() → ② tui.NewTUI(Runner) → p.Run() │
│           创建 Agent 核心      把 Runner 传给 TUI         │
└────────────┬──────────────────────────────┬─────────────┘
             │                              │
   ┌─────────▼──────────┐         ┌────────▼──────────────┐
   │  internal/agent/   │         │ internal/agent/tui/   │
   │  ┌──────────────┐  │         │                       │
   │  │  Session     │◄─┼─────────┤  model.session        │
   │  │  (接口)      │  │  依赖   │  (持有 Session 接口)   │
   │  └──────┬───────┘  │         │                       │
   │         │          │         │  Update() 类型断言      │
   │  ┌──────▼───────┐  │         │  处理 8 种 Event       │
   │  │  Runner      │──┼────────►│  watchRunner() 桥接    │
   │  │  (实现)      │  │  <-chan │                       │
   │  └──────────────┘  │  Event  └───────────────────────┘
   │                    │
   │  ┌──────────────┐  │
   │  │ tools.Registry│  │
   │  │ tools.Event  │  │
   │  │ tools.Previewer│ │
   │  └──────────────┘  │
   └────────────────────┘
```

核心原则：**TUI 只知道接口，不知道实现**。`app.go` 中 `agent.New()` 返回的 `Agent` 结构体暴露了 `Runner` 字段（类型 `*Runner`），但在传给 `tui.NewTUI()` 时是以 `agent.Session` 接口类型传递的 —— TUI 文件 (`tui.go`) 中的 `model.session` 字段类型就是 `agent.Session`，它完全不引用 `Runner` 这个具体类型。

---

## 二、目录结构

```
task-agent\
├── cmd/app/main.go              -- 入口
├── internal/
│   ├── agent/                   -- 核心 agent 逻辑（package agent）
│   │   ├── agent.go             -- Agent 结构体、New()、工具注册、system prompt
│   │   ├── runner.go            -- Runner（Session 实现），主 LLM 循环
│   │   ├── session.go           -- Session 接口定义
│   │   ├── event.go             -- 事件类型（EventText, EventToolCalls 等）
│   │   ├── compact.go           -- 上下文压缩（microCompact, autoCompact）
│   │   ├── paths.go             -- 集中化的路径常量
│   │   ├── bash.go              -- Bash 执行辅助
│   │   ├── tools/               -- 所有工具实现（package tools）
│   │   │   ├── tool.go          -- Event/Tool/Previewer 接口、Registry
│   │   │   ├── bash.go          -- Bash 工具
│   │   │   ├── read.go          -- 文件读取工具
│   │   │   ├── write.go         -- 文件写入工具
│   │   │   ├── edit.go          -- 文件编辑工具
│   │   │   ├── todo.go          -- 内存待办列表
│   │   │   ├── subagent.go      -- 子 agent 任务工具 + SubagentProgress
│   │   │   ├── compact.go       -- 手动压缩工具
│   │   │   ├── task_create.go   -- 任务图：创建
│   │   │   ├── task_get.go      -- 任务图：获取
│   │   │   ├── task_list.go     -- 任务图：列表
│   │   │   ├── task_update.go   -- 任务图：更新
│   │   │   ├── background_bash.go   -- 后台命令工具
│   │   │   └── check_background.go  -- 检查后台任务工具
│   │   ├── tui/                 -- Bubble Tea TUI 前端
│   │   │   ├── tui.go           -- TUI model、Update、View
│   │   │   └── autocomplete.go  -- 斜杠命令自动补全
│   │   ├── tasks/               -- 持久化任务图（DAG）
│   │   ├── background/          -- 后台命令执行
│   │   ├── team/                -- 多 agent 团队系统
│   │   └── skill/               -- 技能加载系统
│   ├── app/app.go               -- App.Run() 引导
│   ├── cmd/                     -- Cobra CLI 命令（root, version, init）
│   ├── config/                  -- Viper 配置管理
│   ├── logger/                  -- Zap 日志
│   ├── signal/                  -- OS 信号处理
│   └── observability/           -- OpenTelemetry、Prometheus、健康检查
├── pkg/version/                 -- 构建时版本注入
└── go.mod                       -- 模块: task-agent, Go 1.26.1
```

---

## 三、三个核心接口

### 3.1 `Session` 接口 —— 前端与 Agent 的完整契约

**文件:** `internal/agent/session.go`

```go
type Session interface {
    // Run 启动一次新的 agent 执行循环。返回一个 channel，持续发射类型化事件
    // (文本、工具调用、结果等)，agent 结束时 channel 关闭。
    Run(ctx context.Context, input string) <-chan tools.Event

    // Tool 按名称返回已注册的工具，不存在时返回 nil。
    Tool(name string) tools.Tool

    // Messages 返回当前消息历史的副本。
    Messages() []anthropic.BetaMessageParam

    // Clear 重置消息历史，开始全新会话。
    Clear()

    // RenderTodo 返回格式化的内存待办列表字符串。
    RenderTodo() string

    // PreviewToolUse 返回工具调用的人类可读单行预览，委托给工具的 Previewer 实现。
    PreviewToolUse(tc tools.ToolUseBlock) string
}
```

`*Runner` 通过编译期断言保证实现：
```go
var _ Session = (*Runner)(nil)
```

### 3.2 `Event` 接口 —— 事件标记接口

**文件:** `internal/agent/tools/tool.go`

```go
type Event interface {
    IsEvent()
}
```

所有事件类型都实现 `IsEvent()` 作为标记方法。

### 3.3 `Previewer` 接口 —— 可选的工具预览

**文件:** `internal/agent/tools/tool.go`

```go
type Previewer interface {
    PreviewInput(input json.RawMessage) string
}
```

工具可选实现此接口，提供一行人类可读的输入摘要。未实现的工具显示 `"..."`。

---

## 四、事件类型全览

| 事件类型 | 所在包 | 定义文件 | 触发时机 |
|---------|--------|---------|---------|
| `EventThinking{}` | agent | event.go | 开始思考 |
| `EventText{Content}` | agent | event.go | 模型输出文本 |
| `EventToolCalls{Tools}` | agent | event.go | 模型请求调用工具 |
| `EventToolResults{Results}` | agent | event.go | 工具执行完毕 |
| `EventTodoUpdate{Content}` | agent | event.go | 待办列表变更 |
| `EventBackgroundResult{TaskID, Status, Summary}` | agent | event.go | 后台任务完成 |
| `EventError{Err}` | agent | event.go | 发生错误 |
| `EventDone{}` | agent | event.go | Agent 执行结束 |
| `SubagentProgress{Description, Turn, MaxTurns}` | tools | subagent.go | 子任务执行进度 |

所有事件类型都实现 `tools.Event` 接口，在 `event.go` 中声明对应的 `IsEvent()` 方法。

---

## 五、Runner 执行流程（核心引擎）

`Runner.Run()` 启动一个 goroutine 执行 `runLoop()`，返回带缓冲（容量 10）的事件 channel。

```
用户输入 "fix the bug"
        │
        ▼
┌─── Run(ctx, input) ─────────────────────────────────────┐
│                                                          │
│  ① 追加用户消息到 r.messages                              │
│  ② ch <- EventThinking                                  │
│                                                          │
│  ┌─────────── 循环开始 ───────────┐                      │
│  │                                │                      │
│  │  ③ microCompact (Layer 1)     │  替换旧 tool_result   │
│  │  ④ injectBackgroundResults    │  注入后台任务通知      │
│  │  ⑤ injectTeamInbox            │  注入 teammate 消息    │
│  │                                │                      │
│  │  ⑥ 调用 Anthropic API ────────│─ 发送 system +        │
│  │     获取模型响应               │   messages + tools    │
│  │                                │                      │
│  │  ⑦ autoCompact (Layer 2)     │  超阈值时 LLM 压缩     │
│  │                                │                      │
│  │  ⑧ 提取 text blocks ──────────│→ ch <- EventText     │
│  │  ⑨ 提取 tool_use blocks       │                      │
│  │                                │                      │
│  │  ⑩ 无工具调用? ───────────────│→ ch <- EventDone     │
│  │     是 → 检查 todo 提醒       │   return             │
│  │     否 → 继续                 │                      │
│  │                                │                      │
│  │  ⑪ ch <- EventToolCalls ─────│→ TUI 渲染黄色工具名    │
│  │  ⑫ ch <- SubagentProgress    │→ 如有 task 工具       │
│  │                                │                      │
│  │  ⑬ registry.Dispatch()       │  并行执行所有工具      │
│  │                                │                      │
│  │  ⑭ ch <- EventToolResults ───│→ TUI 显示执行结果     │
│  │  ⑮ ch <- EventTodoUpdate     │→ 如有 todo 变更       │
│  │  ⑯ 追加 tool_result 到       │                      │
│  │     messages，继续循环         │                      │
│  └────────────────────────────────┘                      │
└──────────────────────────────────────────────────────────┘
```

关键点：
- **事件是单向推送的**：Runner → channel → TUI，没有反向通信
- **工具是并行执行的**（errgroup），一个工具出错不影响其他
- **三层压缩**：microCompact（替换旧结果）+ autoCompact（LLM 摘要）+ compact tool（手动触发）

---

## 六、TUI 对接详解

### 6.1 初始化链路

**`internal/app/app.go`** — `Run()` 函数

```go
func Run(ctx context.Context) error {
    ag, err := agent.New()                            // 创建 Agent + Runner (Session 实现)
    p := tui.NewTUI(ag.Runner, tea.WithContext(ctx))  // Runner 作为 Session 传给 TUI
    if _, err := p.Run(); err != nil { ... }           // 启动 Bubble Tea 事件循环
}
```

**`internal/agent/tui/tui.go`** — `NewTUI()` 函数

```go
func NewTUI(session agent.Session, opts ...tea.ProgramOption) *tea.Program {
    // 创建 textarea (输入区, 8000 字符限制, 3 行高)
    // 创建 viewport (输出显示区, 可滚动)
    // 创建 autocomplete (斜杠命令补全: /exit, /q, /todo, /clear, /memctx)
    // 创建带 cancel 的 context
    return tea.NewProgram(&model{
        session: session,   // ← 只持有 Session 接口
    }, opts...)
}
```

### 6.2 用户提交流程

**`submit()` 方法**（`tui.go`）：

1. 用户按 Enter → `handleKey()` → `submit()`
2. 处理斜杠命令：
   - `/q`, `/exit` → `tea.Quit` 退出
   - `/todo` → 调用 `m.session.RenderTodo()` 显示待办
   - `/clear` → 调用 `m.session.Clear()` 清空会话
   - `/memctx [file]` → 调用 `m.session.Messages()` 导出上下文
3. 对于正常查询：
   - 取消上一次的 context
   - 排空旧的 event channel（防止事件交错）
   - 调用 `m.session.Run(m.ctx, query)` → 获取新的 `<-chan tools.Event`
   - 返回 `tea.Batch(watchRunner(ch), thinkTick(), textarea.Blink)` 启动三个并发命令

### 6.3 事件桥接 —— `watchRunner()`

**`watchRunner()` 函数**（`tui.go`）

```go
func watchRunner(ch <-chan tools.Event) tea.Cmd {
    return func() tea.Msg {
        event, ok := <-ch
        if !ok {
            return agent.EventDone{}    // channel 关闭 = Agent 结束
        }
        return event                    // 直接作为 Bubble Tea Msg 返回
    }
}
```

这是 Runner 世界 → Bubble Tea 世界的唯一桥梁。`tools.Event` 接口的值可以直接作为 `tea.Msg` 使用，因为 Bubble Tea 的 `Update(msg tea.Msg)` 接受 `interface{}`。返回值是 `tea.Cmd`，当 channel 有新事件到达时，Bubble Tea 框架会自动将返回的消息送入下一次 `Update()` 调用。

### 6.4 事件分发 —— `Update()` 方法

**`Update()` 方法**（`tui.go`）使用 **type switch** 处理所有事件：

```go
func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
    switch msg := msg.(type) {
    case tea.WindowSizeMsg:              // 终端尺寸变化 → 重新布局
    case tea.MouseMsg:                   // 鼠标事件 → viewport 滚动
    case tea.KeyPressMsg:                // 键盘事件 → handleKey()
    case thinkTickMsg:                   // 150ms 定时器 → 刷新 "..." 动画
    case agent.EventThinking:            // → 设置 thinking=true, 继续监听
    case agent.EventText:                // → 追加文本到 viewport, 继续监听
    case agent.EventToolCalls:           // → 渲染黄色 "> tool(args)", 继续监听
    case tools.SubagentProgress:         // → 显示子任务进度 (N/30), 继续监听
    case agent.EventToolResults:         // → 显示截断的输出 (200 字符), 继续监听
    case agent.EventBackgroundResult:    // → 显示 [bg done]/[bg failed]/[bg timeout]
    case agent.EventTodoUpdate:          // → 显示青色待办列表, 继续监听
    case agent.EventError:               // → 红色错误信息, 停止思考
    case agent.EventDone:                // → 停止思考, 追加空行
    }
}
```

每种事件处理后的返回模式——大多数 handler 返回 `watchRunner(m.runnerCh)` 继续监听下一个事件，形成一个**自循环的事件消费链**。

### 6.5 TUI 特有功能（与核心无关）

这些都在 `internal/agent/tui/` 包内：

- Bubble Tea 组件管理（`textarea`, `viewport`, `autocomplete`）
- 终端尺寸适配（`WindowSizeMsg`, `resizeViewport`）
- 150ms 思考动画定时器（`thinkTick`）
- 斜杠命令（`/exit`, `/todo`, `/clear`, `/memctx`）—— 这些是 TUI 的快捷方式，但底层调用的仍是 `Session` 接口方法
- Lipgloss 颜色样式（`styleYellow`, `styleGreen`, `styleRed`, `styleCyan`, `styleDim`）
- 内容行上限（`maxContentLines = 2000`）
- 鼠标滚轮滚动支持

---

## 七、工具系统

### 7.1 Registry 和 Dispatch

**文件:** `internal/agent/tools/tool.go`

- `Registry` 持有 `map[string]Tool`，提供 `Tool(name)`, `ToParams()`, `Dispatch()`
- `Dispatch()` 通过 `errgroup` 并行执行所有工具调用块
- 工具错误变为 `"Error: ..."` 结果字符串，不会中止兄弟工具

### 7.2 已注册工具（共 17 个）

| 工具名称 | 实现结构体 | 文件 | 实现 Previewer |
|---------|-----------|------|---------------|
| `bash` | `BashTool{}` | bash.go | 是 — 显示截断的命令 |
| `read_file` | `&ReadFileTool{Workdir}` | read.go | 是 — 显示路径 |
| `write_file` | `&WriteFileTool{Workdir}` | write.go | 否 |
| `edit_file` | `&EditFileTool{Workdir}` | edit.go | 否 |
| `todo` | `&TodoWriteTool{}` | todo.go | 是 — 显示 N/M 完成 |
| `task_create` | `&TaskCreateTool{Mgr}` | task_create.go | 否 |
| `task_get` | `&TaskGetTool{Mgr}` | task_get.go | 否 |
| `task_list` | `&TaskListTool{Mgr}` | task_list.go | 否 |
| `task_update` | `&TaskUpdateTool{Mgr}` | task_update.go | 否 |
| `task` | `NewSubagentTool(...)` | subagent.go | 是 — 显示描述 |
| `load_skill` | `skill.NewLoadSkillTool(loader)` | skill/load_skill_tool.go | 否 |
| `background_bash` | `&BackgroundBashTool{Mgr}` | background_bash.go | 否 |
| `check_background` | `&CheckBackgroundTool{Mgr}` | check_background.go | 否 |
| `team_spawn` | `&team.SpawnTool{Mgr}` | team/tools.go | 否 |
| `team_send` | `&team.SendTool{Mgr, ...}` | team/tools.go | 否 |
| `team_inbox` | `&team.TeamInboxTool{Mgr, ...}` | team/tools.go | 否 |
| `compact` | `NewCompactTool(triggerFunc)` | compact.go | 否 |

`PreviewToolUse()` 在 `tool.go` 中实现：检查工具是否实现 `Previewer` 接口，实现了就调用 `PreviewInput()`，否则返回 `"..."`。

---

## 八、新前端加入流程

### 8.1 核心模式

```
                    ┌──────────────────────┐
                    │   agent.Session       │
                    │   (已有，不变)          │
                    │                      │
                    │ Run() → <-chan Event │
                    │ Tool()               │
                    │ Messages()           │
                    │ Clear()              │
                    │ RenderTodo()         │
                    │ PreviewToolUse()     │
                    └──────────┬───────────┘
                               │
            ┌──────────────────┼──────────────────┐
            │                  │                  │
    ┌───────▼──────┐  ┌───────▼──────┐  ┌───────▼──────┐
    │  TUI 前端     │  │  Web 前端    │  │  API 前端    │
    │  (Bubble Tea) │  │  (HTTP/SSE)  │  │  (gRPC/REST) │
    │               │  │              │  │              │
    │ 已实现        │  │ 新建          │  │ 新建          │
    └───────────────┘  └──────────────┘  └──────────────┘
```

### 8.2 新前端模板代码

```go
// 新前端只需要做三件事：

// ① 获取 Session 实例（和 TUI 完全一样的方式）
ag, _ := agent.New()
session := ag.Runner   // session 是 agent.Session 接口

// ② 调用 Run()，消费事件 channel
ch := session.Run(ctx, "用户的输入")
for event := range ch {
    switch e := event.(type) {
    case agent.EventThinking:
        // 显示思考中...
    case agent.EventText:
        // 渲染文本：e.Content
    case agent.EventToolCalls:
        for _, tc := range e.Tools {
            preview := session.PreviewToolUse(tc)  // 获取可读预览
            // 渲染工具调用：tc.Name + preview
        }
    case agent.EventToolResults:
        // 渲染工具结果
    case agent.EventError:
        // 显示错误
    case agent.EventDone:
        // 结束
    }
}

// ③ 按需调用其他方法
session.Clear()           // 清空会话
session.RenderTodo()      // 显示待办
session.Messages()        // 导出上下文
```

### 8.3 关键原则

| 原则 | 说明 |
|------|------|
| **不修改 agent 核心** | `Session` 接口、`Event` 类型、`Runner` 实现完全不需要改动 |
| **不引入新依赖到 agent** | 新前端可以有自己的 `go.mod` 或独立的 `cmd/` 入口 |
| **事件类型可扩展** | 如果需要新事件类型，只需让它实现 `IsEvent()` 并在 Runner 中 emit，所有前端都能接收 |
| **前端完全控制渲染** | `PreviewToolUse()` 是可选的辅助方法，前端可以自行解析 `ToolUseBlock.Input`（`json.RawMessage`）做自定义渲染 |
| **Channel 是唯一的通信机制** | 不需要回调、WebSocket、共享状态 —— 所有通信都通过 `<-chan tools.Event` |

### 8.4 示例：HTTP SSE 前端

```go
// cmd/web/main.go
func handleChat(w http.ResponseWriter, r *http.Request) {
input := r.URL.Query().Get("q")

flusher, _ := w.(http.Flusher)
w.Header().Set("Content-Type", "text/event-stream")
w.Header().Set("Cache-Control", "no-cache")

ag, _ := agent.New()
ch := ag.Runner.Run(r.Context(), input)

for event := range ch {
data, _ := json.Marshal(event)
fmt.Fprintf(w, "data: %s\n\n", data)
flusher.Flush()
}
}

```

---

## 九、Agent 初始化流程（agent.New()）

**文件:** `internal/agent/agent.go` — `New()` 函数

1. 从 `~/.claude/settings.json` 加载 API key、model、base URL
2. 从 `ANTHROPIC_API_KEY` 环境变量覆盖
3. 创建 Anthropic client
4. 创建 Skill Loader（扫描 `~/.task-agent/skills/` 和 `<cwd>/.task-agent/skills/`）
5. 创建 Task Manager（持久化 DAG 任务图）
6. 创建 Background Manager（非阻塞命令执行）
7. 创建 Team Manager（多 agent 协调）
8. 构建 system prompt（含技能描述，always_load 技能注入 body）
9. 创建 Runner（传入 compact trigger 回调）
10. 注册全工具到 `tools.Registry`

---

## 十、上下文压缩（三层）

| 层级 | 方法 | 文件 | 触发时机 | 机制 |
|------|------|------|---------|------|
| Layer 1 | `microCompact()` | compact.go | 每次 API 调用前 | 替换旧的 tool_result 为占位符（保留最近 N 条） |
| Layer 1b | `injectBackgroundNotifications()` | runner.go | Layer 1 之后 | 将已完成的后台任务结果注入消息历史 |
| Layer 1c | `injectTeamInbox()` | runner.go | Layer 1b 之后 | 将 teammate 消息注入消息历史 |
| Layer 2 | `autoCompact()` | compact.go | API 返回后 | 输入 token 超阈值时，LLM 生成结构化摘要替换历史 |
| Layer 3 | `compact` tool | compact.go | 用户/model 主动触发 | 手动调用 autoCompact，标记 `r.compacted = true` 退出循环 |
