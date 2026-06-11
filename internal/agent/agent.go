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
		homeDir = os.Getenv("HOME")
	}
	globalSkillsDir := filepath.Join(homeDir, ".task-agent", "skills")
	projectSkillsDir := filepath.Join(cwd, ".task-agent", "skills")

	loader, err := skill.NewLoader(globalSkillsDir, projectSkillsDir)
	if err != nil {
		return nil, fmt.Errorf("skill loader: %w", err)
	}

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

	// Layer 1: skill name + description list (~100 tokens/skill)
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
