// Copyright 2025-2026 Oakwood Commons
// SPDX-License-Identifier: Apache-2.0

package httpc

import (
	"fmt"
	"time"

	"github.com/go-logr/logr"
)

// AppConfig holds string-based HTTP client configuration suitable for loading
// from YAML/JSON config files. Duration fields use string format (e.g. "30s", "5m")
// for human-readable configuration.
//
// Use [NewClientFromAppConfig] to create a Client from this configuration.
type AppConfig struct {
	// Timeout is the HTTP request timeout as a duration string (e.g. "30s").
	Timeout string `json:"timeout,omitempty" yaml:"timeout,omitempty"`
	// RetryMax is the maximum number of retries.
	RetryMax int `json:"retryMax,omitempty" yaml:"retryMax,omitempty"`
	// RetryWaitMin is the minimum wait time between retries as a duration string.
	RetryWaitMin string `json:"retryWaitMin,omitempty" yaml:"retryWaitMin,omitempty"`
	// RetryWaitMax is the maximum wait time between retries as a duration string.
	RetryWaitMax string `json:"retryWaitMax,omitempty" yaml:"retryWaitMax,omitempty"`
	// EnableCache enables HTTP response caching.
	EnableCache *bool `json:"enableCache,omitempty" yaml:"enableCache,omitempty"`
	// CacheType is the cache type: "memory" or "filesystem".
	CacheType string `json:"cacheType,omitempty" yaml:"cacheType,omitempty"`
	// CacheDir is the directory for filesystem cache.
	CacheDir string `json:"cacheDir,omitempty" yaml:"cacheDir,omitempty"`
	// CacheTTL is the time-to-live for cached responses as a duration string.
	CacheTTL string `json:"cacheTTL,omitempty" yaml:"cacheTTL,omitempty"`
	// CacheKeyPrefix is the prefix for cache keys.
	CacheKeyPrefix string `json:"cacheKeyPrefix,omitempty" yaml:"cacheKeyPrefix,omitempty"`
	// MaxCacheFileSize is the maximum size for a single cached file in bytes.
	MaxCacheFileSize int64 `json:"maxCacheFileSize,omitempty" yaml:"maxCacheFileSize,omitempty"`
	// MemoryCacheSize is the maximum number of entries in memory cache.
	MemoryCacheSize int `json:"memoryCacheSize,omitempty" yaml:"memoryCacheSize,omitempty"`
	// EnableCircuitBreaker enables the circuit breaker pattern.
	EnableCircuitBreaker *bool `json:"enableCircuitBreaker,omitempty" yaml:"enableCircuitBreaker,omitempty"`
	// CircuitBreakerMaxFailures is the number of failures before opening the circuit.
	CircuitBreakerMaxFailures int `json:"circuitBreakerMaxFailures,omitempty" yaml:"circuitBreakerMaxFailures,omitempty"`
	// CircuitBreakerOpenTimeout is the wait time before half-open state as a duration string.
	CircuitBreakerOpenTimeout string `json:"circuitBreakerOpenTimeout,omitempty" yaml:"circuitBreakerOpenTimeout,omitempty"`
	// CircuitBreakerHalfOpenMaxRequests is the number of successful requests in half-open before closing.
	CircuitBreakerHalfOpenMaxRequests int `json:"circuitBreakerHalfOpenMaxRequests,omitempty" yaml:"circuitBreakerHalfOpenMaxRequests,omitempty"`
	// EnableCompression enables automatic gzip compression.
	EnableCompression *bool `json:"enableCompression,omitempty" yaml:"enableCompression,omitempty"`
	// AllowPrivateIPs allows HTTP requests to private/loopback/link-local IP literals.
	//
	// Deprecated: use AllowedPrivateCIDRs to unblock only the ranges you need.
	AllowPrivateIPs *bool `json:"allowPrivateIPs,omitempty" yaml:"allowPrivateIPs,omitempty"`
	// AllowedPrivateCIDRs lists private/reserved CIDR blocks the client may
	// connect to, e.g. ["10.0.0.0/8"]. Everything else in the default blocklist
	// stays blocked. Cloud instance-metadata endpoints can never be allowed.
	//
	// When both this and AllowPrivateIPs are set, AllowedPrivateCIDRs wins:
	// the resulting client reaches only the listed ranges.
	AllowedPrivateCIDRs []string `json:"allowedPrivateCIDRs,omitempty" yaml:"allowedPrivateCIDRs,omitempty"`
	// TrustProxyResolution allows a proxied request whose target hostname does
	// not resolve here to proceed, leaving egress policy to the proxy.
	//
	// Defaults to false, which fails closed on any DNS failure for a proxied
	// target. Enable it only in a proxy-only environment with no direct
	// resolver, where the proxy is trusted to enforce egress policy itself.
	TrustProxyResolution *bool `json:"trustProxyResolution,omitempty" yaml:"trustProxyResolution,omitempty"`
	// MaxResponseBodySize is the maximum HTTP response body size in bytes.
	MaxResponseBodySize int64 `json:"maxResponseBodySize,omitempty" yaml:"maxResponseBodySize,omitempty"`
}

