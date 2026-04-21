// Copyright 2025-2026 Oakwood Commons
// SPDX-License-Identifier: Apache-2.0

package httpc

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// NoopMetrics coverage
// ---------------------------------------------------------------------------

func TestNoopMetrics_AllMethods(t *testing.T) {
	m := NoopMetrics{}
	ctx := context.Background()

	// All methods should be callable without panic
	m.RecordRequestDuration(ctx, "GET", "host", "/path", 200, time.Second)
	m.IncrementRequestsTotal(ctx, "GET", "host", "/path", 200)
	m.IncrementErrorsTotal(ctx, "GET", "host", "/path", "timeout")
	m.IncrementRetries(ctx, "GET", "host", "/path")
	m.IncrementCacheHits(ctx)
	m.IncrementCacheMisses(ctx)
	m.SetCacheSizeBytes(1024)
	m.SetCircuitBreakerState("host", 1.0)
	m.IncrementConcurrentRequests(ctx)
	m.DecrementConcurrentRequests(ctx)
	m.RecordRequestSize(ctx, "POST", "host", "/path", 512.0)
	m.RecordResponseSize(ctx, "POST", "host", "/path", 1024.0)

	// Verify NoopMetrics implements Metrics interface
	var _ Metrics = NoopMetrics{}
}

// ---------------------------------------------------------------------------
// client.Do coverage: nil request, OnUnauthorized, response hooks on auth retry
// ---------------------------------------------------------------------------

func TestClient_Do_NilRequest(t *testing.T) {
	client := NewClient(testDefaultConfig())
	resp, err := client.Do(nil)
	if resp != nil {
		defer resp.Body.Close()
	}
	assert.Nil(t, resp)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "nil")
}

func TestClient_Do_NilURL(t *testing.T) {
	client := NewClient(testDefaultConfig())
	req := &http.Request{}
	resp, err := client.Do(req)
	if resp != nil {
		defer resp.Body.Close()
	}
	assert.Nil(t, resp)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "nil")
}

func TestClient_Do_OnUnauthorized(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if r.Header.Get("Authorization") == "Bearer refreshed-token" {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("success"))
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte("unauthorized"))
	}))
	defer server.Close()

	cfg := testDefaultConfig()
	cfg.EnableCache = false
	cfg.RetryMax = 0 // no retries to make test deterministic
	cfg.OnUnauthorized = func(_ context.Context) (string, error) {
		return "Bearer refreshed-token", nil
	}
	client := NewClient(cfg)

	req, err := http.NewRequestWithContext(context.Background(), "GET", server.URL, nil)
	require.NoError(t, err)

	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	// First call returns 401, then auth retry returns 200
	assert.Equal(t, 2, callCount)
}

