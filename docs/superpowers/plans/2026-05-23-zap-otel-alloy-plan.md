# zap-otel-alloy Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 通过 OpenTelemetry SDK 将 zap 日志通过 OTLP 协议导出到 Grafana Alloy，同时启用 trace-log 关联。

**Architecture:** 增量扩展现有 observability 体系。logger 包新增 `AddCore` + context-aware 方法；新建 `logs` 包处理 OTLP log export；tracing 包替换 stub 为真实 OTLP trace exporter；config 包用统一 `OTelConfig` 替换旧的 `TracingConfig`。

**Tech Stack:** Go 1.26.1, zap, OTel SDK (sdk/log, sdk/trace, exporters/otlp), otelzap bridge

---

### Task 1: 添加 OpenTelemetry 依赖

**Files:**
- Modify: `go.mod`
- Modify: `go.sum`

- [ ] **Step 1: 安装所有 OTel 依赖**

```bash
go get go.opentelemetry.io/otel/sdk/log@latest
go get go.opentelemetry.io/otel/sdk/trace@latest
go get go.opentelemetry.io/otel/sdk/resource@latest
go get go.opentelemetry.io/otel/semconv/v1.26.0@latest
go get go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc@latest
go get go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp@latest
go get go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc@latest
go get go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp@latest
go get go.opentelemetry.io/contrib/bridges/otelzap@latest
go mod tidy
```

- [ ] **Step 2: 验证编译**

```bash
go build ./...
```

Expected: 编译通过（会有 unused import 警告，后续任务会消除）。

- [ ] **Step 3: 提交**

```bash
git add go.mod go.sum
git commit -m "chore: add OpenTelemetry SDK and otelzap bridge dependencies"
```

---

### Task 2: Config 类型重构

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`

- [ ] **Step 1: 在 config.go 中添加 OTelConfig 和 SignalConfig 类型**

在 `TracingConfig` 定义之后添加（后续步骤会删除 TracingConfig）：

```go
// OTelConfig OpenTelemetry 配置
type OTelConfig struct {
	Endpoint string       `yaml:"endpoint" mapstructure:"endpoint"` // OTLP collector 地址 (e.g., localhost:4317)
	Protocol string       `yaml:"protocol" mapstructure:"protocol"` // 协议: "grpc" 或 "http"
	Logs     SignalConfig `yaml:"logs" mapstructure:"logs"`         // 日志导出配置
	Traces   SignalConfig `yaml:"traces" mapstructure:"traces"`     // 链路导出配置
}

