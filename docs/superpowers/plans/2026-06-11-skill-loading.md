# Skill Loading Mechanism — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement two-layer skill loading: skill names/descriptions in system prompt, full content loaded on demand via `load_skill` tool.

**Architecture:** New `internal/agent/skill` package with Skill struct, Loader, and LoadSkillTool. The Loader scans `~/.task-agent/skills/` (global) and `<project>/.task-agent/skills/` (project), parses SKILL.md files with YAML frontmatter, and merges with project priority. `agent.go` integrates the Loader into system prompt construction and adds LoadSkillTool to the Registry.

**Tech Stack:** Go 1.26, `gopkg.in/yaml.v3` (already in go.mod), `github.com/anthropics/anthropic-sdk-go`

---

### Task 1: Skill struct and Loader

**Files:**
- Create: `internal/agent/skill/skill.go`

- [ ] **Step 1: Write the skill.go file**

```go
package skill

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Skill represents a parsed SKILL.md file.
type Skill struct {
	Name        string // from frontmatter, falls back to directory name
	Description string // from frontmatter
	AlwaysLoad  bool   // if true, body injected into system prompt
	Body        string // markdown after YAML frontmatter
}

// frontmatter is the YAML metadata block in SKILL.md.
type frontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	AlwaysLoad  bool   `yaml:"always_load"`
}

// Loader manages skills loaded from file system.
type Loader struct {
	skills map[string]*Skill // name → skill (project overrides global)
}

// NewLoader scans globalDir then projectDir for SKILL.md files.
// Project skills override global skills with the same name.
// Missing directories are silently skipped.
func NewLoader(globalDir, projectDir string) (*Loader, error) {
	l := &Loader{skills: make(map[string]*Skill)}
	if err := l.loadDir(globalDir); err != nil {
		return nil, err
	}
	if err := l.loadDir(projectDir); err != nil {
		return nil, err
	}
	return l, nil
}

// loadDir scans a single skills directory for <name>/SKILL.md files.
func (l *Loader) loadDir(dir string) error {
	if dir == "" {
		return nil
	}
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		skillPath := filepath.Join(dir, entry.Name(), "SKILL.md")
		data, err := os.ReadFile(skillPath)
		if err != nil {
			if os.IsNotExist(err) {
				continue // directory without SKILL.md is ok
			}
			return err
		}
		skill, err := parseSkill(entry.Name(), data)
		if err != nil {
			return fmt.Errorf("skill %s: %w", entry.Name(), err)
		}
		l.skills[skill.Name] = skill // project overwrites global
	}
	return nil
}

// parseSkill parses a SKILL.md file content.
// dirName is used as fallback when frontmatter name is empty.
func parseSkill(dirName string, data []byte) (*Skill, error) {
	content := strings.TrimSpace(string(data))
	fm, body, err := splitFrontmatter(content)
	if err != nil {
		return nil, err
	}
	name := fm.Name
	if name == "" {
		name = dirName
	}
	if fm.Description == "" {
		return nil, fmt.Errorf("missing description")
	}
	return &Skill{
		Name:        name,
		Description: fm.Description,
		AlwaysLoad:  fm.AlwaysLoad,
		Body:        strings.TrimSpace(body),
	}, nil
}

// splitFrontmatter extracts YAML between the first pair of --- lines.
func splitFrontmatter(content string) (frontmatter, string, error) {
	if !strings.HasPrefix(content, "---") {
		return frontmatter{}, "", fmt.Errorf("frontmatter must start with ---")
	}
	rest := content[3:]
	idx := strings.Index(rest, "\n---")
	if idx < 0 {
		return frontmatter{}, "", fmt.Errorf("frontmatter must end with ---")
	}
	yamlBlock := rest[:idx]
	body := rest[idx+4:] // skip "\n---"
	var fm frontmatter
	if err := yaml.Unmarshal([]byte(yamlBlock), &fm); err != nil {
		return frontmatter{}, "", fmt.Errorf("invalid YAML: %w", err)
	}
	return fm, body, nil
}

// Descriptions returns a sorted list of "  - name: description" lines
// suitable for inclusion in the system prompt.
func (l *Loader) Descriptions() string {
	names := make([]string, 0, len(l.skills))
	for name := range l.skills {
		names = append(names, name)
	}
	sort.Strings(names)
	var lines []string
	for _, name := range names {
		s := l.skills[name]
		lines = append(lines, fmt.Sprintf("  - %s: %s", s.Name, s.Description))
	}
	return strings.Join(lines, "\n")
}

// AlwaysLoaded returns always_load=true skills sorted by name.
func (l *Loader) AlwaysLoaded() []*Skill {
	var result []*Skill
	for _, s := range l.skills {
		if s.AlwaysLoad {
			result = append(result, s)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
}

// Get returns a skill's full body wrapped in <skill> tags, or an error
// if the skill is not found.
func (l *Loader) Get(name string) (string, error) {
	s, ok := l.skills[name]
	if !ok {
		return "", fmt.Errorf("unknown skill '%s'", name)
	}
	return fmt.Sprintf("<skill name=\"%s\">\n%s\n</skill>", s.Name, s.Body), nil
}
```

