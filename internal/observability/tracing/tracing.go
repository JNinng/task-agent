package tracing

import (
	"context"
	"fmt"

	"task-agent/internal/config"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// Init 初始化 OpenTelemetry Tracing。
// 返回的 shutdown 函数应在应用退出前调用以 flush 未发送的 span。
func Init(ctx context.Context, cfg config.OTelConfig, res *resource.Resource) (func(context.Context) error, error) {
	if !cfg.Traces.Enabled {
		return func(context.Context) error { return nil }, nil
	}

	var exporter sdktrace.SpanExporter
	var err error

	switch cfg.Protocol {
	case "http":
		exporter, err = otlptracehttp.New(ctx,
			otlptracehttp.WithEndpoint(cfg.Endpoint),
			otlptracehttp.WithInsecure(),
		)
	default:
		exporter, err = otlptracegrpc.New(ctx,
			otlptracegrpc.WithEndpoint(cfg.Endpoint),
			otlptracegrpc.WithInsecure(),
		)
	}
	if err != nil {
		return nil, fmt.Errorf("create trace exporter: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)

	return tp.Shutdown, nil
}
