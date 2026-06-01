// Package logs 提供 OpenTelemetry 日志导出管线初始化。
//
// Init 创建一个 OTLP 日志导出器（支持 gRPC 和 HTTP），
// 并通过 otelzap bridge 返回一个 zapcore.Core，可注入到 zap logger 中。
package logs

import (
	"context"
	"fmt"

	"task-agent/internal/config"

	"go.opentelemetry.io/contrib/bridges/otelzap"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.uber.org/zap/zapcore"
)

// Init 初始化 OpenTelemetry 日志导出管线，返回一个 zapcore.Core 用于注入到 zap logger。
// 返回的 shutdown 函数应在应用退出前调用以 flush 缓冲区中的日志。
func Init(ctx context.Context, cfg config.OTelConfig, res *resource.Resource) (zapcore.Core, func(context.Context) error, error) {
	if !cfg.Logs.Enabled {
		return nil, func(context.Context) error { return nil }, nil
	}

	var exporter log.Exporter
	var err error

	switch cfg.Protocol {
	case "http":
		exporter, err = otlploghttp.New(ctx,
			otlploghttp.WithEndpoint(cfg.Endpoint),
			otlploghttp.WithInsecure(),
		)
	default:
		exporter, err = otlploggrpc.New(ctx,
			otlploggrpc.WithEndpoint(cfg.Endpoint),
			otlploggrpc.WithInsecure(),
		)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("create log exporter: %w", err)
	}

	provider := log.NewLoggerProvider(
		log.WithProcessor(log.NewBatchProcessor(exporter)),
		log.WithResource(res),
	)

	core := otelzap.NewCore("otel-log", otelzap.WithLoggerProvider(provider))
	return core, provider.Shutdown, nil
}
