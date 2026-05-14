package nacos

import (
	"testing"

	"go-template/internal/config"
)

func TestNewSource(t *testing.T) {
	nc := &config.NacosConfig{
		Addr: "127.0.0.1",
		Port: 8848,
	}
	s := NewSource(nc)
	if s == nil {
		t.Fatal("expected non-nil source")
	}
	if s.Name() != "nacos" {
		t.Errorf("expected nacos, got %s", s.Name())
	}
}

func TestNewSourceNilConfig(t *testing.T) {
	s := NewSource(nil)
	if s == nil {
		t.Fatal("expected non-nil source")
	}
	if s.Name() != "nacos" {
		t.Errorf("expected nacos, got %s", s.Name())
	}
}

func TestGetConfigContentNilClient(t *testing.T) {
	_, err := GetConfigContent(nil, &config.NacosConfig{})
	if err == nil {
		t.Error("expected error with nil client")
	}
}

func TestGetConfigNilClient(t *testing.T) {
	_, err := GetConfig[int](nil, &config.NacosConfig{})
	if err == nil {
		t.Error("expected error with nil client")
	}
}
