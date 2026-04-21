# httpc

[![CI](https://github.com/oakwood-commons/httpc/actions/workflows/test.yml/badge.svg)](https://github.com/oakwood-commons/httpc/actions/workflows/test.yml)
[![codecov](https://codecov.io/gh/oakwood-commons/httpc/branch/main/graph/badge.svg)](https://codecov.io/gh/oakwood-commons/httpc)
[![Go Reference](https://pkg.go.dev/badge/github.com/oakwood-commons/httpc.svg)](https://pkg.go.dev/github.com/oakwood-commons/httpc)

A production-ready HTTP client library for Go with built-in retries, caching, circuit breaker, compression, and observability.

## Features

- **Automatic Retries**: Uses `github.com/hashicorp/go-retryablehttp` for intelligent retry logic
- **HTTP Caching**: Leverages `ivan.dev/httpcache` for efficient response caching (memory or filesystem)
- **Cache Warming**: Pre-populate cache with frequently accessed URLs
- **Circuit Breaker**: Prevent cascading failures with configurable circuit breaker per host
- **Request/Response Hooks**: Middleware-style processing for requests and responses
- **Automatic Compression**: Built-in gzip support for responses
- **Configurable Timeouts**: Set custom timeouts for all requests
- **Flexible Retry Policies**: Customize retry behavior with custom policies
- **Structured Logging**: Integrates with `logr.Logger` for consistent logging
- **Context Support**: Full support for context-based cancellation and timeouts
- **Pluggable Metrics**: `Metrics` interface for observability with any backend (Prometheus, OTel, etc.)
- **SSRF Protection**: Built-in private IP blocking to prevent server-side request forgery
- **Thread-Safe**: All operations are safe for concurrent use by multiple goroutines

## Installation

```bash
go get github.com/oakwood-commons/httpc
```

## Quick Start

### Basic Usage

~~~go
package main

import (
    "context"
    "fmt"
    "io"

    "github.com/oakwood-commons/httpc"
)

func main() {
    client := httpc.NewClient(nil)

    ctx := context.Background()
    resp, err := client.Get(ctx, "https://api.github.com/zen")
    if err != nil {
        panic(err)
    }
    defer resp.Body.Close()

    body, _ := io.ReadAll(resp.Body)
    fmt.Println(string(body))
}
~~~

### Custom Configuration

~~~go
config := &httpc.ClientConfig{
    Timeout:      10 * time.Second,
    RetryMax:     5,
    RetryWaitMin: 500 * time.Millisecond,
    RetryWaitMax: 10 * time.Second,
    EnableCache:  true,
    CacheTTL:     15 * time.Minute,
    Logger:       yourLogger, // logr.Logger instance
}

client := httpc.NewClient(config)
~~~

### From Application Config (YAML/JSON)

The `AppConfig` type uses string-based durations for easy embedding in config files:

~~~go
appCfg := &httpc.AppConfig{
    Timeout:      "60s",
    RetryMax:     5,
    RetryWaitMin: "2s",
    RetryWaitMax: "60s",
    EnableCache:  true,
    CacheType:    "filesystem",
    CacheDir:     "~/.myapp/http-cache",
    CacheTTL:     "30m",
}

client := httpc.NewClientFromAppConfig(appCfg, logger)
~~~

### Merging Configs

Use `MergeAppConfig` to layer per-scope overrides on top of global defaults:

~~~go
globalCfg := &httpc.AppConfig{Timeout: "30s", RetryMax: 3}
overrideCfg := &httpc.AppConfig{Timeout: "120s", RetryMax: 10}

merged := httpc.MergeAppConfig(globalCfg, overrideCfg)
client := httpc.NewClientFromAppConfig(merged, logger)
~~~

## Configuration Options

### ClientConfig Fields

| Field | Type | Default | Description |
| ----- | ---- | ------- | ----------- |
| `Timeout` | `time.Duration` | `30s` | Maximum time to wait for a request |
| `RetryMax` | `int` | `3` | Maximum number of retries |
| `RetryWaitMin` | `time.Duration` | `1s` | Minimum wait time between retries |
| `RetryWaitMax` | `time.Duration` | `30s` | Maximum wait time between retries |
| `EnableCache` | `bool` | `true` | Enable HTTP response caching |
| `CacheType` | `CacheType` | `filesystem` | Cache backend: `memory` or `filesystem` |
| `CacheDir` | `string` | OS cache dir | Directory for filesystem cache |
| `CacheTTL` | `time.Duration` | `10m` | Time-to-live for cached responses |
| `CacheKeyPrefix` | `string` | `httpc:` | Prefix for cache keys |
| `MaxCacheFileSize` | `int64` | `10MB` | Maximum size for a single cached file |
| `MemoryCacheSize` | `int` | `1000` | Maximum entries in memory cache |
| `EnableCircuitBreaker` | `bool` | `false` | Enable circuit breaker pattern |
| `CircuitBreakerConfig` | `*CircuitBreakerConfig` | See below | Circuit breaker settings |
| `EnableCompression` | `bool` | `true` | Enable gzip compression |
| `AllowPrivateIPs` | `bool` | `false` | Allow requests to private/internal IPs |
| `Metrics` | `Metrics` | `NoopMetrics{}` | Metrics collector interface |
| `Logger` | `logr.Logger` | Discard | Logger for client operations |

### CircuitBreakerConfig Fields

| Field | Type | Default | Description |
| ----- | ---- | ------- | ----------- |
| `MaxFailures` | `int` | `5` | Consecutive failures before opening circuit |
| `OpenTimeout` | `time.Duration` | `30s` | Time before transitioning Open to HalfOpen |
| `HalfOpenMaxRequests` | `int` | `1` | Successes required in HalfOpen to close |

## Metrics

httpc uses a pluggable `Metrics` interface. Implement it to connect to your metrics backend:

~~~go
type Metrics interface {
    RecordRequestDuration(ctx context.Context, method, host, pathTemplate string, statusCode int, duration time.Duration)
    IncrementRequestsTotal(ctx context.Context, method, host, pathTemplate string, statusCode int)
    IncrementErrorsTotal(ctx context.Context, method, host, pathTemplate, errorType string)
    IncrementRetries(ctx context.Context, method, host, pathTemplate string)
    IncrementCacheHits(ctx context.Context)
    IncrementCacheMisses(ctx context.Context)
    SetCacheSizeBytes(bytes int64)
    SetCircuitBreakerState(host string, state float64)
    IncrementConcurrentRequests(ctx context.Context)
    DecrementConcurrentRequests(ctx context.Context)
    RecordRequestSize(ctx context.Context, method, host, pathTemplate string, bytes float64)
    RecordResponseSize(ctx context.Context, method, host, pathTemplate string, bytes float64)
}
~~~

The default `NoopMetrics{}` discards all metrics. Pass your implementation via `ClientConfig.Metrics`.

## API Methods

~~~go
resp, err := client.Get(ctx, url)
resp, err := client.Post(ctx, url, contentType, body)
resp, err := client.Put(ctx, url, contentType, body)
resp, err := client.Delete(ctx, url)
resp, err := client.Do(req) // custom *http.Request
~~~

## Advanced Usage

### Circuit Breaker

~~~go
config := httpc.DefaultConfig()
config.EnableCircuitBreaker = true
config.CircuitBreakerConfig = &httpc.CircuitBreakerConfig{
    MaxFailures:         5,
    OpenTimeout:         30 * time.Second,
    HalfOpenMaxRequests: 2,
}
client := httpc.NewClient(config)
~~~

When the circuit is open, requests immediately fail with `httpc.ErrCircuitBreakerOpen`.

### Request and Response Hooks

~~~go
config := httpc.DefaultConfig()
config.RequestHooks = []httpc.RequestHook{
    func(req *http.Request) error {
        req.Header.Set("Authorization", "Bearer "+getToken())
        return nil
    },
}
client := httpc.NewClient(config)
~~~

### Cache Management

~~~go
client.WarmCache(ctx, []string{"https://api.example.com/config"})
client.ClearCache()
client.CleanExpiredCache()
client.DeleteCacheEntry(ctx, "https://api.example.com/data")
stats := client.CacheStats()
~~~

### SSRF Protection

By default, requests to private/internal IP ranges are blocked. Protection covers
IP literals and a small set of well-known private hostnames (e.g., `localhost`,
`metadata.google.internal`). It does **not** DNS-resolve arbitrary hostnames, so
a hostname that resolves to a private IP will not be blocked. Disable with:

~~~go
config := httpc.DefaultConfig()
config.AllowPrivateIPs = true
client := httpc.NewClient(config)
~~~

## Thread Safety

- **Client**: Multiple goroutines can safely share a single instance
- **FileCache**: Thread-safe within a single process (atomic file ops)
- **MemoryCache**: Fully thread-safe with atomic statistics
- **Circuit Breaker**: All state transitions are mutex-protected

## Development

~~~bash
task test          # Run tests
task lint          # Run linter
task bench         # Run benchmarks
task coverage:html # Generate coverage report
task ci            # Full CI pipeline
~~~

## License

Apache-2.0 -- see [LICENSE](LICENSE) for details.
