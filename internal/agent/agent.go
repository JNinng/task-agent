package agent

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"go.uber.org/zap"
	"task-agent/internal/agent/background"
	"task-agent/internal/agent/skill"
	"task-agent/internal/agent/tasks"
	"task-agent/internal/agent/team"
	"task-agent/internal/agent/tools"
	"task-agent/internal/logger"
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

func New(resumeID string) (*Agent, error) {
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

	// --- Agent team manager ---
	teamDir := TeamDir(DataDir())
	teamMgr, err := team.NewManager(&client, anthropic.Model(modelID), cwd, teamDir, "lead", taskMgr)
	if err != nil {
		return nil, fmt.Errorf("team manager: %w", err)
	}

	// Build system prompt with two-layer skill injection
	var systemText strings.Builder
	systemText.WriteString(fmt.Sprintf(
		"You are a coding agent at %s.\n"+
			"Use tools to solve tasks. Act, don't explain.\n"+
			"Do NOT explore the filesystem, list directories, or read config files before acting. "+
			"Go straight to the tool that solves the task.\n\n"+
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
			"Results are automatically delivered to you when they complete.\n\n"+
			"Agent team:\n"+
			"  - team_spawn              - create a persistent teammate with a name, role, and initial task\n"+
			"  - team_send                - send a message to a teammate (or 'all' to broadcast)\n"+
			"  - team_inbox               - emergency-only; results auto-inject, do NOT poll\n"+
			"  - team_shutdown_request    - gracefully request a teammate to shut down (handshake)\n"+
			"  - team_plan_response       - approve or reject a teammate's submitted plan\n"+
			"Teammates run independently. CRITICAL RULES:\n"+
			"1. SPAWN FIRST — no bash/read_file/listing before spawn.\n"+
			"   The teammate explores. You delegate. Example: user says 'run tests'\n"+
			"   → team_spawn(name='tester', role='tester',\n"+
			"     prompt='Run go test ./... and go vet ./..., report results'). Done.\n"+
			"2. NEVER call team_inbox. Results auto-inject before your next response.\n"+
			"   Do not say 'still waiting' or 'checking progress'. Wait silently.\n"+
			"3. NEVER run the same command the teammate is running (no duplicate bash).\n"+
			"4. When a teammate's result arrives, report it to the user naturally.\n"+
			"5. Shutdown protocol: use team_shutdown_request to gracefully stop a teammate.\n"+
			"   The teammate may approve (finish work → exit) or reject (continue working).\n"+
			"   Wait for a type=\"shutdown_response\" message in <team-inbox> to confirm the\n"+
			"   result — the request_id in the response matches the one you sent.\n"+
			"6. Plan approval protocol: when <team-inbox> contains a type=\"plan_request\"\n"+
			"   message, review the plan and use team_plan_response with the message's\n"+
			"   request_id to approve (teammate proceeds) or reject with feedback (teammate\n"+
			"   abandons). The teammate will wait for your response before acting.\n"+
			"Use /team to view the roster.",
		cwd,
	))

	// Layer 1: skill name + description list (~100 tokens/skill)
	if desc := loader.Descriptions(); desc != "" {
		systemText.WriteString(fmt.Sprintf("\n\nSkills loaded from ~/%s/skills/ and "+
			"<project>/%s/skills/. The list below is COMPLETE — only use "+
			"load_skill for names listed here. Do NOT guess or search the filesystem for skills.\n",
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

	// --- Session persistence ---
	sessionStore := NewSessionStore(DataDir())
	// Generate a session ID eagerly so saveSession can work from the
	// very first turn. If Resume() below restores a previous session,
	// the ID is overwritten by the restored session's identifier.
	sessionID := GenerateSessionID()

	var compactTrigger func() (string, error)

	ag.Runner = NewRunner(ag, compactCfg, bgMgr, teamMgr, sessionStore, func(fn func() (string, error)) {
		compactTrigger = fn
	}, cwd)

	// Conditionally resume session based on the --resume flag.
	// No flag → fresh start (sessionID stays as the new generated ID).
	// "latest" → resume most recent; "__select__" → interactive picker.
	ag.Runner.sessionID = sessionID
	if resumeID == "latest" {
		if id, err := sessionStore.LatestSessionID(); err == nil && id != "" {
			resumeID = id
		} else {
			resumeID = ""
		}
	} else if resumeID == "__select__" {
		resumeID = interactiveSessionPicker(sessionStore)
	}
	if resumeID != "" {
		if ag.Runner.ResumeSession(resumeID) {
			logger.Info("Resumed session",
				zap.String("session_id", ag.Runner.sessionID),
				zap.Int("messages", len(ag.Runner.messages)))
		}
	}

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
		&team.SpawnTool{Mgr: teamMgr},
		&team.SendTool{Mgr: teamMgr, SenderName: "lead"},
		&team.TeamInboxTool{Mgr: teamMgr, ReaderName: "lead"},
		&team.ShutdownRequestTool{Mgr: teamMgr},
		&team.PlanResponseTool{Mgr: teamMgr},
		tools.NewCompactTool(func() (string, error) {
			if compactTrigger == nil {
				return "", fmt.Errorf("compact not initialized")
			}
			return compactTrigger()
		}),
	)

	return ag, nil
}

// interactiveSessionPicker lists saved sessions and prompts the user to
// select one interactively via stdin. Returns the chosen session ID, or
// empty string if the user cancels (Enter on empty input) or no sessions
// are available.
func interactiveSessionPicker(store *SessionStore) string {
	sessions, err := store.ListSessions()
	if err != nil || len(sessions) == 0 {
		fmt.Println("\nNo saved sessions. Starting fresh.")
		return ""
	}

	fmt.Println("\nAvailable sessions:")
	fmt.Printf("  %3s  %-30s %-25s %5s  %s\n", "#", "Session ID", "Workdir", "Msgs", "Last Updated")
	fmt.Printf("  %s\n", strings.Repeat("-", 90))
	for i, s := range sessions {
		workdir := s.Workdir
		if len(workdir) > 24 {
			workdir = "..." + workdir[len(workdir)-21:]
		}
		var updated string
		if s.UpdatedAt > 0 {
			updated = time.Unix(s.UpdatedAt, 0).Format("2006-01-02 15:04")
		} else {
			updated = "-"
		}
		fmt.Printf("  %3d  %-30s %-25s %5d  %s\n", i+1, s.SessionID, workdir, s.MessageCount, updated)
	}

	fmt.Print("\nSelect session (number or ID, Enter=cancel): ")
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return ""
	}
	input := strings.TrimSpace(scanner.Text())
	if input == "" {
		return ""
	}

	// Try as number first (1-based index).
	if n, err := strconv.Atoi(input); err == nil && n >= 1 && n <= len(sessions) {
		return sessions[n-1].SessionID
	}

	// Treat as session ID (allow partial prefix match for convenience).
	for _, s := range sessions {
		if strings.HasPrefix(s.SessionID, input) {
			return s.SessionID
		}
	}

	fmt.Printf("No session matching %q. Starting fresh.\n", input)
	return ""
}
