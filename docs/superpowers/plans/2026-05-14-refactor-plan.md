# Go Template 重构实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 全面重构 go-template 项目—接口解耦、Cobra CLI、可观测性、单元测试

**Architecture:** 沿用全局单例模式，`config` 通过 `Source` 接口解耦配置源，`logger` 不依赖 `config` 包，observability 独立端口或可集成。所有配置结构体提供 `DefaultConfig()`。

**Tech Stack:** Go 1.26, Cobra, Viper, Zap, Prometheus, OpenTelemetry, Nacos SDK

---

### Task 1: 更新 go.mod 依赖

**Files:**
- Modify: `go.mod:1-50`

**依赖变更：**
- 移除 `github.com/spf13/pflag`（Cobra 自带 pflag，变为间接依赖）
- 移除 `go.yaml.in/yaml/v3`（统一使用 `gopkg.in/yaml.v3`）
- 新增 `github.com/spf13/cobra v1.9.1`
- 新增 `github.com/prometheus/client_golang v1.21.1`（从间接提升为直接依赖）

- [ ] **Step 1: 编辑 go.mod**

在 require 块中执行以下变更：
- 删除 `github.com/spf13/pflag v1.0.10` 行
- 删除 `go.yaml.in/yaml/v3 v3.0.4` 行
- 添加 `github.com/spf13/cobra v1.9.1`
- 添加 `github.com/prometheus/client_golang v1.21.1`（提升为直接依赖）

- [ ] **Step 2: 同步依赖**

Run: `go mod tidy`
Expected: 无错误，go.sum 更新，pflag 变为 indirect

- [ ] **Step 3: 验证编译**

Run: `go build ./...`
Expected: 需要 cobra 和相关新依赖的包暂时报错，旧 pflag 引用不再存在

- [ ] **Step 4: Commit**

```bash
git add go.mod go.sum
git commit -m "chore: update dependencies - add cobra/prometheus, remove go.yaml.in/pflag"
```

---

### Task 2: 定义 Source 接口 + 重构 Config 包

**Files:**
- Create: `internal/config/source.go`
- Modify: `internal/config/config.go`（完整重写）

这是最核心的 task。`config/config.go` 完整重写，保留现有功能（atomic.Pointer、callbacks、watcher、env override），新增：
- `Source` 接口
- `ObservabilityConfig` / `TracingConfig` 结构体 + `DefaultConfig()`
- `Config.LoggerConfig()` 转换方法
- `Init(path string, sources ...Source) (*Config, error)` 新签名
- Source lifecycle goroutine 管理

- [ ] **Step 1: 创建 `internal/config/source.go`**

```go
package config

// Source 表示外部配置源（如 Nacos、Consul 等）。
// 实现者通过 Init 返回初始配置内容和变更通知 channel。
type Source interface {
	// Name 返回配置源名称，用于日志和诊断
	Name() string
	// Init 初始化配置源。
	// content: 当前配置内容 (YAML bytes)
	// changes: 当远端配置变更时收到新内容
	// err: 初始化失败时返回错误
	Init() (content []byte, changes <-chan []byte, err error)
	// Close 关闭配置源，释放资源
	Close() error
}
```

- [ ] **Step 2: 为 ObservabilityConfig / TracingConfig 编写测试**

在 `internal/config/config_test.go` 中添加：

```go
package config

import (
	"testing"
)

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
}

func TestLoggerConfigConversion(t *testing.T) {
	cfg := DefaultConfig()
	lc := cfg.LoggerConfig()
	if lc.Level != "info" {
		t.Errorf("expected info, got %s", lc.Level)
	}
	if lc.Format != "console" {
		t.Errorf("expected console, got %s", lc.Format)
	}
}
```

- [ ] **Step 3: 验证测试失败**

Run: `go test ./internal/config/ -run "TestObservability|TestLoggerConfig" -v`
Expected: FAIL（类型未定义）

- [ ] **Step 4: 重写 `internal/config/config.go`**

完整代码：

