# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

```bash
# Run the application (development)
go run ./cmd/app

# Run with custom config
go run ./cmd/app -c /path/to/config.yaml

# Generate default config file
go run ./cmd/app init -o config.yaml

# Show version
go run ./cmd/app version

# Build binary
go build -o bin/app ./cmd/app

# Build with version info (production)
go build -ldflags "-X go-template/pkg/version.Version=1.0.0 -X go-template/pkg/version.Commit=$(git rev-parse --short HEAD) -X go-template/pkg/version.Date=$(date -u +%Y-%m-%dT%H:%M:%SZ)" -o bin/app ./cmd/app

# GoReleaser snapshot build
goreleaser release --snapshot --skip=publish --clean -f build/.goreleaser.yaml

# Download dependencies
go mod download

# Tidy dependencies
go mod tidy

# Format code
go fmt ./...

# Vet code
go vet ./...

# Run all tests
go test ./...

# Run tests for a specific package
go test ./internal/config/

# Run a single test
go test ./internal/config/ -run TestInit

# Run tests with race detector
go test -race ./...

# Run tests with coverage
go test -coverprofile=coverage.out ./...

# Lightweight lint (requires golangci-lint)
golangci-lint run ./...
```

## Architecture

This is a **Go application template** (`go 1.26`) following the [golang-standards/project-layout](https://github.com/golang-standards/project-layout) conventions. It wires together config, logging, graceful shutdown, and observability — the concerns nearly every Go service needs before any business logic.

### Startup flow

`cmd/app/main.go` calls `cmd.Execute()`, which dispatches to the cobra root command in `internal/cmd/root.go`:

1. **CLI init** — cobra registers flags (`-c`/`--config` defaulting to `configs/config.yaml` or `config.yaml`) and subcommands (`init`, `version`).
2. `config.Init(configPath, [sources...])` — loads YAML via Viper, sets env var overrides (`APP_` prefix), merges optional `Source` providers, stores config in `atomic.Pointer[Config]`. If `app.watch: true`, starts fsnotify watcher.
3. `logger.Init(&lc)` — builds a Zap logger (console + optional file via lumberjack rotation).
4. `config.AddWatch(...)` — registers a callback that calls `logger.Reset` when log config changes at runtime.
5. `signal.ContextWithShutdown(ctx)` — returns a child context cancelled on SIGINT/SIGTERM/SIGQUIT.
6. `observability.Start(ctx, cfg.Observability)` — if `observability.enabled: true`, starts a separate HTTP server exposing health and metrics endpoints.
7. `app.Run(ctx)` — blocks until ctx is done (placeholder for business logic), then returns.

### Key packages

**`internal/cmd`** — Cobra CLI setup. `rootCmd` wires the full startup sequence. Two subcommands: `init` (generates default config YAML) and `version` (prints build version). The root command's `RunE` is the real entry point; `cmd/app/main.go` is now a one-liner calling `cmd.Execute()`.

**`internal/config`** — Singleton config manager. `Get()` returns the current `*Config` via `atomic.Pointer`. `AddWatch(cb)` / `RemoveWatch(key)` implement the observer pattern (each callback runs in its own goroutine with panic recovery). Hot-reload has two paths: local file watcher (fsnotify via Viper's `WatchConfig`) and external `Source` providers (e.g., Nacos). Either path calls `reloadConfig()` which re-unmarshals, computes a diff, and atomically swaps the global config + triggers callbacks. Env vars with prefix `APP_` override config keys (e.g., `APP_LOG_LEVEL=debug`). The `Source` interface (`source.go`) enables pluggable remote config providers.

**`internal/logger`** — Global structured logger (zap). `Init(cfg)` builds the logger from `Config`. `Reset(cfg)` dynamically updates level and/or rebuilds cores if output settings changed. Exposes both `Info/Warn/Error(msg, fields...)` and `Infof/Warnf/Errorf(template, args...)` APIs via package-level functions. Uses `lumberjack` for log file rotation.

**`internal/nacos`** — Nacos config-center client implementing `config.Source`. `NewSource(cfg)` returns a `config.Source` ready to pass to `config.Init`. On init, it builds the Nacos client, registers a config-change listener, and fetches initial content. Changes are forwarded to the config system via a channel. `GetConfig[T any]()` is a standalone helper for one-shot typed reads.

**`internal/observability`** — Umbrella package providing three observability concerns:
- **`health`** — HTTP handler at `/health` (configurable). Supports pluggable check functions via `Register(name, fn)`. Returns JSON `{"status":"healthy|unhealthy","details":{...}}` with 200/503.
- **`metrics`** — Prometheus `/metrics` endpoint using a dedicated `prometheus.NewRegistry()` (not the global default). Includes process and Go collectors. `Register(c)` allows adding custom collectors.
- **`tracing`** — OpenTelemetry scaffolding. `Init()` is currently a no-op; integrate OTLP exporters here when needed.

`Start()` creates its own HTTP server on a separate port. Alternatively, `HealthHandler()` and `MetricsHandler()` return the handlers for embedding in an existing mux.

**`internal/signal`** — `ContextWithShutdown(ctx)` returns a child context cancelled on SIGINT/SIGTERM/SIGQUIT. `WaitForShutdown(done)` blocks until signal, then calls `done()`.

**`internal/app`** — Placeholder. `Run(ctx)` blocks on `<-ctx.Done()`. Replace with real business logic.

**`pkg/version`** — Build-time version info (`Version`, `Commit`, `Date`) injected via `-ldflags`.

### Configuration

Default config file resolution: `defaultConfigPath()` in `internal/cmd/root.go` checks `configs/config.yaml` first, falls back to `config.yaml`. Config schema (`Config` struct in `internal/config/config.go`) has sections:

| Section         | Purpose                                   |
|-----------------|-------------------------------------------|
| `app`           | Name, env, watch toggle, nacos enable     |
| `log`           | Level, format, output path, rotation      |
| `nacos`         | Nacos server addr, port, auth, dataId     |
| `observability` | Enabled, listen addr, paths, tracing      |
| `secret`        | Secret key placeholder                    |

Nacos integration is opt-in via `app.enable_nacos: true` in the source passed to `config.Init`. When enabled, Nacos content merges on top of local file config — Nacos values take precedence on conflict.
