// Copyright 2025-2026 Oakwood Commons
// SPDX-License-Identifier: Apache-2.0

package httpc

import (
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// ErrDecompressionBombDetected is returned when a decompressed response exceeds the size limit.
var ErrDecompressionBombDetected = errors.New("decompressed response exceeds maximum allowed size")

// compressionTransport wraps an http.RoundTripper and adds automatic compression support.
// maxDecompressedSize is the per-client limit; 0 means unlimited.
type compressionTransport struct {
	base                http.RoundTripper
	maxDecompressedSize int64
}

// newCompressionTransport creates a new compression transport.
// limit is the maximum decompressed response size in bytes (0 = unlimited).
func newCompressionTransport(base http.RoundTripper, limit int64) *compressionTransport {
	return &compressionTransport{
		base:                base,
		maxDecompressedSize: limit,
	}
}

// RoundTrip implements http.RoundTripper with compression support.
// It clones the request before modifying headers to comply with the
// RoundTripper contract (the original request must not be mutated).
func (t *compressionTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Clone the request to avoid mutating the caller's original request.
	// The RoundTripper contract requires that implementations do not modify
	// the request, since it may be shared across goroutines.
	req2 := req.Clone(req.Context())

	// Add Accept-Encoding header if not present (only gzip is supported)
	if req2.Header.Get("Accept-Encoding") == "" {
		req2.Header.Set("Accept-Encoding", "gzip")
	}

	// Execute the request
	resp, err := t.base.RoundTrip(req2)
	if err != nil {
		return nil, err
	}

	// Check if response is compressed
	encoding := resp.Header.Get("Content-Encoding")
	if encoding == "" {
		return resp, nil
	}

	// Decompress gzip responses; other encodings are passed through as-is.
	if strings.ToLower(encoding) == "gzip" {
		gzipReader, gzipErr := gzip.NewReader(resp.Body)
		if gzipErr != nil {
			// Return the original response with headers intact so the caller
			// knows the body is still compressed.
			return resp, fmt.Errorf("failed to create gzip reader: %w", gzipErr)
		}

		// Replace the body with a decompressed reader that enforces a size limit.
		resp.Body = &gzipReadCloser{
			reader: io.NopCloser(gzipReader),
			gzip:   gzipReader,
			closer: resp.Body,
			limit:  t.maxDecompressedSize,
		}

		// Remove Content-Encoding header since we're decompressing
		resp.Header.Del("Content-Encoding")
		// Remove Content-Length as it's no longer accurate
		resp.Header.Del("Content-Length")
		resp.ContentLength = -1
	}

	return resp, nil
}

// gzipReadCloser wraps a gzip reader and ensures both the reader and original closer are closed
type gzipReadCloser struct {
	reader    io.ReadCloser // the gzip reader
	gzip      io.ReadCloser // underlying gzip.Reader (for Close)
	closer    io.Closer     // original response body
	limit     int64         // maximum decompressed bytes
	bytesRead int64         // bytes read so far
}

func (g *gzipReadCloser) Read(p []byte) (n int, err error) {
	if g.limit > 0 {
		remaining := g.limit - g.bytesRead
		if remaining <= 0 {
			return 0, ErrDecompressionBombDetected
		}
		if int64(len(p)) > remaining {
			p = p[:remaining]
		}
	}
	n, err = g.reader.Read(p)
	g.bytesRead += int64(n)
	return n, err
}

func (g *gzipReadCloser) Close() error {
	// Close the gzip reader and the original body
	err1 := g.gzip.Close()
	err2 := g.closer.Close()

	if err1 != nil {
		return err1
	}
	return err2
}
