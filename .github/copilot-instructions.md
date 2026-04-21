# httpc - AI Agent Instructions

## Overview
Production-ready HTTP client library for Go with built-in retries, caching, circuit breaker, compression, and observability.

## Key Patterns

- **Metrics**: Use the `Metrics` interface for observability; `NoopMetrics{}` is the default
- **Configuration**: Use `ClientConfig` for programmatic config, `AppConfig` for YAML/JSON config files
- **Errors**: Return errors with `fmt.Errorf("context: %w", err)`, don't panic
- **Logging**: Use `logr.Logger` -- never `log.Printf` or `fmt.Printf`
- **Thread Safety**: All public types are safe for concurrent use

## Build & Test Commands

```bash
# Build
go build ./...

# Test
go test ./...

# Lint
task lint

# Full CI
task ci
```

## Critical Rules

- **No hardcoded app names**: Use configurable `CacheKeyPrefix` and `CacheDir`, never embed "scafctl" or similar
- **Metrics interface**: All metrics go through the `Metrics` interface, never import a specific metrics backend
- **Test coverage**: Every new or changed file must have tests. Target 70%+ patch coverage
- **Breaking changes**: Allowed -- this library is pre-1.0. Note when doing so
- **Git safety**: Never run `git commit`, `git push`, or `git commit --amend` unless the user explicitly asks

## Conventions

- **Commits**: Use [conventional commits](https://www.conventionalcommits.org/en/v1.0.0/#specification)
- **Signing**: All commits must be GPG/SSH signed (`-S`) and include DCO sign-off (`-s`)

## Architecture

- `client.go` -- Main HTTP client with retry, caching, circuit breaker
- `appconfig.go` -- String-based config for YAML/JSON, `NewClientFromAppConfig`, `MergeAppConfig`
- `defaults.go` -- All default constants and helper functions
- `metrics.go` -- `Metrics` interface + `NoopMetrics` implementation
- `circuitbreaker.go` -- Per-host circuit breaker pattern
- `compression.go` -- Automatic gzip transport
- `filecache.go` -- Filesystem-based HTTP response cache
- `memorycache.go` -- Memory cache wrapper with metrics
- `metrics_transport.go` -- HTTP transport that records metrics
- `helpers.go` -- Retry policy and backoff builders
- `ssrf.go` -- SSRF prevention (private IP blocking)
