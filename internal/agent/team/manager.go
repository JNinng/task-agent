package team

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
)

// TeammateManager maintains the team roster, spawns/shuts down teammates,
// and routes messages between team members through the MessageBus.
// All public methods are safe for concurrent use.
type TeammateManager struct {
	dir     string // team/
	config  Config
	bus     *MessageBus
	client  *anthropic.Client
	model   anthropic.Model
	workdir string
	mu      sync.Mutex
	loops   map[string]*teammateLoop // name -> running loop
}

// NewManager initializes the team directory and loads the existing roster
// from config.json. If no config exists a default one is created with the
// given lead name.
func NewManager(client *anthropic.Client, model anthropic.Model, workdir, teamDir, leadName string) (*TeammateManager, error) {
	if err := os.MkdirAll(teamDir, 0755); err != nil {
		return nil, fmt.Errorf("team manager: %w", err)
	}

	bus, err := NewMessageBus(teamDir)
	if err != nil {
		return nil, err
	}

	m := &TeammateManager{
		dir:     teamDir,
		bus:     bus,
		client:  client,
		model:   model,
		workdir: workdir,
		loops:   make(map[string]*teammateLoop),
	}

	m.config = m.loadConfig(leadName)
	return m, nil
}

// ── Public API ──────────────────────────────────────────────────────

// Spawn creates a teammate and starts its agent loop in a goroutine.
// If a teammate with the same name exists from a previous session (status
// shutdown, no goroutine), it is replaced. Running teammates cannot be
// duplicated.
func (m *TeammateManager) Spawn(name, role, prompt string) (Teammate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check for existing running teammate.
	if _, running := m.loops[name]; running {
		return Teammate{}, fmt.Errorf("spawn: teammate '%s' is already running", name)
	}

	if name == m.config.Lead {
		return Teammate{}, fmt.Errorf("spawn: '%s' is the lead name, choose another", name)
	}
	if name == "all" || name == "lead" {
		return Teammate{}, fmt.Errorf("spawn: '%s' is a reserved name", name)
	}

	// Remove any stale entry from a previous session (goroutine is gone,
	// status is shutdown after loadConfig).
	for i, tm := range m.config.Members {
		if tm.Name == name {
			m.config.Members = append(m.config.Members[:i], m.config.Members[i+1:]...)
			break
		}
	}

	tm := Teammate{
		Name:   name,
		Role:   role,
		Status: StatusWorking,
		Model:  string(m.model),
	}
	m.config.Members = append(m.config.Members, tm)
	m.saveConfig()

	loop := newTeammateLoop(name, role, m.config.Lead, m.client, m.model, m.workdir, m)
	m.loops[name] = loop

	go loop.run(prompt)

	return tm, nil
}

// Shutdown stops a teammate's loop and removes it from the roster.
func (m *TeammateManager) Shutdown(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	loop, ok := m.loops[name]
	if !ok {
		return fmt.Errorf("shutdown: teammate '%s' not running", name)
	}

	loop.shutdown()
	delete(m.loops, name)

	// Update roster.
	for i, tm := range m.config.Members {
		if tm.Name == name {
			m.config.Members[i].Status = StatusShutdown
			break
		}
	}
	m.saveConfig()
	return nil
}

// ShutdownAll stops all running teammates.
func (m *TeammateManager) ShutdownAll() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for name, loop := range m.loops {
		loop.shutdown()
		delete(m.loops, name)
	}

	for i := range m.config.Members {
		m.config.Members[i].Status = StatusShutdown
	}
	m.saveConfig()
}

// Send routes a message from sender to a recipient. If to is "all",
// the message is broadcast to every teammate (including idle ones).
func (m *TeammateManager) Send(sender, to, content, msgType string) error {
	if msgType == "" {
		msgType = "message"
	}

	msg := Message{
		Type:      msgType,
		From:      sender,
		Content:   content,
		Timestamp: time.Now().Unix(),
	}

	if to == "all" {
		return m.broadcast(sender, content, msgType)
	}

	if err := m.bus.Send(to, msg); err != nil {
		return fmt.Errorf("send: %w", err)
	}

	// Check if the recipient is a known teammate and whether they are alive.
	m.mu.Lock()
	loop, loopOk := m.loops[to]
	var knownButDead bool
	if !loopOk {
		for _, tm := range m.config.Members {
			if tm.Name == to {
				knownButDead = true
				break
			}
		}
	}
	m.mu.Unlock()

	if loopOk {
		loop.wake()
	} else if knownButDead {
		return fmt.Errorf("send: teammate '%s' is not running (stale from previous session). Re-spawn them first with team_spawn.", to)
	}
	// If not in loops and not in config, it could be the lead or an external
	// recipient — the message is already in their inbox file, that's fine.

	return nil
}

// ReadInbox reads and drains the named agent's inbox.
func (m *TeammateManager) ReadInbox(name string) ([]Message, error) {
	return m.bus.ReadInbox(name)
}

// Roster returns a snapshot of the current team roster.
func (m *TeammateManager) Roster() []Teammate {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]Teammate, len(m.config.Members))
	copy(out, m.config.Members)
	return out
}

// LeadName returns the lead agent's name.
func (m *TeammateManager) LeadName() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.config.Lead
}

// ── Internal helpers ────────────────────────────────────────────────

// broadcast sends a message to every teammate in the roster.
func (m *TeammateManager) broadcast(sender, content, msgType string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	msg := Message{
		Type:      msgType,
		From:      sender,
		Content:   content,
		Timestamp: time.Now().Unix(),
	}

	for _, tm := range m.config.Members {
		if tm.Status == StatusShutdown {
			continue
		}
		if err := m.bus.Send(tm.Name, msg); err != nil {
			return fmt.Errorf("broadcast to %s: %w", tm.Name, err)
		}
		// Wake each recipient.
		if loop, ok := m.loops[tm.Name]; ok {
			loop.wake()
		}
	}
	return nil
}

// setStatus updates a teammate's status in the roster config.
// Must be called with m.mu held or from a context where concurrent
// access is safe (it acquires the lock internally).
func (m *TeammateManager) setStatus(name string, status Status) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for i, tm := range m.config.Members {
		if tm.Name == name {
			m.config.Members[i].Status = status
			m.saveConfig()
			return
		}
	}
}

// wakeTeammate signals a teammate's wake channel. Safe to call when
// the teammate might not exist or might have shut down.
func (m *TeammateManager) wakeTeammate(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if loop, ok := m.loops[name]; ok {
		loop.wake()
	}
}

// ── Config persistence ──────────────────────────────────────────────

func (m *TeammateManager) configPath() string {
	return filepath.Join(m.dir, "config.json")
}

func (m *TeammateManager) loadConfig(leadName string) Config {
	path := m.configPath()
	data, err := os.ReadFile(path)
	if err != nil {
		// No config yet — create default.
		return Config{Lead: leadName}
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{Lead: leadName}
	}
	if cfg.Lead == "" {
		cfg.Lead = leadName
	}

	// All teammates from a previous session have no running goroutines.
	// Mark them as shutdown so they don't appear alive, and allow
	// re-spawning with the same name.
	for i := range cfg.Members {
		cfg.Members[i].Status = StatusShutdown
	}
	if len(cfg.Members) > 0 {
		data, _ := json.MarshalIndent(cfg, "", "  ")
		_ = os.WriteFile(path, data, 0644)
	}

	return cfg
}

func (m *TeammateManager) saveConfig() {
	data, err := json.MarshalIndent(m.config, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(m.configPath(), data, 0644)
}
