package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
)

// ──────────────────────────────────────────────
// Session persistence: auto-save conversation
// history to disk, resume on restart.
// ──────────────────────────────────────────────

const (
	// sessionDirName is the subdirectory under .task-agent/ for session data.
	sessionDirName = "sessions"

	// messagesFile is the JSONL file holding the message history.
	messagesFile = "messages.jsonl"
	// metaFile is the JSON file holding session metadata.
	metaFile = "session.json"
	// latestPointer is the file that holds the most recent session ID.
	latestPointer = "latest.session"
)

// SessionMetadata holds info about a saved session.
type SessionMetadata struct {
	SessionID    string `json:"session_id"`
	Model        string `json:"model"`
	CreatedAt    int64  `json:"created_at"`
	UpdatedAt    int64  `json:"updated_at"`
	MessageCount int    `json:"message_count"`
	Compacted    bool   `json:"compacted,omitempty"`
}

// SessionStore persists conversation history to disk.
// All public methods are safe for concurrent use.
type SessionStore struct {
	baseDir string // <cwd>/.task-agent/sessions/
	mu      sync.Mutex
}

// NewSessionStore creates a SessionStore rooted at baseDir/sessions/.
func NewSessionStore(baseDir string) *SessionStore {
	return &SessionStore{
		baseDir: filepath.Join(baseDir, sessionDirName),
	}
}

// ─── Path helpers ───────────────────────────────────────────────────────────

func (s *SessionStore) sessionDir(sessionID string) string {
	return filepath.Join(s.baseDir, sessionID)
}

func (s *SessionStore) messagesPath(sessionID string) string {
	return filepath.Join(s.sessionDir(sessionID), messagesFile)
}

func (s *SessionStore) metaPath(sessionID string) string {
	return filepath.Join(s.sessionDir(sessionID), metaFile)
}

func (s *SessionStore) latestPointerPath() string {
	return filepath.Join(s.baseDir, latestPointer)
}

// ─── Save ───────────────────────────────────────────────────────────────────

// SaveMessages persists the full message list and metadata for a session.
// It overwrites the messages.jsonl entirely (not append) so the file always
// reflects the canonical message state even after compaction.
func (s *SessionStore) SaveMessages(sessionID string, messages []anthropic.BetaMessageParam, meta *SessionMetadata) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	dir := s.sessionDir(sessionID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("mkdir session dir: %w", err)
	}

	// Write messages as JSONL (overwrite).
	msgPath := s.messagesPath(sessionID)
	f, err := os.Create(msgPath)
	if err != nil {
		return fmt.Errorf("create messages file: %w", err)
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	for i, msg := range messages {
		if err := enc.Encode(msg); err != nil {
			return fmt.Errorf("encode message %d: %w", i, err)
		}
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("sync messages: %w", err)
	}

	// Write metadata.
	if meta != nil {
		meta.MessageCount = len(messages)
		meta.UpdatedAt = time.Now().Unix()
		if err := s.writeMetaLocked(sessionID, meta); err != nil {
			return err
		}
	}

	// Update latest pointer.
	if err := os.WriteFile(s.latestPointerPath(), []byte(sessionID), 0600); err != nil {
		return fmt.Errorf("write latest pointer: %w", err)
	}

	return nil
}

