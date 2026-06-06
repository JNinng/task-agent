# Tool Dispatch Refactor — Design Spec

Date: 2026-06-06

## Goal

Refactor the agent tool mechanism from a hardcoded single-bash-tool to a dispatch-map architecture, matching the Python reference pattern where "the loop didn't change, I just added tools."

## Architecture

```
internal/agent/
├── agent.go          # Agent: client + system + registry (simplified)
├── runner.go         # Runner: agent loop (batch parallel dispatch)
├── tui.go            # TUI (adapt to batch events)
├── event.go          # Event types (extracted from runner.go)
└── tools/
    ├── tool.go       # Registry + dispatch logic
    ├── bash.go       # BashTool
    ├── read.go       # ReadFileTool
    ├── write.go      # WriteFileTool
    └── edit.go       # EditFileTool
```

### Flow

```
User Prompt → Agent Loop → LLM Response
                              ↓
                    tool_use blocks (all)
                              ↓
                    Registry.Dispatch(ctx, blocks)
                       ↓ errgroup parallel
                    bash / read / write / edit
                              ↓
                    tool_result blocks → next LLM turn
```

## Key Design Decisions

1. **SDK `BetaTool` interface** — tools implement `Name()`, `Description()`, `InputSchema()`, `Execute()`. Compatible with SDK `toolrunner` and `agenttoolset` in the future.

2. **Dispatch map** — `map[string]Tool` (name→handler), exactly like Python's `TOOL_HANDLERS`. No loop changes needed when adding tools.

3. **Parallel execution** — `golang.org/x/sync/errgroup` executes multiple tool_use blocks concurrently. One failing tool does not abort others (returns error result for that tool only).

4. **Batch events** — `EventToolCalls` and `EventToolResults` carry slices instead of singletons.

## Components

### Registry (`tools/tool.go`)

- `NewRegistry(tools ...Tool) *Registry` — builds dispatch map
- `Dispatch(ctx, blocks) ([]ToolResultBlock, error)` — parallel dispatch via errgroup
- `ToParams() []ToolUnionParam` — generates API schema list

### Tools (each in its own file)

| Tool | Name | Key Behavior |
|------|------|--------------|
| BashTool | `bash` | Shell exec, 120s timeout, dangerous command blocklist, 50KB output cap |
| ReadFileTool | `read_file` | Path confinement via `safePath`, optional line limit |
| WriteFileTool | `write_file` | Path confinement, creates parent dirs, returns byte count |
| EditFileTool | `edit_file` | Path confinement, exact string replace (not regex), fails if text not found |

### Runner (`runner.go`)

- Collects ALL `tool_use` blocks from response
- Emits `EventToolCalls` with full slice
- Calls `registry.Dispatch()` — single call handles all tools
- Emits `EventToolResults` with full slice
- Appends all `tool_result` blocks to conversation history
- Loops until no more tool_use blocks

### TUI (`tui.go`)

- `EventToolCalls` handler iterates and displays each tool call with a preview
- `EventToolResults` handler iterates and displays each result (truncated to 200 chars)
- Tool-specific preview extraction (bash shows command, file tools show path)

### Security

- `safePath(workdir, p)` canonicalizes and verifies path stays within workdir
- Symlink-aware: uses `filepath.EvalSymlinks` before checking
- Bash: blocks `rm -rf /`, `sudo`, `shutdown`, `reboot`, `> /dev/`
- Write: creates parent dirs but does not traverse above workdir

## File Changes

| File | Action |
|------|--------|
| `internal/agent/tools/tool.go` | New — Registry + dispatch |
| `internal/agent/tools/bash.go` | New — BashTool (migrated from `bash.go`) |
| `internal/agent/tools/read.go` | New — ReadFileTool |
| `internal/agent/tools/write.go` | New — WriteFileTool |
| `internal/agent/tools/edit.go` | New — EditFileTool |
| `internal/agent/event.go` | New — event types extracted from `runner.go` |
| `internal/agent/agent.go` | Modify — `tools` field → `registry *tools.Registry` |
| `internal/agent/runner.go` | Modify — loop rewritten for batch parallel dispatch |
| `internal/agent/tui.go` | Modify — adapt to batch events |
| `internal/agent/bash.go` | Delete — merged into `tools/bash.go` |
| `internal/app/app.go` | Modify — wire up registry instead of raw tools |

## References

- Python reference: `F:\App\project\Python\learn-claude-code\agents\s02_tool_use.py`
- SDK BetaTool: `anthropic-sdk-go/toolrunner/betatoolrunner.go`
