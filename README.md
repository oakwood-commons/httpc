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
| `AllowPrivateIPs` | `bool` | `false` | Deprecated: allow requests to all private/internal IPs |
| `IPPolicy` | `*IPPolicy` | `nil` | Which destination IPs may be reached (see SSRF Protection) |
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

By default, requests to private, loopback, link-local, CGNAT, and other reserved
IP ranges are blocked. The policy is enforced in three places:

- the request URL (scheme allowlist -- only `http`/`https` -- plus IP literals,
  non-canonical IP forms such as `0x7f000001`, and well-known hostnames like
  `localhost`)
- every redirect target
- **at dial time, on the resolved address**, so a hostname that resolves to a
  private IP (including DNS rebinding) is blocked too

Cloud instance-metadata endpoints (`169.254.169.254`, `169.254.170.2`,
`fd00:ec2::254`) are blocked unconditionally and cannot be re-enabled by any
configuration. The same applies to the metadata hostnames
(`metadata.google.internal`, `metadata.goog`).

Loopback hostnames (`localhost`, `localhost.localdomain`) are different: they
follow the policy. They are rejected by default, and permitted once the policy
allows a loopback address, so allowing `127.0.0.0/8` or `::1/128` lets you use
the name rather than forcing the literal.

The one case where this library cannot enforce that itself is a name it never
resolves: with `TrustProxyResolution` enabled, a proxied hostname that does not
resolve locally is forwarded to the proxy unchecked, so it could resolve at the
proxy to a metadata address. Enabling that option therefore delegates metadata
blocking for those names to the proxy. See [Proxies](#proxies).

IPv6 encodings that carry an IPv4 address are judged on that embedded address,
so `64:ff9b::169.254.169.254` is blocked while `64:ff9b::8.8.8.8` is not --
blocking the prefixes outright would cut off every IPv4 destination on a
DNS64/NAT64 network. This covers IPv4-mapped `::ffff:a.b.c.d` (handled natively
by Go's `net` package), IPv4-compatible `::a.b.c.d`, NAT64 `64:ff9b::/96`, and
6to4 `2002::/16`. The one exception is RFC 8215's `64:ff9b:1::/48`, whose prefix
length is variable: the embedded address cannot be located reliably, so that
range is blocked wholesale and, like the metadata endpoints, cannot be
re-enabled by any policy.

Every denial wraps `httpc.ErrBlockedByPolicy`, so it can be identified with
`errors.Is` and is never retried.

Two behaviour changes to note when upgrading: non-`http(s)` schemes and URLs
with no host (e.g. a bare path) are now rejected by `ValidateURLNotPrivate`
rather than passing, and the scheme check applies even when private addresses
are allowed.

To reach a specific internal range, allow just that range rather than disabling
protection wholesale:

~~~go
policy, err := httpc.NewIPPolicy("10.0.0.0/8")
if err != nil {
    return err
}
config := httpc.DefaultConfig()
config.IPPolicy = policy
client := httpc.NewClient(config)
~~~

Or, via `AppConfig`:

~~~yaml
allowedPrivateCIDRs:
  - 10.0.0.0/8
~~~

To allow every private range (metadata endpoints still excluded):

~~~go
config.IPPolicy = httpc.AllowAllPrivateIPs()
~~~

`config.AllowPrivateIPs = true` is the deprecated equivalent, kept for
backwards compatibility and ignored when `IPPolicy` is set.

A caller-supplied `ClientConfig.Transport` is enforced too. Setting a policy is
an explicit request, so httpc does not quietly decline it:

- An `*http.Transport` with no dialer of its own gets the policy on its dialer's
  `Control` hook, which refuses before a connection exists.
- An `*http.Transport` that brings its own dialer (including a TLS dial hook)
  keeps it -- you set it for a reason -- and the address it actually connected
  to is checked immediately afterwards, with the connection closed if the policy
  denies it. No HTTP request is sent to a denied address. This is marginally
  weaker than `Control` only in that the TCP connection is established first;
  it still judges the real peer address, so a hostname or a rebind cannot fool
  it.
- A `Transport` that is not an `*http.Transport` has no dialer to hook. It falls
  back to validating each request URL, resolving the hostname first. That check
  races DNS, so it is genuinely weaker; a warning says so. For full enforcement,
  install `policy.ControlFunc()` as the `Control` hook of your own `net.Dialer`.

#### Caching

The response cache sits above the transport, so a cache hit is returned
without the dial-time check running. Cache keys therefore include a digest of
the IP policy -- including its resolver, which decides what a proxied hostname
is judged on. Clients sharing a `CacheDir` but configured with different
policies do not share entries, and a restrictive client is never served a
response a permissive one fetched.

A client that injects a custom `Resolver` is identified partly by pointer,
which is not stable across processes, so it gets no cross-process reuse of a
filesystem cache. Callers who do not set one are unaffected.

A consequence worth knowing: changing the policy invalidates that client's
cached entries, since the key changes with it.

#### Proxies

When a request goes through a proxy, the client dials the proxy rather than the
target, so the dial-time check cannot see the target address. In that case:

- dials belonging to that request are exempt from the policy, since they go to
  the proxy (a proxy on a private address is the normal corporate setup). The
  exemption is scoped to the individual request, so it cannot be reused to reach
  the proxy's address directly.
- the target is validated in the proxy-selection hook instead, including a DNS
  lookup of the target host -- best-effort, with the TOCTOU window that dial-time
  enforcement otherwise avoids
- if that lookup fails, the request is refused. This includes "no such host":
  the name may still resolve for the proxy, and an attacker who can make a name
  unresolvable here would otherwise gain an unchecked egress path. Set
  `IPPolicy.TrustProxyResolution` (or `trustProxyResolution` in `AppConfig`) to
  `true` to allow an unresolvable target through and defer to the proxy -- the
  right setting for a proxy-only environment with no direct resolver, but it is
  opt-in because the secure default is to fail closed. Even when enabled, only
  "no such host" is relaxed; every other DNS error stays fatal.
- verdicts are cached per host to avoid a DNS lookup on every round trip.
  Denials are cached for 30s, successes for only 1s: a 1s window still collapses
  a burst of requests to one host into a single lookup, while leaving a residual
  1s window in which a DNS rebind could be laundered past the check. That window
  is not closed entirely by any TTL -- this check is inherently TOCTOU against
  the proxy's own resolution.

#### Connection pooling

Each client builds its own transport (a clone of `http.DefaultTransport`) so the
policy can be wired into the dialer. Clients therefore do not share
`http.DefaultTransport`'s idle-connection pool. Reuse a single client rather than
constructing many short-lived ones.

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
