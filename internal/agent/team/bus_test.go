package team

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMessageBusSendAndRead(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".team")
	bus, err := NewMessageBus(dir)
	if err != nil {
		t.Fatalf("NewMessageBus: %v", err)
	}

	msg := Message{
		Type:      "message",
		From:      "lead",
		Content:   "hello alice",
		Timestamp: time.Now().Unix(),
	}

	if err := bus.Send("alice", msg); err != nil {
		t.Fatalf("Send: %v", err)
	}

	msgs, err := bus.ReadInbox("alice")
	if err != nil {
		t.Fatalf("ReadInbox: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].From != "lead" {
		t.Errorf("expected from=lead, got %s", msgs[0].From)
	}
	if msgs[0].Content != "hello alice" {
		t.Errorf("expected content='hello alice', got %s", msgs[0].Content)
	}

	// Second read should be empty (drained).
	msgs2, err := bus.ReadInbox("alice")
	if err != nil {
		t.Fatalf("ReadInbox 2: %v", err)
	}
	if len(msgs2) != 0 {
		t.Errorf("expected empty inbox after drain, got %d messages", len(msgs2))
	}
}

func TestMessageBusMultipleMessages(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".team")
	bus, err := NewMessageBus(dir)
	if err != nil {
		t.Fatalf("NewMessageBus: %v", err)
	}

	for i := 0; i < 5; i++ {
		if err := bus.Send("bob", Message{
			Type:      "message",
			From:      "alice",
			Content:   "msg",
			Timestamp: time.Now().Unix(),
		}); err != nil {
			t.Fatalf("Send %d: %v", i, err)
		}
	}

	msgs, err := bus.ReadInbox("bob")
	if err != nil {
		t.Fatalf("ReadInbox: %v", err)
	}
	if len(msgs) != 5 {
		t.Errorf("expected 5 messages, got %d", len(msgs))
	}
}

func TestMessageBusEmptyInbox(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".team")
	bus, err := NewMessageBus(dir)
	if err != nil {
		t.Fatalf("NewMessageBus: %v", err)
	}

	msgs, err := bus.ReadInbox("nonexistent")
	if err != nil {
		t.Fatalf("ReadInbox: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("expected empty, got %d messages", len(msgs))
	}
}

func TestMessageBusSeparateInboxes(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".team")
	bus, err := NewMessageBus(dir)
	if err != nil {
		t.Fatalf("NewMessageBus: %v", err)
	}

	bus.Send("alice", Message{Type: "message", From: "lead", Content: "a", Timestamp: 1})
	bus.Send("bob", Message{Type: "message", From: "lead", Content: "b", Timestamp: 2})

	aliceMsgs, _ := bus.ReadInbox("alice")
	bobMsgs, _ := bus.ReadInbox("bob")

	if len(aliceMsgs) != 1 || aliceMsgs[0].Content != "a" {
		t.Errorf("alice: expected [a], got %v", aliceMsgs)
	}
	if len(bobMsgs) != 1 || bobMsgs[0].Content != "b" {
		t.Errorf("bob: expected [b], got %v", bobMsgs)
	}
}

func TestMessageBusInboxFileCreated(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".team")
	bus, err := NewMessageBus(dir)
	if err != nil {
		t.Fatalf("NewMessageBus: %v", err)
	}

	bus.Send("charlie", Message{Type: "message", From: "lead", Content: "test", Timestamp: 1})

	path := filepath.Join(dir, "inbox", "charlie.jsonl")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		// After drain, file still exists but is empty.
		// Actually after first ReadInbox it will be empty. Let's check
		// after send but before read.
	}

	// File should exist after send.
	bus.Send("dave", Message{Type: "message", From: "lead", Content: "x", Timestamp: 1})
	inboxPath := filepath.Join(dir, "inbox", "dave.jsonl")
	info, err := os.Stat(inboxPath)
	if err != nil {
		t.Fatalf("inbox file should exist: %v", err)
	}
	if info.Size() == 0 {
		t.Error("inbox file should not be empty after send")
	}
}
