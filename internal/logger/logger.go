package logger

import (
	"go-template/internal/config"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

var (
	globalLogger  atomic.Value
	globalSugar   atomic.Value
	currentWriter *lumberjack.Logger
	writerMutex   sync.Mutex
	atomicLevel   zap.AtomicLevel
	currentLogCfg atomic.Value
)

func Init(cfg *config.LogConfig) error {
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

func Reset(cfg *config.LogConfig) error {
	oldCfg, ok := currentLogCfg.Load().(*config.LogConfig)
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

func buildLogger(cfg *config.LogConfig, level zap.AtomicLevel) (*zap.Logger, *zap.SugaredLogger, *lumberjack.Logger, error) {
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

func Debug(msg string, fields ...zap.Field) {
	getLogger().Debug(msg, fields...)
}

func Info(msg string, fields ...zap.Field) {
	getLogger().Info(msg, fields...)
}

func Warn(msg string, fields ...zap.Field) {
	getLogger().Warn(msg, fields...)
}

func Error(msg string, fields ...zap.Field) {
	getLogger().Error(msg, fields...)
}

func DPanic(msg string, fields ...zap.Field) {
	getLogger().DPanic(msg, fields...)
}

func Panic(msg string, fields ...zap.Field) {
	getLogger().Panic(msg, fields...)
}

func Fatal(msg string, fields ...zap.Field) {
	getLogger().Fatal(msg, fields...)
}

func Debugf(template string, args ...any) {
	getSugar().Debugf(template, args...)
}

func Infof(template string, args ...any) {
	getSugar().Infof(template, args...)
}

func Warnf(template string, args ...any) {
	getSugar().Warnf(template, args...)
}

func Errorf(template string, args ...any) {
	getSugar().Errorf(template, args...)
}

func DPanicf(template string, args ...any) {
	getSugar().DPanicf(template, args...)
}

func Panicf(template string, args ...any) {
	getSugar().Panicf(template, args...)
}

func Fatalf(template string, args ...any) {
	getSugar().Fatalf(template, args...)
}

func Sync() error {
	return getLogger().Sync()
}
