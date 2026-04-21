---
description: "Go coding conventions for httpc: struct tags, error handling, design principles, functional options, context/timeouts, and formatting. Use when writing or editing Go code."
applyTo: "**/*.go"
---

# Go Conventions

## Struct Tags

Always add JSON/YAML tags on config structs.

## Error Handling

Always wrap errors with context:

```go
if err != nil {
    return fmt.Errorf("failed to create client: %w", err)
}
```

## Design Principles

- Accept interfaces, return structs
- Keep interfaces small (1-3 methods)
- Define interfaces where they are used, not where they are implemented
- Use constructor functions for dependency injection
- Use functional options pattern (`WithX(val) Option`) for configurable constructors
- Always pass `context.Context` as first parameter for timeout/cancellation control
- No package-level mutable state

## Secret Management

Read secrets from environment variables -- never hardcode.

## Formatting

- **gofmt** and **goimports** are mandatory -- no style debates
- Never use magic strings or numbers; always define constants

## Library-Safe Design

httpc is consumed as a library. Code must not assume a specific consumer.

- Use configurable `CacheKeyPrefix` and `CacheDir`, never embed app-specific names
- All metrics go through the `Metrics` interface, never import a specific metrics backend
- Use `logr.Logger` for logging, never `log.Printf` or `fmt.Printf`