// NewClientFromAppConfig creates a new HTTP client using string-based application configuration.
// The cfg parameter can be nil, in which case defaults are used.
// Returns an error if any duration string or config value is invalid.
func NewClientFromAppConfig(cfg *AppConfig, logger logr.Logger) (*Client, error) {
	clientCfg := DefaultConfig()
	clientCfg.Logger = logger

	if cfg == nil {
		return NewClient(clientCfg), nil
	}

	// Apply timeout
	if cfg.Timeout != "" {
		d, err := time.ParseDuration(cfg.Timeout)
		if err != nil {
			return nil, fmt.Errorf("invalid timeout duration %q: %w", cfg.Timeout, err)
		}
		clientCfg.Timeout = d
	}

	// Apply retry settings
	if cfg.RetryMax > 0 {
		clientCfg.RetryMax = cfg.RetryMax
	}
	if cfg.RetryWaitMin != "" {
		d, err := time.ParseDuration(cfg.RetryWaitMin)
		if err != nil {
			return nil, fmt.Errorf("invalid retryWaitMin duration %q: %w", cfg.RetryWaitMin, err)
		}
		clientCfg.RetryWaitMin = d
	}
	if cfg.RetryWaitMax != "" {
		d, err := time.ParseDuration(cfg.RetryWaitMax)
		if err != nil {
			return nil, fmt.Errorf("invalid retryWaitMax duration %q: %w", cfg.RetryWaitMax, err)
		}
		clientCfg.RetryWaitMax = d
	}

	// Apply cache settings
	if cfg.EnableCache != nil {
		clientCfg.EnableCache = *cfg.EnableCache
	}
	if cfg.CacheType != "" {
		ct := CacheType(cfg.CacheType)
		if ct != CacheTypeMemory && ct != CacheTypeFilesystem {
			return nil, fmt.Errorf("invalid cacheType %q: must be %q or %q", cfg.CacheType, CacheTypeMemory, CacheTypeFilesystem)
		}
		clientCfg.CacheType = ct
	}
	if cfg.CacheDir != "" {
		clientCfg.CacheDir = cfg.CacheDir
	}
	if cfg.CacheTTL != "" {
		d, err := time.ParseDuration(cfg.CacheTTL)
		if err != nil {
			return nil, fmt.Errorf("invalid cacheTTL duration %q: %w", cfg.CacheTTL, err)
		}
		clientCfg.CacheTTL = d
	}
	if cfg.CacheKeyPrefix != "" {
		clientCfg.CacheKeyPrefix = cfg.CacheKeyPrefix
	}
	if cfg.MaxCacheFileSize > 0 {
		clientCfg.MaxCacheFileSize = cfg.MaxCacheFileSize
	}
	if cfg.MemoryCacheSize > 0 {
		clientCfg.MemoryCacheSize = cfg.MemoryCacheSize
	}

	// Apply circuit breaker settings
	if cfg.EnableCircuitBreaker != nil {
		clientCfg.EnableCircuitBreaker = *cfg.EnableCircuitBreaker
	}
	if cfg.CircuitBreakerMaxFailures > 0 || cfg.CircuitBreakerOpenTimeout != "" || cfg.CircuitBreakerHalfOpenMaxRequests > 0 {
		clientCfg.CircuitBreakerConfig = DefaultCircuitBreakerConfig()
		if cfg.CircuitBreakerMaxFailures > 0 {
			clientCfg.CircuitBreakerConfig.MaxFailures = cfg.CircuitBreakerMaxFailures
		}
		if cfg.CircuitBreakerOpenTimeout != "" {
			d, err := time.ParseDuration(cfg.CircuitBreakerOpenTimeout)
			if err != nil {
				return nil, fmt.Errorf("invalid circuitBreakerOpenTimeout duration %q: %w", cfg.CircuitBreakerOpenTimeout, err)
			}
			clientCfg.CircuitBreakerConfig.OpenTimeout = d
		}
		if cfg.CircuitBreakerHalfOpenMaxRequests > 0 {
			clientCfg.CircuitBreakerConfig.HalfOpenMaxRequests = cfg.CircuitBreakerHalfOpenMaxRequests
		}
	}

	// Apply compression setting
	if cfg.EnableCompression != nil {
		clientCfg.EnableCompression = *cfg.EnableCompression
	}

	// Apply SSRF settings
	if cfg.AllowPrivateIPs != nil {
		clientCfg.AllowPrivateIPs = *cfg.AllowPrivateIPs
	}
	// A non-nil but empty list is meaningful: it means "no exceptions", and
	// must still win over a legacy AllowPrivateIPs: true. Only an absent
	// (nil) list leaves the policy unset.
	if cfg.AllowedPrivateCIDRs != nil {
		policy, err := NewIPPolicy(cfg.AllowedPrivateCIDRs...)
		if err != nil {
			return nil, fmt.Errorf("invalid allowedPrivateCIDRs: %w", err)
		}
		clientCfg.IPPolicy = policy
	}
	// Applies to whichever policy the config resolves to, including the
	// implicit one, so it does not require AllowedPrivateCIDRs to be set.
	if cfg.TrustProxyResolution != nil {
		policy := *clientCfg.ipPolicy()
		policy.TrustProxyResolution = *cfg.TrustProxyResolution
		clientCfg.IPPolicy = &policy
	}

	// Apply max response body size
	if cfg.MaxResponseBodySize > 0 {
		clientCfg.MaxResponseBodySize = cfg.MaxResponseBodySize
	}

	return NewClient(clientCfg), nil
}

