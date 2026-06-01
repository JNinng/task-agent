package tracing

import (
	"context"
	"testing"
	"time"

	"task-agent/internal/config"

	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.27.0"
)

func TestInitDisabled(t *testing.T) {
	cfg := config.OTelConfig{
		Traces: config.SignalConfig{Enabled: false},
	}
	res := resource.NewWithAttributes(semconv.SchemaURL,
		semconv.ServiceName("test"),
	)

	shutdown, err := Init(context.Background(), cfg, res)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
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
		Traces:   config.SignalConfig{Enabled: true},
	}
	res := resource.NewWithAttributes(semconv.SchemaURL,
		semconv.ServiceName("test"),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	shutdown, err := Init(ctx, cfg, res)
	if err != nil {
		t.Logf("Init returned error (expected for unreachable endpoint): %v", err)
		return
	}
	if shutdown == nil {
		t.Fatal("expected non-nil shutdown even on bad endpoint")
	}
	// shutdown must still work even if init "succeeded" (lazy gRPC connection)
	if err := shutdown(context.Background()); err != nil {
		t.Logf("shutdown returned error: %v", err)
	}
}
