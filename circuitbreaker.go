// Copyright 2025-2026 Oakwood Commons
// SPDX-License-Identifier: Apache-2.0

package httpc

import (
	"errors"
	"sync"
	"time"
)

// ErrCircuitBreakerOpen is returned when the circuit breaker is open and prevents requests
var ErrCircuitBreakerOpen = errors.New("circuit breaker is open")

// CircuitBreakerState represents the state of a circuit breaker
type CircuitBreakerState int

const (
	// StateClosed allows all requests through
	StateClosed CircuitBreakerState = iota
	// StateOpen blocks all requests
	StateOpen
	// StateHalfOpen allows a single test request through
	StateHalfOpen
)

// CircuitBreakerConfig holds configuration for circuit breaker
type CircuitBreakerConfig struct {
	// MaxFailures is the number of consecutive failures before opening the circuit
	MaxFailures int
	// OpenTimeout is how long to wait before transitioning from Open to HalfOpen
	OpenTimeout time.Duration
	// HalfOpenMaxRequests is the number of successful requests in HalfOpen before closing
	HalfOpenMaxRequests int
}

// DefaultCircuitBreakerConfig returns default circuit breaker configuration
func DefaultCircuitBreakerConfig() *CircuitBreakerConfig {
	return &CircuitBreakerConfig{
		MaxFailures:         DefaultCircuitBreakerMaxFailures,
		OpenTimeout:         DefaultCircuitBreakerOpenTimeout,
		HalfOpenMaxRequests: DefaultCircuitBreakerHalfOpenRequests,
	}
}

// circuitBreaker implements the circuit breaker pattern per host.
//
// Thread-Safety: circuitBreaker is safe for concurrent use by multiple goroutines.
// All state changes are protected by a mutex.
type circuitBreaker struct {
	config   *CircuitBreakerConfig
	mu       sync.RWMutex
	breakers map[string]*hostBreaker
	metrics  Metrics
}

// hostBreaker tracks state for a specific host
type hostBreaker struct {
	state           CircuitBreakerState
	failures        int
	lastStateChange time.Time
	halfOpenSuccess int
	halfOpenAllowed int // number of requests allowed through in half-open state
}

// newCircuitBreaker creates a new circuit breaker
func newCircuitBreaker(config *CircuitBreakerConfig, m Metrics) *circuitBreaker {
	if config == nil {
		config = DefaultCircuitBreakerConfig()
	}
	if m == nil {
		m = NoopMetrics{}
	}

	return &circuitBreaker{
		config:   config,
		breakers: make(map[string]*hostBreaker),
		metrics:  m,
	}
}

// allow checks if a request should be allowed for the given host.
// In half-open state, only HalfOpenMaxRequests total requests are permitted
// before a state transition (success → closed, failure → open).
func (cb *circuitBreaker) allow(host string) error {
	// Fast path: read-lock to check the common closed state without contention.
	cb.mu.RLock()
	hb, exists := cb.breakers[host]
	if exists && hb.state == StateClosed {
		cb.mu.RUnlock()
		return nil
	}
	cb.mu.RUnlock()

	// Slow path: acquire write lock for state transitions or new hosts.
	cb.mu.Lock()
	hb = cb.getOrCreateBreaker(host)

	switch hb.state {
	case StateOpen:
		if time.Since(hb.lastStateChange) >= cb.config.OpenTimeout {
			hb.state = StateHalfOpen
			hb.lastStateChange = time.Now()
			hb.halfOpenAllowed = 1 // count the transition request
			cb.mu.Unlock()
			cb.metrics.SetCircuitBreakerState(host, float64(StateHalfOpen))
		} else {
			cb.mu.Unlock()
			return ErrCircuitBreakerOpen
		}
	case StateHalfOpen:
		if hb.halfOpenAllowed >= cb.config.HalfOpenMaxRequests {
			cb.mu.Unlock()
			return ErrCircuitBreakerOpen
		}
		hb.halfOpenAllowed++
		cb.mu.Unlock()
	case StateClosed:
		cb.mu.Unlock()
	}

	return nil
}

// recordSuccess records a successful request
func (cb *circuitBreaker) recordSuccess(host string) {
	cb.mu.Lock()
	hb := cb.getOrCreateBreaker(host)

	var newState CircuitBreakerState
	stateChanged := false

	switch hb.state {
	case StateHalfOpen:
		hb.halfOpenSuccess++
		if hb.halfOpenSuccess >= cb.config.HalfOpenMaxRequests {
			newState = StateClosed
			stateChanged = true
			hb.state = StateClosed
			hb.lastStateChange = time.Now()
			hb.failures = 0
			hb.halfOpenSuccess = 0
			hb.halfOpenAllowed = 0
		}
	case StateClosed:
		// Reset failures on success
		hb.failures = 0
	case StateOpen:
		// No action needed in open state
	}
	cb.mu.Unlock()

	if stateChanged {
		cb.metrics.SetCircuitBreakerState(host, float64(newState))
	}
}

// recordFailure records a failed request
func (cb *circuitBreaker) recordFailure(host string) {
	cb.mu.Lock()
	hb := cb.getOrCreateBreaker(host)

	var newState CircuitBreakerState
	stateChanged := false

	switch hb.state {
	case StateHalfOpen:
		// Failed in half-open, go back to open
		newState = StateOpen
		stateChanged = true
		hb.state = StateOpen
		hb.lastStateChange = time.Now()
		hb.halfOpenSuccess = 0
		hb.halfOpenAllowed = 0
	case StateClosed:
		hb.failures++
		if hb.failures >= cb.config.MaxFailures {
			newState = StateOpen
			stateChanged = true
			hb.state = StateOpen
			hb.lastStateChange = time.Now()
		}
	case StateOpen:
		// No action needed, already open
	}
	cb.mu.Unlock()

	if stateChanged {
		cb.metrics.SetCircuitBreakerState(host, float64(newState))
	}
}

// getOrCreateBreaker gets or creates a breaker for a host
func (cb *circuitBreaker) getOrCreateBreaker(host string) *hostBreaker {
	hb, exists := cb.breakers[host]
	if !exists {
		hb = &hostBreaker{
			state:           StateClosed,
			lastStateChange: time.Now(),
		}
		cb.breakers[host] = hb
		cb.metrics.SetCircuitBreakerState(host, float64(StateClosed))
	}
	return hb
}

// getState returns the current state for a host
func (cb *circuitBreaker) getState(host string) CircuitBreakerState {
	cb.mu.RLock()
	defer cb.mu.RUnlock()

	hb, exists := cb.breakers[host]
	if !exists {
		return StateClosed
	}
	return hb.state
}
