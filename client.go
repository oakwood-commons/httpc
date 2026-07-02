// Copyright 2025-2026 Oakwood Commons
// SPDX-License-Identifier: Apache-2.0

package httpc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/go-logr/logr"
	"github.com/hashicorp/go-retryablehttp"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"ivan.dev/httpcache"
)

// ErrCacheSizeLimitExceeded is returned when attempting to cache data exceeds size limit
var ErrCacheSizeLimitExceeded = errors.New("cache: size limit exceeded")

// ErrResponseBodyTooLarge is returned when the response body exceeds MaxResponseBodySize
var ErrResponseBodyTooLarge = errors.New("response body exceeds maximum allowed size")

// RequestHook is a function that processes a request before it's sent
type RequestHook func(*http.Request) error

// ResponseHook is a function that processes a response after it's received
type ResponseHook func(*http.Response) error

// CacheStats represents cache hit and miss statistics with computed hit rate
type CacheStats struct {
	Hits    uint64
	Misses  uint64
	HitRate float64 // Computed as Hits / (Hits + Misses)
}

// CacheType defines the type of cache to use
type CacheType string

const (
	// CacheTypeMemory uses in-memory caching
	CacheTypeMemory CacheType = "memory"
	// CacheTypeFilesystem uses filesystem-based caching
	CacheTypeFilesystem CacheType = "filesystem"
)

// ClientConfig holds the configuration for the HTTP client
type ClientConfig struct {
	// Timeout is the maximum time to wait for a request to complete
	Timeout time.Duration
	// RetryMax is the maximum number of retries
	RetryMax int
	// RetryWaitMin is the minimum time to wait between retries
	RetryWaitMin time.Duration
	// RetryWaitMax is the maximum time to wait between retries
	RetryWaitMax time.Duration
	// EnableCache enables HTTP caching
	EnableCache bool
	// CacheType specifies the type of cache to use (memory or filesystem)
	CacheType CacheType
	// CacheDir is the directory to use for filesystem cache (only used when CacheType is filesystem)
	CacheDir string
	// CacheTTL is the time-to-live for cached responses
	CacheTTL time.Duration
	// CacheKeyPrefix is a prefix added to all cache keys to prevent collisions
	CacheKeyPrefix string
	// MaxCacheFileSize is the maximum size in bytes for a single cached file (0 = no limit)
	// Only applies to filesystem cache
	MaxCacheFileSize int64
	// MemoryCacheSize is the maximum number of entries in the memory cache (default: 1000)
	MemoryCacheSize int
	// Logger is the logger to use for the client
	Logger logr.Logger
	// CheckRetry is a custom retry policy function
	CheckRetry retryablehttp.CheckRetry
	// Backoff is a custom backoff policy function
	Backoff retryablehttp.Backoff
	// ErrorHandler is called if retries are exhausted
	ErrorHandler retryablehttp.ErrorHandler
	// RequestHooks are functions called before each request
	RequestHooks []RequestHook
	// ResponseHooks are functions called after each response
	ResponseHooks []ResponseHook
	// OnUnauthorized is called when a 401 Unauthorized response is received.
	// Return the new full Authorization header value (e.g. "Bearer <new-token>") to
	// inject a single transparent retry with the refreshed token.
	// Return an empty string (or an error) to pass the 401 response through as-is.
	OnUnauthorized func(ctx context.Context) (authorizationHeader string, err error)
	// EnableCircuitBreaker enables circuit breaker pattern
	EnableCircuitBreaker bool
	// CircuitBreakerConfig holds circuit breaker configuration
	CircuitBreakerConfig *CircuitBreakerConfig
	// EnableCompression enables automatic gzip compression for requests/responses
	EnableCompression bool
	// Metrics is the metrics collector for the HTTP client.
	// Defaults to NoopMetrics{} if not set.
	Metrics Metrics
	// AllowPrivateIPs allows HTTP requests to private/loopback/link-local IP literals.
	// Defaults to false (deny), enforcing secure-by-default behaviour.
	AllowPrivateIPs bool
	// MaxRedirects is the maximum number of HTTP redirects to follow.
	// Defaults to DefaultMaxRedirects (10).
	MaxRedirects int
	// MaxResponseBodySize is the maximum HTTP response body size in bytes.
	// Defaults to DefaultMaxResponseBodySize (100 MB).
	MaxResponseBodySize int64
	// Transport is the base HTTP transport used for network I/O.
	// If nil, http.DefaultTransport is used.
	// The provided transport is wrapped with OTel tracing, metrics,
	// compression, and caching layers as configured.
	Transport http.RoundTripper
}

