package logger

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func TestInitAndLog(t *testing.T) {
	cfg := DefaultConfig()
	cfg.LogToConsole = false
	if err := Init(&cfg); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	// These should not panic
	Info("test message")
	Infof("test formatted %s", "message")
	SetLevel("debug")
	Debug("debug message")
	Sync()

	// Close the writer so deferred cleanup succeeds
	writerMutex.Lock()
	if currentWriter != nil {
		currentWriter.Close()
		currentWriter = nil
	}
	writerMutex.Unlock()
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

	// Use a new config for reset (LoggerConfig() pattern from production code)
	resetCfg := DefaultConfig()
	resetCfg.LogToConsole = false
	resetCfg.Level = "debug"
	if err := Reset(&resetCfg); err != nil {
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

	// Use a new config for reset (LoggerConfig() pattern from production code)
	resetCfg := DefaultConfig()
	resetCfg.LogToConsole = false
	newPath := filepath.Join(dir, "test2.log")
	resetCfg.Path = newPath
	if err := Reset(&resetCfg); err != nil {
		t.Fatal(err)
	}
	Info("after reset")

	Sync()

	if _, err := os.Stat(newPath); os.IsNotExist(err) {
		t.Errorf("log file not created at %s", newPath)
	}

	// Close the lumberjack writer so temp dir cleanup succeeds
	writerMutex.Lock()
	if currentWriter != nil {
		currentWriter.Close()
		currentWriter = nil
	}
	writerMutex.Unlock()
}

func TestAddCore(t *testing.T) {
	cfg := DefaultConfig()
	cfg.LogToConsole = false
	cfg.Path = ""
	if err := Init(&cfg); err != nil {
		t.Fatal(err)
	}

	core, observed := observer.New(zapcore.DebugLevel)
	if err := AddCore(core); err != nil {
		t.Fatalf("AddCore failed: %v", err)
	}

	Info("test message", zap.String("key", "value"))

	logs := observed.All()
	if len(logs) != 1 {
		t.Fatalf("expected 1 log, got %d", len(logs))
	}
	if logs[0].Message != "test message" {
		t.Errorf("expected 'test message', got %s", logs[0].Message)
	}

	// Cleanup: reset extra cores and close writer
	extraCoresMu.Lock()
	extraCores = nil
	extraCoresMu.Unlock()
	writerMutex.Lock()
	if currentWriter != nil {
		currentWriter.Close()
		currentWriter = nil
	}
	writerMutex.Unlock()
}

func TestAddCoreSurvivesReset(t *testing.T) {
	cfg := DefaultConfig()
	cfg.LogToConsole = false
	cfg.Path = ""
	if err := Init(&cfg); err != nil {
		t.Fatal(err)
	}

	core, observed := observer.New(zapcore.DebugLevel)
	if err := AddCore(core); err != nil {
		t.Fatalf("AddCore failed: %v", err)
	}
	observed.TakeAll() // clear pre-AddCore messages

	// Simulate a config reset — extra cores must survive
	resetCfg := DefaultConfig()
	resetCfg.LogToConsole = false
	resetCfg.Level = "debug"
	if err := Reset(&resetCfg); err != nil {
		t.Fatal(err)
	}

	Info("after reset")

	logs := observed.All()
	if len(logs) != 1 {
		t.Fatalf("expected 1 log after reset, got %d", len(logs))
	}
	if logs[0].Message != "after reset" {
		t.Errorf("expected 'after reset', got %s", logs[0].Message)
	}

	// Cleanup
	extraCoresMu.Lock()
	extraCores = nil
	extraCoresMu.Unlock()
	writerMutex.Lock()
	if currentWriter != nil {
		currentWriter.Close()
		currentWriter = nil
	}
	writerMutex.Unlock()
}

func TestContextMethods(t *testing.T) {
	cfg := DefaultConfig()
	cfg.LogToConsole = false
	cfg.Path = ""
	if err := Init(&cfg); err != nil {
		t.Fatal(err)
	}

	core, observed := observer.New(zapcore.DebugLevel)
	if err := AddCore(core); err != nil {
		t.Fatal(err)
	}

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

	// Cleanup
	extraCoresMu.Lock()
	extraCores = nil
	extraCoresMu.Unlock()
	writerMutex.Lock()
	if currentWriter != nil {
		currentWriter.Close()
		currentWriter = nil
	}
	writerMutex.Unlock()
}
