package observability

import (
	"context"
	"net/http"
	"time"

	"task-agent/internal/config"
	"task-agent/internal/logger"
	"task-agent/internal/observability/health"
	"task-agent/internal/observability/logs"
	"task-agent/internal/observability/metrics"
	"task-agent/internal/observability/tracing"

	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.27.0"
)

const shutdownTimeout = 5 * time.Second

func Start(ctx context.Context, cfg config.ObservabilityConfig) error {
	if !cfg.Enabled {
		return nil
	}
	if cfg.Addr == "" {
		return nil
	}

	res := resource.NewWithAttributes(semconv.SchemaURL,
		semconv.ServiceName(config.Get().App.Name),
		semconv.DeploymentEnvironmentName(config.Get().App.Env),
	)

	traceShutdown, err := tracing.Init(ctx, cfg.OTel, res)
	if err != nil {
		logger.Warnf("Failed to init tracing: %v", err)
	}

	logCore, logShutdown, err := logs.Init(ctx, cfg.OTel, res)
	if err != nil {
		logger.Warnf("Failed to init OTel logs: %v", err)
	}
	if logCore != nil {
		if err := logger.AddCore(logCore); err != nil {
			logger.Warnf("Failed to add OTel log core: %v", err)
		}
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
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if logShutdown != nil {
			logShutdown(shutdownCtx)
		}
		if traceShutdown != nil {
			traceShutdown(shutdownCtx)
		}
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
