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

// RoundTrip implements http.RoundTripper with compression support. It clones
// the request before modifying headers to comply with the RoundTripper
// contract (the original request must not be mutated), and only does so when
// it is actually going to add Accept-Encoding itself.
func (t *compressionTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Only add Accept-Encoding, and only decode what we asked for, when all
	// of these hold -- this matches net/http's own transparent gzip support
	// (net/http/transport.go's addedGzip) and klauspost/gzhttp:
	//   - the caller hasn't set Accept-Encoding themselves. A caller who set
	//     it owns interpreting the response; forcing a decode on their behalf
	//     would silently rewrite bytes they explicitly asked to see raw.
	//   - the method isn't HEAD. A HEAD response carries no body but can
	//     still advertise Content-Encoding: gzip for the resource it
	//     describes; asking for gzip there just reproduces this issue's bug
	//     against an always-empty body.
	//   - there's no Range request. Decoding part of a gzip stream can't
	//     produce valid output (see golang.org/issue/8923).
	addGzip := req.Header.Get("Accept-Encoding") == "" &&
		req.Header.Get("Range") == "" &&
		req.Method != http.MethodHead

	reqToSend := req
	if addGzip {
		// Clone the request to avoid mutating the caller's original request.
		// The RoundTripper contract requires that implementations do not
		// modify the request, since it may be shared across goroutines.
		reqToSend = req.Clone(req.Context())
		reqToSend.Header.Set("Accept-Encoding", "gzip")
	}

	// Execute the request
	resp, err := t.base.RoundTrip(reqToSend)
	if err != nil {
		return nil, err
	}

	// Only decode a response we asked to be compressed, and only when a body
	// can actually exist. resp.ContentLength == 0 covers 204/304 responses
	// and any other response net/http has determined carries no body; a gzip
	// reader can't be built against a body that was never sent, and trying to
	// do so eagerly is the root cause of this issue (an empty or malformed
	// body would fail RoundTrip outright instead of failing a later Read).
	if !addGzip || resp.ContentLength == 0 ||
		!strings.EqualFold(resp.Header.Get("Content-Encoding"), "gzip") {
		return resp, nil
	}

	// Replace the body with a lazily-decompressing reader that enforces a
	// size limit. The gzip.Reader itself isn't constructed until the first
	// Read, so an empty, truncated, or otherwise malformed body surfaces its
	// failure there -- never as a RoundTrip error, which net/http treats as
	// "no usable response" and would discard this still-valid status/headers
	// along with it (see http.Client.Do).
	resp.Body = &gzipReadCloser{
		body:  resp.Body,
		limit: t.maxDecompressedSize,
	}

	// Remove Content-Encoding header since we're decompressing
	resp.Header.Del("Content-Encoding")
	// Remove Content-Length as it's no longer accurate
	resp.Header.Del("Content-Length")
	resp.ContentLength = -1
	resp.Uncompressed = true

	return resp, nil
}

// gzipReadCloser wraps a response body so it can lazily construct a
// *gzip.Reader on the first call to Read, and ensures both the gzip reader's
// resources and the original body are released on Close.
type gzipReadCloser struct {
	body      io.ReadCloser // underlying response body
	zr        *gzip.Reader  // lazily-initialized gzip reader
	zerr      error         // sticky error from gzip.NewReader
	limit     int64         // maximum decompressed bytes
	bytesRead int64         // bytes read so far
}

func (g *gzipReadCloser) Read(p []byte) (n int, err error) {
	if g.zr == nil && g.zerr == nil {
		g.zr, g.zerr = gzip.NewReader(g.body)
		if g.zerr != nil && !errors.Is(g.zerr, io.EOF) {
			// Wrap every error except a bare EOF, which means an empty gzip
			// stream (zero members -- see compress/gzip's own comment on
			// this) and must read as a clean, empty body, not a failure.
			g.zerr = fmt.Errorf("failed to decode gzip response body: %w", g.zerr)
		}
	}
	if g.zerr != nil {
		return 0, g.zerr
	}

	if g.limit > 0 {
		remaining := g.limit - g.bytesRead
		if remaining <= 0 {
			return 0, ErrDecompressionBombDetected
		}
		if int64(len(p)) > remaining {
			p = p[:remaining]
		}
	}
	n, err = g.zr.Read(p)
	g.bytesRead += int64(n)
	return n, err
}

func (g *gzipReadCloser) Close() error {
	// The gzip.Reader holds no resources of its own to release (it reads
	// directly from body), so closing body is sufficient. Deliberately not
	// touching zr/zerr here keeps Close safe to call concurrently with a
	// Read, which net/http's Response.Body documents as permitted.
	return g.body.Close()
}
