package agent

import (
	"os"
	"path/filepath"
)

// ──────────────────────────────────────────────
// Centralized agent data directory constants
// All code MUST use these instead of hardcoding
// ".task-agent" or individual home-dir lookups.
// ──────────────────────────────────────────────

const (
	// DirAgent is the base directory name for all agent runtime data
	// (skills, transcripts, memctx, task graph persistence).
	DirAgent = ".task-agent"
)

var (
	// HomeDir is the user's home directory, resolved once at startup.
	// All paths relative to the home directory derive from this value.
	HomeDir string
)

func init() {
	// Windows → Unix fallback chain
	HomeDir = os.Getenv("USERPROFILE")
	if HomeDir == "" {
		HomeDir = os.Getenv("HOME")
	}
}

// ClaudeSettingsPath returns the path to the Claude settings file
// (typically ~/.claude/settings.json).
func ClaudeSettingsPath() string {
	return filepath.Join(HomeDir, ".claude", "settings.json")
}

// GlobalSkillsDir returns the global skills directory
// (~/.task-agent/skills).
func GlobalSkillsDir() string {
	return filepath.Join(HomeDir, DirAgent, "skills")
}

// DataDir returns the global agent data directory
// (~/.task-agent) used for task-graph persistence and
// other session-independent state.
func DataDir() string {
	return filepath.Join(HomeDir, DirAgent)
}

// ProjectSkillsDir returns the project-level skills directory
// (<cwd>/.task-agent/skills).
func ProjectSkillsDir(cwd string) string {
	return filepath.Join(cwd, DirAgent, "skills")
}

// TranscriptsDir returns the project-level transcripts directory
// (<cwd>/.task-agent/transcripts).
func TranscriptsDir(cwd string) string {
	return filepath.Join(cwd, DirAgent, "transcripts")
}

// MemctxDir returns the project-level memctx dump directory
// (<cwd>/.task-agent/memctx).
func MemctxDir(cwd string) string {
	return filepath.Join(cwd, DirAgent, "memctx")
}

// TeamDir returns the global agent team directory
// (~/.task-agent/.team) used for team roster, inboxes, and
// teammate persistence.
func TeamDir(dataDir string) string {
	return filepath.Join(dataDir, ".team")
}
