// Copyright 2025-2026 Oakwood Commons
// SPDX-License-Identifier: Apache-2.0

package httpc

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestCircuitBreakerStateMachine(t *testing.T) {
	config := &CircuitBreakerConfig{
		MaxFailures:         3,
		OpenTimeout:         100 * time.Millisecond,
		HalfOpenMaxRequests: 2,
	}

	cb := newCircuitBreaker(config, NoopMetrics{})
	host := "example.com"

	// Initial state should be closed
	assert.Equal(t, StateClosed, cb.getState(host))

	// Allow requests in closed state
	err := cb.allow(host)
	assert.NoError(t, err)

	// Record failures
	for i := 0; i < 3; i++ {
		cb.recordFailure(host)
	}

	// Should transition to open
	assert.Equal(t, StateOpen, cb.getState(host))

	// Should reject requests when open
	err = cb.allow(host)
	assert.Equal(t, ErrCircuitBreakerOpen, err)

	// Wait for open timeout
	time.Sleep(150 * time.Millisecond)

	// Should transition to half-open
	err = cb.allow(host)
	assert.NoError(t, err)
	assert.Equal(t, StateHalfOpen, cb.getState(host))

	// Record successes in half-open
	cb.recordSuccess(host)
	cb.recordSuccess(host)

	// Should transition to closed after enough successes
	assert.Equal(t, StateClosed, cb.getState(host))
}

func TestCircuitBreakerFailureInHalfOpen(t *testing.T) {
	config := &CircuitBreakerConfig{
		MaxFailures:         2,
		OpenTimeout:         50 * time.Millisecond,
		HalfOpenMaxRequests: 1,
	}

	cb := newCircuitBreaker(config, NoopMetrics{})
	host := "example.com"

	// Force open state
	cb.recordFailure(host)
	cb.recordFailure(host)
	assert.Equal(t, StateOpen, cb.getState(host))

	// Wait and transition to half-open
	time.Sleep(100 * time.Millisecond)
	err := cb.allow(host)
	assert.NoError(t, err)

	// Fail in half-open state
	cb.recordFailure(host)

	// Should go back to open
	assert.Equal(t, StateOpen, cb.getState(host))
}

func TestCircuitBreakerMultipleHosts(t *testing.T) {
	config := &CircuitBreakerConfig{
		MaxFailures:         2,
		OpenTimeout:         100 * time.Millisecond,
		HalfOpenMaxRequests: 1,
	}

	cb := newCircuitBreaker(config, NoopMetrics{})

	host1 := "example1.com"
	host2 := "example2.com"

	// Fail host1
	cb.recordFailure(host1)
	cb.recordFailure(host1)
	assert.Equal(t, StateOpen, cb.getState(host1))

	// host2 should still be closed
	assert.Equal(t, StateClosed, cb.getState(host2))

	// host2 should allow requests
	err := cb.allow(host2)
	assert.NoError(t, err)

	// host1 should reject requests
	err = cb.allow(host1)
	assert.Equal(t, ErrCircuitBreakerOpen, err)
}

func TestCircuitBreakerSuccessResetsFailures(t *testing.T) {
	config := &CircuitBreakerConfig{
		MaxFailures:         3,
		OpenTimeout:         100 * time.Millisecond,
		HalfOpenMaxRequests: 1,
	}

	cb := newCircuitBreaker(config, NoopMetrics{})
	host := "example.com"

	// Record some failures
	cb.recordFailure(host)
	cb.recordFailure(host)

	// Record success - should reset failure count
	cb.recordSuccess(host)

	// Record more failures (need 3 to open)
	cb.recordFailure(host)
	cb.recordFailure(host)

	// Should still be closed (only 2 consecutive failures)
	assert.Equal(t, StateClosed, cb.getState(host))
}

func TestCircuitBreakerHalfOpenMaxRequestsExceeded(t *testing.T) {
	config := &CircuitBreakerConfig{
		MaxFailures:         2,
		OpenTimeout:         50 * time.Millisecond,
		HalfOpenMaxRequests: 1,
	}

	cb := newCircuitBreaker(config, NoopMetrics{})
	host := "example.com"

	// Force open state
	cb.recordFailure(host)
	cb.recordFailure(host)
	assert.Equal(t, StateOpen, cb.getState(host))

	// Wait and transition to half-open; the transition request counts toward the limit.
	time.Sleep(100 * time.Millisecond)
	err := cb.allow(host)
	assert.NoError(t, err)
	assert.Equal(t, StateHalfOpen, cb.getState(host))

	// With HalfOpenMaxRequests=1, the transition request already consumed the
	// allowance, so the next call must be rejected.
	err = cb.allow(host)
	assert.Equal(t, ErrCircuitBreakerOpen, err)
}

func TestCircuitBreakerHalfOpenMultipleRequests(t *testing.T) {
	config := &CircuitBreakerConfig{
		MaxFailures:         2,
		OpenTimeout:         50 * time.Millisecond,
		HalfOpenMaxRequests: 3,
	}

	cb := newCircuitBreaker(config, NoopMetrics{})
	host := "example.com"

	// Force open state
	cb.recordFailure(host)
	cb.recordFailure(host)

	// Wait and transition to half-open
	time.Sleep(100 * time.Millisecond)

	// With HalfOpenMaxRequests=3: transition uses 1, then 2 more allowed.
	err := cb.allow(host) // transition request (#1)
	assert.NoError(t, err)
	err = cb.allow(host) // #2
	assert.NoError(t, err)
	err = cb.allow(host) // #3
	assert.NoError(t, err)

	// #4 should be rejected
	err = cb.allow(host)
	assert.Equal(t, ErrCircuitBreakerOpen, err)
}
