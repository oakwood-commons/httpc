// Copyright 2025-2026 Oakwood Commons
// SPDX-License-Identifier: Apache-2.0

package httpc

import (
	"context"
	"sync/atomic"
	"time"

	"ivan.dev/httpcache"
)

// metricsMemoryCache wraps an httpcache.Cache to track hits and misses.
//
// Thread-Safety: metricsMemoryCache is safe for concurrent use by multiple goroutines.
// The underlying base cache (httpcache.MemoryCache) is thread-safe, and hit/miss
// statistics are tracked using atomic operations. All methods can be called concurrently
// without additional synchronization.
type metricsMemoryCache struct {
	base    httpcache.Cache
	metrics Metrics
	hits    uint64
	misses  uint64
}

// newMetricsMemoryCache creates a new memory cache wrapper with metrics tracking
func newMetricsMemoryCache(base httpcache.Cache, m Metrics) *metricsMemoryCache {
	if m == nil {
		m = NoopMetrics{}
	}
	return &metricsMemoryCache{
		base:    base,
		metrics: m,
	}
}

// Set stores data in the cache with the given key
func (m *metricsMemoryCache) Set(ctx context.Context, key string, data []byte, ttl time.Duration) error {
	return m.base.Set(ctx, key, data, ttl)
}

// Get retrieves data from the cache for the given key
// Returns (nil, nil) for cache misses - this is not an error, it's standard cache behavior
func (m *metricsMemoryCache) Get(ctx context.Context, key string) ([]byte, error) {
	data, err := m.base.Get(ctx, key)

	// Track cache hit/miss based on data and error.
	// Only nil data indicates a miss; empty data (len==0) is a valid cached entry
	// (e.g. HTTP 204 No Content responses).
	if err != nil || data == nil {
		// Cache miss
		atomic.AddUint64(&m.misses, 1)
		m.metrics.IncrementCacheMisses(ctx)
		// Return nil, nil for httpcache compatibility (ignore base cache error)
		return nil, nil //nolint:nilerr // httpcache expects (nil, nil) for cache misses
	}

	// Cache hit
	atomic.AddUint64(&m.hits, 1)
	m.metrics.IncrementCacheHits(ctx)

	return data, nil
}

// Del removes data from the cache for the given key
func (m *metricsMemoryCache) Del(ctx context.Context, key string) error {
	return m.base.Del(ctx, key)
}

// Stats returns the cache hit and miss statistics
func (m *metricsMemoryCache) Stats() (hits, misses uint64) {
	return atomic.LoadUint64(&m.hits), atomic.LoadUint64(&m.misses)
}
