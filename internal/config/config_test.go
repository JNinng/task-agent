package config

import (
	"os"
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

func (m *mockSource) Name() string              { return m.name }
func (m *mockSource) Init() ([]byte, <-chan []byte, error) { return m.content, m.ch, nil }
func (m *mockSource) Close() error              { return nil }
