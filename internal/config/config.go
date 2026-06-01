// Package config 提供配置管理功能
//
// 功能特点:
//   - 支持从 YAML 文件加载配置 (基于 Viper)
//   - 配置热更新 (通过 Viper WatchConfig)
//   - 线程安全的配置读取
//   - 支持配置变更监听
//   - 支持环境变量覆盖 (前缀 APP_, 如 APP_LOG_LEVEL=debug)
package config

import (
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"task-agent/internal/logger"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
)

// AppConfig 应用程序基础配置
type AppConfig struct {
	Name  string `yaml:"name" mapstructure:"name"`   // 应用名称
	Env   string `yaml:"env" mapstructure:"env"`     // 运行环境
	Watch bool   `yaml:"watch" mapstructure:"watch"` // 是否监控配置文件变更
}

// LogConfig 日志配置
type LogConfig struct {
	Level        string `yaml:"level" mapstructure:"level"`                   // 日志级别
	Format       string `yaml:"format" mapstructure:"format"`                 // 日志格式 (console/json)
	Path         string `yaml:"path" mapstructure:"path"`                     // 日志文件路径
	MaxSize      int    `yaml:"max_size" mapstructure:"max_size"`             // 单个日志文件最大大小 (MB)
	MaxAge       int    `yaml:"max_age" mapstructure:"max_age"`               // 日志文件保留天数
	MaxBackups   int    `yaml:"max_backups" mapstructure:"max_backups"`       // 保留的日志文件数量
	Compress     bool   `yaml:"compress" mapstructure:"compress"`             // 是否压缩历史日志
	LogToConsole bool   `yaml:"log_to_console" mapstructure:"log_to_console"` // 是否输出到控制台
}

// ObservabilityConfig 可观测性配置
type ObservabilityConfig struct {
	Enabled     bool       `yaml:"enabled" mapstructure:"enabled"`
	Addr        string     `yaml:"addr" mapstructure:"addr"`
	MetricsPath string     `yaml:"metrics_path" mapstructure:"metrics_path"`
	HealthPath  string     `yaml:"health_path" mapstructure:"health_path"`
	OTel        OTelConfig `yaml:"otel" mapstructure:"otel"`
}

// OTelConfig OpenTelemetry 配置
type OTelConfig struct {
	Endpoint string       `yaml:"endpoint" mapstructure:"endpoint"` // OTLP collector 地址
	Protocol string       `yaml:"protocol" mapstructure:"protocol"` // 协议: "grpc" 或 "http"
	Logs     SignalConfig `yaml:"logs" mapstructure:"logs"`         // 日志导出配置
	Traces   SignalConfig `yaml:"traces" mapstructure:"traces"`     // 链路导出配置
}

// SignalConfig 单个 OTel 信号的启用配置
type SignalConfig struct {
	Enabled bool `yaml:"enabled" mapstructure:"enabled"`
}

// Config 完整配置结构
type Config struct {
	App           AppConfig           `yaml:"app" mapstructure:"app"`
	Log           LogConfig           `yaml:"log" mapstructure:"log"`
	Observability ObservabilityConfig `yaml:"observability" mapstructure:"observability"`
	Secret        struct {
		Key string `yaml:"key" mapstructure:"key"`
	} `yaml:"secret" mapstructure:"secret"`
}

// ConfigChangeCallback 配置变更回调函数
// newCfg: 新的配置对象
// oldCfg: 旧的配置对象
type ConfigChangeCallback func(newCfg, oldCfg *Config)

// WatchKey 监听器唯一标识符
// 通过 AddWatch 返回，用于取消监听
type WatchKey int

var (
	globalConfig    atomic.Pointer[Config]            // 全局配置指针 (原子操作保证线程安全)
	callbacks       map[WatchKey]ConfigChangeCallback // 配置变更回调函数映射
	callbackRWMutex sync.RWMutex                      // 回调函数表的读写锁
	nextWatchKey    WatchKey                          // 下一个可用的 WatchKey
	v               *viper.Viper                      // Viper 实例
)

// 默认配置值
const (
	DefaultAppName        = "app"          // 默认应用名称
	DefaultAppEnv         = "dev"          // 默认运行环境
	DefaultAppWatch       = false          // 默认监控配置文件变更
	DefaultLogLevel       = "info"         // 默认日志级别
	DefaultLogFormat      = "console"      // 默认日志格式
	DefaultLogPath        = "logs/app.log" // 默认日志路径
	DefaultLogMaxSize     = 200            // 默认单个日志文件最大大小 (MB)
	DefaultLogMaxAge      = 60             // 默认日志文件保留天数
	DefaultLogMaxBackups  = 60             // 默认保留的日志文件数量
	DefaultLogCompress    = true           // 默认启用日志压缩
	DefaultLogToConsole   = true           // 默认启用控制台输出
	DefaultObsAddr        = ":9090"
	DefaultObsMetricsPath = "/metrics"
	DefaultObsHealthPath  = "/health"
	DefaultOTelEndpoint   = "localhost:4317"
	DefaultOTelProtocol   = "grpc"
)

// DefaultAppConfig 返回默认应用配置
func DefaultAppConfig() AppConfig {
	return AppConfig{
		Name:  DefaultAppName,
		Env:   DefaultAppEnv,
		Watch: DefaultAppWatch,
	}
}

// DefaultLogConfig 返回默认日志配置
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

// DefaultObservabilityConfig 返回默认可观测性配置
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

// DefaultConfig 返回默认配置
//
// 返回值:
//   - *Config: 包含所有默认值的配置对象
func DefaultConfig() *Config {
	return &Config{
		App:           DefaultAppConfig(),
		Log:           DefaultLogConfig(),
		Observability: DefaultObservabilityConfig(),
	}
}