// SaveNoLock is like SaveMessages but skips the mutex.
// Used inside the Runner loop during auto-compact where the mutex
// is already held or unnecessary.
func (s *SessionStore) SaveNoLock(sessionID string, messages []anthropic.BetaMessageParam, meta *SessionMetadata) error {
	dir := s.sessionDir(sessionID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("mkdir session dir: %w", err)
	}

	msgPath := s.messagesPath(sessionID)
	f, err := os.Create(msgPath)
	if err != nil {
		return fmt.Errorf("create messages file: %w", err)
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	for i, msg := range messages {
		if err := enc.Encode(msg); err != nil {
			return fmt.Errorf("encode message %d: %w", i, err)
		}
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("sync messages: %w", err)
	}

	if meta != nil {
		meta.MessageCount = len(messages)
		meta.UpdatedAt = time.Now().Unix()
		if err := saveJSON(s.metaPath(sessionID), meta); err != nil {
			return err
		}
	}

	if err := os.WriteFile(s.latestPointerPath(), []byte(sessionID), 0600); err != nil {
		return fmt.Errorf("write latest pointer: %w", err)
	}

	return nil
}

// writeMetaLocked persists session metadata. Caller must hold s.mu.
func (s *SessionStore) writeMetaLocked(sessionID string, meta *SessionMetadata) error {
	return saveJSON(s.metaPath(sessionID), meta)
}

// ─── Load ───────────────────────────────────────────────────────────────────

// LoadMessages reads all messages from a session's JSONL file.
// Returns nil, nil if the session doesn't exist.
func (s *SessionStore) LoadMessages(sessionID string) ([]anthropic.BetaMessageParam, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	path := s.messagesPath(sessionID)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("open messages file: %w", err)
	}
	defer f.Close()

	var messages []anthropic.BetaMessageParam
	dec := json.NewDecoder(f)
	for {
		var msg anthropic.BetaMessageParam
		if err := dec.Decode(&msg); err != nil {
			break
		}
		messages = append(messages, msg)
	}

	return messages, nil
}

// LoadMetadata reads session metadata. Returns nil if not found.
func (s *SessionStore) LoadMetadata(sessionID string) (*SessionMetadata, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.readMetaLocked(sessionID)
}

// readMetaLocked reads session metadata. Caller must hold s.mu.
func (s *SessionStore) readMetaLocked(sessionID string) (*SessionMetadata, error) {
	var meta SessionMetadata
	if err := readJSON(s.metaPath(sessionID), &meta); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return &meta, nil
}

// ─── Session management ────────────────────────────────────────────────────

// LatestSessionID returns the most recent session ID by reading the
// latest pointer file, falling back to directory scan if absent.
func (s *SessionStore) LatestSessionID() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Try pointer file first (fast path).
	if data, err := os.ReadFile(s.latestPointerPath()); err == nil {
		id := strings.TrimSpace(string(data))
		if id != "" {
			// Verify the session directory still exists.
			if _, err := os.Stat(s.sessionDir(id)); err == nil {
				return id, nil
			}
		}
	}

	// Fallback: scan directories, sorted by name (timestamp prefix).
	entries, err := os.ReadDir(s.baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("read sessions dir: %w", err)
	}

	var ids []string
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			// Verify the directory has a messages.jsonl.
			if _, err := os.Stat(s.messagesPath(e.Name())); err == nil {
				ids = append(ids, e.Name())
			}
		}
	}

	if len(ids) == 0 {
		return "", nil
	}

	sort.Strings(ids)
	return ids[len(ids)-1], nil
}

// ListSessions returns all sessions sorted newest first.
func (s *SessionStore) ListSessions() ([]SessionMetadata, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entries, err := os.ReadDir(s.baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read sessions dir: %w", err)
	}

	var sessions []SessionMetadata
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		meta, err := s.readMetaLocked(e.Name())
		if err != nil || meta == nil {
			// Generate basic metadata from directory name.
			meta = &SessionMetadata{SessionID: e.Name()}
		}
		// Double-check message file exists.
		if _, err := os.Stat(s.messagesPath(e.Name())); err == nil {
			sessions = append(sessions, *meta)
		}
	}

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].SessionID > sessions[j].SessionID // newest first
	})

	return sessions, nil
}

// DeleteSession removes a session directory entirely.
func (s *SessionStore) DeleteSession(sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	dir := s.sessionDir(sessionID)
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("remove session dir: %w", err)
	}

	// If this was the latest session, clear the pointer.
	if data, err := os.ReadFile(s.latestPointerPath()); err == nil {
		if strings.TrimSpace(string(data)) == sessionID {
			os.Remove(s.latestPointerPath())
		}
	}

	return nil
}

// ─── Session ID generation ──────────────────────────────────────────────────

// GenerateSessionID creates a timestamp-first session ID consistent
// with the existing task-list format: YYYYMMDD-HHMMSS-hostname-pid.
func GenerateSessionID() string {
	host, _ := os.Hostname()
	return fmt.Sprintf("%s-%s-%d",
		time.Now().Format("20060102-150405"), host, os.Getpid())
}

// ─── JSON helpers ───────────────────────────────────────────────────────────

func saveJSON(path string, v any) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

func readJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}
