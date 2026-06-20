package team

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// MessageBus manages append-only JSONL inboxes for inter-agent communication.
// Each teammate (and the lead) has a {name}.jsonl file under inbox/.
// Messages are appended by Send and atomically drained by ReadInbox.
type MessageBus struct {
	dir string // team/inbox/
	mu  sync.Mutex
}

// NewMessageBus creates a MessageBus rooted at the given team directory.
// The inbox/ subdirectory is created if it does not exist.
func NewMessageBus(teamDir string) (*MessageBus, error) {
	dir := filepath.Join(teamDir, "inbox")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("message bus: %w", err)
	}
	return &MessageBus{dir: dir}, nil
}

// Send appends a message to the recipient's JSONL inbox file.
// It is safe for concurrent use.
func (b *MessageBus) Send(to string, msg Message) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	path := filepath.Join(b.dir, to+".jsonl")
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("send: marshal: %w", err)
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("send: %w", err)
	}
	defer f.Close()

	if _, err := f.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("send: write: %w", err)
	}
	return nil
}

// ReadInbox reads and drains the named agent's inbox.
// Returns an empty slice (not nil) when the inbox is empty or does not exist.
// It is safe for concurrent use.
func (b *MessageBus) ReadInbox(name string) ([]Message, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	path := filepath.Join(b.dir, name+".jsonl")

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []Message{}, nil
		}
		return nil, fmt.Errorf("read_inbox: %w", err)
	}

	// Drain immediately by truncating the file.
	if err := os.WriteFile(path, nil, 0644); err != nil {
		return nil, fmt.Errorf("read_inbox: drain: %w", err)
	}

	if len(data) == 0 {
		return []Message{}, nil
	}

	lines := splitLines(string(data))
	msgs := make([]Message, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			continue
		}
		var msg Message
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			// Skip malformed lines — don't lose other messages.
			continue
		}
		msgs = append(msgs, msg)
	}

	return msgs, nil
}

// splitLines splits text by newlines, handling both \n and \r\n.
func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			// Trim optional \r before \n.
			end := i
			if end > start && s[end-1] == '\r' {
				end--
			}
			if end > start {
				lines = append(lines, s[start:end])
			}
			start = i + 1
		}
	}
	// Remaining text after last newline.
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}
