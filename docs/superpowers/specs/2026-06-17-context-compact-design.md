# Context Compaction — Design Spec

Date: 2026-06-17

Reference: `F:\App\project\Python\learn-claude-code\docs\zh\s06-context-compact.md`

## Goal

Implement a three-layer context compaction mechanism matching the Python reference pattern from `learn-claude-code/s06_context_compact.py`. The agent conversation can grow unbounded as tools are called and files are read — without compaction, long sessions exceed model context windows and degrade reliability.

The compaction strategy is three increasingly aggressive layers:

```
Every turn:
  Layer 1: micro_compact   (silent, every turn)
    Replace tool_result > keepRecent turns old with placeholder

  Layer 2: auto_compact    (triggered by API token count)
    Save transcript → LLM summarizes → replace all messages with summary

  Layer 3: compact tool    (model-invoked on demand)
    Same flow as auto_compact
```

## Architecture

```
internal/agent/
├── runner.go               # Modified: integrate microCompact + autoCompact in loop
├── compact.go              # NEW: CompactionConfig + microCompact + autoCompact
├── compact_test.go         # NEW: unit tests
├── tools/
│   ├── compact.go          # NEW: compact tool (implements tools.Tool)
│   ├── tool.go             # Unchanged
│   └── ...
├── agent.go                # Modified: wire CompactionConfig, register compact tool
├── event.go                # Unchanged
└── ...                     # Other files unchanged
```

### Files NOT Changed

| File | Reason |
|------|--------|
| `internal/config/config.go` | CompactionConfig lives in agent package to avoid circular deps; mapping from YAML done in agent.New() |
| `internal/agent/tui.go` | No TUI changes needed — compaction emits existing EventText events |
| All existing tool files | compact tool registered alongside existing tools |

## Components

### CompactionConfig (`compact.go`)

```go
type CompactionConfig struct {
    AutoThreshold   int    // input_tokens trigger threshold, default 50_000
    MicroKeepRecent int    // keep that many recent tool_results, default 3
    TranscriptDir   string // transcript archive dir, default ".task-agent/transcripts"
}

func DefaultCompactionConfig() CompactionConfig {
    return CompactionConfig{
        AutoThreshold:   50_000,
        MicroKeepRecent: 3,
        TranscriptDir:   ".task-agent/transcripts",
    }
}
```

Configuration is injected into Runner at construction time via agent.go. Future YAML integration can read from `config.Config` and map the fields in `New()` without changing compact internals.

### Layer 1: microCompact (`compact.go`)

Called every loop iteration **before** the API request. Scans all `user`-role messages for `tool_result` blocks and replaces those beyond the `keepRecent` window with a placeholder. Only blocks with content > 100 characters are substituted, leaving short error messages visible.

```go
func microCompact(messages []anthropic.BetaMessageParam, keepRecent int) {
    // collect (msgIdx, blockIdx) for each tool_result
    // if len(toolResults) <= keepRecent → return
    // for each old result → replace text with "[Previous tool result compacted]"
}
```

**Properties:**
- In-place mutation, no copy overhead
- Runs every turn unconditionally (O(n) over user messages, cheap)
- Does not track tool names (Go SDK ToolResultBlockParam does not carry the tool name directly; generic placeholder avoids an extra lookup map)

### Layer 2: autoCompact (`compact.go`)

Called after the API response when `resp.Usage.InputTokens > config.AutoThreshold`.

```
autoCompact(ctx):
  1. Ensure TranscriptDir exists (mkdir -p)
  2. Save full messages → transcript_<unix>.jsonl
  3. Serialize messages to JSON (truncate to 80_000 chars)
  4. Call LLM: "Summarize this conversation for continuity..."
  5. Extract summary text from response
  6. Replace r.messages with: [{role: "user", content: "[Compressed]\n\n{summary}"}]
  7. Return nil on success, error on failure
```

**Key behaviors:**
- Failure does NOT abort the agent loop — emits a warning `EventText` and continues with unconveraged messages
- Full transcript preserved on disk for recovery; information is not lost, just removed from active context
- After compaction, the loop `continue`s — the model re-infers from the compressed context, discarding the previous turn's partially-processed tool_use

### Layer 3: compact Tool (`tools/compact.go`)

Implements `tools.Tool` and wraps a callback to `Runner.autoCompact`:

| Field | Value |
|-------|-------|
| `Name()` | `compact` |
| `Description()` | "Manually compress the conversation to free context space..." |
| `InputSchema()` | `{ properties: { reason: { type: "string" } }, required: [] }` |
| `Execute()` | Calls `trigger()` callback, returns summary string or error |

