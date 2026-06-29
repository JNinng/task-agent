# s11: Autonomous Agents — Go 实现设计

> 参考: `learn-claude-code/docs/zh/s11-autonomous-agents.md`
>
> 状态: 已批准
>
> 日期: 2026-06-29

## 目标

将队友从被动指派模式升级为自组织模式。队友完成初始任务后进入 IDLE 阶段轮询收件箱和任务看板，自动认领未分配任务，空闲超时后优雅退出。同时增加上下文压缩后的身份重注入机制。

## 变更概览

| 文件 | 变更类型 | 说明 |
|------|----------|------|
| `internal/agent/team/teammate.go` | 重构 | `run()` 改为 WORK↔IDLE 循环；新增 `idlePoll()`、`injectIdentity()`、`claimAndInject()`；`processLoop` 增加 idle 工具支持和 compact 后身份检查 |
| `internal/agent/team/tools.go` | 新增工具 | `IdleTool`、`ClaimTaskTool`；更新 `newTeammateLoop` 注册表 |
| `internal/agent/team/manager.go` | 扩展 | 注入 `*tasks.Manager`；新增 `ScanUnclaimedTasks()`；`NewManager` 签名变更 |
| `internal/agent/team/types.go` | 无变更 | 复用现有 `Status` 常量和 `Message` 结构 |

不新增文件，改动集中在 `team` 包内 3 个文件。

## 架构

### 队友生命周期

```
spawn → WORK ⇄ IDLE → SHUTDOWN

WORK: LLM 调用工具直到 stop_reason != tool_use 或主动调用 idle
IDLE: 每 5s 轮询（最长 60s），检查收件箱 → 任务看板
      60s 无事后自动 SHUTDOWN
```

### run() 新流程

```
run(initialPrompt)
  ├── processLoop()                    // WORK: 处理初始任务
  └── for {                            // WORK ↔ IDLE 循环
      ├── drainInbox()                 // 先排空积累的消息
      ├── if 有消息 → processLoop() → continue
      │
      ├── setStatus(Idle)
      ├── idlePoll()                   // IDLE: 轮询 60s
      │   ├── quitCh 信号? → return false
      │   ├── 收件箱有消息? → return true
      │   └── 任务看板有未认领? → 认领 → return true
      │
      ├── 超时 → setStatus(Shutdown) → sendToLead → return
      │
      ├── setStatus(Working)
      ├── identity check               // len(msgs) <= 3 → injectIdentity
      └── processLoop()                // 恢复 WORK
  }
```

### IDLE 轮询

常量:
- `idlePollInterval = 5s`
- `idleTimeout = 60s`
- `idleMaxIterations = 12`

每轮:
1. 监听 `quitCh`（支持即时中断）
2. `time.Sleep(5s)`
3. 检查收件箱 → 有消息则注入 `<team-inbox>` 块，返回 WORK
4. 扫描任务看板 → 有未认领则自动认领，注入 `<auto-claimed>` 块，返回 WORK
5. 12 轮后返回 false → SHUTDOWN

### 任务看板扫描

```go
func (m *TeammateManager) ScanUnclaimedTasks() ([]tasks.Task, error)
```

筛选条件（与 Python 一致）:
- `status == "pending"`
- `owner == ""`
- `len(blockedBy) == 0`

自动认领时调用 `tasks.Manager.Update(taskID, TaskUpdate{Owner: &name, Status: "in_progress"})`。

### 身份重注入

两个检查点:

1. **processLoop 内 compact 后**: 当 `len(messages) > 40` 截断后，若 `len(messages) <= 3` 立即重注入
2. **IDLE → WORK 转换时**: 防御性检查，确保从空闲恢复后身份完整

```go
func (t *teammateLoop) injectIdentity() {
    // 在消息开头插入:
    //   user: <identity>You are 'name', role: role, team: lead. Continue...</identity>
    //   assistant: I am name. Continuing.
}
```

### 新工具

**idle** (队友专用):
- 无参数
- 设置 `idleRequested = true`，processLoop 检测后退出 WORK 阶段
- 描述引导模型在完成工作后主动调用

**claim_task** (队友专用):
- 参数: `task_id` (string)
- 手动认领任务（系统自动认领已覆盖主要场景，此工具保留给 LLM 手动认领需求）

### 超时关机

60s 空闲超时后:
1. `setStatus(name, StatusShutdown)`
2. `sendToLead("idle timeout, shutting down")`
3. 从 `mgr.loops` 中删除自己
4. 更新 roster 中的状态
5. goroutine 自然退出

## 与 Python 参考实现的对比

| 方面 | Python (s11) | Go (本项目) |
|------|-------------|------------|
| IDLE 触发 | `stop_reason != "tool_use"` | `idle` 工具 + `stop_reason != "tool_use"` |
| 收件箱格式 | JSONL 文件 | JSONL 文件（已有 `MessageBus`） |
| 任务看板 | 扫描 `.tasks/` JSON 文件 | 通过 `tasks.Manager.List()` |
| 身份重注入 | `len(messages) <= 3` | 同 + IDLE→WORK 防御性检查 |
| 空闲超时 | 60s (12 × 5s) | 同 |
| 关机 | `sys.exit(0)` | goroutine return + roster 更新 |

## 测试策略

- `idlePoll` 的轮询逻辑：通过注入 mock `tasks.Manager` 和 mock `MessageBus` 测试收件箱唤醒、任务认领、超时三条路径
- `injectIdentity`：验证消息插入位置和内容格式
- `ScanUnclaimedTasks`：验证筛选条件（pending / no owner / no blockedBy）
- 集成测试：spawn 队友 → 创建任务 → 验证自动认领 → 验证超时关机

## 不做的

- 不改变 lead agent 的 runner.go（lead 的 team inbox 注入逻辑不变）
- 不改变 `MessageBus` / `FormatInboxMessages` / 协议请求追踪
- 不为 IDLE 逻辑创建独立文件（保持 team 包扁平结构）
- 不改变 `types.go` 中的数据结构
