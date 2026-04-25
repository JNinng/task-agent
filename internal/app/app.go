package app

import (
	"context"
	"time"

	"go-template/internal/config"
	"go-template/internal/logger"

	"go.uber.org/zap"
)

func Run(ctx context.Context) error {
	cfg := config.Get()
	logger.Info("Application starting",
		zap.String("name", cfg.App.Name),
		zap.String("env", cfg.App.Env),
	)

	config.AddWatch(func(newCfg, oldCfg *config.Config) {
		logger.Infof("old config: %v", oldCfg)
		logger.Infof("new config: %v", newCfg)
	})

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Info("Application shutting down...")
			return nil
		case <-ticker.C:
			logger.Debug("Application running...",
				zap.String("name", config.Get().App.Name),
			)
			logger.Info("Application running...",
				zap.String("name", config.Get().App.Name),
			)
			logger.Warn("Application running...",
				zap.String("name", config.Get().App.Name),
			)
			logger.Error("Application running...",
				zap.String("name", config.Get().App.Name),
			)
		}
	}
}