// SignalConfig 单个 OTel 信号的启用配置
type SignalConfig struct {
	Enabled bool `yaml:"enabled" mapstructure:"enabled"`
}
```

- [ ] **Step 2: 添加默认值常量**

在现有默认值常量区域添加：

```go
DefaultOTelEndpoint = "localhost:4317"
DefaultOTelProtocol = "grpc"
```

- [ ] **Step 3: 更新 ObservabilityConfig，替换 Tracing 为 OTel**

将：
```go
type ObservabilityConfig struct {
	Enabled     bool          `yaml:"enabled" mapstructure:"enabled"`
	Addr        string        `yaml:"addr" mapstructure:"addr"`
	MetricsPath string        `yaml:"metrics_path" mapstructure:"metrics_path"`
	HealthPath  string        `yaml:"health_path" mapstructure:"health_path"`
	Tracing     TracingConfig `yaml:"tracing" mapstructure:"tracing"`
}
```

改为：
```go
type ObservabilityConfig struct {
	Enabled     bool       `yaml:"enabled" mapstructure:"enabled"`
	Addr        string     `yaml:"addr" mapstructure:"addr"`
	MetricsPath string     `yaml:"metrics_path" mapstructure:"metrics_path"`
	HealthPath  string     `yaml:"health_path" mapstructure:"health_path"`
	OTel        OTelConfig `yaml:"otel" mapstructure:"otel"`
}
```

- [ ] **Step 4: 删除 TracingConfig 类型定义**

删除：
```go
// TracingConfig 链路追踪配置
type TracingConfig struct {
	Enabled  bool   `yaml:"enabled" mapstructure:"enabled"`
	Endpoint string `yaml:"endpoint" mapstructure:"endpoint"`
}
```

- [ ] **Step 5: 更新 setDefaults()**

删除：
```go
v.SetDefault("observability.tracing.enabled", ...)
v.SetDefault("observability.tracing.endpoint", ...)
```
（这两行目前不存在，因为 TracingConfig 没有在 setDefaults 中设置默认值。确认后跳过删除。）

添加：
```go
v.SetDefault("observability.otel.endpoint", DefaultOTelEndpoint)
v.SetDefault("observability.otel.protocol", DefaultOTelProtocol)
v.SetDefault("observability.otel.logs.enabled", false)
v.SetDefault("observability.otel.traces.enabled", false)
```

- [ ] **Step 6: 更新 DefaultObservabilityConfig()**

```go
func DefaultObservabilityConfig() ObservabilityConfig {
	return ObservabilityConfig{
		Addr:        DefaultObsAddr,
		MetricsPath: DefaultObsMetricsPath,
		HealthPath:  DefaultObsHealthPath,
		OTel: OTelConfig{
			Endpoint: DefaultOTelEndpoint,
			Protocol: DefaultOTelProtocol,
		},
	}
}
```

- [ ] **Step 7: 更新 config_test.go 中的 TestObservabilityDefaultConfig**

```go
func TestObservabilityDefaultConfig(t *testing.T) {
	cfg := DefaultObservabilityConfig()
	if cfg.Addr != ":9090" {
		t.Errorf("expected :9090, got %s", cfg.Addr)
	}
	if cfg.MetricsPath != "/metrics" {
		t.Errorf("expected /metrics, got %s", cfg.MetricsPath)
	}
	if cfg.HealthPath != "/health" {
		t.Errorf("expected /health, got %s", cfg.HealthPath)
	}
	if cfg.OTel.Endpoint != DefaultOTelEndpoint {
		t.Errorf("expected %s, got %s", DefaultOTelEndpoint, cfg.OTel.Endpoint)
	}
	if cfg.OTel.Protocol != DefaultOTelProtocol {
		t.Errorf("expected %s, got %s", DefaultOTelProtocol, cfg.OTel.Protocol)
	}
	if cfg.OTel.Logs.Enabled {
		t.Error("expected logs.enabled to be false by default")
	}
	if cfg.OTel.Traces.Enabled {
		t.Error("expected traces.enabled to be false by default")
	}
}
```

- [ ] **Step 8: 添加 TestOTelConfigDefaults 测试**

```go
func TestOTelConfigDefaults(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Observability.OTel.Endpoint != DefaultOTelEndpoint {
		t.Errorf("expected %s, got %s", DefaultOTelEndpoint, cfg.Observability.OTel.Endpoint)
	}
	if cfg.Observability.OTel.Protocol != DefaultOTelProtocol {
		t.Errorf("expected %s, got %s", DefaultOTelProtocol, cfg.Observability.OTel.Protocol)
	}
}
```

- [ ] **Step 9: 运行测试**

```bash
go test ./internal/config/ -v
```

Expected: 所有测试通过。

- [ ] **Step 10: 提交**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat(config): replace TracingConfig with OTelConfig for logs and traces"
```

---

### Task 3: Logger — AddCore 支持

**Files:**
- Modify: `internal/logger/logger.go`
- Modify: `internal/logger/logger_test.go`

- [ ] **Step 1: 在 logger.go 中添加 extraCores 变量和 mutex**

在 `var (` 块中，`currentLogCfg` 之后添加：

```go
extraCores   []zapcore.Core
extraCoresMu sync.RWMutex
```

- [ ] **Step 2: 修改 buildLogger 签名，接受可选 extra cores**

```go
func buildLogger(cfg *Config, level zap.AtomicLevel, extra ...zapcore.Core) (*zap.Logger, *zap.SugaredLogger, *lumberjack.Logger, error) {
```

在 `cores` 构建后、`NewTee` 之前添加：

```go
cores = append(cores, extra...)
```

即完整的 core 组装变为：

```go
var cores []zapcore.Core
// ... file core 和 console core 的 append ...

cores = append(cores, extra...)

core := zapcore.NewTee(cores...)
```

- [ ] **Step 3: 添加 AddCore 函数**

在 `Reset` 函数之后添加：

```go
// AddCore 向 logger 注入额外的 zapcore.Core（如 otelzap bridge）。
// 线程安全，注入后自动重建底层 logger。
func AddCore(core zapcore.Core) {
	extraCoresMu.Lock()
	extraCores = append(extraCores, core)
	copyCores := make([]zapcore.Core, len(extraCores))
	copy(copyCores, extraCores)
	extraCoresMu.Unlock()

	cfg, ok := currentLogCfg.Load().(*Config)
	if !ok || cfg == nil {
		return
	}

	logger, sugar, writer, err := buildLogger(cfg, atomicLevel, copyCores...)
	if err != nil {
		return
	}

	writerMutex.Lock()
	if currentWriter != nil {
		currentWriter.Close()
	}
	currentWriter = writer
	writerMutex.Unlock()

	globalLogger.Store(logger)
	globalSugar.Store(sugar)
}
```

