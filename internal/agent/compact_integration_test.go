package agent

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// newAssistantTextContent creates a BetaContentBlockUnion of type "text"
// suitable for mock assistant responses.
func newAssistantTextContent(text string) anthropic.BetaContentBlockUnion {
	return anthropic.BetaContentBlockUnion{
		Type: "text",
		Text: text,
	}
}

// respondJSON is a helper that writes v as JSON to w.
func respondJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func TestCompactSetsCompactedFlag(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		respondJSON(w, anthropic.BetaMessage{
			ID:      "msg_test",
			Model:   "test",
			Content: []anthropic.BetaContentBlockUnion{newAssistantTextContent("Test summary of conversation.")},
			Usage:   anthropic.BetaUsage{InputTokens: 100, OutputTokens: 10},
		})
	}))
	defer srv.Close()

	ag := &Agent{
		client: ptr(anthropic.NewClient(
			option.WithAPIKey("test-key"),
			option.WithBaseURL(srv.URL),
		)),
		model: "claude-sonnet-4-6",
	}

	cfg := DefaultCompactionConfig()
	cfg.TranscriptDir = filepath.Join(t.TempDir(), "transcripts")

	r := NewRunner(ag, cfg, func(fn func() (string, error)) {})

	r.messages = []anthropic.BetaMessageParam{
		anthropic.NewBetaUserMessage(
			anthropic.BetaContentBlockParamUnion{
				OfText: &anthropic.BetaTextBlockParam{Text: "Hello"},
			},
		),
		anthropic.NewBetaUserMessage(
			anthropic.BetaContentBlockParamUnion{
				OfText: &anthropic.BetaTextBlockParam{Text: "Hi! How can I help?"},
			},
		),
	}

	result, err := r.compact()
	if err != nil {
		t.Fatalf("compact() failed: %v", err)
	}
	if result == "" {
		t.Error("compact() returned empty result")
	}

	if !r.compacted {
		t.Error("r.compacted should be true after successful compact()")
	}

	if len(r.messages) != 1 {
		t.Errorf("expected 1 message after compact, got %d", len(r.messages))
	}
	if r.messages[0].Role != "user" {
		t.Errorf("expected user role, got %s", r.messages[0].Role)
	}
	text := r.messages[0].Content[0].OfText.Text
	if text == "" {
		t.Error("compressed message text is empty")
	}
}

func TestCompactDoesNotSetFlagOnFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	ag := &Agent{
		client: ptr(anthropic.NewClient(
			option.WithAPIKey("test-key"),
			option.WithBaseURL(srv.URL),
		)),
		model: "claude-sonnet-4-6",
	}

	cfg := DefaultCompactionConfig()
	cfg.TranscriptDir = filepath.Join(t.TempDir(), "transcripts")

	r := NewRunner(ag, cfg, func(fn func() (string, error)) {})

	r.messages = []anthropic.BetaMessageParam{
		anthropic.NewBetaUserMessage(
			anthropic.BetaContentBlockParamUnion{
				OfText: &anthropic.BetaTextBlockParam{Text: "Hi"},
			},
		),
	}

	_, err := r.compact()
	if err == nil {
		t.Fatal("compact() should have failed with 500 error")
	}

	if r.compacted {
		t.Error("r.compacted should be false after failed compact()")
	}

	if len(r.messages) != 1 {
		t.Errorf("messages should be unchanged after failed compact, got %d messages", len(r.messages))
	}
}

func TestCompactSavesTranscript(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		respondJSON(w, anthropic.BetaMessage{
			ID:      "msg_test",
			Model:   "test",
			Content: []anthropic.BetaContentBlockUnion{newAssistantTextContent("Summary text.")},
			Usage:   anthropic.BetaUsage{InputTokens: 10, OutputTokens: 3},
		})
	}))
	defer srv.Close()

	ag := &Agent{
		client: ptr(anthropic.NewClient(
			option.WithAPIKey("test-key"),
			option.WithBaseURL(srv.URL),
		)),
		model: "claude-sonnet-4-6",
	}

	transcriptDir := filepath.Join(t.TempDir(), "transcripts")
	cfg := DefaultCompactionConfig()
	cfg.TranscriptDir = transcriptDir

	r := NewRunner(ag, cfg, func(fn func() (string, error)) {})

	r.messages = []anthropic.BetaMessageParam{
		anthropic.NewBetaUserMessage(
			anthropic.BetaContentBlockParamUnion{
				OfText: &anthropic.BetaTextBlockParam{Text: "Hello world"},
			},
		),
	}

	_, err := r.compact()
	if err != nil {
		t.Fatalf("compact() failed: %v", err)
	}

	entries, err := os.ReadDir(transcriptDir)
	if err != nil {
		t.Fatalf("read transcript dir: %v", err)
	}
	if len(entries) == 0 {
		t.Error("no transcript file was created")
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".jsonl" {
			t.Errorf("unexpected transcript file extension: %s", e.Name())
		}
	}
}

func ptr[T any](v T) *T { return &v }