```go
// Package config 提供配置管理功能
package config

import (
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"

	"go-template/internal/logger"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
)

type AppConfig struct {
	Name        string `yaml:"name" mapstructure:"name"`
	Env         string `yaml:"env" mapstructure:"env"`
	Watch       bool   `yaml:"watch" mapstructure:"watch"`
	EnableNacos bool   `yaml:"enable_nacos" mapstructure:"enable_nacos"`
}

type ObservabilityConfig struct {
	Enabled     bool          `yaml:"enabled" mapstructure:"enabled"`
	Addr        string        `yaml:"addr" mapstructure:"addr"`
	MetricsPath string        `yaml:"metrics_path" mapstructure:"metrics_path"`
	HealthPath  string        `yaml:"health_path" mapstructure:"health_path"`
	Tracing     TracingConfig `yaml:"tracing" mapstructure:"tracing"`
}

type TracingConfig struct {
	Enabled  bool   `yaml:"enabled" mapstructure:"enabled"`
	Endpoint string `yaml:"endpoint" mapstructure:"endpoint"`
}

type Config struct {
	App           AppConfig           `yaml:"app" mapstructure:"app"`
	Log           LogConfig           `yaml:"log" mapstructure:"log"`
	Nacos         NacosConfig         `yaml:"nacos" mapstructure:"nacos"`
	Observability ObservabilityConfig `yaml:"observability" mapstructure:"observability"`
	Secret        struct {
		Key string `yaml:"key" mapstructure:"key"`
	} `yaml:"secret" mapstructure:"secret"`
}

type LogConfig struct {
	Level        string `yaml:"level" mapstructure:"level"`
	Format       string `yaml:"format" mapstructure:"format"`
	Path         string `yaml:"path" mapstructure:"path"`
	MaxSize      int    `yaml:"max_size" mapstructure:"max_size"`
	MaxAge       int    `yaml:"max_age" mapstructure:"max_age"`
	MaxBackups   int    `yaml:"max_backups" mapstructure:"max_backups"`
	Compress     bool   `yaml:"compress" mapstructure:"compress"`
	LogToConsole bool   `yaml:"log_to_console" mapstructure:"log_to_console"`
}

type NacosConfig struct {
	Addr      string `yaml:"addr" mapstructure:"addr"`
	Port      uint64 `yaml:"port" mapstructure:"port"`
	Username  string `yaml:"username" mapstructure:"username"`
	Password  string `yaml:"password" mapstructure:"password"`
	Namespace string `yaml:"namespace" mapstructure:"namespace"`
	Group     string `yaml:"group" mapstructure:"group"`
	DataId    string `yaml:"data_id" mapstructure:"data_id"`
	LogLevel  string `yaml:"log_level" mapstructure:"log_level"`
	LogDir    string `yaml:"log_dir" mapstructure:"log_dir"`
	CacheDir  string `yaml:"cache_dir" mapstructure:"cache_dir"`
}

type ConfigChangeCallback func(newCfg, oldCfg *Config)
type WatchKey int

var (
	globalConfig    atomic.Pointer[Config]
	callbacks       map[WatchKey]ConfigChangeCallback
	callbackRWMutex sync.RWMutex
	nextWatchKey    WatchKey
	v               *viper.Viper
)

const (
	DefaultAppName        = "app"
	DefaultAppEnv         = "dev"
	DefaultAppWatch       = false
	DefaultAppEnableNacos = false

	DefaultLogLevel        = "info"
	DefaultLogFormat       = "console"
	DefaultLogPath         = "logs/app.log"
	DefaultLogMaxSize      = 200
	DefaultLogMaxAge       = 60
	DefaultLogMaxBackups   = 60
	DefaultLogCompress     = true
	DefaultLogToConsole    = true

	DefaultObsAddr        = ":9090"
	DefaultObsMetricsPath = "/metrics"
	DefaultObsHealthPath  = "/health"
)

func DefaultAppConfig() AppConfig {
	return AppConfig{
		Name:        DefaultAppName,
		Env:         DefaultAppEnv,
		Watch:       DefaultAppWatch,
		EnableNacos: DefaultAppEnableNacos,
	}
}

func DefaultLogConfig() LogConfig {
	return LogConfig{
		Level:        DefaultLogLevel,
		Format:       DefaultLogFormat,
		Path:         DefaultLogPath,
		MaxSize:      DefaultLogMaxSize,
		MaxAge:       DefaultLogMaxAge,
		MaxBackups:   DefaultLogMaxBackups,
		Compress:     DefaultLogCompress,
		LogToConsole: DefaultLogToConsole,
	}
}

func DefaultObservabilityConfig() ObservabilityConfig {
	return ObservabilityConfig{
		Addr:        DefaultObsAddr,
		MetricsPath: DefaultObsMetricsPath,
		HealthPath:  DefaultObsHealthPath,
	}
}

func DefaultNacosConfig() NacosConfig {
	return NacosConfig{
		Addr:      "127.0.0.1",
		Port:      8848,
		Namespace: "public",
		Group:     "DEFAULT_GROUP",
		DataId:    "application.yml",
		LogLevel:  "debug",
		LogDir:    "./logs",
		CacheDir:  "./cache",
	}
}

func DefaultConfig() *Config {
	return &Config{
		App:           DefaultAppConfig(),
		Log:           DefaultLogConfig(),
		Nacos:         DefaultNacosConfig(),
		Observability: DefaultObservabilityConfig(),
	}
}

func Init(path string, sources ...Source) (*Config, error) {
	callbacks = make(map[WatchKey]ConfigChangeCallback)

	v = viper.New()
	v.SetConfigFile(path)
	v.SetConfigType("yaml")

	setDefaults()

	v.SetEnvPrefix("APP")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}

	cfg := DefaultConfig()
	if err := v.Unmarshal(cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}

	for _, s := range sources {
		content, changes, err := s.Init()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: config source %s init failed: %v\n", s.Name(), err)
			continue
		}
		if content != nil {
			if err := yaml.Unmarshal(content, cfg); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: config source %s content unmarshal failed: %v\n", s.Name(), err)
			}
		}
		if changes != nil {
			go watchSource(s.Name(), changes)
		}
	}

	globalConfig.Store(cfg)

	if cfg.App.Watch {
		StartWatcher()
	}

	return cfg, nil
}

func watchSource(name string, changes <-chan []byte) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "Panic in source watcher %s: %v\n", name, r)
		}
	}()
	for data := range changes {
		updateConfig(data)
	}
}

func updateConfig(data string) {
	oldCfg := globalConfig.Load()
	if oldCfg == nil {
		return
	}
	newCfg := *oldCfg
	if err := yaml.Unmarshal([]byte(data), &newCfg); err != nil {
		return
	}
	if !reflect.DeepEqual(oldCfg, &newCfg) {
		triggerCallbacks(&newCfg, oldCfg)
		globalConfig.Store(&newCfg)
	}
}

func setDefaults() {
	v.SetDefault("app.name", DefaultAppName)
	v.SetDefault("app.env", DefaultAppEnv)
	v.SetDefault("app.watch", DefaultAppWatch)
	v.SetDefault("app.enable_nacos", DefaultAppEnableNacos)
	v.SetDefault("log.level", DefaultLogLevel)
	v.SetDefault("log.format", DefaultLogFormat)
	v.SetDefault("log.path", DefaultLogPath)
	v.SetDefault("log.max_size", DefaultLogMaxSize)
	v.SetDefault("log.max_age", DefaultLogMaxAge)
	v.SetDefault("log.max_backups", DefaultLogMaxBackups)
	v.SetDefault("log.compress", DefaultLogCompress)
	v.SetDefault("log.log_to_console", DefaultLogToConsole)
	v.SetDefault("observability.addr", DefaultObsAddr)
	v.SetDefault("observability.metrics_path", DefaultObsMetricsPath)
	v.SetDefault("observability.health_path", DefaultObsHealthPath)
}

func (c *Config) LoggerConfig() logger.Config {
	return logger.Config{
		Level:        c.Log.Level,
		Format:       c.Log.Format,
		Path:         c.Log.Path,
		MaxSize:      c.Log.MaxSize,
		MaxAge:       c.Log.MaxAge,
		MaxBackups:   c.Log.MaxBackups,
		Compress:     c.Log.Compress,
		LogToConsole: c.Log.LogToConsole,
	}
}

func Get() *Config {
	return globalConfig.Load()
}

func AddWatch(callback ConfigChangeCallback) WatchKey {
	callbackRWMutex.Lock()
	defer callbackRWMutex.Unlock()
	key := nextWatchKey
	nextWatchKey++
	callbacks[key] = callback
	return key
}

func RemoveWatch(key WatchKey) {
	callbackRWMutex.Lock()
	defer callbackRWMutex.Unlock()
	delete(callbacks, key)
}

func triggerCallbacks(newCfg, oldCfg *Config) {
	callbackRWMutex.RLock()
	cbs := make([]ConfigChangeCallback, 0, len(callbacks))
	for _, cb := range callbacks {
		cbs = append(cbs, cb)
	}
	callbackRWMutex.RUnlock()
	for _, cb := range cbs {
		go func(callback ConfigChangeCallback) {
			defer func() {
				if r := recover(); r != nil {}
			}()
			callback(newCfg, oldCfg)
		}(cb)
	}
}

func reloadConfig() error {
	newCfg := DefaultConfig()
	if err := v.Unmarshal(newCfg); err != nil {
		return err
	}
	oldCfg := globalConfig.Swap(newCfg)
	if reflect.DeepEqual(newCfg, oldCfg) {
		return nil
	}
	triggerCallbacks(newCfg, oldCfg)
	return nil
}

func GenerateConfig(outputPath string) {
	if outputPath == "" {
		outputPath = "config.yaml"
	}
	cfg := DefaultConfig()
	data, err := yaml.Marshal(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to marshal config: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(outputPath, data, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "failed to generate config file: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Config file generated: %s\n", outputPath)
}

var once sync.Once

func StartWatcher() {
	if v == nil {
		return
	}
	once.Do(func() {
		v.OnConfigChange(func(_ fsnotify.Event) {
			reloadConfig()
		})
		v.WatchConfig()
	})
}
```