- [ ] **Step 4: 为 AddCore 添加测试**

在 `internal/logger/logger_test.go` 中添加：

```go
func TestAddCore(t *testing.T) {
	cfg := DefaultConfig()
	cfg.LogToConsole = false
	cfg.Path = "" // 不写文件，仅测试 core 注入
	if err := Init(&cfg); err != nil {
		t.Fatal(err)
	}

	// 使用 zap 内置的 Observer 来捕获日志
	core, observed := observer.New(zapcore.DebugLevel)
	AddCore(core)

	Info("test message", zap.String("key", "value"))

	logs := observed.All()
	if len(logs) != 1 {
		t.Fatalf("expected 1 log, got %d", len(logs))
	}
	if logs[0].Message != "test message" {
		t.Errorf("expected 'test message', got %s", logs[0].Message)
	}

	writerMutex.Lock()
	if currentWriter != nil {
		currentWriter.Close()
		currentWriter = nil
	}
	writerMutex.Unlock()
}
```

需要在文件头部添加 import：
```go
"go.uber.org/zap"
"go.uber.org/zap/zapcore"
"go.uber.org/zap/zaptest/observer"
```

- [ ] **Step 5: 运行测试**

```bash
go test ./internal/logger/ -v -run TestAddCore
```

Expected: PASS

- [ ] **Step 6: 提交**

```bash
git add internal/logger/logger.go internal/logger/logger_test.go
git commit -m "feat(logger): add AddCore for external zapcore.Core injection"
```

---

### Task 4: Logger — Context-aware 方法

**Files:**
- Modify: `internal/logger/logger.go`
- Modify: `internal/logger/logger_test.go`

- [ ] **Step 1: 添加 import**

在 logger.go 的 import 中添加：
```go
import (
	"context"
	// ... 现有 imports ...

	"go.opentelemetry.io/contrib/bridges/otelzap"
)
```

- [ ] **Step 2: 添加 7 个 Context 方法（非格式化）**

在现有 `Fatal` 方法之后添加：

```go
// DebugContext 输出带有 trace context 的 Debug 级别日志
func DebugContext(ctx context.Context, msg string, fields ...zap.Field) {
	getLogger().Debug(msg, append(fields, otelzap.WithTraceContext(ctx)...)...)
}

// InfoContext 输出带有 trace context 的 Info 级别日志
func InfoContext(ctx context.Context, msg string, fields ...zap.Field) {
	getLogger().Info(msg, append(fields, otelzap.WithTraceContext(ctx)...)...)
}

// WarnContext 输出带有 trace context 的 Warn 级别日志
func WarnContext(ctx context.Context, msg string, fields ...zap.Field) {
	getLogger().Warn(msg, append(fields, otelzap.WithTraceContext(ctx)...)...)
}

// ErrorContext 输出带有 trace context 的 Error 级别日志
func ErrorContext(ctx context.Context, msg string, fields ...zap.Field) {
	getLogger().Error(msg, append(fields, otelzap.WithTraceContext(ctx)...)...)
}

// DPanicContext 输出带有 trace context 的 DPanic 级别日志
func DPanicContext(ctx context.Context, msg string, fields ...zap.Field) {
	getLogger().DPanic(msg, append(fields, otelzap.WithTraceContext(ctx)...)...)
}

// PanicContext 输出带有 trace context 的 Panic 级别日志
func PanicContext(ctx context.Context, msg string, fields ...zap.Field) {
	getLogger().Panic(msg, append(fields, otelzap.WithTraceContext(ctx)...)...)
}

// FatalContext 输出带有 trace context 的 Fatal 级别日志
func FatalContext(ctx context.Context, msg string, fields ...zap.Field) {
	getLogger().Fatal(msg, append(fields, otelzap.WithTraceContext(ctx)...)...)
}
```

- [ ] **Step 3: 添加 7 个 Context 格式化方法**

在 `FatalContext` 之后添加：

