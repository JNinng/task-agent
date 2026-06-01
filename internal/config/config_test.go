package config

import (
	"os"
	"testing"

	"task-agent/internal/logger"
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

func TestOTelConfigDefaults(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Observability.OTel.Endpoint != DefaultOTelEndpoint {
		t.Errorf("expected %s, got %s", DefaultOTelEndpoint, cfg.Observability.OTel.Endpoint)
	}
	if cfg.Observability.OTel.Protocol != DefaultOTelProtocol {
		t.Errorf("expected %s, got %s", DefaultOTelProtocol, cfg.Observability.OTel.Protocol)
	}
}

func TestLoggerConfigConversion(t *testing.T) {
	cfg := DefaultConfig()
	lc := cfg.LoggerConfig()
	if lc.Level != DefaultLogLevel {
		t.Errorf("expected %s, got %s", DefaultLogLevel, lc.Level)
	}
	if lc.Format != DefaultLogFormat {
		t.Errorf("expected %s, got %s", DefaultLogFormat, lc.Format)
	}
	if lc.MaxSize != DefaultLogMaxSize {
		t.Errorf("expected %d, got %d", DefaultLogMaxSize, lc.MaxSize)
	}
	if lc.MaxAge != DefaultLogMaxAge {
		t.Errorf("expected %d, got %d", DefaultLogMaxAge, lc.MaxAge)
	}
	if lc.MaxBackups != DefaultLogMaxBackups {
		t.Errorf("expected %d, got %d", DefaultLogMaxBackups, lc.MaxBackups)
	}
	if lc.Compress != DefaultLogCompress {
		t.Errorf("expected %v, got %v", DefaultLogCompress, lc.Compress)
	}
	if lc.LogToConsole != DefaultLogToConsole {
		t.Errorf("expected %v, got %v", DefaultLogToConsole, lc.LogToConsole)
	}

	// Verify type assertion: lc must be logger.Config type
	_ = logger.Config(lc)
}

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
	if cfg.App.Name != "overridden-app" {
		t.Errorf("expected overridden-app, got %s", cfg.App.Name)
	}
	if cfg.App.Env != "dev" {
		t.Errorf("expected dev, got %s", cfg.App.Env)
	}
}

type mockSource struct {
	name    string
	content []byte
	ch      chan []byte
}

func (m *mockSource) Name() string                         { return m.name }
func (m *mockSource) Init() ([]byte, <-chan []byte, error) { return m.content, m.ch, nil }
func (m *mockSource) Close() error                         { return nil }