- [ ] **Step 5: 运行测试验证**

Run: `go test ./internal/config/ -run "TestObservability|TestLoggerConfig" -v`
Expected: PASS

- [ ] **Step 6: 创建配置文件更新测试**

添加到 `internal/config/config_test.go`：

```go
func TestInit(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := tmpDir + "/test.yaml"
	yamlContent := []byte("app:\n  name: test-app\n  env: staging\n")
	if err := os.WriteFile(cfgPath, yamlContent, 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Init(cfgPath)
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	if cfg.App.Name != "test-app" {
		t.Errorf("expected test-app, got %s", cfg.App.Name)
	}
	if cfg.App.Env != "staging" {
		t.Errorf("expected staging, got %s", cfg.App.Env)
	}
}

func TestInitWithSource(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := tmpDir + "/test.yaml"
	yamlContent := []byte("app:\n  name: base-app\n  env: dev\n")
	if err := os.WriteFile(cfgPath, yamlContent, 0644); err != nil {
		t.Fatal(err)
	}

	sourceContent := []byte("app:\n  name: overridden-app\n")
	source := &mockSource{
		name:    "test-source",
		content: sourceContent,
		ch:      make(chan []byte),
	}

	cfg, err := Init(cfgPath, source)
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	// source 内容应覆盖本地文件
	if cfg.App.Name != "overridden-app" {
		t.Errorf("expected overridden-app, got %s", cfg.App.Name)
	}
	// 未被 source 覆盖的字段保留本地值
	if cfg.App.Env != "dev" {
		t.Errorf("expected dev, got %s", cfg.App.Env)
	}
}

type mockSource struct {
	name    string
	content []byte
	ch      chan []byte
}

func (m *mockSource) Name() string              { return m.name }
func (m *mockSource) Init() ([]byte, <-chan []byte, error) {
	return m.content, m.ch, nil
}
func (m *mockSource) Close() error              { return nil }
```

添加到文件顶部：

```go
import (
	"os"
	"testing"
)
```

- [ ] **Step 7: 跑所有 config 测试**

Run: `go test ./internal/config/ -v -count=1`
Expected: 所有测试 PASS