```go
// DebugfContext 输出带有 trace context 的 Debug 级别格式化日志
func DebugfContext(ctx context.Context, template string, args ...any) {
	getSugar().With(otelzap.WithTraceContext(ctx)...).Debugf(template, args...)
}

// InfofContext 输出带有 trace context 的 Info 级别格式化日志
func InfofContext(ctx context.Context, template string, args ...any) {
	getSugar().With(otelzap.WithTraceContext(ctx)...).Infof(template, args...)
}

// WarnfContext 输出带有 trace context 的 Warn 级别格式化日志
func WarnfContext(ctx context.Context, template string, args ...any) {
	getSugar().With(otelzap.WithTraceContext(ctx)...).Warnf(template, args...)
}

// ErrorfContext 输出带有 trace context 的 Error 级别格式化日志
func ErrorfContext(ctx context.Context, template string, args ...any) {
	getSugar().With(otelzap.WithTraceContext(ctx)...).Errorf(template, args...)
}

// DPanicfContext 输出带有 trace context 的 DPanic 级别格式化日志
func DPanicfContext(ctx context.Context, template string, args ...any) {
	getSugar().With(otelzap.WithTraceContext(ctx)...).DPanicf(template, args...)
}

// PanicfContext 输出带有 trace context 的 Panic 级别格式化日志
func PanicfContext(ctx context.Context, template string, args ...any) {
	getSugar().With(otelzap.WithTraceContext(ctx)...).Panicf(template, args...)
}

// FatalfContext 输出带有 trace context 的 Fatal 级别格式化日志
func FatalfContext(ctx context.Context, template string, args ...any) {
	getSugar().With(otelzap.WithTraceContext(ctx)...).Fatalf(template, args...)
}
```

- [ ] **Step 4: 添加测试**

在 `internal/logger/logger_test.go` 中添加：

```go
func TestContextMethods(t *testing.T) {
	cfg := DefaultConfig()
	cfg.LogToConsole = false
	cfg.Path = ""
	if err := Init(&cfg); err != nil {
		t.Fatal(err)
	}

	core, observed := observer.New(zapcore.DebugLevel)
	AddCore(core)

	ctx := context.Background()
	InfoContext(ctx, "info with context", zap.String("k", "v"))
	InfofContext(ctx, "formatted with context: %s", "hello")
	DebugContext(ctx, "debug with context")
	WarnContext(ctx, "warn with context")
	ErrorContext(ctx, "error with context")

	logs := observed.All()
	if len(logs) < 5 {
		t.Fatalf("expected at least 5 logs, got %d", len(logs))
	}
	if logs[0].Message != "info with context" {
		t.Errorf("expected 'info with context', got %s", logs[0].Message)
	}
	if logs[1].Message != "formatted with context: hello" {
		t.Errorf("expected 'formatted with context: hello', got %s", logs[1].Message)
	}

	writerMutex.Lock()
	if currentWriter != nil {
		currentWriter.Close()
		currentWriter = nil
	}
	writerMutex.Unlock()
}
```

需要添加 `"context"` 到 test 文件的 import。

- [ ] **Step 5: 运行测试**

```bash
go test ./internal/logger/ -v
```

Expected: 所有测试通过（TestInitAndLog, TestResetLevel, TestResetOutput, TestAddCore, TestContextMethods）。

- [ ] **Step 6: 验证编译**

```bash
go build ./...
```

Expected: 编译通过。

- [ ] **Step 7: 提交**

```bash
git add internal/logger/logger.go internal/logger/logger_test.go
git commit -m "feat(logger): add context-aware logging methods with trace correlation"
```

---

### Task 5: 替换 tracing stub 为真实 OTLP trace exporter

**Files:**
- Modify: `internal/observability/tracing/tracing.go`
- Create: `internal/observability/tracing/tracing_test.go`

- [ ] **Step 1: 重写 tracing.go**

```go
package tracing

import (
	"context"
	"fmt"

	"go-template/internal/config"

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
```

- [ ] **Step 2: 创建 tracing_test.go**

```go
package tracing

import (
	"context"
	"testing"

	"go-template/internal/config"

	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

func TestInitDisabled(t *testing.T) {
	cfg := config.OTelConfig{
		Traces: config.SignalConfig{Enabled: false},
	}
	res := resource.NewWithAttributes(semconv.SchemaURL,
		semconv.ServiceName("test"),
	)

	shutdown, err := Init(context.Background(), cfg, res)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if shutdown == nil {
		t.Fatal("expected non-nil shutdown func")
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown failed: %v", err)
	}
}

func TestInitInvalidEndpoint(t *testing.T) {
	cfg := config.OTelConfig{
		Endpoint: "invalid-endpoint:99999",
		Protocol: "grpc",
		Traces:   config.SignalConfig{Enabled: true},
	}
	res := resource.NewWithAttributes(semconv.SchemaURL,
		semconv.ServiceName("test"),
	)

	// Init 本身不会因为 endpoint 不可达而失败（exporter 延迟连接），
	// 但我们可以验证它不 panic
	_, err := Init(context.Background(), cfg, res)
	if err != nil {
		t.Logf("Init returned error (expected for invalid config): %v", err)
	}
}
```