Registered in `agent.go` via `tools.NewCompactTool(compactFn)` where `compactFn` is a closure wired back to `Runner.compact()`.

The callback avoids passing the entire Runner to the tool package. Wiring in agent.go:

```go
// agent.go
runner := NewRunner(agent, cfg, func(fn func() (string, error)) {
    compactTrigger = fn
})
registry := tools.NewRegistry(
    bashTool, readTool, writeTool, editTool, todoTool, subagentTool,
    loadSkillTool,
    tools.NewCompactTool(func() (string, error) { return compactTrigger() }),
)
```

### Runner Integration (`runner.go`)

The existing `runLoop` gains two insertion points:

```go
for {
    // Layer 1
    microCompact(r.messages, r.compactCfg.MicroKeepRecent)

    resp, err := r.agent.client.Beta.Messages.New(ctx, params)
    if err != nil { ... }

    // Layer 2
    if resp.Usage.InputTokens > r.compactCfg.AutoThreshold {
        emit("[context: %d tokens — auto-compacting]", resp.Usage.InputTokens)
        if err := r.autoCompact(ctx); err != nil {
            emit("[compact warning: %v]", err)
        } else {
            emit("[auto-compact done]")
        }
        continue
    }

    // ... existing append + dispatch (unchanged) ...
}
```

The `continue` after successful autoCompact is critical — it prevents the model from receiving its own (now stale) response on top of the compressed summary.

## Data Flow

```
User Input
    ↓
[microCompact]              Layer 1: replace old tool_results
    ↓
[API Call]                  with compressed messages
    ↓
[InputTokens > threshold?]  Layer 2 check
  ├─ No  → append response, dispatch tools, append results → loop
  └─ Yes → [save transcript] → [LLM summarize] → [replace messages] → continue
                                               ↑
Layer 3 (compact tool) ────────────────────────┘
```

## Error Handling

| Scenario | Behavior |
|----------|----------|
| Transcript dir missing | auto-mkdir-all at top of autoCompact |
| Summary API call fails | Emit warning EventText; do not replace messages; continue |
| Empty summary returned | Not replaced — treated as failure |
| messages is empty / no tool_results | microCompact returns immediately |
| AutoThreshold = 0 | Check `> 0 && InputTokens > Threshold` — 0 disables |
| Repeated compact calls | Idempotent — re-summarizes already-compressed context (wasteful but safe) |
| Disk full on transcript save | Warning emitted; summarization continues |
| Concurrent access | Single goroutine loop — no race conditions on messages |

## Design Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Config location | `agent.CompactionConfig`, not `config.Config` | Avoid cross-package dependency; config is low-level YAML, agent owns compaction semantics |
| Token source | `resp.Usage.InputTokens` from API response | Zero dependencies, matches billing, no estimation error |
| Summary method | Independent LLM call | Clean separation, high-quality summaries; one extra API call per compaction is acceptable |
| After-compact flow | `continue` (skip tool dispatch) | Models infer better from fresh context than from mixing stale response + compressed history |
| Transcript format | `.jsonl` | Simple append-friendly format; one message per line |
| Compact tool wiring | Callback closure | Avoids passing Runner into tool package; keeps tools unaware of Runner internals |
| microCompact placeholder | Generic "[Previous tool result compacted]" | SDK ToolResultBlockParam lacks direct tool name; generic avoids extra tracking state |

## Testing

File: `internal/agent/compact_test.go`

| Test | Coverage |
|------|----------|
| TestMicroCompact_RemovesOld | 5 tool_results, keep=3 → 2 replaced |
| TestMicroCompact_UnderLimit | 2 tool_results, keep=3 → no change |
| TestMicroCompact_ShortContent | <100 char results not replaced |
| TestMicroCompact_Empty | Empty messages → no panic |
| TestAutoCompact_SaveTranscript | Verify .jsonl written correctly |
| TestAutoCompact_Threshold | Below threshold → no trigger |
| TestCompactTool_Execute | Callback success/failure format |
| TestCompactTool_InputSchema | Schema includes `reason` with correct type |
| TestDefaultConfig | Defaults: 50000, 3, ".task-agent/transcripts" |

Summary API calls inside autoCompact are left to manual/integration testing.

## Migration

New feature — no migration needed. Existing sessions without `.task-agent/transcripts/` will have it auto-created on first compaction.

## References

- Python reference: `F:\App\project\Python\learn-claude-code\agents\s06_context_compact.py`
- Python docs: `F:\App\project\Python\learn-claude-code\docs\zh\s06-context-compact.md`
- Existing Runner: `internal/agent/runner.go`
- Existing Tool interface: `internal/agent/tools/tool.go`
- Existing agent setup: `internal/agent/agent.go`
