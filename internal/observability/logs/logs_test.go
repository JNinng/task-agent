package logs

import (
	"context"
	"testing"
	"time"

	"go-template/internal/config"

	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.27.0"
)

func TestInitDisabled(t *testing.T) {
	cfg := config.OTelConfig{
		Logs: config.SignalConfig{Enabled: false},
	}
	res := resource.NewWithAttributes(semconv.SchemaURL,
		semconv.ServiceName("test"),
	)

	core, shutdown, err := Init(context.Background(), cfg, res)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if core != nil {
		t.Error("expected nil core when disabled")
	}
	if shutdown == nil {
		t.Fatal("expected non-nil shutdown func")
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown failed: %v", err)
	}
}

func TestInitDoesNotPanicWithBadEndpoint(t *testing.T) {
	// Use a non-routable IP to ensure connection fails, not a valid-but-unreachable host.
	cfg := config.OTelConfig{
		Endpoint: "127.255.255.255:9999",
		Protocol: "grpc",
		Logs:     config.SignalConfig{Enabled: true},
	}
	res := resource.NewWithAttributes(semconv.SchemaURL,
		semconv.ServiceName("test"),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	core, shutdown, err := Init(ctx, cfg, res)
	if err != nil {
		t.Logf("Init returned error (expected for unreachable endpoint): %v", err)
		return
	}
	if core == nil {
		t.Fatal("expected non-nil core even with bad endpoint")
	}
	if shutdown == nil {
		t.Fatal("expected non-nil shutdown even on bad endpoint")
	}
	if err := shutdown(context.Background()); err != nil {
		t.Logf("shutdown returned error: %v", err)
	}
}