- [ ] **Step 3: 运行测试**

```bash
go test ./internal/observability/tracing/ -v
```

Expected: PASS

- [ ] **Step 4: 提交**

```bash
git add internal/observability/tracing/tracing.go internal/observability/tracing/tracing_test.go
git commit -m "feat(tracing): replace stub with real OTLP trace exporter (grpc/http)"
```

---

### Task 6: 创建 observability/logs 包

**Files:**
- Create: `internal/observability/logs/logs.go`
- Create: `internal/observability/logs/logs_test.go`

- [ ] **Step 1: 创建 logs.go**

```go
package logs

import (
	"context"
	"fmt"

	"go-template/internal/config"

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

	core := otelzap.NewCore(provider)
	return core, provider.Shutdown, nil
}
```

- [ ] **Step 2: 创建 logs_test.go**

```go
package logs

import (
	"context"
	"testing"

	"go-template/internal/config"

	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

func TestInitDisabled(t *testing.T) {
	cfg := config.OTelConfig{
		Logs: config.SignalConfig{Enabled: false},
	}
	res := resource.NewWithAttributes(semconv.SchemaURL,
		semconv.ServiceName("test"),
	)

	core, shutdown, err := Init(context.Background(), cfg, res)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if core != nil {
		t.Error("expected nil core when disabled")
	}
	if shutdown == nil {
		t.Fatal("expected non-nil shutdown func")
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown failed: %v", err)
	}
}

func TestInitHTTPProtocol(t *testing.T) {
	cfg := config.OTelConfig{
		Endpoint: "localhost:4318",
		Protocol: "http",
		Logs:     config.SignalConfig{Enabled: true},
	}
	res := resource.NewWithAttributes(semconv.SchemaURL,
		semconv.ServiceName("test"),
	)

	_, _, err := Init(context.Background(), cfg, res)
	if err != nil {
		t.Logf("Init returned error (connection will fail without collector): %v", err)
	}
}
```

- [ ] **Step 3: 运行测试**

```bash
go test ./internal/observability/logs/ -v
```

Expected: PASS（连接失败不 panic，日志正常输出）

- [ ] **Step 4: 提交**

```bash
git add internal/observability/logs/
git commit -m "feat(logs): add OTLP log exporter with otelzap bridge (grpc/http)"
```

---

### Task 7: 串联 observability.Start 启动流程

**Files:**
- Modify: `internal/observability/observability.go`

- [ ] **Step 1: 更新 observability.go import**

添加：
```go
import (
	// ... 现有 imports ...
	"go-template/internal/config"
	"go-template/internal/observability/logs"
	"go-template/internal/observability/tracing"

	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)
```

- [ ] **Step 2: 重写 Start() 函数**

```go
func Start(ctx context.Context, cfg config.ObservabilityConfig) error {
	if !cfg.Enabled {
		return nil
	}
	if cfg.Addr == "" {
		return nil
	}

	res := resource.NewWithAttributes(semconv.SchemaURL,
		semconv.ServiceName(config.Get().App.Name),
		semconv.DeploymentEnvironment(config.Get().App.Env),
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
		logger.AddCore(logCore)
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
```

- [ ] **Step 3: 运行测试**

```bash
go test ./internal/observability/ -v
```

- [ ] **Step 4: 验证全项目编译**

```bash
go build ./...
go vet ./...
```

Expected: 编译通过，无 vet 警告。

- [ ] **Step 5: 运行所有测试**

```bash
go test ./...
```

Expected: 所有测试通过。

- [ ] **Step 6: 提交**

```bash
git add internal/observability/observability.go
git commit -m "feat(observability): wire OTel tracing and logs into Start()"
```

---

### Task 8: 端到端验证

**Files:**
- (无代码变更)

- [ ] **Step 1: 运行完整测试套件**

```bash
go test -race ./...
```

Expected: 所有测试通过，无 race condition。

- [ ] **Step 2: 构建二进制**

```bash
go build -o bin/app ./cmd/app
```

Expected: 构建成功。

- [ ] **Step 3: 验证默认配置文件生成包含 OTel 配置**

```bash
go run ./cmd/app init -o /tmp/test-config.yaml
Get-Content /tmp/test-config.yaml | Select-String -Pattern "otel" -Context 1,3
```

Expected: 输出包含 `otel:` 配置块，含 endpoint、protocol、logs、traces 字段。

- [ ] **Step 4: 运行 golangci-lint**

```bash
golangci-lint run ./...
```