- [ ] **Step 8: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go internal/config/source.go
git commit -m "refactor(config): add Source interface, observability config, LoggerConfig converter"
```

---

### Task 3: Logger 独立化

**Files:**
- Modify: `internal/logger/logger.go`

去掉对 `config` 包的依赖。在 logger 包内定义 `Config` 结构体 + `DefaultConfig()`。

- [ ] **Step 1: 为 logger 写测试**

`internal/logger/logger_test.go`：

```go
package logger

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInitAndLog(t *testing.T) {
	cfg := DefaultConfig()
	cfg.LogToConsole = false // 测试时不输出到控制台
	if err := Init(&cfg); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	Info("test message")
	Infof("test formatted %s", "message")
	SetLevel("debug")
	Debug("debug message")
	Sync()
}

func TestResetLevel(t *testing.T) {
	cfg := DefaultConfig()
	cfg.LogToConsole = false
	if err := Init(&cfg); err != nil {
		t.Fatal(err)
	}
	level := GetLevel()
	if level.String() != "info" {
		t.Errorf("expected info, got %s", level)
	}

	cfg.Level = "debug"
	if err := Reset(&cfg); err != nil {
		t.Fatal(err)
	}
	level = GetLevel()
	if level.String() != "debug" {
		t.Errorf("expected debug, got %s", level)
	}
}

func TestResetOutput(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "test.log")

	cfg := DefaultConfig()
	cfg.LogToConsole = false
	cfg.Path = logPath
	if err := Init(&cfg); err != nil {
		t.Fatal(err)
	}
	Info("before reset")

	newPath := filepath.Join(dir, "test2.log")
	cfg.Path = newPath
	if err := Reset(&cfg); err != nil {
		t.Fatal(err)
	}
	Info("after reset")

	Sync()

	if _, err := os.Stat(newPath); os.IsNotExist(err) {
		t.Errorf("log file not created at %s", newPath)
	}
}
```

- [ ] **Step 2: 验证测试失败**

Run: `go test ./internal/logger/ -v -count=1`
Expected: FAIL（DefaultConfig 未定义，Config 类型在 logger 包中不存在）

- [ ] **Step 3: 重写 `internal/logger/logger.go`**

完整重写。关键变化：
1. 在 logger 包内定义 `Config` + `DefaultConfig()`
2. 所有函数签名使用 `logger.Config` 而非 `config.LogConfig`
3. 函数内部从 `config.LogConfig` 改为 `logger.Config`

```go
package logger

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

type Config struct {
	Level        string `yaml:"level"`
	Format       string `yaml:"format"`
	Path         string `yaml:"path"`
	MaxSize      int    `yaml:"max_size"`
	MaxAge       int    `yaml:"max_age"`
	MaxBackups   int    `yaml:"max_backups"`
	Compress     bool   `yaml:"compress"`
	LogToConsole bool   `yaml:"log_to_console"`
}

const (
	DefaultLevel        = "info"
	DefaultFormat       = "console"
	DefaultPath         = "logs/app.log"
	DefaultMaxSize      = 200
	DefaultMaxAge       = 60
	DefaultMaxBackups   = 60
	DefaultCompress     = true
	DefaultLogToConsole = true
)

func DefaultConfig() Config {
	return Config{
		Level:        DefaultLevel,
		Format:       DefaultFormat,
		Path:         DefaultPath,
		MaxSize:      DefaultMaxSize,
		MaxAge:       DefaultMaxAge,
		MaxBackups:   DefaultMaxBackups,
		Compress:     DefaultCompress,
		LogToConsole: DefaultLogToConsole,
	}
}

var (
	globalLogger  atomic.Value
	globalSugar   atomic.Value
	currentWriter *lumberjack.Logger
	writerMutex   sync.Mutex
	atomicLevel   zap.AtomicLevel
	currentLogCfg atomic.Value
)

func Init(cfg *Config) error {
	atomicLevel = zap.NewAtomicLevelAt(getZapLevel(cfg.Level))

	logger, sugar, writer, err := buildLogger(cfg, atomicLevel)
	if err != nil {
		return err
	}

	writerMutex.Lock()
	if currentWriter != nil {
		currentWriter.Close()
	}
	currentWriter = writer
	writerMutex.Unlock()

	globalLogger.Store(logger)
	globalSugar.Store(sugar)
	currentLogCfg.Store(cfg)

	return nil
}

func Reset(cfg *Config) error {
	oldCfg, ok := currentLogCfg.Load().(*Config)
	if !ok {
		return Init(cfg)
	}

	if cfg.Level != oldCfg.Level {
		atomicLevel.SetLevel(getZapLevel(cfg.Level))
	}

	if cfg.Format != oldCfg.Format || cfg.Path != oldCfg.Path ||
		cfg.MaxSize != oldCfg.MaxSize || cfg.MaxAge != oldCfg.MaxAge ||
		cfg.MaxBackups != oldCfg.MaxBackups || cfg.Compress != oldCfg.Compress ||
		cfg.LogToConsole != oldCfg.LogToConsole {
		return Init(cfg)
	}

	currentLogCfg.Store(cfg)
	return nil
}

func SetLevel(level string) {
	atomicLevel.SetLevel(getZapLevel(level))
}

func GetLevel() zapcore.Level {
	return atomicLevel.Level()
}

