# Go Template 重构设计

## 概述

对 `go-template` 项目进行全面的架构和代码质量重构，将其打磨为一个真正可用的生产级 Go 服务模板。

## 目标

- 接口解耦：config 包不再直接依赖 nacos，通过接口实现可插拔配置源
- 代码质量：修复拼写、统一依赖、完善错误处理
- 可测试性：所有包实现独立单元测试，目标 80%+ 覆盖率
- 可观测性：添加 metrics (Prometheus)、tracing (OpenTelemetry)、health check
- CLI 框架：从 pflag 迁移到 Cobra，抽离命令定义

## 包结构

```
go-template/
├── cmd/
│   └── app/main.go              # 仅调用 cmd.Execute()
├── internal/
│   ├── cmd/                     # Cobra 命令定义
│   │   ├── root.go              # 组装启动流程
│   │   ├── version.go           # version 子命令
│   │   └── init.go              # init 子命令 (生成默认配置)
│   ├── app/app.go               # 业务逻辑占位 (精简)
│   ├── config/
│   │   ├── config.go            # Config 结构体 + 单例 + Init + 观察者
│   │   ├── source.go            # Source 接口定义
│   │   └── config_test.go
│   ├── logger/
│   │   ├── logger.go            # 独立配置，不依赖 config 包
│   │   └── logger_test.go
│   ├── signal/signal.go         # 信号处理 (保持不变)
│   ├── nacos/                   # 原 outsid，实现 Source 接口
│   │   ├── source.go
│   │   └── source_test.go
│   └── observability/
│       ├── metrics/metrics.go   # Prometheus
│       ├── tracing/tracing.go   # OTel
│       └── health/health.go     # Health check
├── pkg/version/version.go
└── configs/config.yaml
```

## Source 接口

`internal/config/source.go`:

```go
type Source interface {
    Name() string
    Init() (content []byte, changes <-chan []byte, err error)
    Close() error
}
```

- `config.Init(path string, sources ...Source) (*Config, error)` 接受可选外部配置源
- 首次启动时依次获取 sources 的初始内容，按顺序覆盖合并到本地配置
- 监听每个 source 的 changes channel，收到新内容后触发 reload+callbacks

## Logger 独立化

- `logger` 包定义自己的 `Config` 结构体，不再 import `config` 包
- `config.Config` 提供 `LoggerConfig() logger.Config` 转换方法
- 测试时可直构造 `logger.Config`，无需 config 包

## Observability 设计

```go
type ObservabilityConfig struct {
    Enabled     bool   `yaml:"enabled"`
    Addr        string `yaml:"addr"`                     // 默认 ":9090"
    MetricsPath string `yaml:"metrics_path"`              // 默认 "/metrics"
    HealthPath  string `yaml:"health_path"`               // 默认 "/health"
    Tracing     TracingConfig `yaml:"tracing"`
}

type TracingConfig struct {
    Enabled  bool   `yaml:"enabled"`
    Endpoint string `yaml:"endpoint"` // OTLP endpoint
}
```

- 默认独立端口启动 HTTP server，提供 /health 和 /metrics
- `HealthHandler()` 和 `MetricsHandler()` 返回 `http.Handler`，供用户集成到业务端口
- 启动失败仅 warn，不阻塞主流程

## 错误处理策略

| 阶段 | 策略 |
|------|------|
| config.Init | Fatal |
| logger.Init | Fatal |
| Observability HTTP | Warn only |
| Nacos source 初始化 | Warn only |
| Source changes 消费 | Panic recover + log |

## 依赖变更

- 移除：`github.com/spf13/pflag` (cobra 自带 pflag)
- 移除：`go.yaml.in/yaml/v3` (统一用 `gopkg.in/yaml.v3`)
- 新增：`github.com/spf13/cobra`
- 新增：`github.com/prometheus/client_golang` (metrics)
- 新增：`go.opentelemetry.io/otel` (tracing, 可选)
- 保留：`go.uber.org/zap`, `gopkg.in/natefinch/lumberjack.v2`, `github.com/nacos-group/nacos-sdk-go/v2`

## 启动流程

```
cmd.Execute()
  ├── versionCmd → version.Print() → exit(0)
  ├── initCmd → config.GenerateConfig() → exit(0)
  └── rootCmd.RunE
        ├── config.Init(path, sources...)
        ├── logger.Init(cfg.LoggerConfig())
        ├── config.AddWatch(logger reset)
        ├── observability.Start(ctx, cfg) (if enabled)
        ├── signal.ContextWithShutdown(ctx)
        └── app.Run(ctx)
```

## 文件变更汇总

新增 (13): `internal/cmd/root.go`, `internal/cmd/version.go`, `internal/cmd/init.go`, `internal/config/source.go`, `internal/nacos/source.go`, `internal/observability/metrics/metrics.go`, `internal/observability/tracing/tracing.go`, `internal/observability/health/health.go`, `internal/config/config_test.go`, `internal/config/source_test.go`, `internal/logger/logger_test.go`, `internal/signal/signal_test.go`, `internal/nacos/source_test.go`

修改 (5): `cmd/app/main.go`, `internal/config/config.go`, `internal/logger/logger.go`, `internal/app/app.go`, `configs/config.yaml`

删除 (1): `internal/outsid/nacos.go`

不变 (3): `pkg/version/version.go`, `internal/signal/signal.go`, `build/.goreleaser.yaml`
