package app

import (
	"context"
	"fmt"
	"task-agent/internal/agent/tui"

	tea "charm.land/bubbletea/v2"
	"go.uber.org/zap"
	"task-agent/internal/agent"
	"task-agent/internal/logger"
)

func Run(ctx context.Context) error {
	logger.Info("Agent TUI starting")

	ag, err := agent.New()
	if err != nil {
		logger.Error("Agent initialization failed", zap.Error(err))
		return fmt.Errorf("agent init: %w", err)
	}

	p := tui.NewTUI(ag.Runner, tea.WithContext(ctx))

	if _, err := p.Run(); err != nil {
		logger.Error("Agent TUI error", zap.Error(err))
		return err
	}

	logger.Info("Agent TUI shutting down")
	return nil
}