// MergeAppConfig merges two AppConfig values. Fields set in override take
// precedence over base. Returns base if override is nil.
func MergeAppConfig(base, override *AppConfig) *AppConfig {
	if override == nil {
		return base
	}
	if base == nil {
		return override
	}

	// Start with a copy of base
	merged := *base

	// Override with per-config values if set
	if override.Timeout != "" {
		merged.Timeout = override.Timeout
	}
	if override.RetryMax > 0 {
		merged.RetryMax = override.RetryMax
	}
	if override.RetryWaitMin != "" {
		merged.RetryWaitMin = override.RetryWaitMin
	}
	if override.RetryWaitMax != "" {
		merged.RetryWaitMax = override.RetryWaitMax
	}
	if override.EnableCache != nil {
		merged.EnableCache = override.EnableCache
	}
	if override.CacheType != "" {
		merged.CacheType = override.CacheType
	}
	if override.CacheDir != "" {
		merged.CacheDir = override.CacheDir
	}
	if override.CacheTTL != "" {
		merged.CacheTTL = override.CacheTTL
	}
	if override.CacheKeyPrefix != "" {
		merged.CacheKeyPrefix = override.CacheKeyPrefix
	}
	if override.MaxCacheFileSize > 0 {
		merged.MaxCacheFileSize = override.MaxCacheFileSize
	}
	if override.MemoryCacheSize > 0 {
		merged.MemoryCacheSize = override.MemoryCacheSize
	}
	if override.EnableCircuitBreaker != nil {
		merged.EnableCircuitBreaker = override.EnableCircuitBreaker
	}
	if override.CircuitBreakerMaxFailures > 0 {
		merged.CircuitBreakerMaxFailures = override.CircuitBreakerMaxFailures
	}
	if override.CircuitBreakerOpenTimeout != "" {
		merged.CircuitBreakerOpenTimeout = override.CircuitBreakerOpenTimeout
	}
	if override.CircuitBreakerHalfOpenMaxRequests > 0 {
		merged.CircuitBreakerHalfOpenMaxRequests = override.CircuitBreakerHalfOpenMaxRequests
	}
	if override.EnableCompression != nil {
		merged.EnableCompression = override.EnableCompression
	}
	if override.AllowPrivateIPs != nil {
		merged.AllowPrivateIPs = override.AllowPrivateIPs
	}
	// Non-nil-but-empty overrides the base list, so a narrower scope can clear
	// inherited exceptions; nil means "not specified" and inherits.
	if override.AllowedPrivateCIDRs != nil {
		merged.AllowedPrivateCIDRs = override.AllowedPrivateCIDRs
	}
	if override.TrustProxyResolution != nil {
		merged.TrustProxyResolution = override.TrustProxyResolution
	}
	if override.MaxResponseBodySize > 0 {
		merged.MaxResponseBodySize = override.MaxResponseBodySize
	}

	return &merged
}