- [ ] **Step 2: Verify compilation**

```powershell
go build ./internal/agent/skill/...
```

Expected: no errors

- [ ] **Step 3: Commit**

```bash
git add internal/agent/skill/skill.go
git commit -m "feat(skill): add Skill struct and Loader with YAML frontmatter parsing"
```

---

### Task 2: Loader tests

**Files:**
- Create: `internal/agent/skill/skill_test.go`

- [ ] **Step 1: Write the test file**

```go
package skill

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTempSkill(t *testing.T, dir, skillDir, content string) string {
	t.Helper()
	skillPath := filepath.Join(dir, skillDir)
	if err := os.MkdirAll(skillPath, 0755); err != nil {
		t.Fatal(err)
	}
	filePath := filepath.Join(skillPath, "SKILL.md")
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return filePath
}

func TestParseSkill_Valid(t *testing.T) {
	content := `---
name: code-review
description: Review code for correctness
always_load: false
---
# Code Review

Step 1: Read the diff.`

	s, err := parseSkill("code-review", []byte(content))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Name != "code-review" {
		t.Errorf("name = %q, want %q", s.Name, "code-review")
	}
	if s.Description != "Review code for correctness" {
		t.Errorf("description = %q", s.Description)
	}
	if s.AlwaysLoad {
		t.Error("always_load should be false")
	}
	if s.Body != "# Code Review\n\nStep 1: Read the diff." {
		t.Errorf("body = %q", s.Body)
	}
}

func TestParseSkill_FallsBackToDirName(t *testing.T) {
	content := `---
description: Some skill
---

Body text.`

	s, err := parseSkill("my-skill", []byte(content))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Name != "my-skill" {
		t.Errorf("name = %q, want %q", s.Name, "my-skill")
	}
}

func TestParseSkill_MissingDescription(t *testing.T) {
	content := `---
name: bad
---

Body.`

	_, err := parseSkill("bad", []byte(content))
	if err == nil {
		t.Fatal("expected error for missing description")
	}
}

func TestParseSkill_AlwaysLoad(t *testing.T) {
	content := `---
name: auto
description: Auto-loaded skill
always_load: true
---

Always present.`

	s, err := parseSkill("auto", []byte(content))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !s.AlwaysLoad {
		t.Error("always_load should be true")
	}
}

func TestParseSkill_NoFrontmatter(t *testing.T) {
	content := `# Just a heading
No frontmatter here.`

	_, err := parseSkill("test", []byte(content))
	if err == nil {
		t.Fatal("expected error for missing frontmatter")
	}
}

func TestParseSkill_EmptyBody(t *testing.T) {
	content := `---
name: minimal
description: Just metadata
---

`

	s, err := parseSkill("minimal", []byte(content))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Body != "" {
		t.Errorf("body = %q, want empty", s.Body)
	}
}

func TestNewLoader_EmptyDirs(t *testing.T) {
	dir := t.TempDir()
	l, err := NewLoader(dir, filepath.Join(dir, "project"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if desc := l.Descriptions(); desc != "" {
		t.Errorf("descriptions = %q, want empty", desc)
	}
}

func TestNewLoader_LoadsSkills(t *testing.T) {
	globalDir := t.TempDir()
	writeTempSkill(t, globalDir, "git", `---
name: git
description: Git workflow helpers
---

# Git Skill
`)

	l, err := NewLoader(globalDir, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content, err := l.Get("git")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if content != "<skill name=\"git\">\n# Git Skill\n</skill>" {
		t.Errorf("content = %q", content)
	}
}

func TestNewLoader_ProjectOverridesGlobal(t *testing.T) {
	globalDir := t.TempDir()
	writeTempSkill(t, globalDir, "review", `---