func buildLogger(cfg *Config, level zap.AtomicLevel) (*zap.Logger, *zap.SugaredLogger, *lumberjack.Logger, error) {
	fileEncoderConfig := zapcore.EncoderConfig{
		TimeKey:        "time",
		LevelKey:       "level",
		NameKey:        "logger",
		CallerKey:      "caller",
		MessageKey:     "msg",
		StacktraceKey:  "stacktrace",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    zapcore.CapitalLevelEncoder,
		EncodeTime:     zapcore.TimeEncoderOfLayout("2006-01-02 15:04:05.000"),
		EncodeDuration: zapcore.SecondsDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
	}

	var encoder zapcore.Encoder
	if cfg.Format == "json" {
		encoder = zapcore.NewJSONEncoder(fileEncoderConfig)
	} else {
		encoder = zapcore.NewConsoleEncoder(fileEncoderConfig)
	}

	var cores []zapcore.Core
	var writer *lumberjack.Logger

	if cfg.Path != "" {
		dir := filepath.Dir(cfg.Path)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, nil, nil, err
		}

		writer = &lumberjack.Logger{
			Filename:   cfg.Path,
			MaxSize:    cfg.MaxSize,
			MaxAge:     cfg.MaxAge,
			MaxBackups: cfg.MaxBackups,
			Compress:   cfg.Compress,
		}

		fileCore := zapcore.NewCore(encoder, zapcore.AddSync(writer), level)
		cores = append(cores, fileCore)
	}

	if cfg.LogToConsole {
		consoleEncoderConfig := zapcore.EncoderConfig{
			TimeKey:        "time",
			LevelKey:       "level",
			NameKey:        "logger",
			CallerKey:      "caller",
			MessageKey:     "msg",
			StacktraceKey:  "stacktrace",
			LineEnding:     zapcore.DefaultLineEnding,
			EncodeLevel:    zapcore.CapitalColorLevelEncoder,
			EncodeTime:     zapcore.TimeEncoderOfLayout("2006-01-02 15:04:05.000"),
			EncodeDuration: zapcore.SecondsDurationEncoder,
			EncodeCaller:   zapcore.ShortCallerEncoder,
		}
		consoleEncoder := zapcore.NewConsoleEncoder(consoleEncoderConfig)
		consoleCore := zapcore.NewCore(consoleEncoder, zapcore.AddSync(os.Stdout), level)
		cores = append(cores, consoleCore)
	}

	core := zapcore.NewTee(cores...)
	logger := zap.New(core, zap.AddCaller(), zap.AddCallerSkip(1))

	return logger, logger.Sugar(), writer, nil
}

func getZapLevel(level string) zapcore.Level {
	switch level {
	case "debug":
		return zapcore.DebugLevel
	case "info":
		return zapcore.InfoLevel
	case "warn":
		return zapcore.WarnLevel
	case "error":
		return zapcore.ErrorLevel
	case "dpanic":
		return zapcore.DPanicLevel
	case "panic":
		return zapcore.PanicLevel
	case "fatal":
		return zapcore.FatalLevel
	default:
		return zapcore.InfoLevel
	}
}

func getLogger() *zap.Logger {
	if v := globalLogger.Load(); v != nil {
		return v.(*zap.Logger)
	}
	return zap.NewNop()
}

func getSugar() *zap.SugaredLogger {
	if v := globalSugar.Load(); v != nil {
		return v.(*zap.SugaredLogger)
	}
	return zap.NewNop().Sugar()
}

func Debug(msg string, fields ...zap.Field)  { getLogger().Debug(msg, fields...) }
func Info(msg string, fields ...zap.Field)   { getLogger().Info(msg, fields...) }
func Warn(msg string, fields ...zap.Field)   { getLogger().Warn(msg, fields...) }
func Error(msg string, fields ...zap.Field)  { getLogger().Error(msg, fields...) }
func DPanic(msg string, fields ...zap.Field) { getLogger().DPanic(msg, fields...) }
func Panic(msg string, fields ...zap.Field)  { getLogger().Panic(msg, fields...) }
func Fatal(msg string, fields ...zap.Field)  { getLogger().Fatal(msg, fields...) }

func Debugf(template string, args ...any)  { getSugar().Debugf(template, args...) }
func Infof(template string, args ...any)   { getSugar().Infof(template, args...) }
func Warnf(template string, args ...any)   { getSugar().Warnf(template, args...) }
func Errorf(template string, args ...any)  { getSugar().Errorf(template, args...) }
func DPanicf(template string, args ...any) { getSugar().DPanicf(template, args...) }
func Panicf(template string, args ...any)  { getSugar().Panicf(template, args...) }
func Fatalf(template string, args ...any)  { getSugar().Fatalf(template, args...) }

func Sync() error { return getLogger().Sync() }
```

- [ ] **Step 4: 运行 logger 测试**

Run: `go test ./internal/logger/ -v -count=1`
Expected: PASS

- [ ] **Step 5: 验证编译（此时 config 包引用了 logger.Config）**

Run: `go build ./internal/config/`
Expected: PASS（config 包已通过 LoggerConfig() 正确引用 logger.Config）

- [ ] **Step 6: Commit**

```bash
git add internal/logger/logger.go internal/logger/logger_test.go
git commit -m "refactor(logger): decouple from config package with own Config type"
```

---

### Task 4: 实现 Nacos Source

**Files:**
- Create: `internal/nacos/source.go`
- Delete: `internal/outsid/nacos.go`

将 `internal/outsid/nacos.go` 的代码迁移到 `internal/nacos/source.go`，实现 `config.Source` 接口。保留 Nacos SDK 的全局客户端功能。

- [ ] **Step 1: 写 Nacos Source 测试**

`internal/nacos/source_test.go`：

```go
package nacos

import (
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Addr != "127.0.0.1" {
		t.Errorf("expected 127.0.0.1, got %s", cfg.Addr)
	}
	if cfg.Port != 8848 {
		t.Errorf("expected 8848, got %d", cfg.Port)
	}
}