// DefaultConfig returns a ClientConfig with sensible defaults
func DefaultConfig() *ClientConfig {
	return &ClientConfig{
		Timeout:              DefaultTimeout,
		RetryMax:             DefaultRetryMax,
		RetryWaitMin:         DefaultRetryWait,
		RetryWaitMax:         DefaultRetryMaxWait,
		EnableCache:          true,
		CacheType:            CacheTypeFilesystem,
		CacheDir:             defaultCacheDir(),
		CacheTTL:             DefaultCacheTTL,
		CacheKeyPrefix:       DefaultCacheKeyPrefix,
		MaxCacheFileSize:     DefaultMaxCacheFileSize,
		MemoryCacheSize:      DefaultMemoryCacheSize,
		Logger:               logr.Discard(),
		EnableCircuitBreaker: false,
		CircuitBreakerConfig: DefaultCircuitBreakerConfig(),
		EnableCompression:    true,
		Metrics:              NoopMetrics{},
		MaxRedirects:         DefaultMaxRedirects,
		MaxResponseBodySize:  DefaultMaxResponseBodySize,
	}
}

// Client is an HTTP client with retry, timeout, and caching capabilities.
//
// Thread-Safety: Client is safe for concurrent use by multiple goroutines.
// All methods can be called concurrently. The underlying retryable HTTP client,
// cache implementations, and circuit breaker are all thread-safe.
// Multiple goroutines can share a single Client instance without additional synchronization.
type Client struct {
	retryClient    *retryablehttp.Client
	httpClient     *http.Client
	config         *ClientConfig
	cache          httpcache.Cache // Store reference to cache for clearing
	circuitBreaker *circuitBreaker
	metrics        Metrics
}

// NewClient creates a new HTTP client with the provided configuration
func NewClient(config *ClientConfig) *Client {
	if config == nil {
		config = DefaultConfig()
	}

	m := config.Metrics
	if m == nil {
		m = NoopMetrics{}
	}

	retryClient := newRetryClient(config, m)
	httpClient, cache := setupTransport(retryClient, config, m)

	// Initialize circuit breaker if enabled
	var cb *circuitBreaker
	if config.EnableCircuitBreaker {
		cb = newCircuitBreaker(config.CircuitBreakerConfig, m)
	}

	return &Client{
		retryClient:    retryClient,
		httpClient:     httpClient,
		config:         config,
		cache:          cache,
		circuitBreaker: cb,
		metrics:        m,
	}
}

