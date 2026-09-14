# httpc - Copilot Instructions

Project context for GitHub Copilot. `AGENTS.md` at the repository root is the
shared, tool-neutral version and goes into more depth; this file stays short
and points there rather than keeping a second copy that drifts.

## Overview
Production-ready HTTP client library for Go with built-in retries, caching, circuit breaker, compression, and observability.

## Key Patterns

- **Metrics**: Use the `Metrics` interface for observability; `NoopMetrics{}` is the default
- **Configuration**: Use `ClientConfig` for programmatic config, `AppConfig` for YAML/JSON config files
- **Errors**: Return errors with `fmt.Errorf("context: %w", err)`, don't panic
- **Logging**: Use `logr.Logger` -- never `log.Printf` or `fmt.Printf`
- **Thread Safety**: All public types are safe for concurrent use

## Build & Test Commands

Use the task targets -- they pin the toolchain and the linters to the versions
CI runs.

```bash
task build      # go build ./...
task test       # unit tests, shuffled
task lint       # golangci-lint, pinned version
task ci         # the full local gate; matches what CI checks
```

## Critical Rules

- **No hardcoded app names**: Use configurable `CacheKeyPrefix` and `CacheDir`, never embed "scafctl" or similar
- **Metrics interface**: All metrics go through the `Metrics` interface, never import a specific metrics backend
- **Test coverage**: Every new or changed file must have tests. Codecov enforces 70% project and 50% patch; treat those as floors
- **Breaking changes**: Allowed -- this library is pre-1.0. Note when doing so
- **Git safety**: Never run `git commit`, `git push`, or `git commit --amend` unless the user explicitly asks

## Conventions

- **Commits**: Use [conventional commits](https://www.conventionalcommits.org/en/v1.0.0/#specification)
- **Signing**: All commits must be GPG/SSH signed (`-S`) and include DCO sign-off (`-s`)

## Architecture

See `AGENTS.md` for the file-by-file map, the transport chain (the order of
the layered round-trippers matters), and the three SSRF enforcement points.
It is kept current there rather than duplicated here.
