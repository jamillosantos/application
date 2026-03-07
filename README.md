# application

An opinionated bootstrap library for Go services. It wires together structured logging, config management, service lifecycle, health/readiness checks, and Prometheus metrics so you can focus on writing services instead of plumbing.

## Installation

```sh
go get github.com/jamillosantos/application
```

Requires Go 1.25+.

## Quick start

```go
package main

import (
    "context"

    "github.com/jamillosantos/application"
    goservices "github.com/jamillosantos/go-services"
)

func main() {
    application.New().
        WithName("my-service").
        Run(func(ctx context.Context, app *application.Application) ([]goservices.Service, error) {
            return []goservices.Service{
                NewDatabaseService(),
                NewHTTPServer(),
            }, nil
        })
}
```

`Run` blocks until the process receives `SIGINT` or `SIGTERM`, then shuts down all services gracefully.

## What you get out of the box

| Feature | Details |
|---|---|
| Structured logging | `zap` logger, dev or production mode based on `ENV` env var |
| Config loading | Via `jamillosantos/config`, driven by `CONFIG_LOAD_OPTIONS` |
| Service lifecycle | Start, stop, and retry management via `jamillosantos/go-services` |
| Graceful shutdown | Configurable grace period (default 30 s) |
| System HTTP server | Health, readiness, and metrics endpoints on `:8082` |
| Build info | Version, commit, and build date auto-detected from Go build info |

## System server endpoints

The system server starts automatically on `:8082`.

| Endpoint | Description |
|---|---|
| `GET /healthz` | Returns 200 when all registered health checks pass |
| `GET /readyz` | Returns 200 when the app and all registered readiness checks pass |
| `GET /metrics` | Prometheus metrics in text format |

## Services

Services must implement the `goservices.Service` interface from [`jamillosantos/go-services`](https://github.com/jamillosantos/go-services). Services are started concurrently and stopped in reverse order on shutdown.

### Health and readiness checks

Services that also implement `HealthChecker` or `ReadyChecker` are automatically registered with the system server — no extra wiring needed.

```go
// Participates in GET /healthz
type HealthChecker interface {
    IsHealthy(ctx context.Context) error
}

// Participates in GET /readyz
type ReadyChecker interface {
    IsReady(ctx context.Context) error
}
```

The application itself only becomes ready (i.e. `/readyz` returns 200) once all services that implement `ReadyChecker` have returned `nil` from `IsReady`.

## Configuration

All options follow the `With*` builder pattern and can be chained:

```go
application.New().
    WithName("my-service").
    WithEnvironment("dev").
    WithShutdownGracePeriod(10 * time.Second).
    WithSystemServerBindAddress(":9090").
    Run(setup)
```

| Method | Description |
|---|---|
| `WithName(name)` | Service name added to every log entry. Defaults to the module name from build info. |
| `WithEnvironment(env)` | `"dev"` enables the development logger; anything else uses production. Defaults to `ENV` env var, falling back to `"production"`. |
| `WithContext(ctx)` | Base context. Wrapped with signal handling inside `Run`. |
| `WithShutdownGracePeriod(d)` | Maximum time to wait for services and shutdown handlers to finish. Default: 30 s. |
| `WithSystemServerBindAddress(addr)` | Bind address for the system HTTP server. Default: `:8082`. |
| `WithDisableSystemServer(true)` | Disables the system HTTP server entirely. |
| `WithSkipConfig(true)` | Skips config loading; `ConfigManager` will be `nil`. |
| `WithConfigManagerOptions(opts...)` | Options forwarded to `config.NewManager`. |
| `WithLoggerZapOptions(opts...)` | Extra `zap.Option` values applied when building the logger. |
| `WithZapConfigModifier(f)` | Callback to mutate `zap.Config` before the logger is built. |

### Shutdown handlers

`Shutdown` registers a function called after all services are stopped. Multiple handlers are called sequentially and must respect the grace period.

```go
application.New().
    Shutdown(func() {
        flushTelemetry()
    }).
    Run(setup)
```

## Accessing the runner and config manager

`Application.Runner` and `Application.ConfigManager` are populated before the `ServiceSetup` callback is invoked, so they are safe to use inside it.

```go
application.New().
    Run(func(ctx context.Context, app *application.Application) ([]goservices.Service, error) {
        // app.Runner and app.ConfigManager are ready here
        cfg := &MyConfig{}
        if err := app.ConfigManager.Populate(cfg); err != nil {
            return nil, err
        }
        return []goservices.Service{NewHTTPServer(cfg)}, nil
    })
```

## Logging

The logger is available via the context using [`logctx`](https://github.com/jamillosantos/logctx):

```go
import "github.com/jamillosantos/logctx"

func (s *MyService) Listen(ctx context.Context) error {
    logger := logctx.From(ctx)
    logger.Info("service started")
    return nil
}
```

## Version information

Version, commit hash, and build date are automatically read from the Go 1.18+ build info embedded in the binary. They can still be overridden at link time with LDFLAGS:

```sh
go build \
  -ldflags "-X github.com/jamillosantos/application.Version=1.2.3 \
            -X github.com/jamillosantos/application.Build=$(git rev-parse HEAD) \
            -X github.com/jamillosantos/application.BuildDate=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  ./...
```