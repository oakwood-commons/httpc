// Copyright 2025-2026 Oakwood Commons
// SPDX-License-Identifier: Apache-2.0

package httpc

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCompressionTransport_Gzip(t *testing.T) {
	// Create test data
	testData := []byte("This is test data that should be compressed")

	// Create a server that returns gzipped data
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify Accept-Encoding header
		assert.Contains(t, r.Header.Get("Accept-Encoding"), "gzip")

		// Compress the response
		w.Header().Set("Content-Encoding", "gzip")
		gzipWriter := gzip.NewWriter(w)
		gzipWriter.Write(testData)
		gzipWriter.Close()
	}))
	defer server.Close()

	// Create transport with compression
	transport := newCompressionTransport(http.DefaultTransport, DefaultMaxResponseBodySize)

	// Make request
	req, err := http.NewRequestWithContext(context.Background(), "GET", server.URL, nil)
	require.NoError(t, err)

	resp, err := transport.RoundTrip(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	// Verify response is decompressed
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, testData, body)

	// Verify Content-Encoding header is removed after decompression
	assert.Empty(t, resp.Header.Get("Content-Encoding"))
}

func TestCompressionTransport_NoCompression(t *testing.T) {
	testData := []byte("This is uncompressed data")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(testData)
	}))
	defer server.Close()

	transport := newCompressionTransport(http.DefaultTransport, DefaultMaxResponseBodySize)

	req, err := http.NewRequestWithContext(context.Background(), "GET", server.URL, nil)
	require.NoError(t, err)

	resp, err := transport.RoundTrip(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, testData, body)
}

func TestCompressionTransport_AcceptEncodingPreserved(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Should use custom Accept-Encoding if provided
		assert.Equal(t, "custom-encoding", r.Header.Get("Accept-Encoding"))
		w.Write([]byte("ok"))
	}))
	defer server.Close()

	transport := newCompressionTransport(http.DefaultTransport, DefaultMaxResponseBodySize)

	req, err := http.NewRequestWithContext(context.Background(), "GET", server.URL, nil)
	require.NoError(t, err)
	req.Header.Set("Accept-Encoding", "custom-encoding")

	resp, err := transport.RoundTrip(req)
	require.NoError(t, err)
	defer resp.Body.Close()
}

func TestGzipReadCloser_Close(t *testing.T) {
	// Create gzipped data
	var buf bytes.Buffer
	gzipWriter := gzip.NewWriter(&buf)
	gzipWriter.Write([]byte("test data"))
	gzipWriter.Close()

	// Create readers
	gzipReader, err := gzip.NewReader(&buf)
	require.NoError(t, err)

	closer := io.NopCloser(bytes.NewReader(buf.Bytes()))

	grc := &gzipReadCloser{
		reader: io.NopCloser(gzipReader),
		gzip:   gzipReader,
		closer: closer,
	}

	// Read data
	data, err := io.ReadAll(grc)
	require.NoError(t, err)
	assert.Equal(t, []byte("test data"), data)

	// Close should not error
	err = grc.Close()
	assert.NoError(t, err)
}

func TestCompressionTransport_InvalidGzip(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		// Write invalid gzip data
		w.Write([]byte("not gzipped data"))
	}))
	defer server.Close()

	transport := newCompressionTransport(http.DefaultTransport, DefaultMaxResponseBodySize)

	req, err := http.NewRequestWithContext(context.Background(), "GET", server.URL, nil)
	require.NoError(t, err)

	resp, err := transport.RoundTrip(req)
	// The RoundTrip itself can fail when trying to create the gzip reader
	// This is expected behavior for invalid gzip data
	if err != nil {
		assert.Contains(t, err.Error(), "gzip")
		return
	}

	// Or the error might occur when reading the body
	_, err = io.ReadAll(resp.Body)
	if err != nil {
		assert.Contains(t, err.Error(), "gzip")
	}
	resp.Body.Close()
}

func TestGzipReadCloser_StrictLimit(t *testing.T) {
	// Create gzipped data larger than the limit
	var buf bytes.Buffer
	gzipWriter := gzip.NewWriter(&buf)
	gzipWriter.Write([]byte("abcdefghij")) // 10 bytes
	gzipWriter.Close()

	gzipReader, err := gzip.NewReader(bytes.NewReader(buf.Bytes()))
	require.NoError(t, err)

	closer := io.NopCloser(bytes.NewReader(buf.Bytes()))

	grc := &gzipReadCloser{
		reader: io.NopCloser(gzipReader),
		gzip:   gzipReader,
		closer: closer,
		limit:  5, // limit to 5 bytes
	}

	// First read: buffer of 10, but should be capped to 5
	p := make([]byte, 10)
	n, err := grc.Read(p)
	assert.LessOrEqual(t, n, 5) // must not exceed limit
	if err != nil {
		// could be io.EOF for small reads
		assert.True(t, errors.Is(err, io.EOF) || errors.Is(err, ErrDecompressionBombDetected))
	}

	// Subsequent read after limit reached: must return 0 bytes and the bomb error
	n2, err2 := grc.Read(p)
	assert.Equal(t, 0, n2)
	assert.ErrorIs(t, err2, ErrDecompressionBombDetected)
}

func TestGzipReadCloser_CloseError(t *testing.T) {
	// Test that Close returns error from gzip reader
	gzipErr := errors.New("gzip close error")
	errRC := io.NopCloser(bytes.NewReader(nil))
	// Wrap with a custom closer that returns an error
	faultyGzip := &faultyReadCloser{ReadCloser: errRC, closeErr: gzipErr}
	okCloser := io.NopCloser(bytes.NewReader(nil))

	grc := &gzipReadCloser{
		reader: io.NopCloser(bytes.NewReader(nil)),
		gzip:   faultyGzip,
		closer: okCloser,
	}

	err := grc.Close()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "gzip close error")
}

// faultyReadCloser wraps an io.ReadCloser but returns a fixed error on Close.
type faultyReadCloser struct {
	io.ReadCloser
	closeErr error
}

func (f *faultyReadCloser) Close() error { return f.closeErr }

func TestCompressionTransport_UnlimitedDecompression(t *testing.T) {
	testData := []byte("This is test data that should be compressed")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		gzipWriter := gzip.NewWriter(w)
		gzipWriter.Write(testData)
		gzipWriter.Close()
	}))
	defer server.Close()

	// limit=0 means unlimited decompression
	transport := newCompressionTransport(http.DefaultTransport, 0)

	req, err := http.NewRequestWithContext(context.Background(), "GET", server.URL, nil)
	require.NoError(t, err)

	resp, err := transport.RoundTrip(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, testData, body)
}

func TestCompressionTransport_CustomLimit(t *testing.T) {
	// Create gzipped data larger than the custom limit
	largeData := bytes.Repeat([]byte("x"), 100)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		gzipWriter := gzip.NewWriter(w)
		gzipWriter.Write(largeData)
		gzipWriter.Close()
	}))
	defer server.Close()

	// Set a small limit
	transport := newCompressionTransport(http.DefaultTransport, 10)

	req, err := http.NewRequestWithContext(context.Background(), "GET", server.URL, nil)
	require.NoError(t, err)

	resp, err := transport.RoundTrip(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	_, err = io.ReadAll(resp.Body)
	assert.ErrorIs(t, err, ErrDecompressionBombDetected)
}
