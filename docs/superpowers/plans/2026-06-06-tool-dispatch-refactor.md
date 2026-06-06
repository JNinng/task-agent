# Tool Dispatch Refactor — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Refactor the agent tool mechanism from hardcoded single-bash-tool to a dispatch-map architecture with 4 tools (bash, read_file, write_file, edit_file), parallel execution, and Beta API.

**Architecture:** Implement `anthropic.BetaTool` interface per tool, a `Registry` dispatch map mirroring Python's `TOOL_HANDLERS`, errgroup-based parallel execution, and batch events for the TUI. Switch from v1 API (`client.Messages.New`) to Beta API (`client.Beta.Messages.New`) because `BetaTool` interface returns Beta types.

**Tech Stack:** Go 1.26, `anthropic-sdk-go` Beta API, `golang.org/x/sync` errgroup, Bubble Tea v2

---

### Task 1: Create `internal/agent/tools/tool.go` — Registry and types

**Files:**
- Create: `internal/agent/tools/tool.go`

- [ ] **Step 1: Write the Registry and types**

```go
package tools

import (
	"context"
	"encoding/json"
	"fmt"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"golang.org/x/sync/errgroup"
)

// Tool is an alias for the SDK's BetaTool interface.
type Tool = anthropic.BetaTool

// ToolUseBlock carries the info extracted from a BetaContentBlockUnion tool_use.
type ToolUseBlock struct {
	ID    string
	Name  string
	Input json.RawMessage
}

// ToolResult pairs a tool_use ID with its output content.
type ToolResult struct {
	ToolUseID string
	Content   string
}

// Registry maps tool names to Tool implementations.
type Registry struct {
	tools map[string]Tool
}

// NewRegistry creates a Registry and builds the dispatch map.
func NewRegistry(tools ...Tool) *Registry {
	m := make(map[string]Tool, len(tools))
	for _, t := range tools {
		m[t.Name()] = t
	}
	return &Registry{tools: m}
}

// Dispatch executes all tool_use blocks in parallel via errgroup.
// A tool error becomes an error result string — it does not abort sibling tools.
func (r *Registry) Dispatch(ctx context.Context, blocks []ToolUseBlock) ([]ToolResult, error) {
	g, gctx := errgroup.WithContext(ctx)
	results := make([]ToolResult, len(blocks))

	for i, block := range blocks {
		i, block := i, block
		g.Go(func() error {
			tool, ok := r.tools[block.Name]
			if !ok {
				results[i] = ToolResult{
					ToolUseID: block.ID,
					Content:   fmt.Sprintf("Error: Tool '%s' not found", block.Name),
				}
				return nil
			}
			content, err := tool.Execute(gctx, block.Input)
			if err != nil {
				results[i] = ToolResult{
					ToolUseID: block.ID,
					Content:   fmt.Sprintf("Error: %v", err),
				}
				return nil
			}
			results[i] = ToolResult{
				ToolUseID: block.ID,
				Content:   contentBlockToString(content),
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return results, nil
}

// ToParams converts registered tools to BetaToolUnionParam slice for the API.
func (r *Registry) ToParams() []anthropic.BetaToolUnionParam {
	params := make([]anthropic.BetaToolUnionParam, 0, len(r.tools))
	for _, t := range r.tools {
		params = append(params, anthropic.BetaToolUnionParam{
			OfTool: &anthropic.BetaToolParam{
				Name:        t.Name(),
				Description: anthropic.String(t.Description()),
				InputSchema: t.InputSchema(),
			},
		})
	}
	return params
}

// contentBlockToString extracts text from BetaToolResultBlockParamContentUnion slice.
func contentBlockToString(content []anthropic.BetaToolResultBlockParamContentUnion) string {
	var texts []string
	for _, c := range content {
		if c.OfText != nil {
			texts = append(texts, c.OfText.Text)
		}
	}
	if len(texts) == 0 {
		return "(no output)"
	}
	result := ""
	for i, t := range texts {
		if i > 0 {
			result += "\n"
		}
		result += t
	}
	return result
}
```

- [ ] **Step 2: Verify compilation**

Run: `go build ./internal/agent/tools/`
Expected: no errors

- [ ] **Step 3: Commit**