// newRetryClient creates and configures the underlying retryable HTTP client.
func newRetryClient(config *ClientConfig, m Metrics) *retryablehttp.Client {
	retryClient := retryablehttp.NewClient()
	retryClient.RetryMax = config.RetryMax
	retryClient.RetryWaitMin = config.RetryWaitMin
	retryClient.RetryWaitMax = config.RetryWaitMax

	if config.CheckRetry != nil {
		retryClient.CheckRetry = wrapCheckRetryWithMetrics(config.CheckRetry, m)
	} else {
		retryClient.CheckRetry = wrapCheckRetryWithMetrics(retryablehttp.DefaultRetryPolicy, m)
	}

	if config.Backoff != nil {
		retryClient.Backoff = config.Backoff
	}

	if config.ErrorHandler != nil {
		retryClient.ErrorHandler = config.ErrorHandler
	}

	// Set up logging
	if config.Logger.GetSink() != nil {
		retryClient.Logger = &retryableLogger{logger: config.Logger}
	} else {
		// Silence retryablehttp's default stdlib logger, which prints [DEBUG]/[ERROR]
		// directly to stderr when no logr sink is configured.
		retryClient.Logger = nil
	}

	// Wrap the retryClient's inner transport with OTel tracing so every actual
	// HTTP attempt gets a span and W3C Trace Context headers are injected.
	{
		base := config.Transport
		if base == nil {
			base = retryClient.HTTPClient.Transport
		}
		if base == nil {
			base = http.DefaultTransport
		}
		retryClient.HTTPClient.Transport = otelhttp.NewTransport(base)
	}

	// Validate redirect targets against private IP ranges when AllowPrivateIPs is
	// false. net/http follows redirects automatically, so a public URL that 30x-
	// redirects to a private IP literal (e.g. 169.254.169.254) would bypass the
	// initial ValidateURLNotPrivate check without this hook.
	allowPrivate := config.AllowPrivateIPs
	maxRedirects := config.MaxRedirects
	if maxRedirects <= 0 {
		maxRedirects = DefaultMaxRedirects
	}
	retryClient.HTTPClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= maxRedirects {
			return fmt.Errorf("stopped after %d redirects", len(via))
		}
		if !allowPrivate {
			if err := ValidateURLNotPrivate(req.URL.String()); err != nil {
				return err
			}
		}
		return nil
	}

	return retryClient
}

// newCache creates the cache backend based on config.CacheType.
func newCache(config *ClientConfig, m Metrics) httpcache.Cache {
	switch config.CacheType {
	case CacheTypeFilesystem:
		cacheDir := config.CacheDir
		if cacheDir == "" {
			cacheDir = defaultCacheDir()
		}
		fileCacheConfig := &FileCacheConfig{
			Dir:       cacheDir,
			TTL:       config.CacheTTL,
			KeyPrefix: config.CacheKeyPrefix,
			MaxSize:   config.MaxCacheFileSize,
			Logger:    config.Logger,
			Metrics:   m,
		}
		fileCache, err := NewFileCache(fileCacheConfig)
		if err != nil {
			// Fall back to memory cache if filesystem cache fails
			if config.Logger.GetSink() != nil {
				config.Logger.Error(err, "Failed to create filesystem cache, falling back to memory cache")
			}
			return newMemoryCache(config, m)
		}
		return fileCache
	case CacheTypeMemory:
		return newMemoryCache(config, m)
	default:
		return newMemoryCache(config, m)
	}
}

// newMemoryCache creates a metrics-wrapped in-memory cache.
func newMemoryCache(config *ClientConfig, m Metrics) httpcache.Cache {
	cacheSize := config.MemoryCacheSize
	if cacheSize <= 0 {
		cacheSize = DefaultMemoryCacheSize
	}
	return newMetricsMemoryCache(httpcache.MemoryCache(cacheSize, config.CacheTTL), m)
}

// wrapTransport layers metrics and (optionally) compression on top of baseTransport.
func wrapTransport(baseTransport http.RoundTripper, config *ClientConfig, m Metrics) http.RoundTripper {
	if baseTransport == nil {
		baseTransport = http.DefaultTransport
	}
	var t http.RoundTripper = newMetricsTransport(baseTransport, m)
	if config.EnableCompression {
		t = newCompressionTransport(t, config.MaxResponseBodySize)
	}
	return t
}

