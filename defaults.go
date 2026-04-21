// Copyright 2025-2026 Oakwood Commons
// SPDX-License-Identifier: Apache-2.0

package httpc

import (
	"os"
	"path/filepath"
	"time"
)

// Default configuration values for the HTTP client.
const (
	// DefaultTimeout is the default HTTP request timeout.
	DefaultTimeout = 30 * time.Second

	// DefaultRetryMax is the default maximum number of retries.
	DefaultRetryMax = 3

	// DefaultRetryWait is the default minimum wait time between retries.
	DefaultRetryWait = 1 * time.Second

	// DefaultRetryMaxWait is the default maximum wait time between retries.
	DefaultRetryMaxWait = 30 * time.Second

	// DefaultCacheTTL is the default time-to-live for cached responses.
	DefaultCacheTTL = 10 * time.Minute

	// DefaultCacheKeyPrefix is the default prefix for cache keys.
	DefaultCacheKeyPrefix = "httpc:"

	// DefaultMaxResponseBodySize is the default maximum HTTP response body size in bytes (100 MB).
	DefaultMaxResponseBodySize int64 = 100 * 1024 * 1024

	// DefaultMaxCacheFileSize is the default maximum size for a single cached file in bytes (10 MB).
	DefaultMaxCacheFileSize int64 = 10 * 1024 * 1024

	// DefaultMemoryCacheSize is the default maximum number of entries in the memory cache.
	DefaultMemoryCacheSize = 1000

	// DefaultCircuitBreakerMaxFailures is the default number of failures before opening the circuit.
	DefaultCircuitBreakerMaxFailures = 5

	// DefaultCircuitBreakerOpenTimeout is the default wait time before transitioning from Open to HalfOpen.
	DefaultCircuitBreakerOpenTimeout = 30 * time.Second

	// DefaultCircuitBreakerHalfOpenRequests is the default number of successful requests in HalfOpen before closing.
	DefaultCircuitBreakerHalfOpenRequests = 1

	// DefaultMaxRedirects is the default maximum number of HTTP redirects to follow.
	DefaultMaxRedirects = 10

	// httpCacheDirName is the subdirectory name used for HTTP cache storage.
	httpCacheDirName = "http-cache"
)

// defaultCacheDir returns the default directory for HTTP cache storage.
// It uses os.UserCacheDir() with a fallback to a temporary directory.
func defaultCacheDir() string {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		cacheDir = os.TempDir()
	}
	return filepath.Join(cacheDir, "httpc", httpCacheDirName)
}

// expandHome expands a leading ~ in a path to the user's home directory.
func expandHome(path string) (string, error) {
	if len(path) == 0 || path[0] != '~' {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	// "~" alone -> home; "~/foo" -> home + "foo" (strip the separator)
	rest := path[1:]
	if len(rest) > 0 && (rest[0] == '/' || rest[0] == filepath.Separator) {
		rest = rest[1:]
	}
	return filepath.Join(home, rest), nil
}