```bash
git add internal/agent/tools/tool.go
git commit -m "feat(tools): add Registry with parallel dispatch and BetaTool support"
```

---

### Task 2: Create `internal/agent/tools/bash.go` — BashTool

**Files:**
- Create: `internal/agent/tools/bash.go`

- [ ] **Step 1: Write BashTool implementing BetaTool**

```go
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"

	anthropic "github.com/anthropics/anthropic-sdk-go"
)

var dangerousPatterns = []string{
	"rm -rf /",
	"sudo",
	"shutdown",
	"reboot",
	"> /dev/",
}

// BashTool executes shell commands.
type BashTool struct{}

func (BashTool) Name() string        { return "bash" }
func (BashTool) Description() string { return "Run a shell command." }

func (BashTool) InputSchema() anthropic.BetaToolInputSchemaParam {
	return anthropic.BetaToolInputSchemaParam{
		Properties: map[string]any{
			"command": map[string]any{"type": "string"},
		},
		Required: []string{"command"},
	}
}

func (BashTool) Execute(ctx context.Context, input json.RawMessage) ([]anthropic.BetaToolResultBlockParamContentUnion, error) {
	var args struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return nil, fmt.Errorf("bash: %w", err)
	}
	output := runBash(ctx, args.Command)
	return []anthropic.BetaToolResultBlockParamContentUnion{
		{OfText: &anthropic.BetaTextBlockParam{Text: output}},
	}, nil
}

func runBash(ctx context.Context, command string) string {
	for _, d := range dangerousPatterns {
		if strings.Contains(command, d) {
			return "Error: Dangerous command blocked"
		}
	}

	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "powershell", "-NoProfile", "-Command",
			"[Console]::OutputEncoding = [Text.Encoding]::UTF8; "+command)
	} else {
		cmd = exec.CommandContext(ctx, "bash", "-c", command)
	}

	out, err := cmd.CombinedOutput()
	if err != nil {
		out = append(out, []byte(fmt.Sprintf("\nError: %v", err))...)
	}

	result := strings.TrimSpace(string(out))
	if result == "" {
		return "(no output)"
	}
	if len(result) > 50000 {
		result = result[:50000]
	}
	return result
}
```

- [ ] **Step 2: Verify compilation**

Run: `go build ./internal/agent/tools/`
Expected: no errors

- [ ] **Step 3: Commit**

```bash
git add internal/agent/tools/bash.go
git commit -m "feat(tools): add BashTool implementing BetaTool"
```

---

### Task 3: Create `internal/agent/tools/read.go` — ReadFileTool

**Files:**
- Create: `internal/agent/tools/read.go`

- [ ] **Step 1: Write ReadFileTool with path safety**

```go
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	anthropic "github.com/anthropics/anthropic-sdk-go"
)

// ReadFileTool reads file contents.
type ReadFileTool struct {
	Workdir string
}

func (ReadFileTool) Name() string        { return "read_file" }
func (ReadFileTool) Description() string { return "Read file contents." }

func (ReadFileTool) InputSchema() anthropic.BetaToolInputSchemaParam {
	return anthropic.BetaToolInputSchemaParam{
		Properties: map[string]any{
			"path":  map[string]any{"type": "string"},
			"limit": map[string]any{"type": "integer"},
		},
		Required: []string{"path"},
	}
}

func (t ReadFileTool) Execute(ctx context.Context, input json.RawMessage) ([]anthropic.BetaToolResultBlockParamContentUnion, error) {
	var args struct {
		Path  string `json:"path"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return nil, fmt.Errorf("read_file: %w", err)
	}
	output, err := t.read(args.Path, args.Limit)
	if err != nil {
		return nil, err
	}
	return []anthropic.BetaToolResultBlockParamContentUnion{
		{OfText: &anthropic.BetaTextBlockParam{Text: output}},
	}, nil
}

func (t ReadFileTool) read(path string, limit int) (string, error) {
	p, err := safePath(t.Workdir, path)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return "", fmt.Errorf("read_file: %w", err)
	}
	lines := strings.Split(string(data), "\n")
	if limit > 0 && limit < len(lines) {
		lines = append(lines[:limit], fmt.Sprintf("... (%d more lines)", len(lines)-limit))
	}
	result := strings.Join(lines, "\n")
	if len(result) > 50000 {
		result = result[:50000]
	}
	return result, nil
}

