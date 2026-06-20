# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Test

```bash
# Build
go build ./cmd/app/

# Run all tests
go test ./...

# Run tests in a single package
go test ./internal/agent/tasks/

# Run a single test
go test ./internal/agent/ -run TestMicroCompact -v

# Vet + test
go vet ./... && go test ./...
```

The binary is `task-agent`. It reads config from `configs/config.yaml` by default, falling back to `./config.yaml`. Start with `go run ./cmd/app/`.

No Makefile, no Docker — plain Go toolchain.

## Architecture

```
cmd/app/main.go          → entry point, calls cmd.Execute()
internal/
  cmd/                   → Cobra CLI (root, version, init subcommands)
  app/app.go             → wires Agent + Bubble Tea TUI, calls p.Run()
  config/                → Viper-based YAML config, hot-reload via fsnotify, env override (APP_ prefix)
  logger/                → zap logger with lumberjack rotation
  observability/         → OTEL traces + Prometheus metrics + health endpoint
  signal/                → OS signal handling (SIGINT/SIGTERM → context cancellation)
  agent/
    agent.go             → Agent struct: constructs Anthropic client, loads skills, wires tools → Runner
    session.go           → Session interface — decouples TUI (or any frontend) from Runner
    runner.go            → Core LLM conversation loop: API call → tool dispatch → loop
    event.go             → Typed events sent through channel: EventThinking, EventText, EventToolCalls, etc.
    compact.go           → Three-layer context compaction (microCompact, autoCompact, manual compact tool)
    paths.go             → Centralized directory constants under ~/.task-agent/
    tui/tui.go           → Bubble Tea terminal UI, consumes Session interface
    tools/
      tool.go            → Registry, Tool/ToolUseBlock/ToolResult types, Previewer interface
      bash.go, read.go, write.go, edit.go  → File/shell tools
      todo.go            → In-memory session checklist
      subagent.go        → "task" tool — subagent with fresh context, restricted tools, 30-turn limit
      task_create.go, task_update.go, task_list.go, task_get.go  → Persistent task graph tools
      compact.go         → Manual compact tool
      background_bash.go, check_background.go → Background task tools
    background/          → Background bash execution with notification queue
    team/
      manager.go         → Spawns/shuts down teammates, routes messages via bus, persists roster to config.json
      bus.go             → File-based message bus (one inbox file per teammate)
      teammate.go        → Teammate agent loop (own Anthropic client, inbox polling)
      tools.go           → SpawnTool, SendTool, TeamInboxTool
      types.go           → Teammate, Message, Config, Status types
    tasks/manager.go     → Persistent task graph with dependency tracking (JSON file per task list)
    skill/
      skill.go           → SKILL.md parser/loader from ~/.task-agent/skills/ and <project>/.task-agent/skills/
      load_skill_tool.go → Tool that loads a skill's full body into context on demand
```

### Key architectural points

**Session interface** (`internal/agent/session.go`): The contract between any frontend and the agent Runner. `Run(ctx, input) → <-chan tools.Event` starts the agent loop; events flow through the channel. The TUI (`tui/tui.go`) is the only current frontend, consuming this interface.

**Three-layer compaction** (`internal/agent/compact.go`):
1. `microCompact` — replaces old tool_result content with placeholders (per-turn, before API call)
2. `autoCompact` — when input tokens exceed threshold (default 50k), saves transcript and replaces history with an LLM-generated summary
3. Manual compact — the `compact` tool, triggered by the model

**Subagent (task tool)**: Spawns a child agent loop with a restricted tool set (bash, read, write, edit only — no task recursion, no todo). Has a 30-turn limit and returns only the final text summary. Uses a global mutex to serialize concurrent subagent calls.

**Agent team**: Lead agent spawns teammates via `team_spawn`. Each teammate runs its own agent loop (inbox-polling goroutine). The lead's inbox is auto-injected into the LLM context before each API call — `team_inbox` exists only for emergencies. Communication is through a file-based message bus under `~/.task-agent/team/`.

**Two-layer skill loading**: Skills with `always_load: true` are injected directly into the system prompt (layer 1.5). All others appear as a name+description list (layer 1). The `load_skill` tool fetches full bodies on demand.

**Task graph vs todo**: `todo` is an in-memory session checklist. `task_create/update/list/get` are persistent (JSON files under `~/.task-agent/`) with dependency tracking — completing a task auto-unlocks its dependents.

**Background bash**: Runs shell commands asynchronously. Results are auto-delivered as `<background-results>` blocks before the next LLM call.

## Code conventions

- Comments and UI strings are in zh-CN (Chinese).
- All agent data directories derive from `agent.DirAgent` (`.task-agent`) — never hardcode the path.
- Tools implement the `anthropic.BetaTool` interface (`Name()`, `Description()`, `InputSchema()`, `Execute()`). Tools with display-friendly input summaries also implement `Previewer`.
- The `Registry.Dispatch` runs tool calls in parallel via `errgroup`; tool errors become error result strings rather than aborting sibling tools.
- Config hot-reload uses `config.AddWatch` callbacks with atomic pointer swaps.
- The `internal/signal` package provides `ContextWithShutdown` for graceful OS signal handling.
