package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
	"unicode/utf8"

	"github.com/anthropics/anthropic-sdk-go"
)

// CompactionConfig controls the three-layer context compaction.
// Zero values disable the corresponding layer.
type CompactionConfig struct {
	AutoThreshold   int    // input_tokens trigger threshold; 0 disables autoCompact
	MicroKeepRecent int    // number of recent tool_results to preserve; 0 disables microCompact
	TranscriptDir   string // directory for full-conversation transcripts
}

func DefaultCompactionConfig() CompactionConfig {
	return CompactionConfig{
		AutoThreshold:   50_000,
		MicroKeepRecent: 3,
		TranscriptDir:   ".task-agent/transcripts",
	}
}

// microCompact replaces old tool_result content with placeholders.
// Only tool_result blocks beyond the keepRecent window that have > 100 characters
// are replaced. It mutates messages in-place.
func microCompact(messages []anthropic.BetaMessageParam, keepRecent int) {
	if keepRecent <= 0 {
		return
	}

	// Collect positions of all tool_result blocks
	type pos struct{ msgIdx, blockIdx int }
	var toolResults []pos
	for i, msg := range messages {
		if msg.Role == "user" && len(msg.Content) > 0 {
			for j, block := range msg.Content {
				if block.OfToolResult != nil {
					toolResults = append(toolResults, pos{i, j})
				}
			}
		}
	}

	if len(toolResults) <= keepRecent {
		return
	}

	// Replace old results with generic placeholder
	for _, p := range toolResults[:len(toolResults)-keepRecent] {
		tr := messages[p.msgIdx].Content[p.blockIdx].OfToolResult
		if tr == nil {
			continue
		}
		for k, c := range tr.Content {
			if c.OfText != nil && len(c.OfText.Text) > 100 {
				c.OfText.Text = "[Previous tool result compacted]"
				// Write back the modified copy into the slice
				tr.Content[k] = c
			}
		}
	}
}

// saveTranscript writes all messages as JSONL to a timestamped file.
// Directory is created if it doesn't exist.
func saveTranscript(dir string, messages []anthropic.BetaMessageParam) (string, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("mkdir transcript dir: %w", err)
	}
	path := filepath.Join(dir, fmt.Sprintf("transcript_%d.jsonl", time.Now().Unix()))
	f, err := os.Create(path)
	if err != nil {
		return "", fmt.Errorf("create transcript: %w", err)
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	for _, msg := range messages {
		if err := enc.Encode(msg); err != nil {
			return "", fmt.Errorf("encode message: %w", err)
		}
	}

	if err := f.Sync(); err != nil {
		return "", fmt.Errorf("sync transcript: %w", err)
	}
	return path, nil
}

// autoCompact saves the full transcript, calls the LLM to summarize the
// conversation, and replaces r.messages with a single compressed user message.
// On failure the caller should emit a warning but continue the loop.
func (r *Runner) autoCompact(ctx context.Context) error {
	// 1. Save transcript
	path, err := saveTranscript(r.compactCfg.TranscriptDir, r.messages)
	if err != nil {
		return fmt.Errorf("save transcript: %w", err)
	}

	// 2. Serialize messages for summarization (truncated to ~80k chars)
	raw, _ := json.Marshal(r.messages)
	payload := string(raw)
	if len(payload) > 80_000 {
		// Truncate at the byte boundary, backing up to avoid splitting a multi-byte rune
		payload = payload[:80_000]
		for len(payload) > 0 && !utf8.Valid([]byte{payload[len(payload)-1]}) {
			payload = payload[:len(payload)-1]
		}
	}

	// 3. Call LLM to summarize
	summarizeResp, err := r.agent.client.Beta.Messages.New(ctx, anthropic.BetaMessageNewParams{
		Model: r.agent.model,
		System: []anthropic.BetaTextBlockParam{
			{Text: "Summarize this conversation for continuity. " +
				"Include: current goals, completed steps, key decisions, open issues. " +
				"Be concise (under 1500 tokens)."},
		},
		Messages: []anthropic.BetaMessageParam{
			{Role: "user", Content: []anthropic.BetaContentBlockParamUnion{
				{OfText: &anthropic.BetaTextBlockParam{Text: payload}},
			}},
		},
		MaxTokens: 2000,
	})
	if err != nil {
		return fmt.Errorf("summarize call: %w", err)
	}

	// 4. Extract summary text
	var summary string
	for _, block := range summarizeResp.Content {
		if block.Type == "text" {
			summary += block.AsText().Text
		}
	}
	if summary == "" {
		return fmt.Errorf("summarize returned empty text")
	}

	// 5. Replace all messages with compressed form
	r.messages = []anthropic.BetaMessageParam{
		{Role: "user", Content: []anthropic.BetaContentBlockParamUnion{
			{OfText: &anthropic.BetaTextBlockParam{
				Text: fmt.Sprintf("[Compressed conversation]\nTranscript: %s\n\n%s", path, summary),
			}},
		}},
	}

	return nil
}
