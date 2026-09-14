# AGENTS.md

Guidance for AI coding agents working in this repository. Every major agent
tool reads this file, so it is the single place for project-wide context.

**How to build, test, and verify a change lives in the `httpc-dev-loop`
skill** (`.opencode/skills/httpc-dev-loop/SKILL.md`), not here. Read it before
running any command. This file covers what the project *is*; that one covers
how work gets done in it.

## What this is

`github.com/oakwood-commons/httpc` is a production HTTP client library for Go:
retries, response caching, a per-host circuit breaker, transparent gzip,
OpenTelemetry tracing, and SSRF protection. It is a **library**, not an
application -- it is consumed by other projects, which constrains several
conventions below.

Single Go package (`package httpc`), all source at the repository root. There
are no subpackages, so any file can reach any other; keep each file's
responsibility clear instead of relying on package boundaries.

## Where things live

| File | Responsibility |
| --- | --- |
| `client.go` | `Client`, `ClientConfig`, request lifecycle, transport-chain assembly, hooks, auth retry |
| `appconfig.go` | `AppConfig` (YAML/JSON), `NewClientFromAppConfig`, `MergeAppConfig` |
| `ssrf.go` | `IPPolicy`, blocklists, URL/IP validation, `ControlFunc`, `ErrBlockedByPolicy` |
| `circuitbreaker.go` | Per-host circuit breaker and its config |
| `compression.go` | gzip round-tripper, decompression-bomb guard |
| `filecache.go` | Filesystem response cache (atomic write/rename, TTL) |
| `memorycache.go` | In-memory cache wrapper adding hit/miss metrics |
| `metrics.go` | `Metrics` interface and `NoopMetrics` |
| `metrics_transport.go` | Metrics-recording round-tripper, path parameterization |
| `defaults.go` | Every `Default*` constant |
| `helpers.go` | Retry-policy and backoff builders |

Entry points: `NewClient(*ClientConfig) *Client` and `DefaultConfig()` in
`client.go`; `NewClientFromAppConfig` in `appconfig.go`.

## The transport chain

Most behaviour is layered round-trippers, assembled in `client.go`. Knowing
the order matters, because a change in the wrong layer silently does nothing:

```
Client.Do
  -> retryablehttp
    -> httpcache        (only when EnableCache)
      -> compression    (only when enabled)
        -> metrics
          -> otelhttp
            -> markingTransport  (tags a request that selected a proxy)
              -> *http.Transport with the SSRF policy on its dialer
```

Two things sit outside that chain and are easy to miss:

- **The circuit breaker is not a transport.** It is checked directly in
  `Client.Do`, per host. A request that never reaches `Do` never trips it.
- **Each client owns its transport**, cloned rather than sharing
  `http.DefaultTransport`, so the SSRF policy can be wired into the dialer.
  `Client.Close` therefore has to release that client's idle connections.

## SSRF protection: three enforcement points

Security-critical and the most subtle area of the codebase. Read `ssrf.go`'s
comments before changing anything here.

1. **URL validation** in `Client.Do` and in `CheckRedirect` -- catches IP
   literals and blocked hostnames early, including across redirects.
2. **Dial-time enforcement** via `net.Dialer.Control` -- the authoritative
   check. It runs after DNS resolution on the real socket address, so it
   cannot be defeated by a hostname that resolves to a private address, and it
   has no time-of-check/time-of-use gap.
3. **Proxy-path validation** -- when a proxy is used the transport dials the
   proxy, not the target, so the dial hook never sees the target. The target
   is resolved and validated in the `Proxy` hook instead.

Cloud metadata addresses are blocked unconditionally: no configuration can
re-enable them. When adding a range, decide deliberately whether it is
exemptible (`privateCIDRs`) or not (`metadataCIDRs`, `nonDecodableCIDRs`) --
placing it in the wrong list is how an exemption silently reaches metadata.

## Conventions

These are observable in the code; match them rather than inventing your own.

- **License header on every file**, enforced by the `goheader` linter:

  ```go
  // Copyright 2025-2026 Oakwood Commons
  // SPDX-License-Identifier: Apache-2.0
  ```

- **Errors** wrap with context: `fmt.Errorf("failed to X: %w", err)`. Sentinel
  errors are package-level `errors.New` values. Never panic in library code.
- **Logging** goes through `logr.Logger`. Never `log.Printf` or `fmt.Printf` --
  a library must not write to a consumer's stdout.
- **Metrics** go through the `Metrics` interface. Never import a concrete
  metrics backend; that choice belongs to the consumer.
- **No hardcoded consumer names.** `CacheKeyPrefix` and `CacheDir` are
  configurable for exactly this reason.
- **Public types are safe for concurrent use.** Keep them that way.
- **Comment the "why" on security-critical code.** `ssrf.go` and the proxy
  handling in `client.go` are the models: they explain the attack being
  prevented, not the syntax.

## Library constraints

- **Pre-1.0: breaking changes are allowed**, but must be called out in the
  commit body with a `BREAKING CHANGE:` trailer, since the changelog is
  generated from commits.
- **`httpc` is the HTTP client for `scafctl`.** A breaking public-API change
  needs coordination with that project -- see `CONTRIBUTING.md`.
