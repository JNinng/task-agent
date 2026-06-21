# task-agent

基于 Anthropic Claude API 的终端 AI 编程助手（TUI），使用 Go 语言构建。

## 特性

- **终端交互界面** — 基于 [Bubble Tea](https://github.com/charmbracelet/bubbletea) 构建的全功能 TUI，支持斜杠命令、自动补全、会话管理
- **丰富的工具系统** — 内置 bash 执行、文件读写编辑、待办清单、持久化任务图、子代理、后台任务等
- **三层上下文压缩** — microCompact（增量替换）+ autoCompact（自动摘要）+ 手动压缩，高效管理上下文窗口
- **代理团队** — 支持 spawn 子代理作为"队友"，通过文件消息总线通信，独立并行工作
- **技能系统** — 双层加载机制（名称列表 + always_load 直接注入），按需加载完整技能体
- **持久化任务图** — 带依赖跟踪的 JSON 持久化任务管理，完成任务自动解锁依赖项
- **后台任务** — 异步执行长时间命令，完成后自动通知
- **配置热更新** — Viper + fsnotify 实现配置文件监听，原子指针交换保证线程安全
- **可观测性** — OpenTelemetry 链路追踪 + Prometheus 指标 + 健康检查端点
- **优雅关闭** — OS 信号处理（SIGINT/SIGTERM），context 传播取消

## 快速开始

### 前置条件

- Go 1.26+
- Anthropic API 密钥

### 安装与运行

```bash
# 克隆仓库
git clone <repo-url> && cd task-agent

# 构建
go build ./cmd/app/

# 生成默认配置文件
go run ./cmd/app/ init

# 设置 API 密钥
export ANTHROPIC_API_KEY="your-api-key"

# 启动
go run ./cmd/app/
```

### 配置文件

默认读取 `configs/config.yaml`，回退到 `./config.yaml`。可通过 `-c` 指定路径：

```bash
go run ./cmd/app/ -c /path/to/config.yaml
```

环境变量覆盖（前缀 `APP_`）：

```bash
export APP_LOG_LEVEL=debug
export APP_OBSERVABILITY_ENABLED=true
```

配置示例：

```yaml
app:
  name: task-agent
  env: dev
  watch: false       # 启用配置文件热更新

log:
  level: info        # debug / info / warn / error
  format: console    # console / json
  path: logs/app.log
  max_size: 200      # MB
  max_age: 60        # 天
  max_backups: 60
  compress: true
  log_to_console: true

observability:
  enabled: false
  addr: ":9090"
  metrics_path: "/metrics"
  health_path: "/health"
  otel:
    endpoint: "localhost:4317"
    protocol: grpc
    logs:
      enabled: false
    traces:
      enabled: false
```

## 项目架构

```
cmd/app/main.go          → 入口，调用 cmd.Execute()
internal/
  cmd/                   → Cobra CLI（root、version、init 子命令）
  app/app.go             → 组装 Agent + Bubble Tea TUI，调用 p.Run()
  config/                → Viper YAML 配置，fsnotify 热更新，APP_ 环境变量覆盖
  logger/                → zap 日志 + lumberjack 轮转
  observability/         → OTEL 追踪 + Prometheus 指标 + 健康检查端点
  signal/                → OS 信号处理（SIGINT/SIGTERM → context 取消）
  agent/
    agent.go             → Agent 结构体：构建 Anthropic 客户端，加载技能，注册工具 → Runner
    session.go           → Session 接口 — 解耦 TUI（或任意前端）与 Runner
    runner.go            → 核心 LLM 对话循环：API 调用 → 工具分发 → 循环
    event.go             → 类型化事件，通过 channel 发送
    compact.go           → 三层上下文压缩
    paths.go             → 集中式目录常量（~/.task-agent/）
    tui/tui.go           → Bubble Tea 终端 UI，消费 Session 接口
    tools/
      tool.go            → 注册中心，Tool/ToolUseBlock/ToolResult 类型，Previewer 接口
      bash.go, read.go, write.go, edit.go  → 文件/Shell 工具
      todo.go            → 会话内内存待办清单
      subagent.go        → "task" 工具 — 子代理，干净上下文，受限工具集，30 轮限制
      task_create.go, task_update.go, task_list.go, task_get.go → 持久化任务图工具
      compact.go         → 手动压缩工具
      background_bash.go, check_background.go → 后台任务工具
    background/          → 后台 bash 执行 + 通知队列
    team/
      manager.go         → 管理队友生命周期，消息路由，持久化名册
      bus.go             → 文件消息总线（每个队友一个 inbox 文件）
      teammate.go        → 队友代理循环（独立 Anthropic 客户端，inbox 轮询）
      tools.go           → SpawnTool、SendTool、TeamInboxTool
      types.go           → Teammate、Message、Config、Status 类型
    tasks/manager.go     → 持久化任务图，依赖跟踪（每个任务列表一个 JSON 文件）
    skill/
      skill.go           → SKILL.md 解析/加载器
      load_skill_tool.go → 按需将技能完整内容加载到上下文的工具
```

## 核心设计

### Session 接口

`Session` 是任意前端与 Agent Runner 之间的契约。Runner 实现此接口，TUI 消费它：

```go
type Session interface {
    Run(ctx context.Context, input string) <-chan tools.Event
    Tool(name string) tools.Tool
    Messages() []anthropic.BetaMessageParam
    Clear()
    RenderTodo() string
    PreviewToolUse(tc tools.ToolUseBlock) string
}
```

前端通过类型断言处理各种 `Event` 类型（`EventThinking`、`EventText`、`EventToolCalls`、`EventToolResults` 等）。

### 三层上下文压缩

| 层级 | 名称 | 触发时机 | 机制 |
|------|------|----------|------|
| 1 | microCompact | 每次 API 调用前 | 将旧的 tool_result 内容替换为占位符 |
| 2 | autoCompact | 输入 token 超过阈值（默认 50k） | LLM 生成摘要，替换历史消息 |
| 3 | 手动 compact | 模型调用 `compact` 工具 | 保存完整对话记录，生成摘要 |

### 子代理（Task Tool）

子代理以受限工具集（bash、read、write、edit — 无递归 task、无 todo）运行独立的 agent 循环，最多 30 轮。使用全局互斥锁序列化并发调用。子代理的中间步骤不会污染主代理的上下文窗口。

### 代理团队

Lead 代理通过 `team_spawn` 创建持久的"队友"。每个队友运行独立的 agent 循环（inbox 轮询 goroutine）。通信基于 `~/.task-agent/team/` 下的文件消息总线。Lead 的 inbox 在每次 API 调用前自动注入到 LLM 上下文。

### 双层技能加载

1. **Layer 1** — 仅注入技能名称和描述列表（~100 tokens/skill），保持上下文精简
2. **Layer 1.5** — 标记为 `always_load: true` 的技能直接注入到系统提示中
3. **按需加载** — 通过 `load_skill` 工具获取完整技能体

## 工具列表

| 工具 | 描述 |
|------|------|
| `bash` | 执行 shell 命令 |
| `read` | 读取文件内容 |
| `write` | 写入文件 |
| `edit` | 精确字符串替换编辑 |
| `todo` | 会话内内存待办清单 |
| `task` | 启动子代理处理复杂任务 |
| `task_create` | 在持久化图中创建任务 |
| `task_update` | 更新任务状态/依赖 |
| `task_list` | 列出所有任务 |
| `task_get` | 获取任务详情 |
| `background_bash` | 异步后台执行命令 |
| `check_background` | 检查后台任务状态 |
| `load_skill` | 按需加载技能完整内容 |
| `team_spawn` | 创建队友代理 |
| `team_send` | 向队友发送消息 |
| `team_inbox` | 读取团队 inbox（紧急情况） |
| `compact` | 手动压缩上下文 |

## TUI 斜杠命令

| 命令 | 描述 |
|------|------|
| `/exit` `/q` | 退出程序 |
| `/todo` | 显示待办任务列表 |
| `/clear` | 清空会话上下文 |
| `/memctx` | 导出上下文到文件 |

## 开发

```bash
# 构建
go build ./cmd/app/

# 运行全部测试
go test ./...

# 运行单个包的测试
go test ./internal/agent/tasks/

# 运行单个测试
go test ./internal/agent/ -run TestMicroCompact -v

# 静态检查 + 测试
go vet ./... && go test ./...
```

## 许可证

MIT License
