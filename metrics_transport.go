// Copyright 2025-2026 Oakwood Commons
// SPDX-License-Identifier: Apache-2.0

package httpc

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"syscall"
	"time"
)

// metricsTransport is an http.RoundTripper that records metrics for HTTP requests
type metricsTransport struct {
	base    http.RoundTripper
	metrics Metrics
}

// newMetricsTransport creates a new metrics transport that wraps the base transport
func newMetricsTransport(base http.RoundTripper, m Metrics) *metricsTransport {
	if m == nil {
		m = NoopMetrics{}
	}
	return &metricsTransport{
		base:    base,
		metrics: m,
	}
}

// RoundTrip implements http.RoundTripper and records metrics for each request
func (t *metricsTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	start := time.Now()

	// Extract labels for metrics (method, host, and parameterized path template)
	method := req.Method
	host, pathTemplate := extractMetricLabels(req.URL)

	ctx := req.Context()

	// Track request size if body is present
	if req.Body != nil && req.ContentLength > 0 {
		t.metrics.RecordRequestSize(ctx, method, host, pathTemplate, float64(req.ContentLength))
	}

	// Execute the request
	resp, err := t.base.RoundTrip(req)

	// Calculate duration
	duration := time.Since(start)

	// Determine status code (-1 indicates no response received, e.g. network error)
	statusCode := -1
	if resp != nil {
		statusCode = resp.StatusCode
		// Track response size if available
		if resp.ContentLength > 0 {
			t.metrics.RecordResponseSize(ctx, method, host, pathTemplate, float64(resp.ContentLength))
		}
	}

	// Record request duration
	t.metrics.RecordRequestDuration(ctx, method, host, pathTemplate, statusCode, duration)

	// Record request counter
	t.metrics.IncrementRequestsTotal(ctx, method, host, pathTemplate, statusCode)

	// Record errors if present
	if err != nil {
		errorType := categorizeError(err)
		t.metrics.IncrementErrorsTotal(ctx, method, host, pathTemplate, errorType)
	}

	return resp, err
}

// categorizeError categorizes a transport-level error into a specific error type.
// This is only called when err != nil, meaning a transport failure occurred.
// HTTP-level errors (4xx/5xx) are not transport errors and are tracked via status code metrics.
func categorizeError(err error) string {
	if err == nil {
		return "none"
	}

	// Check for context errors
	if errors.Is(err, context.Canceled) {
		return "context_canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "context_timeout"
	}

	// Check for network timeout errors
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return "network_timeout"
	}

	// Check for connection refused errors
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		if errors.Is(opErr.Err, syscall.ECONNREFUSED) {
			return "connection_refused"
		}
	}

	// Check for DNS errors
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return "dns_error"
	}

	// Default to unknown error
	return "unknown"
}

var (
	// Tier 1 parameterization patterns (applied in order from most to least specific)
	uuidPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	shaPattern  = regexp.MustCompile(`(?i)^[0-9a-f]{40,64}$`)
	intPattern  = regexp.MustCompile(`^\d+$`)
)

// parameterizePath applies Tier 1 parameterization patterns to URL path segments.
// It replaces UUIDs with {id}, SHA hashes with {hash}, and integers with {id}.
// This reduces cardinality while preserving route structure.
func parameterizePath(path string) string {
	if path == "" || path == "/" {
		return path
	}

	segments := strings.Split(strings.Trim(path, "/"), "/")
	for i, segment := range segments {
		// Skip empty segments
		if segment == "" {
			continue
		}

		// Apply patterns in order: UUID -> SHA hash -> integer
		switch {
		case uuidPattern.MatchString(segment):
			segments[i] = "{id}"
		case shaPattern.MatchString(segment):
			segments[i] = "{hash}"
		case intPattern.MatchString(segment):
			segments[i] = "{id}"
		}
	}

	return "/" + strings.Join(segments, "/")
}

// extractMetricLabels extracts host and path_template from a URL for metric labels.
// Host includes non-standard ports (omits 80 for http and 443 for https).
// Path is parameterized using Tier 1 patterns. Query parameters are stripped.
func extractMetricLabels(u *url.URL) (host, pathTemplate string) {
	// Extract host with non-standard ports
	host = u.Hostname()
	port := u.Port()

	// Include port only if non-standard
	if port != "" {
		scheme := u.Scheme
		if (scheme == "http" && port != "80") || (scheme == "https" && port != "443") {
			host = host + ":" + port
		}
	}

	// Parameterize path (query parameters already stripped by u.Path)
	pathTemplate = parameterizePath(u.Path)

	return host, pathTemplate
}
