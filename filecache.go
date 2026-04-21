// Copyright 2025-2026 Oakwood Commons
// SPDX-License-Identifier: Apache-2.0

package httpc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/go-logr/logr"
)

// FileCacheConfig holds configuration for filesystem cache
type FileCacheConfig struct {
	// Dir is the directory to use for cache storage
	Dir string
	// TTL is the time-to-live for cached entries
	TTL time.Duration
	// KeyPrefix is a prefix added to all cache keys to prevent collisions
	KeyPrefix string
	// MaxSize is the maximum size in bytes for a single cached file (0 = no limit)
	MaxSize int64
	// FileIOTimeout is the best-effort maximum time for file I/O operations (default: 5s).
	// Context is checked before and after I/O syscalls, but cannot interrupt them mid-call.
	FileIOTimeout time.Duration
	// Logger is used for logging cache operations
	Logger logr.Logger
	// Metrics is the metrics collector for cache operations
	Metrics Metrics
}

// FileCache is a filesystem-based cache implementation.
//
// Thread-Safety: FileCache is safe for concurrent use by multiple goroutines.
// Get, Set, and Del operations can be called concurrently. Hit/miss statistics
// are tracked using atomic operations. File operations are performed atomically
// where possible (e.g., write-then-rename for Set). However, due to filesystem
// limitations, there may be race conditions if multiple processes (not goroutines)
// access the same cache directory simultaneously.
type FileCache struct {
	dir           string
	ttl           time.Duration
	keyPrefix     string
	maxSize       int64
	fileIOTimeout time.Duration
	logger        logr.Logger
	metrics       Metrics
	hits          uint64
	misses        uint64
	cachedSize    atomic.Int64 // tracked incrementally on Set/Del
}

// defaultFileIOTimeout is the default timeout for individual cache file I/O operations.
const defaultFileIOTimeout = 5 * time.Second

// NewFileCache creates a new filesystem cache in the specified directory
func NewFileCache(config *FileCacheConfig) (*FileCache, error) {
	if config == nil {
		return nil, errors.New("config cannot be nil")
	}

	dir := config.Dir
	if dir == "" {
		return nil, errors.New("cache directory cannot be empty")
	}

	// Expand home directory if present
	dir, err := expandHome(dir)
	if err != nil {
		return nil, fmt.Errorf("failed to get home directory: %w", err)
	}

	// Create directory if it doesn't exist (0700 restricts to owner only)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("failed to create cache directory: %w", err)
	}

	logger := config.Logger
	if logger.GetSink() == nil {
		logger = logr.Discard()
	}

	m := config.Metrics
	if m == nil {
		m = NoopMetrics{}
	}

	ioTimeout := config.FileIOTimeout
	if ioTimeout <= 0 {
		ioTimeout = defaultFileIOTimeout
	}

	fc := &FileCache{
		dir:           dir,
		ttl:           config.TTL,
		keyPrefix:     config.KeyPrefix,
		maxSize:       config.MaxSize,
		fileIOTimeout: ioTimeout,
		logger:        logger,
		metrics:       m,
	}

	// Update cache size metric on initialization
	fc.updateCacheSizeMetric()

	return fc, nil
}

