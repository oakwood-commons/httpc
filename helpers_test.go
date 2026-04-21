// Copyright 2025-2026 Oakwood Commons
// SPDX-License-Identifier: Apache-2.0

package httpc

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildStatusCodeCheckRetry_TableDriven(t *testing.T) {
	checkRetry := BuildStatusCodeCheckRetry([]int{503, 429, 502})

	tests := []struct {
		name       string
		statusCode int
		wantRetry  bool
	}{
		{name: "retry on 503", statusCode: 503, wantRetry: true},
		{name: "retry on 429", statusCode: 429, wantRetry: true},
		{name: "retry on 502", statusCode: 502, wantRetry: true},
		{name: "no retry on 200", statusCode: 200, wantRetry: false},
		{name: "no retry on 404", statusCode: 404, wantRetry: false},
		{name: "no retry on 500", statusCode: 500, wantRetry: false},
		{name: "no retry on 201", statusCode: 201, wantRetry: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := &http.Response{StatusCode: tt.statusCode}
			got, err := checkRetry(context.Background(), resp, nil)
			require.NoError(t, err)
			assert.Equal(t, tt.wantRetry, got)
		})
	}
}

func TestBuildStatusCodeCheckRetry_EmptySlice(t *testing.T) {
	checkRetry := BuildStatusCodeCheckRetry(nil)
	resp := &http.Response{StatusCode: 503}
	got, err := checkRetry(context.Background(), resp, nil)
	require.NoError(t, err)
	assert.False(t, got, "nil status code list should not retry on status codes")
}

func TestBuildStatusCodeCheckRetry_NilResponse(t *testing.T) {
	checkRetry := BuildStatusCodeCheckRetry([]int{503})
	// nil response with error should delegate to default policy
	_, err := checkRetry(context.Background(), nil, assert.AnError)
	// Should not panic; error may or may not be nil depending on default policy
	_ = err
}

func TestBuildNamedBackoff_None(t *testing.T) {
	initial := 100 * time.Millisecond
	maxWait := 5 * time.Second
	backoff := BuildNamedBackoff(BackoffNone, initial, maxWait)

	for attempt := 0; attempt < 10; attempt++ {
		wait := backoff(0, 0, attempt, nil)
		assert.Equal(t, initial, wait, "none strategy should always return initialWait for attempt %d", attempt)
	}
}

func TestBuildNamedBackoff_Linear(t *testing.T) {
	initial := 100 * time.Millisecond
	maxWait := 500 * time.Millisecond
	backoff := BuildNamedBackoff(BackoffLinear, initial, maxWait)

	tests := []struct {
		attempt int
		want    time.Duration
	}{
		{attempt: 0, want: 100 * time.Millisecond},  // 100ms * 1
		{attempt: 1, want: 200 * time.Millisecond},  // 100ms * 2
		{attempt: 2, want: 300 * time.Millisecond},  // 100ms * 3
		{attempt: 3, want: 400 * time.Millisecond},  // 100ms * 4
		{attempt: 4, want: 500 * time.Millisecond},  // 100ms * 5, clamped to max
		{attempt: 10, want: 500 * time.Millisecond}, // clamped to max
	}
	for _, tt := range tests {
		wait := backoff(0, 0, tt.attempt, nil)
		assert.Equal(t, tt.want, wait, "linear attempt %d", tt.attempt)
	}
}

func TestBuildNamedBackoff_Exponential(t *testing.T) {
	initial := 100 * time.Millisecond
	maxWait := 10 * time.Second
	backoff := BuildNamedBackoff(BackoffExponential, initial, maxWait)

	tests := []struct {
		attempt int
		want    time.Duration
	}{
		{attempt: 0, want: 100 * time.Millisecond},              // 100ms * 2^0
		{attempt: 1, want: 200 * time.Millisecond},              // 100ms * 2^1
		{attempt: 2, want: 400 * time.Millisecond},              // 100ms * 2^2
		{attempt: 3, want: 800 * time.Millisecond},              // 100ms * 2^3
		{attempt: 10, want: 100 * time.Millisecond * (1 << 10)}, // 100ms * 1024 = capped at max
		{attempt: 20, want: 100 * time.Millisecond * (1 << 10)}, // exponent capped at 10
	}
	for _, tt := range tests {
		wait := backoff(0, 0, tt.attempt, nil)
		if tt.want > maxWait {
			assert.Equal(t, maxWait, wait, "exponential attempt %d should be clamped to maxWait", tt.attempt)
		} else {
			assert.Equal(t, tt.want, wait, "exponential attempt %d", tt.attempt)
		}
	}
}

func TestBuildNamedBackoff_UnknownStrategy(t *testing.T) {
	initial := 100 * time.Millisecond
	maxWait := 5 * time.Second
	backoff := BuildNamedBackoff("unknown-strategy", initial, maxWait)

	// Unknown strategy should behave like "none" (returns initialWait)
	for attempt := 0; attempt < 5; attempt++ {
		wait := backoff(0, 0, attempt, nil)
		assert.Equal(t, initial, wait, "unknown strategy should return initialWait for attempt %d", attempt)
	}
}

func TestBuildNamedBackoff_ClampToMax(t *testing.T) {
	initial := 1 * time.Second
	maxWait := 2 * time.Second
	backoff := BuildNamedBackoff(BackoffLinear, initial, maxWait)

	// Attempt 5 would be 6s, should be clamped to 2s
	wait := backoff(0, 0, 5, nil)
	assert.Equal(t, maxWait, wait, "should be clamped to maxWait")
}

func TestBuildNamedBackoff_ClampToMin(t *testing.T) {
	initial := 500 * time.Millisecond
	maxWait := 5 * time.Second
	backoff := BuildNamedBackoff(BackoffExponential, initial, maxWait)

	// First attempt should be at least initialWait
	wait := backoff(0, 0, 0, nil)
	assert.GreaterOrEqual(t, wait, initial, "should never be less than initialWait")
}

func TestBackoffConstants(t *testing.T) {
	assert.Equal(t, "none", BackoffNone)
	assert.Equal(t, "linear", BackoffLinear)
	assert.Equal(t, "exponential", BackoffExponential)
}
