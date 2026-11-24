package anthropicwire

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

// DefaultMaxDecodedBytes is the output limit of DecodeBody when maxBytes <= 0
const DefaultMaxDecodedBytes = 64 << 20

// Bounds of the zstd window DecodeBody accepts. The window follows the
// output limit but never goes below zstdMinWindow (frames of common encoder
// levels use up to 8 MiB, and a frame whose window exceeds the limit would
// fail instead of being truncated) nor above zstdMaxWindow.
const (
	zstdMinWindow = 32 << 20
	zstdMaxWindow = 1 << 30
)

// maxDecodeLimit caps maxBytes so that limit+1 cannot overflow
const maxDecodeLimit = 1 << 40

// DecodeBody decodes a copy of a body for interpretation, following the
// Content-Encoding header value contentEncoding: a comma-separated list of
// codings in the order they were applied, decoded in reverse order.
// Supported codings are identity, gzip (x-gzip), deflate (zlib, or raw
// deflate as some servers send), br and zstd.
//
// Without a coding (or only identity) the body itself is returned with
// DecodeIdentity; an empty body is returned the same way. An unknown coding
// yields DecodeUnsupported, corrupt data DecodeError, both with a nil slice
// and an error. Output above maxBytes (DefaultMaxDecodedBytes when <= 0) is
// cut: its first maxBytes bytes are returned with DecodeTruncated and a nil
// error. Memory and work are bounded by maxBytes, so decompression bombs are
// harmless. body is never modified.
func DecodeBody(body []byte, contentEncoding string, maxBytes int64) ([]byte, string, error) {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxDecodedBytes
	}
	maxBytes = min(maxBytes, maxDecodeLimit)
	codings, err := parseCodings(contentEncoding)
	if err != nil {
		return nil, DecodeUnsupported, err
	}
	if len(codings) == 0 || len(body) == 0 {
		return body, DecodeIdentity, nil
	}
	data := body
	truncated := false
	for i := len(codings) - 1; i >= 0; i-- {
		out, cut, err := decodeOne(codings[i], data, maxBytes)
		if err != nil {
			// a stage fed with truncated input fails at its end: keep what it
			// produced before
			if !truncated || !errors.Is(err, io.ErrUnexpectedEOF) {
				return nil, DecodeError, fmt.Errorf("%s: %w", codings[i], err)
			}
			cut = true
		}
		data = out
		truncated = truncated || cut
	}
	if truncated {
		return data, DecodeTruncated, nil
	}
	return data, DecodeDecoded, nil
}

// parseCodings splits a Content-Encoding value into lowercase codings,
// dropping identity and empty items; an unsupported coding is an error
func parseCodings(header string) ([]string, error) {
	var codings []string
	for item := range strings.SplitSeq(header, ",") {
		coding := strings.ToLower(strings.TrimSpace(item))
		switch coding {
		case "", "identity":
		case "gzip", "x-gzip", "deflate", "br", "zstd":
			codings = append(codings, coding)
		default:
			return nil, fmt.Errorf("unsupported content coding %q", coding)
		}
	}
	return codings, nil
}

// decodeOne decodes data with one coding, keeping at most limit bytes; cut
// tells whether the output went beyond limit. On an error, out holds what was
// decoded before it.
func decodeOne(coding string, data []byte, limit int64) (out []byte, cut bool, err error) {
	switch coding {
	case "gzip", "x-gzip":
		zr, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, false, err
		}
		return readLimited(zr, limit)
	case "deflate":
		// zlib as the RFC says; raw deflate as some servers send instead
		zr, zerr := zlib.NewReader(bytes.NewReader(data))
		zlibHeader := zerr == nil
		if zlibHeader {
			out, cut, zerr = readLimited(zr, limit)
			if zerr == nil {
				return out, cut, nil
			}
		}
		fout, fcut, ferr := readLimited(flate.NewReader(bytes.NewReader(data)), limit)
		if ferr != nil && zlibHeader {
			// neither worked: with a valid zlib header its error is the
			// meaningful one (e.g. unexpected EOF on truncated input)
			return out, cut, zerr
		}
		return fout, fcut, ferr
	case "br":
		return readLimited(brotli.NewReader(bytes.NewReader(data)), limit)
	case "zstd":
		window := min(max(uint64(limit)+1, zstdMinWindow), zstdMaxWindow)
		zr, err := zstd.NewReader(bytes.NewReader(data),
			zstd.WithDecoderConcurrency(1),
			zstd.WithDecoderLowmem(true),
			zstd.WithDecoderMaxMemory(window),
			zstd.WithDecoderMaxWindow(window))
		if err != nil {
			return nil, false, err
		}
		defer zr.Close()
		return readLimited(zr, limit)
	}
	return nil, false, fmt.Errorf("unsupported content coding %q", coding)
}

// readLimited reads r up to limit bytes; reading one byte more tells an
// output of exactly limit bytes from a longer one
func readLimited(r io.Reader, limit int64) ([]byte, bool, error) {
	var buf bytes.Buffer
	n, err := buf.ReadFrom(io.LimitReader(r, limit+1))
	if n > limit {
		return buf.Bytes()[:limit], true, nil
	}
	if err != nil {
		return buf.Bytes(), false, err
	}
	return buf.Bytes(), false, nil
}