// safePath resolves and validates that the path stays within workdir.
func safePath(workdir, p string) (string, error) {
	absPath, err := filepath.Abs(filepath.Join(workdir, p))
	if err != nil {
		return "", fmt.Errorf("path resolution: %w", err)
	}
	realPath, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		// If path doesn't exist yet (e.g., for writes), use absPath.
		realPath = absPath
	}
	realWorkdir, _ := filepath.EvalSymlinks(workdir)
	if !strings.HasPrefix(realPath, realWorkdir+string(os.PathSeparator)) && realPath != realWorkdir {
		return "", fmt.Errorf("path escapes workspace: %s", p)
	}
	return absPath, nil
}
```

- [ ] **Step 2: Need to add "strings" import — fix after writing**

The import block needs `"strings"` and `"os"`. Run `go build ./internal/agent/tools/` to validate.

- [ ] **Step 3: Commit**

```bash
git add internal/agent/tools/read.go
git commit -m "feat(tools): add ReadFileTool with path confinement"
```

---

### Task 4: Create `internal/agent/tools/write.go` — WriteFileTool

**Files:**
- Create: `internal/agent/tools/write.go`

- [ ] **Step 1: Write WriteFileTool**

```go
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	anthropic "github.com/anthropics/anthropic-sdk-go"
)

// WriteFileTool writes content to a file.
type WriteFileTool struct {
	Workdir string
}

func (WriteFileTool) Name() string        { return "write_file" }
func (WriteFileTool) Description() string { return "Write content to file." }

func (WriteFileTool) InputSchema() anthropic.BetaToolInputSchemaParam {
	return anthropic.BetaToolInputSchemaParam{
		Properties: map[string]any{
			"path":    map[string]any{"type": "string"},
			"content": map[string]any{"type": "string"},
		},
		Required: []string{"path", "content"},
	}
}

func (t WriteFileTool) Execute(ctx context.Context, input json.RawMessage) ([]anthropic.BetaToolResultBlockParamContentUnion, error) {
	var args struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return nil, fmt.Errorf("write_file: %w", err)
	}
	result, err := t.write(args.Path, args.Content)
	if err != nil {
		return nil, err
	}
	return []anthropic.BetaToolResultBlockParamContentUnion{
		{OfText: &anthropic.BetaTextBlockParam{Text: result}},
	}, nil
}

func (t WriteFileTool) write(path, content string) (string, error) {
	p, err := safePath(t.Workdir, path)
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(p)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("write_file: %w", err)
	}
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		return "", fmt.Errorf("write_file: %w", err)
	}
	return fmt.Sprintf("Wrote %d bytes to %s", len(content), path), nil
}
```

- [ ] **Step 2: Verify compilation**

Run: `go build ./internal/agent/tools/`
Expected: no errors

- [ ] **Step 3: Commit**

```bash
git add internal/agent/tools/write.go
git commit -m "feat(tools): add WriteFileTool with path confinement"
```

---

### Task 5: Create `internal/agent/tools/edit.go` — EditFileTool

**Files:**
- Create: `internal/agent/tools/edit.go`

- [ ] **Step 1: Write EditFileTool**

```go
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	anthropic "github.com/anthropics/anthropic-sdk-go"
)

// EditFileTool replaces exact text in a file.
type EditFileTool struct {
	Workdir string
}

func (EditFileTool) Name() string        { return "edit_file" }
func (EditFileTool) Description() string { return "Replace exact text in file." }

func (EditFileTool) InputSchema() anthropic.BetaToolInputSchemaParam {
	return anthropic.BetaToolInputSchemaParam{
		Properties: map[string]any{
			"path":     map[string]any{"type": "string"},
			"old_text": map[string]any{"type": "string"},
			"new_text": map[string]any{"type": "string"},
		},
		Required: []string{"path", "old_text", "new_text"},
	}
}