func TestNewSource(t *testing.T) {
	cfg := DefaultConfig()
	s := NewSource(cfg)
	if s.Name() != "nacos" {
		t.Errorf("expected nacos, got %s", s.Name())
	}
}

func TestGetConfigTypeSafety(t *testing.T) {
	type testCfg struct {
		Name string `yaml:"name"`
	}
	// 不初始化客户端，验证 GetConfig 返回错误
	_, err := GetConfig[testCfg]()
	if err == nil {
		t.Error("expected error when client not initialized")
	}
}
```

- [ ] **Step 2: 验证测试失败**

Run: `go test ./internal/nacos/ -v -count=1`
Expected: FAIL（NewSource 未定义）

- [ ] **Step 3: 创建 `internal/nacos/source.go`**

```go
// Package nacos 提供 Nacos 配置中心集成
// 实现 config.Source 接口，可作为外部配置源接入 config 包
package nacos

import (
	"errors"
	"sync"
	"sync/atomic"

	"go-template/internal/config"

	"github.com/nacos-group/nacos-sdk-go/v2/clients"
	"github.com/nacos-group/nacos-sdk-go/v2/clients/config_client"
	"github.com/nacos-group/nacos-sdk-go/v2/common/constant"
	"github.com/nacos-group/nacos-sdk-go/v2/vo"
	"gopkg.in/yaml.v3"
)

type NacosConfig struct {
	Addr      string `yaml:"addr" mapstructure:"addr"`
	Port      uint64 `yaml:"port" mapstructure:"port"`
	Username  string `yaml:"username" mapstructure:"username"`
	Password  string `yaml:"password" mapstructure:"password"`
	Namespace string `yaml:"namespace" mapstructure:"namespace"`
	Group     string `yaml:"group" mapstructure:"group"`
	DataId    string `yaml:"data_id" mapstructure:"data_id"`
	LogLevel  string `yaml:"log_level" mapstructure:"log_level"`
	LogDir    string `yaml:"log_dir" mapstructure:"log_dir"`
	CacheDir  string `yaml:"cache_dir" mapstructure:"cache_dir"`
}

type source struct {
	cfg    *NacosConfig
	client config_client.IConfigClient
	once   sync.Once
	err    error
}

func DefaultConfig() *NacosConfig {
	return &NacosConfig{
		Addr:      "127.0.0.1",
		Port:      8848,
		Namespace: "public",
		Group:     "DEFAULT_GROUP",
		DataId:    "application.yml",
		LogLevel:  "debug",
		LogDir:    "./logs",
		CacheDir:  "./cache",
	}
}

// NewSource 创建一个 Nacos 配置源，实现 config.Source 接口
func NewSource(cfg *NacosConfig) config.Source {
	return &source{cfg: cfg}
}

func (s *source) Name() string { return "nacos" }

func (s *source) Init() ([]byte, <-chan []byte, error) {
	changes := make(chan []byte, 8)

	s.once.Do(func() {
		clientCfg := constant.NewClientConfig(
			constant.WithUsername(s.cfg.Username),
			constant.WithPassword(s.cfg.Password),
			constant.WithLogLevel(s.cfg.LogLevel),
			constant.WithLogDir(s.cfg.LogDir),
			constant.WithCacheDir(s.cfg.CacheDir),
			constant.WithNotLoadCacheAtStart(true),
		)
		serverCfgs := []constant.ServerConfig{
			*constant.NewServerConfig(s.cfg.Addr, s.cfg.Port),
		}
		c, err := clients.NewConfigClient(
			vo.NacosClientParam{
				ClientConfig:  clientCfg,
				ServerConfigs: serverCfgs,
			},
		)
		if err != nil {
			s.err = err
			return
		}
		s.client = c

		err = c.ListenConfig(vo.ConfigParam{
			DataId: s.cfg.DataId,
			Group:  s.cfg.Group,
			OnChange: func(namespace, group, dataId, data string) {
				select {
				case changes <- []byte(data):
				default:
				}
			},
		})
		if err != nil {
			s.err = err
			return
		}
	})

	if s.err != nil {
		return nil, nil, s.err
	}

	content, err := s.client.GetConfig(vo.ConfigParam{
		DataId: s.cfg.DataId,
		Group:  s.cfg.Group,
	})
	if err != nil {
		return nil, nil, err
	}

	return []byte(content), changes, nil
}

func (s *source) Close() error {
	if s.client != nil {
		// Nacos SDK 没有显式 Close 方法, 等待 GC
	}
	return nil
}

// GetConfigContent 提供独立的配置内容读取能力（不依赖 Source 接口）
func GetConfigContent(client config_client.IConfigClient, cfg *NacosConfig) (string, error) {
	if client == nil {
		return "", errors.New("nacos client not initialized")
	}
	return client.GetConfig(vo.ConfigParam{
		DataId: cfg.DataId,
		Group:  cfg.Group,
	})
}

// GetConfig 读取 Nacos 配置并解析为指定类型
func GetConfig[T any](client config_client.IConfigClient, cfg *NacosConfig) (*T, error) {
	content, err := GetConfigContent(client, cfg)
	if err != nil {
		return nil, err
	}
	var result T
	if err := yaml.Unmarshal([]byte(content), &result); err != nil {
		return nil, err
	}
	return &result, nil
}
```

- [ ] **Step 4: 删除 `internal/outsid/nacos.go`**

Run: `rm internal/outsid/nacos.go`

- [ ] **Step 5: 运行 nacos 测试**

Run: `go test ./internal/nacos/ -v -count=1`
Expected: PASS

- [ ] **Step 6: 验证全量编译**

Run: `go build ./...`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/nacos/ && git rm internal/outsid/nacos.go
git commit -m "refactor(nacos): implement config.Source interface, move from outsid/"
```