name: review
description: Global review
---

Global instructions.
`)

	projectDir := t.TempDir()
	writeTempSkill(t, projectDir, "review", `---
name: review
description: Project review
---

Project instructions.
`)

	l, err := NewLoader(globalDir, projectDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content, err := l.Get("review")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if content != "<skill name=\"review\">\nProject instructions.\n</skill>" {
		t.Errorf("content = %q, want project version", content)
	}
}

func TestLoader_Descriptions_Sorted(t *testing.T) {
	dir := t.TempDir()
	writeTempSkill(t, dir, "zzz", `---
name: zzz
description: Last
---

Z.
`)
	writeTempSkill(t, dir, "aaa", `---
name: aaa
description: First
---

A.
`)

	l, err := NewLoader(dir, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	desc := l.Descriptions()
	expected := "  - aaa: First\n  - zzz: Last"
	if desc != expected {
		t.Errorf("descriptions = %q, want %q", desc, expected)
	}
}

func TestLoader_AlwaysLoaded(t *testing.T) {
	dir := t.TempDir()
	writeTempSkill(t, dir, "always", `---
name: always
description: Always loaded
always_load: true
---

Always here.
`)
	writeTempSkill(t, dir, "manual", `---
name: manual
description: Manual load
always_load: false
---

Load me manually.
`)

	l, err := NewLoader(dir, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	loaded := l.AlwaysLoaded()
	if len(loaded) != 1 {
		t.Fatalf("len(always_loaded) = %d, want 1", len(loaded))
	}
	if loaded[0].Name != "always" {
		t.Errorf("always_loaded[0].Name = %q, want %q", loaded[0].Name, "always")
	}
}

func TestLoader_Get_Unknown(t *testing.T) {
	dir := t.TempDir()
	l, err := NewLoader(dir, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, err = l.Get("nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown skill")
	}
}

func TestLoader_DirNotFound(t *testing.T) {
	l, err := NewLoader("/nonexistent/path/12345", "")
	if err != nil {
		t.Fatalf("missing dir should not error: %v", err)
	}
	if len(l.skills) != 0 {
		t.Errorf("skills = %d, want 0", len(l.skills))
	}
}
```