// setupTransport configures the transport chain (metrics, compression, cache) on
// retryClient and returns the final *http.Client plus the cache backend (or nil).
func setupTransport(retryClient *retryablehttp.Client, config *ClientConfig, m Metrics) (*http.Client, httpcache.Cache) {
	if config.EnableCache {
		cache := newCache(config, m)

		finalTransport := wrapTransport(retryClient.HTTPClient.Transport, config, m)

		// Layer caching on top of the metrics/compression transport.
		cachedTransport := httpcache.NewCacheTransport(
			finalTransport,
			cache,
			httpcache.WithTTL(config.CacheTTL),
		)

		retryClient.HTTPClient.Transport = cachedTransport
		retryClient.HTTPClient.Timeout = config.Timeout

		return retryClient.StandardClient(), cache
	}

	// Non-cache path: set timeout and wrap transport with metrics/compression.
	retryClient.HTTPClient.Timeout = config.Timeout
	retryClient.HTTPClient.Transport = wrapTransport(retryClient.HTTPClient.Transport, config, m)

	stdClient := retryClient.StandardClient()
	stdClient.Timeout = config.Timeout

	return stdClient, nil
}

// Do executes an HTTP request with retry logic, hooks, and circuit breaker support
func (c *Client) Do(req *http.Request) (*http.Response, error) {
	// Validate request has a URL
	if req == nil || req.URL == nil {
		return nil, fmt.Errorf("request or request URL is nil")
	}

	// Enforce SSRF protection on the initial request URL (not just redirects).
	if !c.config.AllowPrivateIPs {
		if err := ValidateURLNotPrivate(req.URL.String()); err != nil {
			return nil, err
		}
	}

	// Check circuit breaker if enabled
	if c.circuitBreaker != nil {
		host := req.URL.Hostname()
		if err := c.circuitBreaker.allow(host); err != nil {
			return nil, err
		}
	}

	// Run request hooks
	for _, hook := range c.config.RequestHooks {
		if err := hook(req); err != nil {
			return nil, fmt.Errorf("request hook failed: %w", err)
		}
	}

	// Track concurrent requests
	c.metrics.IncrementConcurrentRequests(req.Context())
	defer c.metrics.DecrementConcurrentRequests(req.Context())

	// Convert to retryable request for retry logic
	retryReq, err := retryablehttp.FromRequest(req)
	if err != nil {
		return nil, fmt.Errorf("failed to create retryable request: %w", err)
	}

	resp, err := c.retryClient.Do(retryReq)

	// Enforce MaxResponseBodySize by wrapping the response body in a limited reader.
	if err == nil && resp != nil && resp.Body != nil && c.config.MaxResponseBodySize > 0 {
		resp.Body = &limitedReadCloser{
			rc:    resp.Body,
			limit: c.config.MaxResponseBodySize,
		}
	}

	// Record circuit breaker result
	if c.circuitBreaker != nil {
		host := req.URL.Hostname()
		if err != nil || (resp != nil && resp.StatusCode >= 500) {
			c.circuitBreaker.recordFailure(host)
		} else if resp != nil {
			c.circuitBreaker.recordSuccess(host)
		}
	}

	// Run response hooks if we got a response
	if resp != nil {
		for _, hook := range c.config.ResponseHooks {
			if hookErr := hook(resp); hookErr != nil {
				// Close response body before returning hook error
				if resp.Body != nil {
					resp.Body.Close()
				}
				return nil, fmt.Errorf("response hook failed: %w", hookErr)
			}
		}
	}

	// Handle 401 Unauthorized with optional token refresh (single retry).
	// This runs after the retryablehttp layer has already exhausted its own retries.
	if err == nil && resp != nil && resp.StatusCode == http.StatusUnauthorized && c.config.OnUnauthorized != nil {
		resp, err = c.handleAuthRetry(req, resp)
	}

	return resp, err
}

