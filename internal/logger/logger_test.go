package logger

import (
	"os"
	"path/filepath"
	"testing"
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
