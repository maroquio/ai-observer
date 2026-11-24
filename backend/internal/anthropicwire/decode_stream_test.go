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
	"testing"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

// flusher is a compressing writer that can flush what it holds
type flusher interface {
	io.Writer
	Flush() error
	Close() error
}

// chunkReader returns one chunk per Read, like a network body whose server
// flushed each chunk, and counts the chunks handed out
type chunkReader struct {
	chunks [][]byte
	served int
	rest   []byte
}

func (r *chunkReader) Read(p []byte) (int, error) {
	if len(r.rest) == 0 {
		if r.served == len(r.chunks) {
			return 0, io.EOF
		}
		r.rest = r.chunks[r.served]
		r.served++
	}
	n := copy(p, r.rest)
	r.rest = r.rest[n:]
	return n, nil
}

// compressedEvents compresses events with w, flushing after each, and
// returns the compressed bytes cut where each flush ended (the trailer
// written by Close is the last chunk)
func compressedEvents(t *testing.T, events []string, newWriter func(io.Writer) flusher) [][]byte {
	t.Helper()
	var buf bytes.Buffer
	w := newWriter(&buf)
	var chunks [][]byte
	cut := func() {
		chunks = append(chunks, append([]byte(nil), buf.Bytes()...))
		buf.Reset()
	}
	for _, ev := range events {
		if _, err := io.WriteString(w, ev); err != nil {
			t.Fatal(err)
		}
		if err := w.Flush(); err != nil {
			t.Fatal(err)
		}
		cut()
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	cut()
	return chunks
}

type rawFlate struct{ *flate.Writer }

type zstdWriter struct{ *zstd.Encoder }

func (z zstdWriter) Flush() error { return z.Encoder.Flush() }

var streamCodings = []struct {
	name   string
	header string
	writer func(io.Writer) flusher
}{
	{"gzip", "gzip", func(w io.Writer) flusher { return gzip.NewWriter(w) }},
	{"zlib", "deflate", func(w io.Writer) flusher { return zlib.NewWriter(w) }},
	{"raw deflate", "deflate", func(w io.Writer) flusher {
		fw, _ := flate.NewWriter(w, flate.DefaultCompression)
		return rawFlate{fw}
	}},
	{"brotli", "br", func(w io.Writer) flusher { return brotli.NewWriter(w) }},
	{"zstd", "zstd", func(w io.Writer) flusher {
		zw, _ := zstd.NewWriter(w, zstd.WithEncoderConcurrency(1))
		return zstdWriter{zw}
	}},
}

func sseTestEvents(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"parte %d %s\"}}\n\n", i, strings.Repeat("x", i*7))
	}
	return out
}

// Each event decodes after its own chunk was read and before the next one:
// the decoders read input only when they need it, which is what lets the
// capture give each event of a compressed stream its arrival time
func TestStreamDecoderDecodesChunkByChunk(t *testing.T) {
	events := sseTestEvents(6)
	for _, c := range streamCodings {
		t.Run(c.name, func(t *testing.T) {
			chunks := compressedEvents(t, events, c.writer)
			src := &chunkReader{chunks: chunks}
			dec, release, err := NewStreamDecoder(src, c.header)
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			var got bytes.Buffer
			next := 0 // next event expected
			buf := make([]byte, 64<<10)
			for {
				n, err := dec.Read(buf)
				got.Write(buf[:n])
				for next < len(events) && got.Len() >= len(strings.Join(events[:next+1], "")) {
					if src.served != next+1 {
						t.Fatalf("event %d decoded after %d chunks were read, want %d", next, src.served, next+1)
					}
					next++
				}
				if errors.Is(err, io.EOF) {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			if got.String() != strings.Join(events, "") {
				t.Fatalf("decoded %q", got.String())
			}
		})
	}
}

func TestStreamDecoderErrors(t *testing.T) {
	if _, _, err := NewStreamDecoder(strings.NewReader("x"), "compress"); err == nil {
		t.Fatal("unsupported coding accepted")
	}
	if _, _, err := NewStreamDecoder(strings.NewReader("not gzip"), "gzip"); err == nil {
		t.Fatal("corrupt gzip header accepted")
	}
	// identity: the reader itself
	src := strings.NewReader("data: x\n\n")
	dec, release, err := NewStreamDecoder(src, "identity")
	if err != nil || dec != io.Reader(src) {
		t.Fatalf("identity: %v %v", dec, err)
	}
	release()
	// a stream cut short decodes its complete part, then fails
	chunks := compressedEvents(t, sseTestEvents(3), streamCodings[0].writer)
	cut := &chunkReader{chunks: chunks[:2]}
	dec, release, err = NewStreamDecoder(cut, "gzip")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	out, err := io.ReadAll(dec)
	if !errors.Is(err, io.ErrUnexpectedEOF) || string(out) != strings.Join(sseTestEvents(3)[:2], "") {
		t.Fatalf("truncated: %q %v", out, err)
	}
}