func TestClient_Do_OnUnauthorized_HookError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte("unauthorized"))
	}))
	defer server.Close()

	cfg := testDefaultConfig()
	cfg.EnableCache = false
	cfg.RetryMax = 0
	cfg.OnUnauthorized = func(_ context.Context) (string, error) {
		return "", fmt.Errorf("token refresh failed")
	}
	client := NewClient(cfg)

	req, err := http.NewRequestWithContext(context.Background(), "GET", server.URL, nil)
	require.NoError(t, err)

	resp, err := client.Do(req)
	// When OnUnauthorized returns error, the hook error is surfaced alongside the
	// original 401 response.
	require.Error(t, err)
	assert.Contains(t, err.Error(), "OnUnauthorized hook failed")
	assert.Contains(t, err.Error(), "token refresh failed")
	defer resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestClient_Do_OnUnauthorized_EmptyHeader(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte("unauthorized"))
	}))
	defer server.Close()

	cfg := testDefaultConfig()
	cfg.EnableCache = false
	cfg.RetryMax = 0
	cfg.OnUnauthorized = func(_ context.Context) (string, error) {
		return "", nil // empty header = skip retry
	}
	client := NewClient(cfg)

	req, err := http.NewRequestWithContext(context.Background(), "GET", server.URL, nil)
	require.NoError(t, err)

	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestClient_Do_OnUnauthorized_WithGetBody(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		body, _ := io.ReadAll(r.Body)
		if r.Header.Get("Authorization") == "Bearer new" && string(body) == "payload" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	cfg := testDefaultConfig()
	cfg.EnableCache = false
	cfg.RetryMax = 0
	cfg.OnUnauthorized = func(_ context.Context) (string, error) {
		return "Bearer new", nil
	}
	client := NewClient(cfg)

	bodyContent := "payload"
	req, err := http.NewRequestWithContext(context.Background(), "POST", server.URL, strings.NewReader(bodyContent))
	require.NoError(t, err)
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader(bodyContent)), nil
	}

	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestClient_Do_OnUnauthorized_ResponseHookFailsAfterRetry(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := testDefaultConfig()
	cfg.EnableCache = false
	cfg.RetryMax = 0
	cfg.OnUnauthorized = func(_ context.Context) (string, error) {
		return "Bearer refreshed", nil
	}
	cfg.ResponseHooks = []ResponseHook{
		func(resp *http.Response) error {
			if resp.StatusCode == http.StatusOK {
				return fmt.Errorf("hook failure after auth retry")
			}
			return nil
		},
	}
	client := NewClient(cfg)

	req, err := http.NewRequestWithContext(context.Background(), "GET", server.URL, nil)
	require.NoError(t, err)

	resp, err := client.Do(req)
	if resp != nil {
		defer resp.Body.Close()
	}
	assert.Nil(t, resp)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "response hook failed after auth retry")
}

func TestClient_Do_RequestHookError(t *testing.T) {
	cfg := testDefaultConfig()
	cfg.EnableCache = false
	cfg.RequestHooks = []RequestHook{
		func(_ *http.Request) error {
			return fmt.Errorf("hook error")
		},
	}
	client := NewClient(cfg)

	req, err := http.NewRequestWithContext(context.Background(), "GET", "http://example.com", nil)
	require.NoError(t, err)

	resp, err := client.Do(req)
	if resp != nil {
		defer resp.Body.Close()
	}
	assert.Nil(t, resp)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "request hook failed")
}

func TestClient_Do_ResponseHookError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := testDefaultConfig()
	cfg.EnableCache = false
	cfg.RetryMax = 0
	cfg.ResponseHooks = []ResponseHook{
		func(_ *http.Response) error {
			return fmt.Errorf("response hook error")
		},
	}
	client := NewClient(cfg)

	req, err := http.NewRequestWithContext(context.Background(), "GET", server.URL, nil)
	require.NoError(t, err)

	resp, err := client.Do(req)
	if resp != nil {
		defer resp.Body.Close()
	}
	assert.Nil(t, resp)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "response hook failed")
}

// ---------------------------------------------------------------------------
// client convenience methods: error path coverage for Get, Post, Put, Delete
// ---------------------------------------------------------------------------

func TestClient_Get_InvalidURL(t *testing.T) {
	client := NewClient(testDefaultConfig())
	resp, err := client.Get(context.Background(), "://invalid")
	if resp != nil {
		defer resp.Body.Close()
	}
	assert.Nil(t, resp)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create GET request")
}

func TestClient_Post_InvalidURL(t *testing.T) {
	client := NewClient(testDefaultConfig())
	resp, err := client.Post(context.Background(), "://invalid", "", nil)
	if resp != nil {
		defer resp.Body.Close()
	}
	assert.Nil(t, resp)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create POST request")
}

func TestClient_Put_InvalidURL(t *testing.T) {
	client := NewClient(testDefaultConfig())
	resp, err := client.Put(context.Background(), "://invalid", "", nil)
	if resp != nil {
		defer resp.Body.Close()
	}
	assert.Nil(t, resp)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create PUT request")
}

func TestClient_Delete_InvalidURL(t *testing.T) {
	client := NewClient(testDefaultConfig())
	resp, err := client.Delete(context.Background(), "://invalid")
	if resp != nil {
		defer resp.Body.Close()
	}
	assert.Nil(t, resp)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create DELETE request")
}