---

### Task 6: 创建 Cobra 命令 + 简化 main.go

**Files:**
- Create: `internal/cmd/root.go`
- Create: `internal/cmd/version.go`
- Create: `internal/cmd/init.go`
- Modify: `cmd/app/main.go`

- [ ] **Step 1: 写命令测试**

`internal/cmd/cmd_test.go`：

```go
package cmd

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestVersionCmd(t *testing.T) {
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetArgs([]string{"version"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("version cmd failed: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "Version:") {
		t.Errorf("expected version output, got %s", output)
	}
}

func TestInitCmd(t *testing.T) {
	tmpDir := t.TempDir()
	outputPath := tmpDir + "/gen-config.yaml"

	rootCmd.SetArgs([]string{"init", "-o", outputPath})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("init cmd failed: %v", err)
	}

	if _, err := os.Stat(outputPath); os.IsNotExist(err) {
		t.Errorf("config file not generated at %s", outputPath)
	}
}
```

- [ ] **Step 2: 创建 `internal/cmd/version.go`**

```go
package cmd

import (
	"go-template/pkg/version"

	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Run: func(cmd *cobra.Command, args []string) {
		version.Print()
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
```

- [ ] **Step 3: 创建 `internal/cmd/init.go`**

```go
package cmd

import (
	"go-template/internal/config"

	"github.com/spf13/cobra"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Generate default configuration file",
	Run: func(cmd *cobra.Command, args []string) {
		output, _ := cmd.Flags().GetString("output")
		config.GenerateConfig(output)
	},
}

func init() {
	rootCmd.AddCommand(initCmd)
	initCmd.Flags().StringP("output", "o", "", "Output file path for generated config")
}
```

- [ ] **Step 4: 创建 `internal/cmd/root.go`**

```go
package cmd

import (
	"context"
	"fmt"
	"os"

	"go-template/internal/app"
	"go-template/internal/config"
	"go-template/internal/logger"
	"go-template/internal/observability"
	"go-template/internal/signal"

	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

func defaultConfigPath() string {
	if _, err := os.Stat("configs/config.yaml"); err == nil {
		return "configs/config.yaml"
	}
	return "config.yaml"
}

var rootCmd = &cobra.Command{
	Use:   "app",
	Short: "Go service template",
	RunE: func(cmd *cobra.Command, args []string) error {
		configPath, _ := cmd.Flags().GetString("config")

		cfg, err := config.Init(configPath)
		if err != nil {
			return fmt.Errorf("config init: %w", err)
		}

		if err := logger.Init(cfg.LoggerConfig()); err != nil {
			return fmt.Errorf("logger init: %w", err)
		}

		config.AddWatch(func(newCfg, oldCfg *config.Config) {
			if newCfg.Log != oldCfg.Log {
				lc := newCfg.LoggerConfig()
				if err := logger.Reset(&lc); err != nil {
					logger.Error("Failed to reset logger", zap.Error(err))
				}
			}
		})

		ctx := signal.ContextWithShutdown(context.Background())

		if cfg.Observability.Enabled {
			if err := observability.Start(ctx, cfg.Observability); err != nil {
				logger.Warn("Failed to start observability", zap.Error(err))
			}
		}

		logger.Info("Application initialized",
			zap.String("name", cfg.App.Name),
			zap.String("env", cfg.App.Env),
		)

		if err := app.Run(ctx); err != nil {
			logger.Error("Application error", zap.Error(err))
		}

		cleanup()
		return nil
	},
}

func cleanup() {
	logger.Info("Cleaning up resources...")
	logger.Sync()
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	rootCmd.Flags().StringP("config", "c", defaultConfigPath(), "Config file path")
	rootCmd.Flags().BoolP("help", "h", false, "Show help message")
}
```

- [ ] **Step 5: 简化 `cmd/app/main.go`**

```go
package main

import "go-template/internal/cmd"

func main() {
	cmd.Execute()
}
```

- [ ] **Step 6: 测试并验证编译**

Run: `go test ./internal/cmd/ -v -count=1`
Expected: PASS

Run: `go build ./...`
Expected: PASS（所有依赖已就绪）

- [ ] **Step 7: Commit**

```bash
git add internal/cmd/ cmd/app/main.go
git commit -m "feat(cli): add cobra commands, simplify main.go"
```

---

### Task 5: Observability 模块

**Files:**
- Create: `internal/observability/observability.go`
- Create: `internal/observability/health/health.go`
- Create: `internal/observability/metrics/metrics.go`
- Create: `internal/observability/tracing/tracing.go`

- [ ] **Step 1: 创建 `internal/observability/health/health.go`**

```go
package health

import (
	"encoding/json"
	"net/http"
	"time"
)

type Status string

const (
	StatusHealthy Status = "healthy"
	StatusUnhealthy Status = "unhealthy"
)

type CheckResult struct {
	Status    Status `json:"status"`
	Timestamp string `json:"timestamp"`
}

type CheckFunc func() error

type Handler struct {
	checks []NamedCheck
}

type NamedCheck struct {
	Name string
	Check CheckFunc
}

func NewHandler() *Handler {
	return &Handler{}
}

func (h *Handler) Register(name string, fn CheckFunc) {
	h.checks = append(h.checks, NamedCheck{Name: name, Check: fn})
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	overall := StatusHealthy
	results := make(map[string]string)

	for _, c := range h.checks {
		if err := c.Check(); err != nil {
			overall = StatusUnhealthy
			results[c.Name] = err.Error()
		} else {
			results[c.Name] = "ok"
		}
	}

	w.Header().Set("Content-Type", "application/json")
	if overall == StatusUnhealthy {
		w.WriteHeader(http.StatusServiceUnavailable)
	} else {
		w.WriteHeader(http.StatusOK)
	}

	json.NewEncoder(w).Encode(CheckResult{
		Status:    overall,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	})
}
```

