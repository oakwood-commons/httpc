// Copyright 2025-2026 Oakwood Commons
// SPDX-License-Identifier: Apache-2.0

package httpc

import (
	"strconv"
	"testing"
	"time"
)

// BenchmarkTargetVerdictCacheHit measures the per-request cost of a cached
// proxied-target verdict -- the path every proxied request takes once a host
// has been validated, so it is the one that has to stay cheap.
func BenchmarkTargetVerdictCacheHit(b *testing.B) {
	cache := &targetVerdictCache{}
	now := time.Now()
	validate := func() error { return nil }

	// Prime the entry so every measured iteration is a hit.
	if err := cache.check("benchmark.test", now, validate); err != nil {
		b.Fatalf("priming the cache: %v", err)
	}

	b.ReportAllocs()
	for b.Loop() {
		_ = cache.check("benchmark.test", now, validate)
	}
}

// BenchmarkTargetVerdictCacheMiss measures the admission path: cap check,
// eviction sweep and insert, all serialised behind the admit mutex. Each
// iteration uses a distinct host so it is always a miss, which also exercises
// eviction once the cap is reached.
func BenchmarkTargetVerdictCacheMiss(b *testing.B) {
	cache := &targetVerdictCache{}
	now := time.Now()
	validate := func() error { return nil }

	i := 0
	b.ReportAllocs()
	for b.Loop() {
		_ = cache.check("miss-"+strconv.Itoa(i)+".test", now, validate)
		i++
	}
}