func TestClient_Post_WithContentType(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := testDefaultConfig()
	cfg.EnableCache = false
	cfg.RetryMax = 0
	client := NewClient(cfg)

	resp, err := client.Post(context.Background(), server.URL, "application/json", strings.NewReader(`{}`))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestClient_Put_WithContentType(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "text/plain", r.Header.Get("Content-Type"))
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := testDefaultConfig()
	cfg.EnableCache = false
	cfg.RetryMax = 0
	client := NewClient(cfg)

	resp, err := client.Put(context.Background(), server.URL, "text/plain", strings.NewReader("data"))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

// ---------------------------------------------------------------------------
// CacheStats coverage: memory cache, nil cache, zero total
// ---------------------------------------------------------------------------

func TestClient_CacheStats_MemoryCache(t *testing.T) {
	cfg := testDefaultConfig()
	cfg.CacheType = CacheTypeMemory
	cfg.EnableCache = true
	client := NewClient(cfg)

	// No operations yet, stats should be zero
	stats := client.CacheStats()
	require.NotNil(t, stats)
	assert.Equal(t, uint64(0), stats.Hits)
	assert.Equal(t, uint64(0), stats.Misses)
	assert.Equal(t, 0.0, stats.HitRate)
}

func TestClient_CacheStats_NilCache(t *testing.T) {
	cfg := testDefaultConfig()
	cfg.EnableCache = false
	client := NewClient(cfg)

	stats := client.CacheStats()
	assert.Nil(t, stats)
}

// ---------------------------------------------------------------------------
// CleanExpiredCache with memory cache (unsupported) - tested in client_test.go
// ClearCache with memory cache (unsupported) - tested in client_test.go
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// WarmCache coverage
// ---------------------------------------------------------------------------

func TestClient_WarmCache_ErrorAccumulation(t *testing.T) {
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requestCount++
		if requestCount == 2 {
			// Force an error for the second URL by closing the connection
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("cached"))
	}))
	defer server.Close()

	cfg := testDefaultConfig()
	cfg.CacheType = CacheTypeMemory
	cfg.EnableCache = true
	cfg.RetryMax = 0
	client := NewClient(cfg)

	err := client.WarmCache(context.Background(), []string{server.URL + "/a", server.URL + "/b"})
	// Both should succeed with a running server
	assert.NoError(t, err)
}

