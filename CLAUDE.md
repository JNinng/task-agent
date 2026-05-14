# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

```bash
# Run the application (development)
go run ./cmd/app

# Run with custom config
go run ./cmd/app -c /path/to/config.yaml

# Generate default config file
go run ./cmd/app -i -o config.yaml

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
```

## Architecture

This is a **Go application template** (`go 1.26`) following the [golang-standards/project-layout](https://github.com/golang-standards/project-layout) conventions. It wires together config, logging, and graceful shutdown — the three concerns nearly every Go service needs before any business logic.

### Startup flow

`cmd/app/main.go` → `config.Init(path)` → `logger.Init(cfg)` → `config.AddWatch(logger reset callback)` → `signal.ContextWithShutdown(ctx)` → `app.Run(ctx)`

1. Parse CLI flags (pflag) — `-c` config path, `-v` version, `-i` generate default config.
2. `config.Init` loads YAML via Viper, stores global config in `atomic.Pointer[Config]`. If `app.watch` is true, starts fsnotify watcher. If `app.enable_nacos` is true, initializes the Nacos config client and wires its listener into `updateConfig`.
3. `logger.Init` builds a Zap logger with optional file output (lumberjack rotation) and console output, stores it in `atomic.Value`.
4. A config-watch callback is registered that calls `logger.Reset` when log config changes at runtime.
5. `signal.ContextWithShutdown` wraps a context that cancels on SIGINT/SIGTERM/SIGQUIT.
6. `app.Run` enters a 5-second ticker loop until ctx is done, then returns.

### Key packages

**`internal/config`** — Singleton config manager. `Get()` returns the current `*Config` via `atomic.Pointer`. `AddWatch(cb)` / `RemoveWatch(key)` implement the observer pattern for config-change notifications (each callback runs in its own goroutine with panic recovery). Hot-reload has two sources: local file watcher (fsnotify via `v.WatchConfig()`) and Nacos config-center listener (via the `nacos` package). When either fires, `reloadConfig()` re-unmarshals from Viper, merges Nacos content if enabled, and if there's a diff, atomically swaps the global config and triggers callbacks. Env vars with prefix `APP_` override config keys (e.g., `APP_LOG_LEVEL=debug`).

**`internal/logger`** — Global structured logger (zap). `Init(cfg)` builds the logger from `LogConfig`. `Reset(cfg)` dynamically updates level and/or rebuilds cores if output settings changed. Exposes both structured (`logger.Info(msg, fields...)`) and sugared (`logger.Infof(template, args...)`) APIs via package-level functions. Uses `lumberjack` for log file rotation.

**`internal/outsid`** (package name `nacos`) — Nacos config-center client wrapper. `Init` is a `sync.Once` operation that creates the Nacos client, registers config-change listeners, and fetches initial config content. `GetConfigContent()` returns current config content from Nacos. `GetConfig[T any]()` returns a typed struct parsed from Nacos content.

**`internal/signal`** — Two helpers: `WaitForShutdown(done)` blocks until signal then calls `done()`. `ContextWithShutdown(ctx)` returns a child context cancelled on signal.

**`internal/app`** — Placeholder business logic (ticker loop printing log messages at all levels).

**`pkg/version`** — Build-time version info injected via `-ldflags`.

### Configuration

Default config file resolution: checks `configs/config.yaml` first (dev), falls back to `config.yaml` (packaged). Config schema is defined by the `Config` struct in `internal/config/config.go` with sections: `app`, `log`, `nacos`, `secret`.

Nacos integration is opt-in (`app.enable_nacos: true`). When enabled, Nacos content is merged on top of the local file config on each reload — the Nacos data takes precedence. Both local file changes and Nacos config pushes trigger the same `triggerCallbacks` path.