// handleAuthRetry performs a single retry with refreshed credentials when a 401 is received.
// The retried request goes through c.httpClient (wraps retryablehttp.StandardClient), which
// preserves the full transport chain (OTel, metrics, compression, cache). Note that
// retryablehttp retry semantics still apply to this attempt (e.g. a 5xx response will be
// retried up to RetryMax times).
func (c *Client) handleAuthRetry(req *http.Request, resp401 *http.Response) (*http.Response, error) {
	reqCtx := req.Context()
	newAuthHeader, hookErr := c.config.OnUnauthorized(reqCtx)
	if hookErr != nil {
		return resp401, fmt.Errorf("OnUnauthorized hook failed: %w", hookErr)
	}
	if newAuthHeader == "" {
		return resp401, nil
	}

	// Drain and discard the 401 response body before re-using the connection.
	_, _ = io.Copy(io.Discard, resp401.Body)
	resp401.Body.Close()

	// Clone the original request and inject the refreshed credential.
	retryReq := req.Clone(reqCtx)
	retryReq.Header.Set("Authorization", newAuthHeader)
	// Replay the request body if the caller provided a GetBody func.
	if req.GetBody != nil {
		body, getBodyErr := req.GetBody()
		if getBodyErr != nil {
			return nil, fmt.Errorf("failed to replay request body for auth retry: %w", getBodyErr)
		}
		retryReq.Body = body
	}

	resp, err := c.httpClient.Do(retryReq) //nolint:gosec // request cloned from caller-supplied req

	// Enforce MaxResponseBodySize on the retried response.
	if err == nil && resp != nil && resp.Body != nil && c.config.MaxResponseBodySize > 0 {
		resp.Body = &limitedReadCloser{
			rc:    resp.Body,
			limit: c.config.MaxResponseBodySize,
		}
	}

	// Run response hooks on the retried response.
	if err == nil && resp != nil {
		for _, hook := range c.config.ResponseHooks {
			if hookErr2 := hook(resp); hookErr2 != nil {
				if resp.Body != nil {
					resp.Body.Close()
				}
				return nil, fmt.Errorf("response hook failed after auth retry: %w", hookErr2)
			}
		}
	}

	return resp, err
}

// limitedReadCloser wraps a ReadCloser and returns ErrResponseBodyTooLarge
// when more than limit bytes are read.
type limitedReadCloser struct {
	rc    io.ReadCloser
	limit int64
	read  int64
}

func (l *limitedReadCloser) Read(p []byte) (int, error) {
	remaining := l.limit - l.read
	if remaining <= 0 {
		return 0, ErrResponseBodyTooLarge
	}
	if int64(len(p)) > remaining {
		p = p[:remaining]
	}
	n, err := l.rc.Read(p)
	l.read += int64(n)
	return n, err
}

func (l *limitedReadCloser) Close() error {
	return l.rc.Close()
}

// Get performs a GET request
func (c *Client) Get(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create GET request: %w", err)
	}

	return c.Do(req)
}

// Post performs a POST request
func (c *Client) Post(ctx context.Context, url, contentType string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return nil, fmt.Errorf("failed to create POST request: %w", err)
	}

	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	return c.Do(req)
}

// Put performs a PUT request
func (c *Client) Put(ctx context.Context, url, contentType string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, body)
	if err != nil {
		return nil, fmt.Errorf("failed to create PUT request: %w", err)
	}

	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	return c.Do(req)
}

// Delete performs a DELETE request
func (c *Client) Delete(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create DELETE request: %w", err)
	}

	return c.Do(req)
}

// StandardClient returns the underlying standard HTTP client (useful for external libraries)
func (c *Client) StandardClient() *http.Client {
	return c.httpClient
}

// RetryableClient returns the underlying retryable HTTP client
func (c *Client) RetryableClient() *retryablehttp.Client {
	return c.retryClient
}

// ClearCache clears all cached entries
// For filesystem cache, this also removes files from disk
func (c *Client) ClearCache() error {
	if c.cache == nil {
		return nil // No cache configured
	}

	// If it's a FileCache, use the Clear method
	if fc, ok := c.cache.(*FileCache); ok {
		return fc.Clear()
	}

	// For other cache types, we can't clear them directly
	// as they don't expose a Clear method
	return fmt.Errorf("cache clearing not supported for this cache type")
}

// CleanExpiredCache removes expired cache entries
// Only supported for filesystem cache
func (c *Client) CleanExpiredCache() error {
	if c.cache == nil {
		return nil // No cache configured
	}

	// Only FileCache supports cleaning expired entries
	if fc, ok := c.cache.(*FileCache); ok {
		return fc.CleanExpired()
	}

	return fmt.Errorf("cache cleanup not supported for this cache type")
}

