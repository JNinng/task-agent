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

	// 协议请求追踪器
	shutdownRequests map[string]*ShutdownRequest // request_id → shutdown request
	planRequests     map[string]*PlanRequest     // request_id → plan request
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
		dir:              teamDir,
		bus:              bus,
		client:           client,
		model:            model,
		workdir:          workdir,
		loops:            make(map[string]*teammateLoop),
		shutdownRequests: make(map[string]*ShutdownRequest),
		planRequests:     make(map[string]*PlanRequest),
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

// ── Protocol: Shutdown Request ────────────────────────────────────────

// RequestShutdown 向队友发起优雅关机请求。返回 request_id 供后续追踪。
// 与直接调用 Shutdown() 不同，此方法通过消息总线发送 shutdown_request，
// 由队友的 LLM 决定批准或拒绝。
func (m *TeammateManager) RequestShutdown(teammate, reason string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 检查目标是否存在
	found := false
	for _, tm := range m.config.Members {
		if tm.Name == teammate && tm.Status != StatusShutdown {
			found = true
			break
		}
	}
	if !found {
		return "", fmt.Errorf("shutdown_request: 队友 '%s' 未在运行", teammate)
	}

	req := m.trackShutdownRequest(teammate, reason)

	msg := Message{
		Type:      "shutdown_request",
		From:      m.config.Lead,
		Content:   reason,
		RequestID: req.RequestID,
		Timestamp: time.Now().Unix(),
	}
	if err := m.bus.Send(teammate, msg); err != nil {
		return "", fmt.Errorf("shutdown_request: %w", err)
	}

	// 唤醒目标队友
	if loop, ok := m.loops[teammate]; ok {
		loop.wake()
	}

	return req.RequestID, nil
}

// ResolveShutdownRequest 队友调用此方法来响应关机请求。
// approve=true 表示同意关机，会实际执行 Shutdown()。
// approve=false 表示拒绝关机，队友继续工作。
func (m *TeammateManager) ResolveShutdownRequest(requestID string, approve bool, reason string) error {
	m.mu.Lock()

	if err := m.resolveShutdownRequest(requestID, approve, reason); err != nil {
		m.mu.Unlock()
		return err
	}

	req := m.shutdownRequests[requestID]

	// 发送响应给 lead
	resp := Message{
		Type:      "shutdown_response",
		From:      req.Target,
		Content:   reason,
		RequestID: requestID,
		Approve:   &approve,
		Timestamp: time.Now().Unix(),
	}
	m.mu.Unlock()

	// 不持锁发送（避免死锁）
	if err := m.bus.Send(m.config.Lead, resp); err != nil {
		return fmt.Errorf("shutdown_response: %w", err)
	}

	if approve {
		// 优雅关机 — 复用现有的 Shutdown 方法
		return m.Shutdown(req.Target)
	}

	return nil
}

// ── Protocol: Plan Approval ──────────────────────────────────────────

// SubmitPlan 队友提交计划给 lead 审批。返回 request_id 供后续追踪。
func (m *TeammateManager) SubmitPlan(from, plan string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	req := m.trackPlanRequest(from, plan)

	msg := Message{
		Type:      "plan_request",
		From:      from,
		Content:   plan,
		RequestID: req.RequestID,
		Timestamp: time.Now().Unix(),
	}
	if err := m.bus.Send(m.config.Lead, msg); err != nil {
		return "", fmt.Errorf("plan_request: %w", err)
	}

	return req.RequestID, nil
}

// ResolvePlanRequest lead 调用此方法来批准或拒绝队友的计划。
func (m *TeammateManager) ResolvePlanRequest(requestID string, approve bool, feedback string) error {
	m.mu.Lock()

	if err := m.resolvePlanRequest(requestID, approve, feedback); err != nil {
		m.mu.Unlock()
		return err
	}

	req := m.planRequests[requestID]

	resp := Message{
		Type:      "plan_response",
		From:      m.config.Lead,
		Content:   feedback,
		RequestID: requestID,
		Approve:   &approve,
		Timestamp: time.Now().Unix(),
	}
	m.mu.Unlock()

	// 不持锁发送（避免死锁）
	if err := m.bus.Send(req.From, resp); err != nil {
		return fmt.Errorf("plan_response: %w", err)
	}

	// 唤醒等待审批结果的队友
	m.wakeTeammate(req.From)

	return nil
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
