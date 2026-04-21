// Copyright 2025-2026 Oakwood Commons
// SPDX-License-Identifier: Apache-2.0

package httpc

import (
	"context"
	"time"
)

// Metrics defines the interface for collecting HTTP client metrics.
// Implement this interface to integrate with your preferred metrics backend
// (Prometheus, OpenTelemetry, StatsD, etc.).
//
// All methods must be safe for concurrent use by multiple goroutines.
type Metrics interface {
	// RecordRequestDuration records the duration of an HTTP request.
	RecordRequestDuration(ctx context.Context, method, host, pathTemplate string, statusCode int, duration time.Duration)

	// IncrementRequestsTotal increments the total request counter.
	IncrementRequestsTotal(ctx context.Context, method, host, pathTemplate string, statusCode int)

	// IncrementErrorsTotal increments the error counter with the given error type.
	IncrementErrorsTotal(ctx context.Context, method, host, pathTemplate, errorType string)

	// IncrementRetries increments the retry counter.
	IncrementRetries(ctx context.Context, method, host, pathTemplate string)

	// IncrementCacheHits increments the cache hit counter.
	IncrementCacheHits(ctx context.Context)

	// IncrementCacheMisses increments the cache miss counter.
	IncrementCacheMisses(ctx context.Context)

	// SetCacheSizeBytes reports the total cache size in bytes.
	SetCacheSizeBytes(bytes int64)

	// SetCircuitBreakerState reports the circuit breaker state for a host.
	// State values: 0=closed, 1=open, 2=half-open.
	SetCircuitBreakerState(host string, state float64)

	// IncrementConcurrentRequests increments the in-flight request counter.
	IncrementConcurrentRequests(ctx context.Context)

	// DecrementConcurrentRequests decrements the in-flight request counter.
	DecrementConcurrentRequests(ctx context.Context)

	// RecordRequestSize records the size of an HTTP request body in bytes.
	RecordRequestSize(ctx context.Context, method, host, pathTemplate string, bytes float64)

	// RecordResponseSize records the size of an HTTP response body in bytes.
	RecordResponseSize(ctx context.Context, method, host, pathTemplate string, bytes float64)
}

// NoopMetrics is a no-op implementation of Metrics that discards all data.
// This is the default when no metrics backend is configured.
type NoopMetrics struct{}

var _ Metrics = NoopMetrics{}

func (NoopMetrics) RecordRequestDuration(context.Context, string, string, string, int, time.Duration) {
}
func (NoopMetrics) IncrementRequestsTotal(context.Context, string, string, string, int)  {}
func (NoopMetrics) IncrementErrorsTotal(context.Context, string, string, string, string) {}
func (NoopMetrics) IncrementRetries(context.Context, string, string, string)             {}
func (NoopMetrics) IncrementCacheHits(context.Context)                                   {}
func (NoopMetrics) IncrementCacheMisses(context.Context)                                 {}
func (NoopMetrics) SetCacheSizeBytes(int64)                                              {}
func (NoopMetrics) SetCircuitBreakerState(string, float64)                               {}
func (NoopMetrics) IncrementConcurrentRequests(context.Context)                          {}
func (NoopMetrics) DecrementConcurrentRequests(context.Context)                          {}
func (NoopMetrics) RecordRequestSize(context.Context, string, string, string, float64)   {}
func (NoopMetrics) RecordResponseSize(context.Context, string, string, string, float64)  {}