// Set stores data in the cache with the given key
// The ttl parameter is required by the httpcache.Cache interface but is not used.
// This implementation uses the cache's default TTL (fc.ttl) for all entries.
func (fc *FileCache) Set(ctx context.Context, key string, data []byte, _ time.Duration) error {
	// Check context before proceeding
	if err := ctx.Err(); err != nil {
		return err
	}

	// Check size limit
	if fc.maxSize > 0 && int64(len(data)) > fc.maxSize {
		fc.logger.V(1).Info("cache entry exceeds size limit",
			"key", key,
			"size", len(data),
			"maxSize", fc.maxSize,
		)
		return fmt.Errorf("%w: entry size %d exceeds limit %d", ErrCacheSizeLimitExceeded, len(data), fc.maxSize)
	}

	filename := fc.keyToFilename(key)

	// Capture old file size for incremental metric tracking.
	var oldSize int64
	if info, statErr := os.Stat(filename); statErr == nil {
		oldSize = info.Size()
	}

	// Create a context with timeout for file operations
	writeCtx, cancel := context.WithTimeout(ctx, fc.fileIOTimeout)
	defer cancel()

	// Check context before write
	if err := writeCtx.Err(); err != nil {
		return fmt.Errorf("cache write timeout: %w", err)
	}

	// Write to a uniquely-named temp file to avoid races from concurrent Set
	// calls for the same key (or stale .tmp files from a previous crash).
	tmpFile, err := os.CreateTemp(fc.dir, ".httpc-tmp-*")
	if err != nil {
		return fmt.Errorf("failed to create temp cache file: %w", err)
	}
	tmpPath := tmpFile.Name()

	if _, writeErr := tmpFile.Write(data); writeErr != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("failed to write cache file: %w", writeErr)
	}
	if err := tmpFile.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to close temp cache file: %w", err)
	}

	// Restrict permissions (CreateTemp uses 0600 by default on most OS, but be explicit).
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to set cache file permissions: %w", err)
	}

	// Check context again before rename
	if err := writeCtx.Err(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("cache write timeout: %w", err)
	}

	// Atomic rename
	if err := os.Rename(tmpPath, filename); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to rename cache file: %w", err)
	}

	fc.logger.V(2).Info("cached entry", "key", key, "size", len(data))
	fc.adjustCacheSize(int64(len(data)) - oldSize)

	return nil
}

// Get retrieves data from the cache for the given key
// Returns (nil, nil) for cache misses - this is not an error, it's standard cache behavior
func (fc *FileCache) Get(ctx context.Context, key string) ([]byte, error) {
	// Check context before proceeding
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	filename := fc.keyToFilename(key)

	// Create a context with timeout for file operations
	readCtx, cancel := context.WithTimeout(ctx, fc.fileIOTimeout)
	defer cancel()

	// Check context before read
	if err := readCtx.Err(); err != nil {
		return nil, fmt.Errorf("cache read timeout: %w", err)
	}

	// Read the file directly. If the file was deleted between our read attempt,
	// we handle ErrNotExist as a cache miss. This avoids a TOCTOU race from
	// doing stat-then-read.
	data, err := os.ReadFile(filename)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// Cache miss
			atomic.AddUint64(&fc.misses, 1)
			fc.metrics.IncrementCacheMisses(ctx)
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read cache file: %w", err)
	}

	// Check context after read for consistency with Set().
	if err := readCtx.Err(); err != nil {
		return nil, fmt.Errorf("cache read timeout: %w", err)
	}

	// Check if cache entry is expired using file modification time
	if fc.ttl > 0 {
		info, statErr := os.Stat(filename)
		if statErr != nil {
			if errors.Is(statErr, fs.ErrNotExist) {
				// File was removed between read and stat (race); treat as miss
				atomic.AddUint64(&fc.misses, 1)
				fc.metrics.IncrementCacheMisses(ctx)
				return nil, nil
			}
			return nil, fmt.Errorf("failed to stat cache file: %w", statErr)
		}
		if time.Since(info.ModTime()) > fc.ttl {
			// Clean up expired file
			if err := os.Remove(filename); err != nil && !errors.Is(err, fs.ErrNotExist) {
				fc.logger.V(1).Info("failed to remove expired cache file", "key", key, "error", err)
			}
			// Cache miss (expired)
			atomic.AddUint64(&fc.misses, 1)
			fc.metrics.IncrementCacheMisses(ctx)
			return nil, nil
		}
	}

	// Cache hit
	atomic.AddUint64(&fc.hits, 1)
	fc.metrics.IncrementCacheHits(ctx)
	fc.logger.V(2).Info("cache hit", "key", key, "size", len(data))
	return data, nil
}

// Del removes data from the cache for the given key
func (fc *FileCache) Del(ctx context.Context, key string) error {
	// Check context before proceeding
	if err := ctx.Err(); err != nil {
		return err
	}

	filename := fc.keyToFilename(key)

	// Capture file size before removal for incremental metric tracking.
	var removedSize int64
	if info, statErr := os.Stat(filename); statErr == nil {
		removedSize = info.Size()
	}

	if err := os.Remove(filename); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("failed to delete cache file: %w", err)
	}

	fc.logger.V(2).Info("deleted cache entry", "key", key)
	if removedSize > 0 {
		fc.adjustCacheSize(-removedSize)
	}
	return nil
}

