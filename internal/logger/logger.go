package logger

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

// Config 日志配置
type Config struct {
	Level        string `yaml:"level" mapstructure:"level"`                   // 日志级别
	Format       string `yaml:"format" mapstructure:"format"`                 // 日志格式 (console/json)
	Path         string `yaml:"path" mapstructure:"path"`                     // 日志文件路径
	MaxSize      int    `yaml:"max_size" mapstructure:"max_size"`             // 单个日志文件最大大小 (MB)
	MaxAge       int    `yaml:"max_age" mapstructure:"max_age"`               // 日志文件保留天数
	MaxBackups   int    `yaml:"max_backups" mapstructure:"max_backups"`       // 保留的日志文件数量
	Compress     bool   `yaml:"compress" mapstructure:"compress"`             // 是否压缩历史日志
	LogToConsole bool   `yaml:"log_to_console" mapstructure:"log_to_console"` // 是否输出到控制台
}

// 默认日志配置常量
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

// DefaultConfig 返回默认日志配置
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
	extraCores   []zapcore.Core
	extraCoresMu sync.RWMutex
)

// Init 初始化日志系统
func Init(cfg *Config) error {
	atomicLevel = zap.NewAtomicLevelAt(getZapLevel(cfg.Level))

	extraCoresMu.RLock()
	copyCores := make([]zapcore.Core, len(extraCores))
	copy(copyCores, extraCores)
	extraCoresMu.RUnlock()

	logger, sugar, writer, err := buildLogger(cfg, atomicLevel, copyCores...)
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

// Reset 重置日志配置
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

// AddCore 向 logger 注入额外的 zapcore.Core（如 otelzap bridge）。
// 线程安全，注入后自动重建底层 logger。
// 如果 logger 尚未初始化或重建失败，返回 error。
func AddCore(core zapcore.Core) error {
	extraCoresMu.Lock()
	oldLen := len(extraCores)
	extraCores = append(extraCores, core)
	copyCores := make([]zapcore.Core, len(extraCores))
	copy(copyCores, extraCores)
	extraCoresMu.Unlock()

	rollback := func() {
		extraCoresMu.Lock()
		extraCores = extraCores[:oldLen]
		extraCoresMu.Unlock()
	}

	cfg, ok := currentLogCfg.Load().(*Config)
	if !ok || cfg == nil {
		rollback()
		return fmt.Errorf("logger not initialized, call Init first")
	}

	logger, sugar, writer, err := buildLogger(cfg, atomicLevel, copyCores...)
	if err != nil {
		rollback()
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
	return nil
}

// SetLevel 动态设置日志级别
func SetLevel(level string) {
	atomicLevel.SetLevel(getZapLevel(level))
}

// GetLevel 获取当前日志级别
func GetLevel() zapcore.Level {
	return atomicLevel.Level()
}

func buildLogger(cfg *Config, level zap.AtomicLevel, extra ...zapcore.Core) (*zap.Logger, *zap.SugaredLogger, *lumberjack.Logger, error) {
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

	cores = append(cores, extra...)
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

// Debug 输出 Debug 级别日志
func Debug(msg string, fields ...zap.Field) {
	getLogger().Debug(msg, fields...)
}

// Info 输出 Info 级别日志
func Info(msg string, fields ...zap.Field) {
	getLogger().Info(msg, fields...)
}

// Warn 输出 Warn 级别日志
func Warn(msg string, fields ...zap.Field) {
	getLogger().Warn(msg, fields...)
}

// Error 输出 Error 级别日志
func Error(msg string, fields ...zap.Field) {
	getLogger().Error(msg, fields...)
}

// DPanic 输出 DPanic 级别日志
func DPanic(msg string, fields ...zap.Field) {
	getLogger().DPanic(msg, fields...)
}

// Panic 输出 Panic 级别日志
func Panic(msg string, fields ...zap.Field) {
	getLogger().Panic(msg, fields...)
}

// Fatal 输出 Fatal 级别日志
func Fatal(msg string, fields ...zap.Field) {
	getLogger().Fatal(msg, fields...)
}

// DebugContext 输出带有 trace context 的 Debug 级别日志
func DebugContext(ctx context.Context, msg string, fields ...zap.Field) {
	getLogger().Debug(msg, append(fields, withTraceContext(ctx)...)...)
}

// InfoContext 输出带有 trace context 的 Info 级别日志
func InfoContext(ctx context.Context, msg string, fields ...zap.Field) {
	getLogger().Info(msg, append(fields, withTraceContext(ctx)...)...)
}

// WarnContext 输出带有 trace context 的 Warn 级别日志
func WarnContext(ctx context.Context, msg string, fields ...zap.Field) {
	getLogger().Warn(msg, append(fields, withTraceContext(ctx)...)...)
}

// ErrorContext 输出带有 trace context 的 Error 级别日志
func ErrorContext(ctx context.Context, msg string, fields ...zap.Field) {
	getLogger().Error(msg, append(fields, withTraceContext(ctx)...)...)
}

// DPanicContext 输出带有 trace context 的 DPanic 级别日志
func DPanicContext(ctx context.Context, msg string, fields ...zap.Field) {
	getLogger().DPanic(msg, append(fields, withTraceContext(ctx)...)...)
}

// PanicContext 输出带有 trace context 的 Panic 级别日志
func PanicContext(ctx context.Context, msg string, fields ...zap.Field) {
	getLogger().Panic(msg, append(fields, withTraceContext(ctx)...)...)
}

// FatalContext 输出带有 trace context 的 Fatal 级别日志
func FatalContext(ctx context.Context, msg string, fields ...zap.Field) {
	getLogger().Fatal(msg, append(fields, withTraceContext(ctx)...)...)
}

// DebugfContext 输出带有 trace context 的 Debug 级别格式化日志
func DebugfContext(ctx context.Context, template string, args ...any) {
	getSugar().With(sugarTraceContext(ctx)...).Debugf(template, args...)
}

// InfofContext 输出带有 trace context 的 Info 级别格式化日志
func InfofContext(ctx context.Context, template string, args ...any) {
	getSugar().With(sugarTraceContext(ctx)...).Infof(template, args...)
}

// WarnfContext 输出带有 trace context 的 Warn 级别格式化日志
func WarnfContext(ctx context.Context, template string, args ...any) {
	getSugar().With(sugarTraceContext(ctx)...).Warnf(template, args...)
}

// ErrorfContext 输出带有 trace context 的 Error 级别格式化日志
func ErrorfContext(ctx context.Context, template string, args ...any) {
	getSugar().With(sugarTraceContext(ctx)...).Errorf(template, args...)
}

// DPanicfContext 输出带有 trace context 的 DPanic 级别格式化日志
func DPanicfContext(ctx context.Context, template string, args ...any) {
	getSugar().With(sugarTraceContext(ctx)...).DPanicf(template, args...)
}

// PanicfContext 输出带有 trace context 的 Panic 级别格式化日志
func PanicfContext(ctx context.Context, template string, args ...any) {
	getSugar().With(sugarTraceContext(ctx)...).Panicf(template, args...)
}

// FatalfContext 输出带有 trace context 的 Fatal 级别格式化日志
func FatalfContext(ctx context.Context, template string, args ...any) {
	getSugar().With(sugarTraceContext(ctx)...).Fatalf(template, args...)
}

// Debugf 输出 Debug 级别格式化日志
func Debugf(template string, args ...any) {
	getSugar().Debugf(template, args...)
}

// Infof 输出 Info 级别格式化日志
func Infof(template string, args ...any) {
	getSugar().Infof(template, args...)
}

// Warnf 输出 Warn 级别格式化日志
func Warnf(template string, args ...any) {
	getSugar().Warnf(template, args...)
}

// Errorf 输出 Error 级别格式化日志
func Errorf(template string, args ...any) {
	getSugar().Errorf(template, args...)
}

// DPanicf 输出 DPanic 级别格式化日志
func DPanicf(template string, args ...any) {
	getSugar().DPanicf(template, args...)
}

// Panicf 输出 Panic 级别格式化日志
func Panicf(template string, args ...any) {
	getSugar().Panicf(template, args...)
}

// Fatalf 输出 Fatal 级别格式化日志
func Fatalf(template string, args ...any) {
	getSugar().Fatalf(template, args...)
}

// withTraceContext 从 context 中提取 TraceID 和 SpanID 作为 zap.Field。
// 如果 context 中没有活跃的 Span，返回空切片。
func withTraceContext(ctx context.Context) []zap.Field {
	span := trace.SpanFromContext(ctx)
	if !span.SpanContext().IsValid() {
		return nil
	}
	return []zap.Field{
		zap.String("trace_id", span.SpanContext().TraceID().String()),
		zap.String("span_id", span.SpanContext().SpanID().String()),
	}
}

// sugarTraceContext 从 context 中提取 TraceID 和 SpanID，以 []interface{} 形式返回，
// 兼容 SugaredLogger.With 的参数签名。
func sugarTraceContext(ctx context.Context) []any {
	fields := withTraceContext(ctx)
	if len(fields) == 0 {
		return nil
	}
	result := make([]any, len(fields))
	for i, f := range fields {
		result[i] = f
	}
	return result
}

// Sync 同步日志缓冲区
func Sync() error {
	return getLogger().Sync()
}
