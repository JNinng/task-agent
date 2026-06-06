// Package agent 提供 TUI 聊天界面，用户通过终端与 AI 模型交互。
package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// Agent 封装了 Anthropic API 客户端及相关配置。
type Agent struct {
	client *anthropic.Client
	model  anthropic.Model
	system []anthropic.TextBlockParam // 系统提示词
	tools  []anthropic.ToolUnionParam // 可用工具列表
}

// claudeSettings 对应 Claude Code settings.json 的结构。
type claudeSettings struct {
	Env map[string]string `json:"env"`
}

// loadSettings 从指定路径加载 Claude Code 的 settings.json，提取其中的环境变量配置。
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

// envOrSettings 优先返回环境变量值，若为空则回退到 settings.json 中的值。
func envOrSettings(envKey, settingsKey string, settingsEnv map[string]string) string {
	if v := os.Getenv(envKey); v != "" {
		return v
	}
	return settingsEnv[settingsKey]
}

// New 创建 Agent 实例。
// 配置来源优先级：环境变量 > settings.json。
// 模型由 MODEL_ID 环境变量或 settings.json 中的 ANTHROPIC_MODEL 指定。
// API 密钥由 ANTHROPIC_API_KEY 环境变量或 settings.json 中的 ANTHROPIC_AUTH_TOKEN 指定。
func New() (*Agent, error) {
	settingsEnv := loadSettings(filepath.Join(os.Getenv("USERPROFILE"), ".claude", "settings.json"))

	// 读取模型 ID
	modelID := envOrSettings("MODEL_ID", "ANTHROPIC_MODEL", settingsEnv)
	if modelID == "" {
		return nil, fmt.Errorf("model ID not set: set ANTHROPIC_MODEL in %s or MODEL_ID env var",
			filepath.Join(os.Getenv("USERPROFILE"), ".claude", "settings.json"))
	}

	var opts []option.RequestOption

	// API 密钥：优先 ANTHROPIC_API_KEY 环境变量，其次 ANTHROPIC_AUTH_TOKEN
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

	// 构建系统提示词
	cwd, _ := os.Getwd()
	system := []anthropic.TextBlockParam{
		{Text: fmt.Sprintf("You are a coding agent at %s. Use bash to solve tasks. Act, don't explain.", cwd)},
	}

	// 注册 bash 工具
	toolSchema := anthropic.ToolInputSchemaParam{
		Properties: map[string]any{
			"command": map[string]any{"type": "string"},
		},
		Required: []string{"command"},
	}
	tools := []anthropic.ToolUnionParam{
		anthropic.ToolUnionParamOfTool(toolSchema, "bash"),
	}

	return &Agent{
		client: &client,
		model:  modelID,
		system: system,
		tools:  tools,
	}, nil
}
