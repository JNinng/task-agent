package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"task-agent/internal/agent/background"
	"task-agent/internal/agent/skill"
	"task-agent/internal/agent/tasks"
	"task-agent/internal/agent/tools"
)

type Agent struct {
	client   *anthropic.Client
	model    anthropic.Model
	system   []anthropic.BetaTextBlockParam
	registry *tools.Registry
	Runner   *Runner
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
	settingsEnv := loadSettings(ClaudeSettingsPath())

	modelID := envOrSettings("MODEL_ID", "ANTHROPIC_MODEL", settingsEnv)
	if modelID == "" {
		return nil, fmt.Errorf("model ID not set: set ANTHROPIC_MODEL in %s or MODEL_ID env var",
			ClaudeSettingsPath())
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
	loader, err := skill.NewLoader(GlobalSkillsDir(), ProjectSkillsDir(cwd))
	if err != nil {
		return nil, fmt.Errorf("skill loader: %w", err)
	}

	// --- Task graph persistence ---
	taskMgr, err := tasks.NewManager(DataDir(), tasks.ResolveTaskListID(""))
	if err != nil {
		return nil, fmt.Errorf("task manager: %w", err)
	}

	// --- Background task manager ---
	bgMgr := background.NewManager()

	// Build system prompt with two-layer skill injection
	var systemText strings.Builder
	systemText.WriteString(fmt.Sprintf(
		"You are a coding agent at %s.\n"+
			"Use tools to solve tasks. Act, don't explain.\n\n"+
			"The todo tool is a quick in-memory checklist for this session only.\n"+
			"For structured, persistent work with dependencies, use the task graph tools:\n"+
			"  - task_create — create tasks in the persistent graph\n"+
			"  - task_update — update status/dependencies (completed tasks auto-unlock dependents)\n"+
			"  - task_list   — list all tasks with status and blockers\n"+
			"  - task_get    — get full details of a task\n"+
			"The task tool launches a subagent for complex multi-step work (research, code exploration, "+
			"multi-file edits). Prefer task over doing exploration yourself — the subagent's intermediate "+
			"steps won't pollute your context window. For simple single-step actions (one read, one bash "+
			"command), use the direct tool instead.\n\n"+
			"Background tasks:\n"+
			"  - background_bash - run a shell command in the background and continue working\n"+
			"  - check_background - check the status of background tasks\n"+
			"Use background_bash for long-running commands (npm install, pytest, etc.). "+
			"Results are automatically delivered to you when they complete.",
		cwd,
	))

	// Layer 1: skill name + description list (~100 tokens/skill)
	if desc := loader.Descriptions(); desc != "" {
		systemText.WriteString(fmt.Sprintf("\n\nSkills loaded from ~/%s/skills/ and "+
			"<project>/%s/skills/. The list below is complete — use "+
			"load_skill to expand full instructions. Do NOT search the filesystem for skills.\n",
			DirAgent, DirAgent))
		systemText.WriteString(desc)
	}

	// Layer 1.5: always_load skills injected directly into system prompt
	for _, s := range loader.AlwaysLoaded() {
		systemText.WriteString(fmt.Sprintf("\n\n<skill name=\"%s\">\n%s\n</skill>", s.Name, s.Body))
	}

	system := []anthropic.BetaTextBlockParam{
		{Text: systemText.String()},
	}

	// Create Agent struct first so we can wire it to the Runner
	ag := &Agent{
		client: &client,
		model:  modelID,
		system: system,
	}

	compactCfg := DefaultCompactionConfig()

	var compactTrigger func() (string, error)

	ag.Runner = NewRunner(ag, compactCfg, bgMgr, func(fn func() (string, error)) {
		compactTrigger = fn
	})

	ag.registry = tools.NewRegistry(
		tools.BashTool{},
		&tools.ReadFileTool{Workdir: cwd},
		&tools.WriteFileTool{Workdir: cwd},
		&tools.EditFileTool{Workdir: cwd},
		&tools.TodoWriteTool{},
		&tools.TaskCreateTool{Mgr: taskMgr},
		&tools.TaskGetTool{Mgr: taskMgr},
		&tools.TaskListTool{Mgr: taskMgr},
		&tools.TaskUpdateTool{Mgr: taskMgr},
		tools.NewSubagentTool(&client, anthropic.Model(modelID), cwd),
		skill.NewLoadSkillTool(loader),
		&tools.BackgroundBashTool{Mgr: bgMgr},
		&tools.CheckBackgroundTool{Mgr: bgMgr},
		tools.NewCompactTool(func() (string, error) {
			if compactTrigger == nil {
				return "", fmt.Errorf("compact not initialized")
			}
			return compactTrigger()
		}),
	)

	return ag, nil
}
