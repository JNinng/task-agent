package app

import (
	"context"

	"go-template/internal/logger"
)

func Run(ctx context.Context) error {
	logger.Info("Business logic starting")

	<-ctx.Done()

	logger.Info("Business logic shutting down")
	return nil
}