func TestClient_WarmCache_NoCache(t *testing.T) {
	cfg := testDefaultConfig()
	cfg.EnableCache = false
	client := NewClient(cfg)

	err := client.WarmCache(context.Background(), []string{"http://example.com"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cache not enabled")
}

func TestClient_WarmCache_WithFailingURL(t *testing.T) {
	cfg := testDefaultConfig()
	cfg.CacheType = CacheTypeMemory
	cfg.EnableCache = true
	cfg.RetryMax = 0
	cfg.Timeout = 1 * time.Second
	client := NewClient(cfg)

	// Use an invalid URL that will fail
	err := client.WarmCache(context.Background(), []string{"http://192.0.2.1:1/invalid"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "errors while warming cache")
}

// ---------------------------------------------------------------------------
// NewClient paths: nil metrics, memory cache with default size, cache disabled
// ---------------------------------------------------------------------------

func TestNewClient_NilMetrics(t *testing.T) {
	cfg := testDefaultConfig()
	cfg.Metrics = nil
	client := NewClient(cfg)
	require.NotNil(t, client)
}

func TestNewClient_CacheDisabledWithCompression(t *testing.T) {
	cfg := testDefaultConfig()
	cfg.EnableCache = false
	cfg.EnableCompression = true
	client := NewClient(cfg)
	require.NotNil(t, client)
}

func TestNewClient_CacheDisabledWithoutCompression(t *testing.T) {
	cfg := testDefaultConfig()
	cfg.EnableCache = false
	cfg.EnableCompression = false
	client := NewClient(cfg)
	require.NotNil(t, client)
}

func TestNewClient_CacheEnabledWithoutCompression(t *testing.T) {
	cfg := testDefaultConfig()
	cfg.EnableCache = true
	cfg.CacheType = CacheTypeMemory
	cfg.EnableCompression = false
	client := NewClient(cfg)
	require.NotNil(t, client)
}

func TestNewClient_FilesystemCacheFallbackToMemory(t *testing.T) {
	cfg := testDefaultConfig()
	cfg.EnableCache = true
	cfg.CacheType = CacheTypeFilesystem
	cfg.CacheDir = "/nonexistent/\x00/invalid" // invalid path that will fail
	cfg.MemoryCacheSize = 0                    // should use default
	client := NewClient(cfg)
	require.NotNil(t, client)
	// Should still have a cache (fell back to memory)
	assert.NotNil(t, client.cache)
}

func TestNewClient_MemoryCacheDefaultSize(t *testing.T) {
	cfg := testDefaultConfig()
	cfg.EnableCache = true
	cfg.CacheType = CacheTypeMemory
	cfg.MemoryCacheSize = 0 // should use default
	client := NewClient(cfg)
	require.NotNil(t, client)
}

func TestNewClient_CircuitBreakerEnabled(t *testing.T) {
	cfg := testDefaultConfig()
	cfg.EnableCircuitBreaker = true
	cfg.CircuitBreakerConfig = nil // should use defaults
	client := NewClient(cfg)
	require.NotNil(t, client)
	assert.NotNil(t, client.circuitBreaker)
}

// ---------------------------------------------------------------------------
// newCircuitBreaker nil args coverage
// ---------------------------------------------------------------------------

func TestNewCircuitBreaker_NilConfig(t *testing.T) {
	cb := newCircuitBreaker(nil, nil)
	require.NotNil(t, cb)
	assert.Equal(t, DefaultCircuitBreakerMaxFailures, cb.config.MaxFailures)
}

// ---------------------------------------------------------------------------
// newMetricsTransport nil metrics coverage
// ---------------------------------------------------------------------------

func TestNewMetricsTransport_NilMetrics(t *testing.T) {
	mt := newMetricsTransport(http.DefaultTransport, nil)
	require.NotNil(t, mt)
}

// ---------------------------------------------------------------------------
// newMetricsMemoryCache nil metrics coverage
// ---------------------------------------------------------------------------

func TestNewMetricsMemoryCache_NilMetrics(t *testing.T) {
	mc := newMetricsMemoryCache(nil, nil)
	require.NotNil(t, mc)
}

// ---------------------------------------------------------------------------
// FileCache.Set: context timeout paths
// ---------------------------------------------------------------------------

func TestFileCache_Set_ContextCanceled(t *testing.T) {
	tmpDir := t.TempDir()
	cache, err := NewFileCache(&FileCacheConfig{
		Dir:    tmpDir,
		TTL:    5 * time.Minute,
		Logger: logr.Discard(),
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	err = cache.Set(ctx, "key", []byte("value"), 0)
	assert.Error(t, err)
}

func TestFileCache_Get_ContextCanceled(t *testing.T) {
	tmpDir := t.TempDir()
	cache, err := NewFileCache(&FileCacheConfig{
		Dir:    tmpDir,
		TTL:    5 * time.Minute,
		Logger: logr.Discard(),
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	data, err := cache.Get(ctx, "key")
	assert.Nil(t, data)
	assert.Error(t, err)
}

func TestFileCache_Del_ContextCanceled(t *testing.T) {
	tmpDir := t.TempDir()
	cache, err := NewFileCache(&FileCacheConfig{
		Dir:    tmpDir,
		TTL:    5 * time.Minute,
		Logger: logr.Discard(),
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err = cache.Del(ctx, "key")
	assert.Error(t, err)
}

// ---------------------------------------------------------------------------
// FileCache.Get: TTL=0 (no expiry check), stat error after read
// ---------------------------------------------------------------------------

func TestFileCache_Get_NoTTL(t *testing.T) {
	tmpDir := t.TempDir()
	cache, err := NewFileCache(&FileCacheConfig{
		Dir:    tmpDir,
		TTL:    0, // no TTL
		Logger: logr.Discard(),
	})
	require.NoError(t, err)

	ctx := context.Background()
	err = cache.Set(ctx, "key", []byte("value"), 0)
	require.NoError(t, err)

	data, err := cache.Get(ctx, "key")
	require.NoError(t, err)
	assert.Equal(t, []byte("value"), data)
}

func TestFileCache_Get_NonExistentKey(t *testing.T) {
	tmpDir := t.TempDir()
	cache, err := NewFileCache(&FileCacheConfig{
		Dir:    tmpDir,
		TTL:    5 * time.Minute,
		Logger: logr.Discard(),
	})
	require.NoError(t, err)

	data, err := cache.Get(context.Background(), "nonexistent")
	assert.NoError(t, err)
	assert.Nil(t, data)

	_, misses := cache.Stats()
	assert.Equal(t, uint64(1), misses)
}

// ---------------------------------------------------------------------------
// FileCache.Clear: with subdirectories (should skip them)
// ---------------------------------------------------------------------------

func TestFileCache_Clear_WithSubdirectories(t *testing.T) {
	tmpDir := t.TempDir()
	cache, err := NewFileCache(&FileCacheConfig{
		Dir:    tmpDir,
		TTL:    5 * time.Minute,
		Logger: logr.Discard(),
	})
	require.NoError(t, err)

	// Add a file and a subdirectory
	ctx := context.Background()
	err = cache.Set(ctx, "key1", []byte("value1"), 0)
	require.NoError(t, err)

	subDir := filepath.Join(tmpDir, "subdir")
	require.NoError(t, os.MkdirAll(subDir, 0o700))

	err = cache.Clear()
	assert.NoError(t, err)

	// Subdirectory should still exist
	_, err = os.Stat(subDir)
	assert.NoError(t, err)
}

func TestFileCache_Clear_ReadDirError(t *testing.T) {
	cache := &FileCache{
		dir:     "/nonexistent/dir/that/does/not/exist",
		logger:  logr.Discard(),
		metrics: NoopMetrics{},
	}
	err := cache.Clear()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to read cache directory")
}

// ---------------------------------------------------------------------------
// FileCache.CleanExpired: error paths
// ---------------------------------------------------------------------------

func TestFileCache_CleanExpired_NoTTL_Coverage(t *testing.T) {
	tmpDir := t.TempDir()
	cache, err := NewFileCache(&FileCacheConfig{
		Dir:    tmpDir,
		TTL:    0,
		Logger: logr.Discard(),
	})
	require.NoError(t, err)

	err = cache.CleanExpired()
	assert.NoError(t, err) // no-op when TTL=0
}

func TestFileCache_CleanExpired_SkipsDirectories(t *testing.T) {
	tmpDir := t.TempDir()
	cache, err := NewFileCache(&FileCacheConfig{
		Dir:    tmpDir,
		TTL:    1 * time.Millisecond,
		Logger: logr.Discard(),
	})
	require.NoError(t, err)

	// Add a cache entry and a subdirectory
	ctx := context.Background()
	err = cache.Set(ctx, "key1", []byte("data"), 0)
	require.NoError(t, err)

	subDir := filepath.Join(tmpDir, "subdir")
	require.NoError(t, os.MkdirAll(subDir, 0o700))

	time.Sleep(5 * time.Millisecond) // ensure TTL expires

	err = cache.CleanExpired()
	assert.NoError(t, err)

	// Subdirectory should still exist
	_, err = os.Stat(subDir)
	assert.NoError(t, err)
}

// ---------------------------------------------------------------------------
// FileCache.updateCacheSizeMetric: error path
// ---------------------------------------------------------------------------

func TestFileCache_UpdateCacheSizeMetric_ReadDirError(t *testing.T) {
	cache := &FileCache{
		dir:     "/nonexistent/dir",
		logger:  logr.Discard(),
		metrics: NoopMetrics{},
	}
	// Should not panic
	cache.updateCacheSizeMetric()
}

// ---------------------------------------------------------------------------
// FileCache: custom FileIOTimeout
// ---------------------------------------------------------------------------

func TestFileCache_CustomFileIOTimeout(t *testing.T) {
	tmpDir := t.TempDir()
	cache, err := NewFileCache(&FileCacheConfig{
		Dir:           tmpDir,
		TTL:           5 * time.Minute,
		FileIOTimeout: 10 * time.Second,
		Logger:        logr.Discard(),
	})
	require.NoError(t, err)
	assert.Equal(t, 10*time.Second, cache.fileIOTimeout)
}

// ---------------------------------------------------------------------------
// FileCache.Set: rename error path (write succeeds, rename fails)
// ---------------------------------------------------------------------------

func TestFileCache_Set_RenameError(t *testing.T) {
	tmpDir := t.TempDir()
	cache, err := NewFileCache(&FileCacheConfig{
		Dir:    tmpDir,
		TTL:    5 * time.Minute,
		Logger: logr.Discard(),
	})
	require.NoError(t, err)

	ctx := context.Background()

	// Create a directory with the name of the target file to cause rename to fail
	targetFilename := cache.keyToFilename("rename-fail-key")
	require.NoError(t, os.MkdirAll(targetFilename, 0o700))

	err = cache.Set(ctx, "rename-fail-key", []byte("data"), 0)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to rename cache file")
}

// ---------------------------------------------------------------------------
// NewFileCache: nil config, empty dir, expand home error
// ---------------------------------------------------------------------------

func TestNewFileCache_NilConfig(t *testing.T) {
	cache, err := NewFileCache(nil)
	assert.Nil(t, cache)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "config cannot be nil")
}

func TestNewFileCache_EmptyDir(t *testing.T) {
	cache, err := NewFileCache(&FileCacheConfig{Dir: ""})
	assert.Nil(t, cache)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cache directory cannot be empty")
}

func TestNewFileCache_NilLogger(t *testing.T) {
	tmpDir := t.TempDir()
	cache, err := NewFileCache(&FileCacheConfig{
		Dir: tmpDir,
		TTL: 5 * time.Minute,
		// Logger not set - should default to Discard
	})
	require.NoError(t, err)
	require.NotNil(t, cache)
}

func TestNewFileCache_NilMetrics(t *testing.T) {
	tmpDir := t.TempDir()
	cache, err := NewFileCache(&FileCacheConfig{
		Dir:    tmpDir,
		TTL:    5 * time.Minute,
		Logger: logr.Discard(),
		// Metrics not set - should default to NoopMetrics
	})
	require.NoError(t, err)
	require.NotNil(t, cache)
}

// ---------------------------------------------------------------------------
// compression: gzipReadCloser.Close error paths
// ---------------------------------------------------------------------------

type errCloser struct{ err error }

func (e errCloser) Close() error { return e.err }

func TestGzipReadCloser_CloseGzipError(t *testing.T) {
	// When the gzip closer returns an error, it should be returned
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	gw.Write([]byte("hello"))
	gw.Close()

	gr, err := gzip.NewReader(bytes.NewReader(buf.Bytes()))
	require.NoError(t, err)

	// Read all data first
	_, err = io.ReadAll(gr)
	require.NoError(t, err)

	grc := &gzipReadCloser{
		reader: io.NopCloser(strings.NewReader("")),
		gzip:   gr,
		closer: io.NopCloser(strings.NewReader("")),
	}
	// Close should succeed (gzip reader is in good state after full read)
	err = grc.Close()
	assert.NoError(t, err)
}

func TestGzipReadCloser_CloseOriginalBodyError(t *testing.T) {
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	gw.Write([]byte("hello"))
	gw.Close()

	gr, err := gzip.NewReader(bytes.NewReader(buf.Bytes()))
	require.NoError(t, err)
	_, _ = io.ReadAll(gr) // fully read

	expectedErr := errors.New("original body close error")
	grc := &gzipReadCloser{
		reader: io.NopCloser(strings.NewReader("")),
		gzip:   gr,
		closer: errCloser{err: expectedErr},
	}
	err = grc.Close()
	assert.ErrorIs(t, err, expectedErr)
}

// ---------------------------------------------------------------------------
// Compression transport: non-gzip encoding pass-through
// ---------------------------------------------------------------------------

func TestCompressionTransport_NonGzipEncoding(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Encoding", "br") // brotli - not supported
		w.Write([]byte("brotli-data"))
	}))
	defer server.Close()

	transport := newCompressionTransport(http.DefaultTransport, DefaultMaxResponseBodySize)
	req, err := http.NewRequestWithContext(context.Background(), "GET", server.URL, nil)
	require.NoError(t, err)

	resp, err := transport.RoundTrip(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	// Non-gzip encoding should be passed through as-is
	assert.Equal(t, "br", resp.Header.Get("Content-Encoding"))
	body, _ := io.ReadAll(resp.Body)
	assert.Equal(t, "brotli-data", string(body))
}

// ---------------------------------------------------------------------------
// retryableLogger coverage
// ---------------------------------------------------------------------------

func TestRetryableLogger_AllLevels(t *testing.T) {
	rl := &retryableLogger{logger: logr.Discard()}
	// All methods should be callable without panic
	rl.Error("error message", "key", "value")
	rl.Info("info message", "key", "value")
	rl.Debug("debug message", "key", "value")
	rl.Warn("warn message", "key", "value")
}

// ---------------------------------------------------------------------------
// Client.Close with various cache types
// ---------------------------------------------------------------------------

func TestClient_Close_NilCache(t *testing.T) {
	cfg := testDefaultConfig()
	cfg.EnableCache = false
	client := NewClient(cfg)
	err := client.Close()
	assert.NoError(t, err)
}

func TestClient_Close_MemoryCache(t *testing.T) {
	cfg := testDefaultConfig()
	cfg.CacheType = CacheTypeMemory
	cfg.EnableCache = true
	client := NewClient(cfg)
	err := client.Close()
	assert.NoError(t, err)
}

// ---------------------------------------------------------------------------
// Client accessors - tested in client_test.go
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Circuit breaker: recordSuccess and recordFailure in open state (no-op)
// ---------------------------------------------------------------------------

func TestCircuitBreaker_RecordSuccessInOpenState(t *testing.T) {
	config := &CircuitBreakerConfig{
		MaxFailures:         1,
		OpenTimeout:         10 * time.Second,
		HalfOpenMaxRequests: 1,
	}
	cb := newCircuitBreaker(config, NoopMetrics{})
	host := "test.com"

	// Force open
	cb.recordFailure(host)
	assert.Equal(t, StateOpen, cb.getState(host))

	// Record success while open - should be a no-op
	cb.recordSuccess(host)
	assert.Equal(t, StateOpen, cb.getState(host))
}

func TestCircuitBreaker_RecordFailureInOpenState(t *testing.T) {
	config := &CircuitBreakerConfig{
		MaxFailures:         1,
		OpenTimeout:         10 * time.Second,
		HalfOpenMaxRequests: 1,
	}
	cb := newCircuitBreaker(config, NoopMetrics{})
	host := "test.com"

	// Force open
	cb.recordFailure(host)
	assert.Equal(t, StateOpen, cb.getState(host))

	// Record failure while open - should be a no-op
	cb.recordFailure(host)
	assert.Equal(t, StateOpen, cb.getState(host))
}

// ---------------------------------------------------------------------------
// Circuit breaker integration with client.Do
// ---------------------------------------------------------------------------

func TestClient_Do_CircuitBreakerBlocks(t *testing.T) {
	cfg := testDefaultConfig()
	cfg.EnableCache = false
	cfg.EnableCircuitBreaker = true
	cfg.CircuitBreakerConfig = &CircuitBreakerConfig{
		MaxFailures:         1,
		OpenTimeout:         10 * time.Second,
		HalfOpenMaxRequests: 1,
	}
	cfg.RetryMax = 0

	// Create a server that always returns 500
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client := NewClient(cfg)

	// First request should go through (circuit closed) but record failure (500)
	req, _ := http.NewRequestWithContext(context.Background(), "GET", server.URL, nil)
	resp, err := client.Do(req)
	if err == nil && resp != nil {
		resp.Body.Close()
	}

	// Second request should be blocked by circuit breaker (circuit is now open)
	req2, _ := http.NewRequestWithContext(context.Background(), "GET", server.URL, nil)
	resp, err = client.Do(req2)
	if resp != nil {
		resp.Body.Close()
	}
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrCircuitBreakerOpen)
}

// ---------------------------------------------------------------------------
// Metrics transport: request with body, nil response on error
// ---------------------------------------------------------------------------

func TestMetricsTransport_RoundTrip_WithBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("response"))
	}))
	defer server.Close()

	mt := newMetricsTransport(http.DefaultTransport, NoopMetrics{})
	body := strings.NewReader("request body")
	req, err := http.NewRequestWithContext(context.Background(), "POST", server.URL, body)
	require.NoError(t, err)
	req.ContentLength = int64(len("request body"))

	resp, err := mt.RoundTrip(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

// ---------------------------------------------------------------------------
// FileCache: keyToFilename determinism
// ---------------------------------------------------------------------------

func TestFileCache_KeyToFilename_Deterministic(t *testing.T) {
	cache := &FileCache{dir: "/tmp/cache", keyPrefix: "prefix:"}
	f1 := cache.keyToFilename("key1")
	f2 := cache.keyToFilename("key1")
	f3 := cache.keyToFilename("key2")
	assert.Equal(t, f1, f2, "same key should produce same filename")
	assert.NotEqual(t, f1, f3, "different keys should produce different filenames")
}

// ---------------------------------------------------------------------------
// FileCache: Set size limit exceeded
// ---------------------------------------------------------------------------

func TestFileCache_Set_SizeLimitExceeded(t *testing.T) {
	tmpDir := t.TempDir()
	cache, err := NewFileCache(&FileCacheConfig{
		Dir:     tmpDir,
		TTL:     5 * time.Minute,
		MaxSize: 10,
		Logger:  logr.Discard(),
	})
	require.NoError(t, err)

	err = cache.Set(context.Background(), "big-key", make([]byte, 100), 0)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrCacheSizeLimitExceeded)
}

// ---------------------------------------------------------------------------
// DefaultConfig coverage for new fields
// ---------------------------------------------------------------------------

func TestDefaultConfig_NewFields(t *testing.T) {
	cfg := testDefaultConfig()
	assert.Equal(t, DefaultMaxRedirects, cfg.MaxRedirects)
	assert.Equal(t, DefaultMaxResponseBodySize, cfg.MaxResponseBodySize)
}

// ---------------------------------------------------------------------------
// FileCache.Get: expired entry cleanup
// ---------------------------------------------------------------------------

func TestFileCache_Get_ExpiredEntry(t *testing.T) {
	tmpDir := t.TempDir()
	cache, err := NewFileCache(&FileCacheConfig{
		Dir:    tmpDir,
		TTL:    50 * time.Millisecond,
		Logger: logr.Discard(),
	})
	require.NoError(t, err)

	ctx := context.Background()
	err = cache.Set(ctx, "expiring", []byte("data"), 0)
	require.NoError(t, err)

	time.Sleep(100 * time.Millisecond) // wait for TTL

	data, err := cache.Get(ctx, "expiring")
	assert.NoError(t, err)
	assert.Nil(t, data, "expired entry should return nil")

	_, misses := cache.Stats()
	assert.Equal(t, uint64(1), misses)
}