// DeleteCacheEntry removes a specific entry from the cache by URL
func (c *Client) DeleteCacheEntry(ctx context.Context, url string) error {
	if c.cache == nil {
		return nil // No cache configured
	}

	// Use the URL as the cache key
	return c.cache.Del(ctx, url)
}

// WarmCache pre-populates the cache with the specified URLs
// This is useful for frequently accessed resources
func (c *Client) WarmCache(ctx context.Context, urls []string) error {
	if c.cache == nil {
		return fmt.Errorf("cache not enabled")
	}

	var errs []error
	for _, url := range urls {
		resp, err := c.Get(ctx, url)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to warm cache for %s: %w", url, err))
			continue
		}
		// Close the response body - the data is already cached
		if resp.Body != nil {
			_, _ = io.Copy(io.Discard, resp.Body) // Ensure body is fully read; ignore copy errors
			resp.Body.Close()
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("encountered %d errors while warming cache: %v", len(errs), errs)
	}

	return nil
}

// CacheStats returns cache hit and miss statistics with computed hit rate
// Returns nil if cache stats are not available
func (c *Client) CacheStats() *CacheStats {
	if c.cache == nil {
		return nil
	}

	var hits, misses uint64
	var ok bool

	// Check if cache supports stats
	if fc, isFileCache := c.cache.(*FileCache); isFileCache {
		hits, misses = fc.Stats()
		ok = true
	} else if mc, isMemCache := c.cache.(*metricsMemoryCache); isMemCache {
		hits, misses = mc.Stats()
		ok = true
	}

	if !ok {
		return nil
	}

	// Calculate hit rate
	total := hits + misses
	hitRate := 0.0
	if total > 0 {
		hitRate = float64(hits) / float64(total)
	}

	return &CacheStats{
		Hits:    hits,
		Misses:  misses,
		HitRate: hitRate,
	}
}

// Close gracefully shuts down the client and cleans up resources
// For filesystem cache, this performs a cleanup of expired entries
func (c *Client) Close() error {
	if c.cache == nil {
		return nil
	}

	// If it's a FileCache, close it
	if fc, ok := c.cache.(*FileCache); ok {
		return fc.Close()
	}

	return nil
}

// wrapCheckRetryWithMetrics wraps a CheckRetry function to track retry metrics
func wrapCheckRetryWithMetrics(original retryablehttp.CheckRetry, m Metrics) retryablehttp.CheckRetry {
	return func(ctx context.Context, resp *http.Response, err error) (bool, error) {
		shouldRetry := false
		var checkErr error

		if original != nil {
			shouldRetry, checkErr = original(ctx, resp, err)
		} else {
			shouldRetry, checkErr = retryablehttp.DefaultRetryPolicy(ctx, resp, err)
		}

		if shouldRetry {
			method := "unknown"
			host := "unknown"
			pathTemplate := "/"
			// Extract request info from response if available
			if resp != nil && resp.Request != nil && resp.Request.URL != nil {
				method = resp.Request.Method
				host, pathTemplate = extractMetricLabels(resp.Request.URL)
			}
			m.IncrementRetries(ctx, method, host, pathTemplate)
		}

		return shouldRetry, checkErr
	}
}

// retryableLogger adapts logr.Logger to retryablehttp.LeveledLogger interface
type retryableLogger struct {
	logger logr.Logger
}

func (l *retryableLogger) Error(msg string, keysAndValues ...interface{}) {
	l.logger.Error(nil, msg, keysAndValues...)
}

func (l *retryableLogger) Info(msg string, keysAndValues ...interface{}) {
	l.logger.Info(msg, keysAndValues...)
}

func (l *retryableLogger) Debug(msg string, keysAndValues ...interface{}) {
	l.logger.V(1).Info(msg, keysAndValues...)
}

func (l *retryableLogger) Warn(msg string, keysAndValues ...interface{}) {
	l.logger.V(0).Info(msg, keysAndValues...)
}