func (t EditFileTool) Execute(ctx context.Context, input json.RawMessage) ([]anthropic.BetaToolResultBlockParamContentUnion, error) {
	var args struct {
		Path    string `json:"path"`
		OldText string `json:"old_text"`
		NewText string `json:"new_text"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return nil, fmt.Errorf("edit_file: %w", err)
	}
	result, err := t.edit(args.Path, args.OldText, args.NewText)
	if err != nil {
		return nil, err
	}
	return []anthropic.BetaToolResultBlockParamContentUnion{
		{OfText: &anthropic.BetaTextBlockParam{Text: result}},
	}, nil
}

func (t EditFileTool) edit(path, oldText, newText string) (string, error) {
	p, err := safePath(t.Workdir, path)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return "", fmt.Errorf("edit_file: %w", err)
	}
	content := string(data)
	if !strings.Contains(content, oldText) {
		return "", fmt.Errorf("edit_file: text not found in %s", path)
	}
	replaced := strings.Replace(content, oldText, newText, 1)
	if err := os.WriteFile(p, []byte(replaced), 0644); err != nil {
		return "", fmt.Errorf("edit_file: %w", err)
	}
	return fmt.Sprintf("Edited %s", path), nil
}
```

- [ ] **Step 2: Verify compilation**

Run: `go build ./internal/agent/tools/`
Expected: no errors

- [ ] **Step 3: Commit**

```bash
git add internal/agent/tools/edit.go
git commit -m "feat(tools): add EditFileTool with exact text replace"
```

---

### Task 6: Create `internal/agent/event.go` — Event types

**Files:**
- Create: `internal/agent/event.go`

- [ ] **Step 1: Extract event types from runner.go**

```go
package agent

import "task-agent/internal/agent/tools"

type (
	EventThinking    struct{}
	EventText        struct{ Content string }
	EventToolCalls   struct{ Tools []tools.ToolUseBlock }
	EventToolResults struct{ Results []tools.ToolResult }
	EventError       struct{ Err error }
	EventDone        struct{}
)
```

- [ ] **Step 2: Verify compilation**

Run: `go build ./internal/agent/`
Expected: should fail because runner.go still has old event types — that's OK, fixed in next task.

- [ ] **Step 3: Commit**

```bash
git add internal/agent/event.go
git commit -m "feat(agent): extract event types to event.go"
```

---

### Task 7: Refactor `internal/agent/agent.go` — Switch to Registry and Beta API

**Files:**
- Modify: `internal/agent/agent.go`

- [ ] **Step 1: Replace hardcoded tools with Registry**

Delete the `tools  []anthropic.ToolUnionParam` field from the Agent struct and replace it with `registry *tools.Registry`. Delete the inline bash tool registration (lines 80-93 of current file) and replace with Registry construction.

```go
package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"task-agent/internal/agent/tools"
)

type Agent struct {
	client   *anthropic.Client
	model    anthropic.Model
	system   []anthropic.BetaTextBlockParam
	registry *tools.Registry
}

type claudeSettings struct {
	Env map[string]string `json:"env"`
}

func loadSettings(path string) map[string]string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var s claudeSettings
	if err := json.Unmarshal(data, &s); err != nil {
		return nil
	}
	return s.Env
}

func envOrSettings(envKey, settingsKey string, settingsEnv map[string]string) string {
	if v := os.Getenv(envKey); v != "" {
		return v
	}
	return settingsEnv[settingsKey]
}

func New() (*Agent, error) {
	settingsEnv := loadSettings(filepath.Join(os.Getenv("USERPROFILE"), ".claude", "settings.json"))

	modelID := envOrSettings("MODEL_ID", "ANTHROPIC_MODEL", settingsEnv)
	if modelID == "" {
		return nil, fmt.Errorf("model ID not set: set ANTHROPIC_MODEL in %s or MODEL_ID env var",
			filepath.Join(os.Getenv("USERPROFILE"), ".claude", "settings.json"))
	}

	var opts []option.RequestOption

	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		apiKey = settingsEnv["ANTHROPIC_AUTH_TOKEN"]
	}
	if apiKey != "" {
		opts = append(opts, option.WithAPIKey(apiKey))
	}
	if baseURL := envOrSettings("ANTHROPIC_BASE_URL", "ANTHROPIC_BASE_URL", settingsEnv); baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}

	client := anthropic.NewClient(opts...)

	cwd, _ := os.Getwd()
	system := []anthropic.BetaTextBlockParam{
		{Text: anthropic.String(fmt.Sprintf("You are a coding agent at %s. Use tools to solve tasks. Act, don't explain.", cwd))},
	}

	registry := tools.NewRegistry(
		tools.BashTool{},
		&tools.ReadFileTool{Workdir: cwd},
		&tools.WriteFileTool{Workdir: cwd},
		&tools.EditFileTool{Workdir: cwd},
	)

	return &Agent{
		client:   &client,
		model:    modelID,
		system:   system,
		registry: registry,
	}, nil
}
```

- [ ] **Step 2: Verify compilation**

Run: `go build ./internal/agent/`
Expected: errors in runner.go (old event types + old ToolUnionParam) — fixed next.

- [ ] **Step 3: Commit**

```bash
git add internal/agent/agent.go
git commit -m "refactor(agent): switch from hardcoded bash tool to Tool Registry"
```

---

### Task 8: Refactor `internal/agent/runner.go` — Parallel dispatch loop

**Files:**
- Modify: `internal/agent/runner.go`

- [ ] **Step 1: Rewrite runner with batch parallel dispatch**

Replace the current `runner.go` content. The loop now:
1. Uses Beta API (`client.Beta.Messages.New`)
2. Collects ALL tool_use blocks
3. Dispatches via Registry in parallel
4. Emits batch events
5. Works with `BetaMessageParam` and `BetaContentBlockUnion`

```go
package agent

import (
	"context"
	"encoding/json"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"task-agent/internal/agent/tools"
)

type Runner struct {
	agent    *Agent
	messages []anthropic.BetaMessageParam
}

func NewRunner(ag *Agent) *Runner {
	return &Runner{agent: ag}
}

func (r *Runner) Run(ctx context.Context, input string) <-chan any {
	ch := make(chan any, 10)
	go func() {
		defer close(ch)
		r.runLoop(ctx, input, ch)
	}()
	return ch
}

func (r *Runner) runLoop(ctx context.Context, input string, ch chan<- any) {
	r.messages = append(r.messages, anthropic.NewBetaUserMessage(
		anthropic.BetaContentBlockParamUnion{
			OfText: &anthropic.BetaTextBlockParam{Text: input},
		}))

	ch <- EventThinking{}

	for {
		resp, err := r.agent.client.Beta.Messages.New(ctx, anthropic.BetaMessageNewParams{
			Model:     r.agent.model,
			System:    r.agent.system,
			Messages:  r.messages,
			Tools:     r.agent.registry.ToParams(),
			MaxTokens: 8000,
		})
		if err != nil {
			ch <- EventError{Err: err}
			return
		}

		r.messages = append(r.messages, resp.ToParam())

		var toolBlocks []tools.ToolUseBlock
		for _, block := range resp.Content {
			if block.Type == "tool_use" {
				tu := block.AsToolUse()
				toolBlocks = append(toolBlocks, tools.ToolUseBlock{
					ID:    tu.ID,
					Name:  tu.Name,
					Input: marshalInput(tu.Input),
				})
			} else if block.Type == "text" {
				t := block.AsText()
				if t != nil {
					ch <- EventText{Content: t.Text}
				}
			}
		}

		if len(toolBlocks) == 0 {
			ch <- EventDone{}
			return
		}

		ch <- EventToolCalls{Tools: toolBlocks}

		results, err := r.agent.registry.Dispatch(ctx, toolBlocks)
		if err != nil {
			ch <- EventError{Err: err}
			return
		}

		ch <- EventToolResults{Results: results}

		var contentBlocks []anthropic.BetaContentBlockParamUnion
		for _, result := range results {
			isError := false
			if len(result.Content) > 6 && result.Content[:6] == "Error:" {
				isError = true
			}
			contentBlocks = append(contentBlocks, anthropic.BetaContentBlockParamUnion{
				OfToolResult: &anthropic.BetaToolResultBlockParam{
					ToolUseID: result.ToolUseID,
					Content: []anthropic.BetaToolResultBlockParamContentUnion{
						{OfText: &anthropic.BetaTextBlockParam{Text: result.Content}},
					},
					IsError: anthropic.Bool(isError),
				},
			})
		}
		r.messages = append(r.messages, anthropic.NewBetaUserMessage(contentBlocks...))
	}
}

// marshalInput converts BetaToolUseBlock.Input (json.RawMessage or map) to json.RawMessage.
func marshalInput(input json.RawMessage) json.RawMessage {
	return input
}
```

- [ ] **Step 2: Verify compilation**

Run: `go build ./internal/agent/`
Expected: errors in tui.go (old event types) — fixed next.

- [ ] **Step 3: Commit**

```bash
git add internal/agent/runner.go
git commit -m "refactor(agent): rewrite runner with parallel batch dispatch and Beta API"
```

---

### Task 9: Adapt `internal/agent/tui.go` — Batch events

**Files:**
- Modify: `internal/agent/tui.go`

- [ ] **Step 1: Replace EventToolCall/EventToolResult handlers with batch versions**

Change the two event cases in the `Update` method.

Find the existing code:
```go
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
```

Replace with:
```go
	case EventToolCalls:
		for _, tc := range msg.Tools {
			preview := toolPreview(tc)
			m.content = append(m.content, fmt.Sprintf("\033[33m> %s(%s)\033[0m", tc.Name, preview))
		}
		m.refreshViewport()
		return m, watchRunner(m.runnerCh)

	case EventToolResults:
		for _, tr := range msg.Results {
			out := tr.Content
			if len(out) > 200 {
				m.content = append(m.content, out[:200], fmt.Sprintf("... (%d more bytes)", len(out)-200))
			} else {
				m.content = append(m.content, out)
			}
		}
		m.refreshViewport()
		return m, watchRunner(m.runnerCh)
```

Add `toolPreview` helper function at bottom of file:
```go
func toolPreview(tc tools.ToolUseBlock) string {
	switch tc.Name {
	case "bash":
		var args struct{ Command string `json:"command"` }
		if err := json.Unmarshal(tc.Input, &args); err == nil && args.Command != "" {
			s := args.Command
			if len(s) > 80 {
				s = s[:80] + "..."
			}
			return s
		}
	case "read_file", "write_file", "edit_file":
		var args struct{ Path string `json:"path"` }
		if err := json.Unmarshal(tc.Input, &args); err == nil && args.Path != "" {
			return args.Path
		}
	}
	return "..."
}
```

Need to add imports: `"encoding/json"` and `"task-agent/internal/agent/tools"` to tui.go imports.

- [ ] **Step 2: Verify compilation**

Run: `go build ./internal/agent/`
Expected: no errors

- [ ] **Step 3: Commit**

```bash
git add internal/agent/tui.go
git commit -m "refactor(tui): adapt to batch EventToolCalls and EventToolResults"
```

---

### Task 10: Delete `internal/agent/bash.go` and verify

**Files:**
- Delete: `internal/agent/bash.go`

- [ ] **Step 1: Delete bash.go and verify**

```bash
git rm internal/agent/bash.go
go build ./...
```

Expected: `go build ./...` succeeds with no errors.

- [ ] **Step 2: Commit**

```bash
git commit -m "refactor(agent): remove standalone bash.go, migrated to tools/bash.go"
```

---

### Task 11: Verify full build and fix any issues

**Files:**
- Modify: `internal/app/app.go` (only if needed)

- [ ] **Step 1: Run full build**

Run: `go build ./...`
Expected: no errors

- [ ] **Step 2: Run vet**

Run: `go vet ./...`
Expected: no issues

- [ ] **Step 3: Check unused imports in app.go**

`app.go` currently imports `tea` and creates `agent.NewTUI(runner, tea.WithContext(ctx))`. The `agent.New()` constructor changed its internal behavior but the interface to `app.go` is unchanged (still returns `*Agent`, `Runner` and `TUI` still same constructors). Verify `app.go` needs no changes.

If `app.go` compiles cleanly, no changes needed. Otherwise fix any type mismatches.

- [ ] **Step 4: Commit any fixes**

Only if changes were needed:
```bash
git commit -m "fix: build issues after tool dispatch refactor"
```

---

### Task 12: Final integration test

- [ ] **Step 1: Start the app briefly to verify startup**

Run: `go run ./cmd/app/` (and immediately Ctrl+C after seeing TUI)
Expected: TUI starts without panic or error.

- [ ] **Step 2: Run all tests**

Run: `go test ./...`
Expected: all existing tests pass.

- [ ] **Step 3: Commit if any test fixes needed**

Only if test fixes were needed.
