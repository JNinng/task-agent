# Go 项目模板

生产级 Go 项目模板，遵循 [golang-standards/project-layout](https://github.com/golang-standards/project-layout) 目录规范。整合了配置管理、日志、优雅关闭和可观测性——几乎所有 Go 服务在编写业务逻辑之前都需要的基础设施。

## 功能特性

- **模块化设计**：config、logger、observability 作为独立、可复用的包
- **配置热更新**：基于 fsnotify 的本地文件监听 + 可插拔的远程配置源（Nacos 等）
- **并发安全**：通过 `atomic.Pointer` 保证全局配置的线程安全读写
- **观察者模式**：配置变更通过回调机制松耦合通知订阅者
- **优雅关闭**：SIGINT/SIGTERM/SIGQUIT 信号处理，确保资源清理
- **结构化日志**：基于 Zap 的日志系统，支持日志轮转（lumberjack）
- **CLI 支持**：基于 Cobra 的命令行，内置 `init`、`version` 子命令
- **可观测性**：内置 health 健康检查、Prometheus metrics、OpenTelemetry tracing 脚手架
- **环境变量覆盖**：`APP_` 前缀的环境变量自动映射到配置项
- **Cobra CLI**：标准的命令行入口，支持 `-c` 指定配置文件、`init` 生成默认配置、`version` 输出版本信息

## 项目结构

```
.
├── cmd/
│   └── app/
│       └── main.go              # 入口文件（一行调用 cmd.Execute()）
├── internal/
│   ├── app/
│   │   └── app.go               # 业务逻辑占位
│   ├── cmd/
│   │   ├── root.go              # Cobra 根命令，完整启动流程
│   │   ├── init.go              # init 子命令（生成默认配置文件）
│   │   └── version.go           # version 子命令（输出版本信息）
│   ├── config/
│   │   ├── config.go            # 配置管理器：Init、Get、AddWatch、热更新
│   │   └── source.go            # Source 接口（可插拔远程配置源）
│   ├── logger/
│   │   └── logger.go            # 日志管理器：Init、Reset、全局方法
│   ├── nacos/
│   │   └── source.go            # Nacos 配置源（实现 config.Source 接口）
│   ├── observability/
│   │   ├── observability.go     # 可观测性入口：独立 HTTP 服务器
│   │   ├── health/
│   │   │   └── health.go        # 健康检查：/health 端点，可注册检查函数
│   │   ├── metrics/
│   │   │   └── metrics.go       # Prometheus /metrics 端点
│   │   └── tracing/
│   │       └── tracing.go       # OpenTelemetry 链路追踪脚手架
│   └── signal/
│       └── signal.go            # 信号处理：ContextWithShutdown
├── pkg/
│   └── version/
│       └── version.go           # 构建时注入的版本信息
├── configs/
│   └── config.yaml              # 默认配置文件
├── build/
│   └── .goreleaser.yaml         # GoReleaser 构建配置
├── scripts/
├── go.mod
└── README.md
```

## 快速开始

```bash
# 下载依赖
go mod download

# 运行应用（默认读取 configs/config.yaml 或 config.yaml）
go run ./cmd/app

# 生成默认配置文件
go run ./cmd/app init -o my-config.yaml

# 使用自定义配置文件运行
go run ./cmd/app -c my-config.yaml

# 通过环境变量覆盖配置
APP_LOG_LEVEL=debug APP_APP_ENV=production go run ./cmd/app
```

## CLI 命令

```bash
# 启动服务
go run ./cmd/app -c configs/config.yaml

# 生成默认配置文件
go run ./cmd/app init -o config.yaml

# 查看版本信息
go run ./cmd/app version
```

## 配置说明

通过 `go run ./cmd/app init -o config.yaml` 可生成包含所有默认值的完整配置文件：

```yaml
app:
  name: template-app      # 应用名称
  env: dev                # 运行环境
  watch: true             # 是否监听配置文件变更（热更新）

log:
  level: info             # 日志级别: debug/info/warn/error
  format: console         # 日志格式: console/json
  path: logs/app.log      # 日志文件路径
  max_size: 200           # 单个日志文件最大大小 (MB)
  max_age: 60             # 日志文件保留天数
  max_backups: 60         # 保留的日志文件数量
  compress: true          # 是否压缩历史日志
  log_to_console: true    # 是否同时输出到控制台

nacos:
  addr: 127.0.0.1
  port: 8848
  username: nacos
  password: nacos
  namespace: public
  group: DEFAULT_GROUP
  data_id: template-app.yml
  log_level: debug
  log_dir: logs
  cache_dir: cache

observability:
  enabled: false          # 是否启用可观测性 HTTP 服务器
  addr: ":9090"           # 监听地址
  metrics_path: /metrics  # Prometheus 指标路径
  health_path: /health    # 健康检查路径
  tracing:
    enabled: false        # 是否启用链路追踪
    endpoint: ""          # OTLP exporter 地址

secret:
  key: ""                 # 密钥占位
```

### 热更新

在应用运行时修改配置文件，配置将被自动重新加载并触发所有注册的回调。

### 环境变量

所有配置项均可通过 `APP_` 前缀的环境变量覆盖，点号分隔的路径用下划线替代：

```bash
APP_LOG_LEVEL=debug
APP_APP_ENV=production
APP_OBSERVABILITY_ENABLED=true
APP_NACOS_ADDR=10.0.0.1
```

## 使用示例

### 读取配置

```go
import "go-template/internal/config"

// 获取当前配置（线程安全）
cfg := config.Get()
fmt.Println(cfg.App.Name)
fmt.Println(cfg.Log.Level)

// 直接使用结构体字段
if cfg.Observability.Enabled {
    // ...
}
```

### 监听配置变更

```go
import "go-template/internal/config"

// 注册回调
key := config.AddWatch(func(newCfg, oldCfg *config.Config) {
    if newCfg.Log.Level != oldCfg.Log.Level {
        // 日志级别已变更，执行相应操作
    }
})

// 取消监听
config.RemoveWatch(key)
```

### 使用日志

```go
import (
    "go-template/internal/logger"
    "go.uber.org/zap"
)

// 结构化日志
logger.Info("服务启动成功", zap.String("name", cfg.App.Name), zap.String("env", cfg.App.Env))

// 格式化日志
logger.Infof("User %s logged in", username)

// 错误日志
logger.Error("数据库连接失败", zap.Error(err), zap.String("dsn", dsn))
```

### 优雅关闭

```go
import (
    "context"
    "go-template/internal/signal"
)

func main() {
    ctx := signal.ContextWithShutdown(context.Background())

    // 启动你的服务...

    <-ctx.Done()
    // 执行清理逻辑
}
```

### 可观测性 (Observability) — 可选

#### 独立端口模式（推荐）

在配置文件中启用：

```yaml
observability:
  enabled: true
  addr: ":9090"
  metrics_path: /metrics
  health_path: /health
```

启动后，可观测性端点将在独立端口上运行：

```bash
# 健康检查
curl http://localhost:9090/health
# {"status":"healthy","timestamp":"2025-01-15T10:30:00Z","details":{}}

# Prometheus 指标
curl http://localhost:9090/metrics
```

#### 自定义健康检查

```go
import (
    "database/sql"
    "go-template/internal/observability/health"
)

h := health.NewHandler()

// 注册数据库连接检查
h.Register("database", func() error {
    return db.Ping()
})

// 注册 Redis 连接检查
h.Register("redis", func() error {
    return redisClient.Ping(context.Background()).Err()
})
```

#### 嵌入已有路由

如果不希望使用独立端口，可以将 handler 嵌入到已有 mux：

```go
import (
    "go-template/internal/observability"
)

mux := http.NewServeMux()

healthHandler := observability.HealthHandler()
healthHandler.Register("db", checkDB)
mux.Handle("/health", healthHandler)

mux.Handle("/metrics", observability.MetricsHandler())
```

#### Prometheus 自定义指标

```go
import (
    "go-template/internal/observability/metrics"
    "github.com/prometheus/client_golang/prometheus"
)

requestCounter := prometheus.NewCounterVec(
    prometheus.CounterOpts{
        Name: "http_requests_total",
        Help: "Total number of HTTP requests",
    },
    []string{"method", "path"},
)

// 注册到独立的 Registry（不污染全局默认 Registry）
metrics.Register(requestCounter)
```

### Nacos 配置中心 — 可选

Nacos 是阿里巴巴开源的服务发现与配置管理平台。本模板通过 `config.Source` 接口集成了 Nacos 配置热更新。

#### 启用 Nacos

Nacos 的连接参数（addr、port、namespace 等）属于基础设施配置，写在本地 YAML 中；业务配置由 Nacos 远程提供。通过 `config.MergeSource` 两阶段初始化避免循环依赖：

```go
import (
    "go-template/internal/config"
    "go-template/internal/nacos"
)

func main() {
    // Phase 1: 从本地文件读取配置（含 Nacos 连接参数）
    cfg, err := config.Init("configs/config.yaml")
    if err != nil {
        panic(err)
    }

    // Phase 2: 用本地配置中的 Nacos 连接参数创建 Source，合并远程配置
    nacosSource := nacos.NewSource(&cfg.Nacos)
    if err := config.MergeSource(nacosSource); err != nil {
        panic(err)
    }
    // Nacos 上的配置变更会自动触发热更新回调
}
```

本地 `config.yaml` 中配置 Nacos 连接信息：

```yaml
nacos:
  addr: 127.0.0.1
  port: 8848
  username: nacos
  password: nacos
  namespace: public
  group: DEFAULT_GROUP
  data_id: template-app.yml
```

#### Nacos 配置优先级

- 本地 YAML 文件提供基础配置
- Nacos 远程配置覆盖同名字段
- 环境变量（`APP_` 前缀）具有最高优先级
- Nacos 配置变更通过 `config.AddWatch` 注册的回调自动生效

#### 从 Nacos 直接读取配置（不通过 Source 接口）

```go
import (
    "go-template/internal/config"
    "go-template/internal/nacos"
    nacosClient "github.com/nacos-group/nacos-sdk-go/v2/clients"
    "github.com/nacos-group/nacos-sdk-go/v2/clients/config_client"
)

// 如果你只需要一次性读取 Nacos 配置（不需要热更新）
type AppSettings struct {
    FeatureFlags map[string]bool `yaml:"feature_flags"`
}

// 使用泛型辅助函数
var client config_client.IConfigClient // 需自行初始化 Nacos 客户端
settings, err := nacos.GetConfig[AppSettings](client, &config.NacosConfig{
    Group:  "DEFAULT_GROUP",
    DataId: "settings.yml",
})
```

### 自定义远程配置源

实现 `config.Source` 接口即可接入任意配置中心：

```go
type Source interface {
    Name() string
    Init() (content []byte, changes <-chan []byte, err error)
    Close() error
}
```

示例：接入 Consul

```go
type consulSource struct {
    client *consul.Client
    key    string
}

func (s *consulSource) Name() string { return "consul" }

func (s *consulSource) Init() ([]byte, <-chan []byte, error) {
    // 获取初始配置内容
    // 返回变更通知 channel
}

func (s *consulSource) Close() error {
    return nil
}
```

## 构建

```bash
# 开发构建
go build -o bin/app ./cmd/app

# 生产构建（注入版本信息）
go build -ldflags "\
  -X go-template/pkg/version.Version=1.0.0 \
  -X go-template/pkg/version.Commit=$(git rev-parse --short HEAD) \
  -X go-template/pkg/version.Date=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  -o bin/app ./cmd/app

# GoReleaser 快照构建
goreleaser release --snapshot --skip=publish --clean -f build/.goreleaser.yaml
```

## 开发命令

```bash
# 格式化代码
go fmt ./...

# 静态检查
go vet ./...

# 运行所有测试
go test ./...

# 带竞态检测
go test -race ./...

# 生成覆盖率报告
go test -coverprofile=coverage.out ./...

# 代码检查（需要 golangci-lint）
golangci-lint run ./...
```

## 启动流程

应用从 `cmd/app/main.go` 调用 `cmd.Execute()`，进入 Cobra 根命令：

1. **加载配置** — 读取 YAML 文件，合并远程配置源，设置环境变量覆盖
2. **初始化日志** — 基于配置构建 Zap logger
3. **注册日志热更新** — 当日志相关配置变更时，自动重建 logger
4. **信号处理** — 创建可被 SIGINT/SIGTERM/SIGQUIT 取消的 context
5. **启动可观测性** — 如果 `observability.enabled: true`，启动独立 HTTP 服务器
6. **运行业务逻辑** — `app.Run(ctx)` 阻塞直到收到关闭信号

## License

MIT
