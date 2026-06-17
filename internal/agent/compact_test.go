package agent

import (
	"os"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
)

// makeToolResultBlock creates a BetaContentBlockParamUnion containing a tool_result
// with the given text content.
func makeToolResultBlock(text string) anthropic.BetaContentBlockParamUnion {
	return anthropic.BetaContentBlockParamUnion{
		OfToolResult: &anthropic.BetaToolResultBlockParam{
			ToolUseID: "test",
			Content: []anthropic.BetaToolResultBlockParamContentUnion{
				{OfText: &anthropic.BetaTextBlockParam{Text: text}},
			},
		},
	}
}

func TestMicroCompact_RemovesOld(t *testing.T) {
	// 5 tool_results, keep=3 -> oldest 2 should be replaced
	msgs := []anthropic.BetaMessageParam{
		{Role: "user", Content: []anthropic.BetaContentBlockParamUnion{
			makeToolResultBlock("result A " + string(make([]byte, 200))),
		}},
		{Role: "user", Content: []anthropic.BetaContentBlockParamUnion{
			makeToolResultBlock("result B " + string(make([]byte, 200))),
		}},
		{Role: "user", Content: []anthropic.BetaContentBlockParamUnion{
			makeToolResultBlock("result C " + string(make([]byte, 200))),
		}},
		{Role: "user", Content: []anthropic.BetaContentBlockParamUnion{
			makeToolResultBlock("result D " + string(make([]byte, 200))),
		}},
		{Role: "user", Content: []anthropic.BetaContentBlockParamUnion{
			makeToolResultBlock("result E " + string(make([]byte, 200))),
		}},
	}

	microCompact(msgs, 3)

	// First 2 should be replaced
	for i := 0; i < 2; i++ {
		got := msgs[i].Content[0].OfToolResult.Content[0].OfText.Text
		if got != "[Previous tool result compacted]" {
			t.Errorf("msg[%d] expected placeholder, got: %s", i, got)
		}
	}
	// Last 3 should be preserved
	for i := 2; i < 5; i++ {
		got := msgs[i].Content[0].OfToolResult.Content[0].OfText.Text
		if got == "[Previous tool result compacted]" {
			t.Errorf("msg[%d] should not have been replaced", i)
		}
	}
}

func TestMicroCompact_UnderLimit(t *testing.T) {
	// 2 tool_results, keep=3 -> no change
	msgs := []anthropic.BetaMessageParam{
		{Role: "user", Content: []anthropic.BetaContentBlockParamUnion{
			makeToolResultBlock("short"),
		}},
		{Role: "user", Content: []anthropic.BetaContentBlockParamUnion{
			makeToolResultBlock("still short"),
		}},
	}

	microCompact(msgs, 3)

	if msgs[0].Content[0].OfToolResult.Content[0].OfText.Text != "short" {
		t.Error("msg[0] should be unchanged")
	}
}

func TestMicroCompact_ShortContent(t *testing.T) {
	// Messages with <= 100 chars should not be replaced even if old
	msgs := []anthropic.BetaMessageParam{
		{Role: "user", Content: []anthropic.BetaContentBlockParamUnion{
			makeToolResultBlock("short error: not found"),
		}},
		{Role: "user", Content: []anthropic.BetaContentBlockParamUnion{
			makeToolResultBlock(string(make([]byte, 200))),
		}},
	}

	microCompact(msgs, 1)

	// First message (short) should remain unchanged
	got := msgs[0].Content[0].OfToolResult.Content[0].OfText.Text
	if got != "short error: not found" {
		t.Errorf("short content should not be replaced, got: %s", got)
	}
}

func TestMicroCompact_Empty(t *testing.T) {
	// Empty messages should not panic
	microCompact(nil, 3)
	microCompact([]anthropic.BetaMessageParam{}, 3)
}

func TestMicroCompact_Disabled(t *testing.T) {
	// keepRecent <= 0 should be a no-op
	msgs := []anthropic.BetaMessageParam{
		{Role: "user", Content: []anthropic.BetaContentBlockParamUnion{
			makeToolResultBlock(string(make([]byte, 200))),
		}},
	}
	microCompact(msgs, 0)
	got := msgs[0].Content[0].OfToolResult.Content[0].OfText.Text
	if len(got) < 200 {
		t.Error("content should not have been replaced when disabled")
	}
}

func TestSaveTranscript(t *testing.T) {
	dir := t.TempDir()
	msgs := []anthropic.BetaMessageParam{
		{Role: "user", Content: []anthropic.BetaContentBlockParamUnion{
			{OfText: &anthropic.BetaTextBlockParam{Text: "hello"}},
		}},
	}

	path, err := saveTranscript(dir, msgs)
	if err != nil {
		t.Fatalf("saveTranscript failed: %v", err)
	}

	// Verify file exists and is not empty
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat transcript: %v", err)
	}
	if info.Size() == 0 {
		t.Error("transcript file is empty")
	}
}

func TestDefaultCompactionConfig(t *testing.T) {
	cfg := DefaultCompactionConfig()
	if cfg.AutoThreshold != 50_000 {
		t.Errorf("AutoThreshold = %d, want 50000", cfg.AutoThreshold)
	}
	if cfg.MicroKeepRecent != 3 {
		t.Errorf("MicroKeepRecent = %d, want 3", cfg.MicroKeepRecent)
	}
	if cfg.TranscriptDir != ".task-agent/transcripts" {
		t.Errorf("TranscriptDir = %q, want .task-agent/transcripts", cfg.TranscriptDir)
	}
}
