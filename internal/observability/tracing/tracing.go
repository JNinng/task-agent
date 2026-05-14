package tracing

import (
	"context"
	"fmt"
)

type Config struct {
	Enabled  bool
	Endpoint string
}

func Init(ctx context.Context, cfg Config) (func(), error) {
	if !cfg.Enabled {
		return func() {}, nil
	}

	// OTel SDK integration point — enable by adding OTLP exporter and
	// configuring the global TracerProvider when cfg.Endpoint is set.
	// Current implementation is a no-op to avoid heavy dependencies.
	fmt.Printf("Tracing enabled, endpoint: %s\n", cfg.Endpoint)
	return func() {}, nil
}
