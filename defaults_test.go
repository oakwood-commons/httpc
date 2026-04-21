// Copyright 2025-2026 Oakwood Commons
// SPDX-License-Identifier: Apache-2.0

package httpc

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultCacheDir(t *testing.T) {
	dir := defaultCacheDir()
	assert.NotEmpty(t, dir)
	assert.Contains(t, dir, "httpc")
	assert.Contains(t, dir, httpCacheDirName)
}

func TestExpandHome(t *testing.T) {
	home, err := os.UserHomeDir()
	require.NoError(t, err)

	tests := []struct {
		name    string
		path    string
		want    string
		wantErr bool
	}{
		{name: "tilde only", path: "~", want: home},
		{name: "tilde with subpath", path: "~/cache/test", want: filepath.Join(home, "cache/test")},
		{name: "no tilde", path: "/tmp/cache", want: "/tmp/cache"},
		{name: "empty path", path: "", want: ""},
		{name: "relative path", path: "relative/path", want: "relative/path"},
		{name: "tilde in middle", path: "/path/~/something", want: "/path/~/something"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := expandHome(tt.path)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func TestDefaultConstants(t *testing.T) {
	assert.Greater(t, DefaultTimeout.Seconds(), 0.0)
	assert.Greater(t, DefaultRetryMax, 0)
	assert.Greater(t, DefaultRetryWait.Seconds(), 0.0)
	assert.Greater(t, DefaultRetryMaxWait.Seconds(), 0.0)
	assert.Greater(t, DefaultCacheTTL.Seconds(), 0.0)
	assert.NotEmpty(t, DefaultCacheKeyPrefix)
	assert.Greater(t, DefaultMaxResponseBodySize, int64(0))
	assert.Greater(t, DefaultMaxCacheFileSize, int64(0))
	assert.Greater(t, DefaultMemoryCacheSize, 0)
	assert.Greater(t, DefaultCircuitBreakerMaxFailures, 0)
	assert.Greater(t, DefaultCircuitBreakerOpenTimeout.Seconds(), 0.0)
	assert.Greater(t, DefaultCircuitBreakerHalfOpenRequests, 0)
	assert.Greater(t, DefaultMaxRedirects, 0)
}