// Clear removes all cached files
func (fc *FileCache) Clear() error {
	entries, err := os.ReadDir(fc.dir)
	if err != nil {
		return fmt.Errorf("failed to read cache directory: %w", err)
	}

	var errs []error
	for _, entry := range entries {
		if !entry.IsDir() {
			filePath := filepath.Join(fc.dir, entry.Name())
			if err := os.Remove(filePath); err != nil {
				errs = append(errs, fmt.Errorf("failed to remove %s: %w", filePath, err))
				fc.logger.Error(err, "failed to remove cache file", "path", filePath)
			}
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("encountered %d errors while clearing cache", len(errs))
	}

	fc.logger.Info("cleared all cache entries", "directory", fc.dir)
	fc.updateCacheSizeMetric()
	return nil
}

// CleanExpired removes expired cache entries
func (fc *FileCache) CleanExpired() error {
	if fc.ttl == 0 {
		return nil // No TTL, nothing to clean
	}

	entries, err := os.ReadDir(fc.dir)
	if err != nil {
		return fmt.Errorf("failed to read cache directory: %w", err)
	}

	now := time.Now()
	removedCount := 0
	var errs []error

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		filePath := filepath.Join(fc.dir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			fc.logger.V(1).Info("failed to get file info during cleanup", "path", filePath, "error", err)
			continue
		}

		if now.Sub(info.ModTime()) > fc.ttl {
			if err := os.Remove(filePath); err != nil && !errors.Is(err, fs.ErrNotExist) {
				errs = append(errs, err)
				fc.logger.V(1).Info("failed to remove expired cache file", "path", filePath, "error", err)
			} else {
				removedCount++
			}
		}
	}

	fc.logger.V(1).Info("cleaned expired cache entries", "removed", removedCount, "errors", len(errs))
	fc.updateCacheSizeMetric()

	if len(errs) > 0 {
		return fmt.Errorf("encountered %d errors while cleaning expired entries", len(errs))
	}

	return nil
}

// keyToFilename converts a cache key to a safe filename using SHA-256 hash
func (fc *FileCache) keyToFilename(key string) string {
	// Add prefix to key before hashing
	prefixedKey := fc.keyPrefix + key
	hash := sha256.Sum256([]byte(prefixedKey))
	filename := hex.EncodeToString(hash[:])
	return filepath.Join(fc.dir, filename)
}

// Stats returns the cache hit and miss statistics
func (fc *FileCache) Stats() (hits, misses uint64) {
	return atomic.LoadUint64(&fc.hits), atomic.LoadUint64(&fc.misses)
}

// Close performs cleanup and releases resources
// This method cleans expired entries as a final housekeeping step
func (fc *FileCache) Close() error {
	fc.logger.V(1).Info("closing file cache", "directory", fc.dir)
	// Clean up expired entries on close
	if err := fc.CleanExpired(); err != nil {
		fc.logger.V(1).Info("error cleaning expired entries on close", "error", err)
		return err
	}
	return nil
}

// updateCacheSizeMetric calculates the total size of cached files and updates the metric.
// This performs a full directory walk and should only be called during initialization
// or infrequent operations (Clear, CleanExpired). Use adjustCacheSize for incremental
// updates during Set/Del.
func (fc *FileCache) updateCacheSizeMetric() {
	entries, err := os.ReadDir(fc.dir)
	if err != nil {
		fc.logger.V(1).Info("failed to read cache directory for size metric", "error", err)
		return
	}

	var totalSize int64
	for _, entry := range entries {
		if !entry.IsDir() {
			info, err := entry.Info()
			if err != nil {
				continue
			}
			totalSize += info.Size()
		}
	}

	fc.cachedSize.Store(totalSize)
	fc.metrics.SetCacheSizeBytes(totalSize)
	fc.logger.V(2).Info("updated cache size metric", "bytes", totalSize)
}

// adjustCacheSize atomically adjusts the tracked cache size by delta bytes
// and reports the new value to the metrics collector.
func (fc *FileCache) adjustCacheSize(delta int64) {
	newSize := fc.cachedSize.Add(delta)
	if newSize < 0 {
		// Guard against underflow from racing deletes; resync on next full scan.
		fc.cachedSize.Store(0)
		newSize = 0
	}
	fc.metrics.SetCacheSizeBytes(newSize)
}
