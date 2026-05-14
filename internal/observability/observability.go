// Package observability provides observability infrastructure:
// - health: HTTP health check endpoint
// - metrics: Prometheus metrics endpoint
// - tracing: OpenTelemetry tracing (scaffold)
//
// Two integration modes:
//  1. Standalone port (default): Start() creates its own HTTP server
//  2. User integration: HealthHandler() and MetricsHandler() return handlers
package observability

import (
	"context"
	"net/http"
	"time"

	"go-template/internal/config"
	"go-template/internal/logger"
	"go-template/internal/observability/health"
	"go-template/internal/observability/metrics"
	"go-template/internal/observability/tracing"
)

const shutdownTimeout = 5 * time.Second

func Start(ctx context.Context, cfg config.ObservabilityConfig) error {
	if !cfg.Enabled {
		return nil
	}
	if cfg.Addr == "" {
		// Empty addr means user handles integration themselves
		return nil
	}

	// Initialize tracing
	tracingShutdown, err := tracing.Init(ctx, tracing.Config{
		Enabled:  cfg.Tracing.Enabled,
		Endpoint: cfg.Tracing.Endpoint,
	})
	if err != nil {
		logger.Warnf("Failed to init tracing: %v", err)
	}

	mux := http.NewServeMux()

	healthHandler := health.NewHandler()
	mux.HandleFunc(cfg.HealthPath, healthHandler.ServeHTTP)
	mux.Handle(cfg.MetricsPath, metrics.Handler())

	srv := &http.Server{
		Addr:    cfg.Addr,
		Handler: mux,
	}

	go func() {
		<-ctx.Done()
		tracingShutdown()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		srv.Shutdown(shutdownCtx)
	}()

	go func() {
		logger.Infof("Observability HTTP server starting on %s", cfg.Addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Warnf("Observability HTTP server error: %v", err)
		}
	}()

	return nil
}

func HealthHandler() *health.Handler {
	return health.NewHandler()
}

func MetricsHandler() http.Handler {
	return metrics.Handler()
}
