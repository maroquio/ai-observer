package anthropicwire

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"fmt"
	"io"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

// NewStreamDecoder returns a reader of r's bytes decoded per contentEncoding
// (the codings DecodeBody accepts, decoded in reverse order). The decoders
// read from r only when they need more input to produce output, so a reader
// that returns one network chunk per Read lets the caller tell which chunk
// the decoded bytes came from: a compressed SSE stream whose server flushes
// each event decodes event by event. Without a coding r itself is returned.
//
// The caller bounds the output it reads (decompression bombs) and calls
// release when done. Construction fails on an unsupported coding or on a
// header that cannot be read (as with gzip, whose header is read at once).
func NewStreamDecoder(r io.Reader, contentEncoding string) (dec io.Reader, release func(), err error) {
	codings, err := parseCodings(contentEncoding)
	if err != nil {
		return nil, func() {}, err
	}
	var closers []func()
	release = func() {
		for _, c := range closers {
			c()
		}
	}
	dec = r
	for i := len(codings) - 1; i >= 0; i-- {
		next, closeFn, err := streamDecoder(codings[i], dec)
		if err != nil {
			release()
			return nil, func() {}, err
		}
		if closeFn != nil {
			closers = append(closers, closeFn)
		}
		dec = next
	}
	return dec, release, nil
}

// streamDecoder wraps r with the decoder of one coding
func streamDecoder(coding string, r io.Reader) (io.Reader, func(), error) {
	switch coding {
	case "gzip", "x-gzip":
		zr, err := gzip.NewReader(r)
		if err != nil {
			return nil, nil, err
		}
		return zr, nil, nil
	case "deflate":
		// zlib as the RFC says, raw deflate as some servers send: the first
		// two bytes tell them apart
		var head [2]byte
		n, err := io.ReadFull(r, head[:])
		rest := io.MultiReader(bytes.NewReader(head[:n]), r)
		if err == nil && isZlibHeader(head) {
			zr, err := zlib.NewReader(rest)
			if err != nil {
				return nil, nil, err
			}
			return zr, nil, nil
		}
		fr := flate.NewReader(rest)
		return fr, func() { fr.Close() }, nil
	case "br":
		return brotli.NewReader(r), nil, nil
	case "zstd":
		zr, err := zstd.NewReader(r,
			zstd.WithDecoderConcurrency(1),
			zstd.WithDecoderLowmem(true),
			zstd.WithDecoderMaxMemory(zstdMinWindow),
			zstd.WithDecoderMaxWindow(zstdMinWindow))
		if err != nil {
			return nil, nil, err
		}
		return zr, zr.Close, nil
	}
	return nil, nil, fmt.Errorf("unsupported content coding %q", coding)
}

// isZlibHeader tells whether b starts a zlib stream (RFC 1950: method 8,
// window up to 32 KiB, header checksum)
func isZlibHeader(b [2]byte) bool {
	return b[0]&0x0f == 8 && b[0]>>4 <= 7 && (uint16(b[0])<<8|uint16(b[1]))%31 == 0
}
