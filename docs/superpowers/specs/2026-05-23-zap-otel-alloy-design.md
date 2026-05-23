# Design: zap 日志通过 OpenTelemetry 输出到 Grafana Alloy

Date: 2026-05-23

## 目标

在现有 zap logger 基础上，通过 OpenTelemetry SDK 将日志导出到 Grafana Alloy，实现：
- 日志通过 OTLP 协议发送到 Alloy，最终进入 Loki
- 日志自动携带 TraceID/SpanID，实现 log-trace 关联
- 支持 gRPC 和 HTTP 两种协议，通过配置切换
- 对现有文件和控制台日志输出零影响

## 架构

```
应用代码 (InfoContext/ErrorContext 等)
  │
  ▼
zap.Logger (Tee: File + Console + otelzap Core)
  │                              │
  │                              ▼
  │                       otelzap.NewCore
  │                              │
  │                       LoggerProvider (sdk/log)
  │                              │
  │                       OTLP exporter (grpc/http)
  │                              │
  │                       Grafana Alloy :4317/:4318
  │                              │
  │                       Loki/其他后端
  │
  ▼
TracerProvider (sdk/trace)
  │
  ▼
Alloy → Tempo/其他后端
```

## 关键决策

| 决策 | 选择 | 理由 |
|---|---|---|
| OTLP 协议 | gRPC + HTTP 都支持 | 配置切换，灵活性最大化 |
| Trace 关联 | 需要 | 日志携带 TraceID/SpanID，核心价值 |
| Core 注入方式 | `logger.AddCore()` 松耦合 | logger 包无需感知 OTel |
| Config 结构 | 共用 `otel.endpoint`，信号独立 `enabled` | 减少配置冗余 |

## 配置结构

```yaml
observability:
  enabled: true
  addr: ":9090"
  metrics_path: "/metrics"
  health_path: "/health"
  otel:
    endpoint: "localhost:4317"
    protocol: "grpc"              # "grpc" | "http"
    logs:
      enabled: true
    traces:
      enabled: true
```

对应的 Go 类型：

```go
type OTelConfig struct {
    Endpoint string       `yaml:"endpoint" mapstructure:"endpoint"`
    Protocol string       `yaml:"protocol" mapstructure:"protocol"`
    Logs     SignalConfig `yaml:"logs" mapstructure:"logs"`
    Traces   SignalConfig `yaml:"traces" mapstructure:"traces"`
}

type SignalConfig struct {
    Enabled bool `yaml:"enabled" mapstructure:"enabled"`
}
```

`ObservabilityConfig` 中新增 `OTel OTelConfig` 字段，替换原有的 `Tracing TracingConfig`（原 tracing 为 stub，无需兼容）。

默认值：`protocol = "grpc"`，`logs.enabled = false`，`traces.enabled = false`。

## 包改动详情

### 1. internal/logger/logger.go

新增 `AddCore(core zapcore.Core)`：外部注入 Core 后内部重建 Tee，原子替换全局 logger 实例。

新增 context-aware 方法（共 14 个）：

- `DebugContext(ctx, msg, fields...)`
- `InfoContext(ctx, msg, fields...)`
- `WarnContext(ctx, msg, fields...)`
- `ErrorContext(ctx, msg, fields...)`
- `DPanicContext(ctx, msg, fields...)`
- `PanicContext(ctx, msg, fields...)`
- `FatalContext(ctx, msg, fields...)`
- 对应的 `*fContext(ctx, template, args...)` 格式化版本

实现方式：方法内部调用 `otelzap.WithTraceContext(ctx)` 将 TraceID/SpanID 作为 zap fields 附加后调用底层 logger。

新增依赖：`go.opentelemetry.io/contrib/bridges/otelzap`

### 2. internal/observability/logs/logs.go（新建）

```go
func Init(ctx context.Context, cfg config.OTelConfig, res *resource.Resource) (zapcore.Core, func(context.Context) error, error)
```

流程：

1. 根据 `cfg.Protocol` 创建 `otlploggrpc` 或 `otlploghttp` exporter
2. 创建 `log.NewBatchProcessor(exporter)`
3. 创建 `log.NewLoggerProvider(processor, WithResource(res))`
4. `otelzap.NewCore(provider)` 返回 `zapcore.Core`
5. 返回 core + shutdown 函数

当 `cfg.Logs.Enabled == false` 时返回 `(nil, nopFunc, nil)`。

### 3. internal/observability/tracing/tracing.go

替换当前 stub 实现：

```go
func Init(ctx context.Context, cfg config.OTelConfig, res *resource.Resource) (func(context.Context) error, error)
```

流程：

1. 根据 `cfg.Protocol` 创建 `otlptracegrpc` 或 `otlptracehttp` exporter
2. 创建 `trace.NewBatchSpanProcessor(exporter)`
3. 创建 `trace.NewTracerProvider(processor, WithResource(res))`
4. `otel.SetTracerProvider(provider)`
5. 返回 shutdown 函数

当 `cfg.Traces.Enabled == false` 时返回 `(nopFunc, nil)`。

### 4. internal/observability/observability.go

在 `Start()` 中串联启动：

```go
// 构建 Resource（从 config 提取 service.name 等）
res := resource.New(...)

// 1. Tracing 先初始化
traceShutdown, _ := tracing.Init(ctx, cfg.OTel, res)

// 2. 再初始化 Logs
logCore, logShutdown, _ := logs.Init(ctx, cfg.OTel, res)
if logCore != nil {
    logger.AddCore(logCore)
}

// 3. 合并 shutdown
shutdown := func() {
    logShutdown(ctx)
    traceShutdown(ctx)
}
```

### 5. internal/config/config.go

- 新增 `OTelConfig` 和 `SignalConfig` 类型
- `ObservabilityConfig` 新增 `OTel` 字段，移除 `Tracing` 字段
- `setDefaults()` 新增对应默认值
- `DefaultObservabilityConfig()` 更新
- `GenerateConfig` 输出包含新配置块

## 错误处理

- 所有 OTel 初始化失败仅记录 warn 日志，不阻塞应用启动
- Alloy 不可达时应用仍正常运行，日志仅写入文件和控制台
- Shutdown 时先 flush logs，再 flush traces，确保最后的日志携带 span 信息

## 依赖变更

新增依赖：

- `go.opentelemetry.io/otel/sdk/log`
- `go.opentelemetry.io/otel/sdk/trace`
- `go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc`
- `go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp`
- `go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc`
- `go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp`
- `go.opentelemetry.io/contrib/bridges/otelzap`

## 测试策略

| 层 | 内容 | 方式 |
|---|---|---|
| logger | AddCore 后日志输出到所有 core | 单元测试，`zaptest.Observer` 验证 |
| logger | Context 方法附加 trace fields | 单元测试，mock span context |
| logs | grpc/http 分别创建正确 exporter | 单元测试 |
| tracing | TracerProvider 被正确设置 | 单元测试 |
| e2e | app → otelzap → OTLP → Alloy | 手动 / docker-compose |

## 文件变更清单

| 文件 | 操作 | 说明 |
|---|---|---|
| `internal/config/config.go` | 改 | OTelConfig + SignalConfig，移除 TracingConfig |
| `internal/logger/logger.go` | 改 | AddCore + 14 个 context-aware 方法 |
| `internal/observability/logs/logs.go` | 新建 | OTLP log exporter + otelzap bridge |
| `internal/observability/tracing/tracing.go` | 改 | 替换 stub，实现 OTLP trace exporter |
| `internal/observability/observability.go` | 改 | 串联 logs/tracing，构建 Resource |
| `go.mod` | 改 | 新增依赖 |