- [ ] **Step 2: Run tests to verify they fail (code doesn't exist yet)**

Wait — skip: we already wrote skill.go in Task 1, so tests should pass. Instead, verify they pass:

```powershell
go test ./internal/agent/skill/... -v -count=1
```

Expected: all tests PASS

- [ ] **Step 3: Commit**

```bash
git add internal/agent/skill/skill_test.go
git commit -m "test(skill): add Loader and parseSkill tests"
```

---

### Task 3: load_skill tool

**Files:**
- Create: `internal/agent/skill/load_skill_tool.go`

- [ ] **Step 1: Write the load_skill_tool.go file**

```go
package skill

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
)

// LoadSkillTool implements tools.Tool to load skill content on demand.
type LoadSkillTool struct {
	loader *Loader
}

// NewLoadSkillTool creates a LoadSkillTool backed by the given loader.
func NewLoadSkillTool(loader *Loader) *LoadSkillTool {
	return &LoadSkillTool{loader: loader}
}

func (t *LoadSkillTool) Name() string { return "load_skill" }

func (t *LoadSkillTool) Description() string {
	return "Load a skill's full instructions by name. Use this when a task matches an available skill."
}

func (t *LoadSkillTool) InputSchema() anthropic.BetaToolInputSchemaParam {
	return anthropic.BetaToolInputSchemaParam{
		Properties: map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "The name of the skill to load",
			},
		},
		Required: []string{"name"},
	}
}

func (t *LoadSkillTool) Execute(ctx context.Context, input json.RawMessage) ([]anthropic.BetaToolResultBlockParamContentUnion, error) {
	var args struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return nil, fmt.Errorf("load_skill: %w", err)
	}
	content, err := t.loader.Get(args.Name)
	if err != nil {
		return nil, err
	}
	return []anthropic.BetaToolResultBlockParamContentUnion{
		{OfText: &anthropic.BetaTextBlockParam{Text: content}},
	}, nil
}
```

- [ ] **Step 2: Verify compilation**

```powershell
go build ./internal/agent/skill/...
```

Expected: no errors

- [ ] **Step 3: Commit**

```bash
git add internal/agent/skill/load_skill_tool.go
git commit -m "feat(skill): add load_skill tool implementing tools.Tool interface"
```

---

### Task 4: Integrate SkillLoader into agent.go

**Files:**
- Modify: `internal/agent/agent.go` (full file — small, replace entirely)

- [ ] **Step 1: Rewrite agent.go with skill integration**

Read the current file first to ensure we have the latest content, then apply changes.

Changes:
1. Add import for `"strings"` and `"task-agent/internal/agent/skill"`
2. Determine global and project skill directories
3. Create SkillLoader
4. Build two-layer system prompt using `strings.Builder`
5. Add `LoadSkillTool` to registry

New `agent.go`:

```go
package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"task-agent/internal/agent/skill"
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

	// --- Skill loading ---
	homeDir := os.Getenv("USERPROFILE")
	if homeDir == "" {
		homeDir = os.Getenv("HOME") // fallback for Unix-like
	}
	globalSkillsDir := filepath.Join(homeDir, ".task-agent", "skills")
	projectSkillsDir := filepath.Join(cwd, ".task-agent", "skills")

	loader, err := skill.NewLoader(globalSkillsDir, projectSkillsDir)
	if err != nil {
		return nil, fmt.Errorf("skill loader: %w", err)
	}
	// --- End skill loading ---

	// Build system prompt with two-layer skill injection
	var systemText strings.Builder
	systemText.WriteString(fmt.Sprintf(
		"You are a coding agent at %s.\n"+
			"Use tools to solve tasks. Act, don't explain.\n\n"+
			"The todo tool is self-contained — call it directly, do not explore the codebase first.\n"+
			"The task tool launches a subagent for complex multi-step work (research, code exploration, "+
			"multi-file edits). Prefer task over doing exploration yourself — the subagent's intermediate "+
			"steps won't pollute your context window. For simple single-step actions (one read, one bash "+
			"command), use the direct tool instead.",
		cwd,
	))

	// Layer 1: skill name + description list (cheap, ~100 tokens/skill)
	if desc := loader.Descriptions(); desc != "" {
		systemText.WriteString("\n\nSkills available (use load_skill to get full instructions):\n")
		systemText.WriteString(desc)
	}

	// Layer 1.5: always_load skills injected directly into system prompt
	for _, s := range loader.AlwaysLoaded() {
		systemText.WriteString(fmt.Sprintf("\n\n<skill name=\"%s\">\n%s\n</skill>", s.Name, s.Body))
	}

	system := []anthropic.BetaTextBlockParam{
		{Text: systemText.String()},
	}

	registry := tools.NewRegistry(
		tools.BashTool{},
		&tools.ReadFileTool{Workdir: cwd},
		&tools.WriteFileTool{Workdir: cwd},
		&tools.EditFileTool{Workdir: cwd},
		&tools.TodoWriteTool{},
		tools.NewSubagentTool(&client, anthropic.Model(modelID), cwd),
		skill.NewLoadSkillTool(loader),
	)

	return &Agent{
		client:   &client,
		model:    modelID,
		system:   system,
		registry: registry,
	}, nil
}
```

- [ ] **Step 2: Verify compilation of agent package**

```powershell
go build ./internal/agent/...
```

Expected: no errors

- [ ] **Step 3: Verify full project compilation**

```powershell
go build ./...
```

Expected: no errors

- [ ] **Step 4: Run all tests**

```powershell
go test ./... -count=1
```

Expected: all tests PASS

- [ ] **Step 5: Commit**

```bash
git add internal/agent/agent.go
git commit -m "feat(agent): integrate SkillLoader with two-layer system prompt"
```

---

### Task 5: Create a sample skill for manual smoke test

**Files:**
- Create: `.task-agent/skills/git/SKILL.md`

- [ ] **Step 1: Write sample skill**

```markdown
---
name: git
description: Git workflow and commit conventions
---

# Git Workflow

## Commit Message Format

Use conventional commits: `type(scope): description`

Types: feat, fix, docs, style, refactor, test, chore

## Branch Naming

- Feature: `feat/<description>`
- Fix: `fix/<description>`
- Refactor: `refactor/<description>`

## Before Committing

1. Run `go test ./...` and ensure all pass
2. Run `go build ./...` and ensure no errors
3. Write a meaningful commit message
```

- [ ] **Step 2: Commit**

```bash
git add .task-agent/skills/git/SKILL.md
git commit -m "feat: add sample git skill for testing"
```
