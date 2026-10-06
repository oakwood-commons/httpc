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

	closer := io.NopCloser(bytes.NewReader(buf.Bytes()))

	grc := &gzipReadCloser{
		body: closer,
	}

	// Read data
	data, err := io.ReadAll(grc)
	require.NoError(t, err)
	assert.Equal(t, []byte("test data"), data)

	// Close should not error
	err = grc.Close()
	assert.NoError(t, err)
}

func TestGzipReadCloser_CloseWithoutRead(t *testing.T) {
	// Close before any Read must still close the underlying body.
	tc := &trackingCloser{ReadCloser: io.NopCloser(bytes.NewReader(nil))}
	grc := &gzipReadCloser{body: tc}

	require.NoError(t, grc.Close())
	assert.True(t, tc.closed)
}

// trackingCloser records whether Close was called.
type trackingCloser struct {
	io.ReadCloser
	closed bool
}

func (t *trackingCloser) Close() error {
	t.closed = true
	return t.ReadCloser.Close()
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

	// RoundTrip must never fail for a decode error -- the response and its
	// 200 status are still valid and must reach the caller. Only Read fails.
	resp, err := transport.RoundTrip(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	_, err = io.ReadAll(resp.Body)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "gzip")
	resp.Body.Close()
}

func TestCompressionTransport_TruncatedGzip(t *testing.T) {
	// A gzip header with no body: valid enough to construct a reader, but
	// fails on read with ErrUnexpectedEOF.
	var buf bytes.Buffer
	gzipWriter := gzip.NewWriter(&buf)
	gzipWriter.Write([]byte("some data"))
	gzipWriter.Close()
	truncated := buf.Bytes()[:len(buf.Bytes())-4]

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		w.Write(truncated)
	}))
	defer server.Close()

	transport := newCompressionTransport(http.DefaultTransport, DefaultMaxResponseBodySize)
	req, err := http.NewRequestWithContext(context.Background(), "GET", server.URL, nil)
	require.NoError(t, err)

	resp, err := transport.RoundTrip(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	_, err = io.ReadAll(resp.Body)
	require.Error(t, err)
	assert.True(t, errors.Is(err, io.ErrUnexpectedEOF), "expected io.ErrUnexpectedEOF, got %v", err)
}

func TestGzipReadCloser_StrictLimit(t *testing.T) {
	// Create gzipped data larger than the limit
	var buf bytes.Buffer
	gzipWriter := gzip.NewWriter(&buf)
	gzipWriter.Write([]byte("abcdefghij")) // 10 bytes
	gzipWriter.Close()

	closer := io.NopCloser(bytes.NewReader(buf.Bytes()))

	grc := &gzipReadCloser{
		body:  closer,
		limit: 5, // limit to 5 bytes
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

// ---------------------------------------------------------------------------
// Regression tests for httpc#34: a response whose Content-Encoding advertises
// gzip but carries no body (zero-length, chunked-empty, 204, HEAD) must never
// fail RoundTrip -- the status and headers are always returned with a nil
// error, and decoding is skipped entirely when there is no body to decode.
// ---------------------------------------------------------------------------

func TestCompressionTransport_EmptyBodyContentLengthZero(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Content-Length", "0")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	transport := newCompressionTransport(http.DefaultTransport, DefaultMaxResponseBodySize)
	req, err := http.NewRequestWithContext(context.Background(), "DELETE", server.URL, nil)
	require.NoError(t, err)

	resp, err := transport.RoundTrip(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Empty(t, body)
}

func TestCompressionTransport_EmptyBodyContentLengthZero_FullClient(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Content-Length", "0")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := testDefaultConfig()
	cfg.EnableCompression = true
	cfg.EnableCircuitBreaker = true
	client := NewClient(cfg)
	defer client.Close()

	req, err := http.NewRequestWithContext(context.Background(), "DELETE", server.URL, nil)
	require.NoError(t, err)

	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// A successful request must not have tripped the circuit breaker.
	assert.NoError(t, client.circuitBreaker.allow(req.URL.Hostname()))
}

func TestCompressionTransport_EmptyChunkedBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		w.WriteHeader(http.StatusOK)
		// Flush with no bytes written: forces chunked transfer encoding with
		// an empty body, so resp.ContentLength is -1, not 0, on the client
		// side. This must still be handled without error.
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
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
	assert.Empty(t, body)
}

func TestCompressionTransport_204NoContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	transport := newCompressionTransport(http.DefaultTransport, DefaultMaxResponseBodySize)
	req, err := http.NewRequestWithContext(context.Background(), "GET", server.URL, nil)
	require.NoError(t, err)

	resp, err := transport.RoundTrip(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
}

func TestCompressionTransport_HEAD_NoAcceptEncodingAdded(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Empty(t, r.Header.Get("Accept-Encoding"))
		w.Header().Set("Content-Encoding", "gzip")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	transport := newCompressionTransport(http.DefaultTransport, DefaultMaxResponseBodySize)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodHead, server.URL, nil)
	require.NoError(t, err)

	resp, err := transport.RoundTrip(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestCompressionTransport_Range_NoAcceptEncodingAdded(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Empty(t, r.Header.Get("Accept-Encoding"))
		w.Write([]byte("ok"))
	}))
	defer server.Close()

	transport := newCompressionTransport(http.DefaultTransport, DefaultMaxResponseBodySize)
	req, err := http.NewRequestWithContext(context.Background(), "GET", server.URL, nil)
	require.NoError(t, err)
	req.Header.Set("Range", "bytes=0-10")

	resp, err := transport.RoundTrip(req)
	require.NoError(t, err)
	defer resp.Body.Close()
}

func TestCompressionTransport_CallerSetAcceptEncoding_NoDecode(t *testing.T) {
	testData := []byte("This is test data that should be compressed")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "gzip", r.Header.Get("Accept-Encoding"))
		w.Header().Set("Content-Encoding", "gzip")
		gzipWriter := gzip.NewWriter(w)
		gzipWriter.Write(testData)
		gzipWriter.Close()
	}))
	defer server.Close()

	transport := newCompressionTransport(http.DefaultTransport, DefaultMaxResponseBodySize)
	req, err := http.NewRequestWithContext(context.Background(), "GET", server.URL, nil)
	require.NoError(t, err)
	req.Header.Set("Accept-Encoding", "gzip") // caller owns decoding now

	resp, err := transport.RoundTrip(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	// httpc must not decode on the caller's behalf: headers and raw
	// (still-compressed) bytes pass through untouched.
	assert.Equal(t, "gzip", resp.Header.Get("Content-Encoding"))
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.NotEqual(t, testData, body) // still compressed
}

func TestCompressionTransport_SetsUncompressedFlag(t *testing.T) {
	testData := []byte("This is test data that should be compressed")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		gzipWriter := gzip.NewWriter(w)
		gzipWriter.Write(testData)
		gzipWriter.Close()
	}))
	defer server.Close()

	transport := newCompressionTransport(http.DefaultTransport, DefaultMaxResponseBodySize)
	req, err := http.NewRequestWithContext(context.Background(), "GET", server.URL, nil)
	require.NoError(t, err)

	resp, err := transport.RoundTrip(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.True(t, resp.Uncompressed)
}
