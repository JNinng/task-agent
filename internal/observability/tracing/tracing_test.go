package tracing

import (
	"context"
	"testing"

	"go-template/internal/config"

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

func TestInitInvalidEndpoint(t *testing.T) {
	cfg := config.OTelConfig{
		Endpoint: "invalid-endpoint:99999",
		Protocol: "grpc",
		Traces:   config.SignalConfig{Enabled: true},
	}
	res := resource.NewWithAttributes(semconv.SchemaURL,
		semconv.ServiceName("test"),
	)

	_, err := Init(context.Background(), cfg, res)
	if err != nil {
		t.Logf("Init returned error (expected for invalid config): %v", err)
	}
}