// Init 初始化配置系统
//
// 参数:
//   - path: 配置文件路径
//   - sources: 可选的外部配置源列表
//
// 返回值:
//   - *Config: 初始化的配置对象
//   - error: 加载配置失败时返回错误
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
		return nil, err
	}

	cfg := DefaultConfig()
	if err := v.Unmarshal(cfg); err != nil {
		return nil, err
	}

	// 从外部配置源合并
	for _, s := range sources {
		content, changes, err := s.Init()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: source %s init failed: %v\n", s.Name(), err)
			continue
		}
		if content != nil {
			if err := yaml.Unmarshal(content, cfg); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: source %s content unmarshal failed: %v\n", s.Name(), err)
			}
		}
		if changes != nil {
			go watchSource(s.Name(), changes)
		}
	}

	if cfg.App.Watch {
		StartWatcher()
	}
	globalConfig.Store(cfg)
	return cfg, nil
}

// MergeSource 在 Init 之后合并外部配置源的内容
//
// 用于两阶段初始化场景：先用本地配置获取连接参数，再连接远程配置中心。
// Nacos 的 addr/port/namespace 等从本地配置文件读取，业务配置从远程获取。
//
// 参数:
//   - source: 外部配置源
//
// 返回值:
//   - error: 合并失败时返回错误
func MergeSource(source Source) error {
	content, changes, err := source.Init()
	if err != nil {
		return err
	}

	oldCfg := globalConfig.Load()
	if oldCfg == nil {
		return fmt.Errorf("config not initialized, call Init first")
	}

	newCfg := *oldCfg
	if content != nil {
		if err := yaml.Unmarshal(content, &newCfg); err != nil {
			return err
		}
	}

	if !reflect.DeepEqual(*oldCfg, newCfg) {
		triggerCallbacks(&newCfg, oldCfg)
		globalConfig.Store(&newCfg)
	}

	if changes != nil {
		go watchSource(source.Name(), changes)
	}

	return nil
}

// watchSource 监听外部配置源的变更
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

func updateConfig(data []byte) {
	oldCfg := globalConfig.Load()
	if oldCfg == nil {
		return
	}

	newCfg := *oldCfg
	if err := yaml.Unmarshal(data, &newCfg); err != nil {
		return
	}

	if !reflect.DeepEqual(oldCfg, &newCfg) {
		triggerCallbacks(&newCfg, oldCfg)
		globalConfig.Store(&newCfg)
	}
}

// setDefaults 设置 Viper 默认值
func setDefaults() {
	v.SetDefault("app.name", DefaultAppName)
	v.SetDefault("app.env", DefaultAppEnv)
	v.SetDefault("app.watch", DefaultAppWatch)
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
	v.SetDefault("observability.otel.endpoint", DefaultOTelEndpoint)
	v.SetDefault("observability.otel.protocol", DefaultOTelProtocol)
	v.SetDefault("observability.otel.logs.enabled", false)
	v.SetDefault("observability.otel.traces.enabled", false)
}

// Get 获取当前配置
//
// 返回值:
//   - *Config: 当前配置对象的指针
func Get() *Config {
	return globalConfig.Load()
}

// AddWatch 注册配置变更监听器
//
// 参数:
//   - callback: 配置变更时的回调函数
//
// 返回值:
//   - WatchKey: 监听器唯一标识，用于取消监听
func AddWatch(callback ConfigChangeCallback) WatchKey {
	callbackRWMutex.Lock()
	defer callbackRWMutex.Unlock()

	key := nextWatchKey
	nextWatchKey++
	callbacks[key] = callback
	return key
}

// RemoveWatch 取消配置变更监听
//
// 参数:
//   - key: AddWatch 返回的监听器标识
func RemoveWatch(key WatchKey) {
	callbackRWMutex.Lock()
	defer callbackRWMutex.Unlock()
	delete(callbacks, key)
}

// triggerCallbacks 触发所有配置变更回调
// 在持有读锁的情况下复制回调列表，然后在无锁状态下异步执行
//
// 参数:
//   - newCfg: 新的配置对象
//   - oldCfg: 旧的配置对象
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
				if r := recover(); r != nil {
				}
			}()
			callback(newCfg, oldCfg)
		}(cb)
	}
}

// reloadConfig 从 Viper 当前状态重新解析配置并触发回调
//
// 返回值:
//   - error: 解析失败时返回错误
func reloadConfig() error {
	newCfg := DefaultConfig()
	if err := v.Unmarshal(newCfg); err != nil {
		return err
	}

	oldCfg := globalConfig.Load()
	if reflect.DeepEqual(newCfg, oldCfg) {
		return nil
	}

	triggerCallbacks(newCfg, oldCfg)
	globalConfig.Store(newCfg)
	return nil
}

// GenerateConfig 生成默认配置文件
//
// 参数:
//   - outputPath: 输出文件路径，为空时使用默认路径 "config.yaml"
func GenerateConfig(outputPath string) {
	if outputPath == "" {
		outputPath = "config.yaml"
	}

	cfg := DefaultConfig()
	data, err := cfg.ToYAML()
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

// ToYAML 将配置转换为 YAML 格式
//
// 返回值:
//   - []byte: YAML 格式的字节数据
//   - error: 转换失败时返回错误
func (c *Config) ToYAML() ([]byte, error) {
	return yaml.Marshal(c)
}

// LoggerConfig 返回 logger.Config，用于初始化日志系统
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

var once sync.Once

// StartWatcher 启动配置文件监控
// 使用 Viper 内置的 WatchConfig 机制监听配置文件变更
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