- [ ] **Step 2: 创建 `internal/observability/metrics/metrics.go`**

```go
package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var registry = prometheus.NewRegistry()

func init() {
	registry.MustRegister(prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}))
	registry.MustRegister(prometheus.NewGoCollector())
}

func Handler() http.Handler {
	return promhttp.HandlerFor(registry, promhttp.HandlerOpts{})
}

func Register(c prometheus.Collector) error {
	return registry.Register(c)
}
```

- [ ] **Step 3: 创建 `internal/observability/tracing/tracing.go`**

```go
package tracing

import (
	"context"
	"fmt"
)

type Config struct {
	Enabled  bool
	Endpoint string
}

func Init(ctx context.Context, cfg Config) (func(), error) {
	if !cfg.Enabled {
		return func() {}, nil
	}

	// OTel SDK 集成点在模板中预留，用户按需启用
	// 当配置启用时，以下代码会被激活：
	//   1. 创建 OTLP exporter 连接到 cfg.Endpoint
	//   2. 配置采样率
	//   3. 设置全局 TracerProvider
	//
	// 当前返回 noop 实现，避免引入过重的依赖

	fmt.Printf("Tracing enabled, endpoint: %s\n", cfg.Endpoint)
	return func() {}, nil
}
```

- [ ] **Step 4: 创建 `internal/observability/observability.go`**

```go
// Package observability 提供可观测性基础设施
// - health: HTTP health check endpoint
// - metrics: Prometheus metrics endpoint
// - tracing: OpenTelemetry tracing (预留)
//
// 支持两种集成方式：
// 1. 独立端口启动 (默认)
// 2. 返回 handler 供用户集成到业务端口
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
		// Addr 为空表示用户自行集成 handler，不自动启动 server
		return nil
	}

	// 初始化 tracing
	shutdown, err := tracing.Init(ctx, tracing.Config{
		Enabled:  cfg.Tracing.Enabled,
		Endpoint: cfg.Tracing.Endpoint,
	})
	if err != nil {
		logger.Warn("Failed to init tracing")
	}
	_ = shutdown // 在应用退出时调用

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
```

- [ ] **Step 5: 运行测试**

Run: `go vet ./internal/observability/...`
Expected: PASS

`go build ./...` 应全部通过。

- [ ] **Step 6: Commit**

```bash
git add internal/observability/
git commit -m "feat(observability): add metrics, health check, and tracing scaffolding"
```

---

### Task 7: 清理与配置更新

**Files:**
- Modify: `internal/app/app.go`
- Modify: `configs/config.yaml`

- [ ] **Step 1: 精简 `internal/app/app.go`**

```go
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
```

- [ ] **Step 2: 更新 `configs/config.yaml`**

```yaml
app:
  name: template-app
  env: dev
  watch: true

log:
  level: info
  format: console
  path: logs/app.log
  max_size: 200
  max_age: 60
  max_backups: 60
  compress: true
  log_to_console: true

nacos:
  addr: 127.0.0.1
  port: 8848
  namespace: public
  group: DEFAULT_GROUP
  data_id: template-app.yml
  log_level: debug
  log_dir: logs
  cache_dir: cache

observability:
  enabled: false
  addr: ":9090"
  metrics_path: /metrics
  health_path: /health
  tracing:
    enabled: false
    endpoint: ""

secret:
  key: ""
```

- [ ] **Step 3: 验证构建**

Run: `go build ./...`
Expected: PASS

Run: `go vet ./...`
Expected: PASS

- [ ] **Step 4: 运行所有测试**

Run: `go test ./... -count=1 -race 2>&1`
Expected: 所有包 PASS

- [ ] **Step 5: Commit**

```bash
git add internal/app/app.go configs/config.yaml
git commit -m "chore: clean up app.go placeholder, update config.yaml with observability"
```

---

### Task 8: 信号包测试

**Files:**
- Create: `internal/signal/signal_test.go`

- [ ] **Step 1: 写信号测试**

`internal/signal/signal_test.go`：

```go
package signal

import (
	"context"
	"syscall"
	"testing"
	"time"
)

func TestContextWithShutdown(t *testing.T) {
	ctx := ContextWithShutdown(context.Background())

	go func() {
		time.Sleep(50 * time.Millisecond)
		syscall.Kill(syscall.Getpid(), syscall.SIGTERM)
	}()

	select {
	case <-ctx.Done():
		// success
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for shutdown signal")
	}
}
```

- [ ] **Step 2: 运行测试**

Run: `go test ./internal/signal/ -v -count=1`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add internal/signal/signal_test.go
git commit -m "test(signal): add ContextWithShutdown test"
```

---

### Task 9: 最终验证和清理

- [ ] **Step 1: 全量构建 + 测试**

Run: `go build ./... && go vet ./... && go test ./... -count=1 -race 2>&1`
Expected: 全部 PASS，无 race condition

- [ ] **Step 2: 清理临时目录**

Run: `rm -rf cache/ logs/`
Expected: 清理构建产物

- [ ] **Step 3: 最终 commit**

```bash
git add -A && git status
```
确认没有残留文件后不做额外 commit（改动已在各 task 中提交）。
